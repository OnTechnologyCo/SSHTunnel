package server

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// httpsManager는 SSH/관리 화면과 별개로 공개 HTTPS를 수신합니다.
// 인증서 미등록·포트 충돌이 있어도 관리 화면에서 원인을 확인할 수 있습니다.
type httpsManager struct {
	s           *Server
	mu          sync.Mutex
	cert        *tls.Certificate
	domain      string
	listener    net.Listener
	server      *http.Server
	listenError string
	certError   string
	jobMessage  string
	busy        bool
	closing     bool
	lastAttempt time.Time
	workers     sync.WaitGroup
}
type tlsBundle struct {
	Domain      string `json:"domain"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
}
type tlsAutomation struct {
	Domain      string `json:"domain"`
	Zone        string `json:"zone"`
	Email       string `json:"email"`
	Token       string `json:"token"`
	Enabled     bool   `json:"enabled"`
	AcceptTerms bool   `json:"acceptTerms"`
}

func httpsListen(c WebConfig) string {
	if c.HTTPSListen != "" {
		return c.HTTPSListen
	}
	return "0.0.0.0:443"
}
func newHTTPSManager(s *Server) *httpsManager { return &httpsManager{s: s} }

func parseTLSBundle(b tlsBundle, now time.Time) (*tls.Certificate, error) {
	pair, err := tls.X509KeyPair([]byte(b.Certificate), []byte(b.PrivateKey))
	if err != nil || len(pair.Certificate) == 0 {
		return nil, errors.New("PEM 인증서와 개인키 형식 또는 키 일치 여부를 확인하세요")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, errors.New("인증서를 읽을 수 없습니다")
	}
	wildcard := false
	for _, n := range leaf.DNSNames {
		if strings.EqualFold(n, "*."+b.Domain) {
			wildcard = true
		}
	}
	if !wildcard {
		return nil, errors.New("인증서에 *." + b.Domain + " 이름이 포함되어야 합니다")
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("인증서가 만료되었거나 아직 유효하지 않습니다")
	}
	if leaf.IsCA {
		return nil, errors.New("CA 인증서 대신 서버 인증서를 등록하세요")
	}
	if len(leaf.ExtKeyUsage) > 0 {
		usable := false
		for _, u := range leaf.ExtKeyUsage {
			if u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny {
				usable = true
			}
		}
		if !usable {
			return nil, errors.New("서버 인증 용도의 인증서가 아닙니다")
		}
	}
	pair.Leaf = leaf
	return &pair, nil
}
func (m *httpsManager) privateJSON(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.s.Store.Dir, "https")
	if err = PrivateDir(dir); err != nil {
		return err
	}
	temp := filepath.Join(dir, ".write-"+secret()+".tmp")
	if err = writeNew(temp, data); err != nil {
		return err
	}
	defer os.Remove(temp)
	if err = PrivateFile(temp); err != nil {
		return err
	}
	return atomicReplace(temp, filepath.Join(dir, name))
}
func (m *httpsManager) readJSON(name string, value any) error {
	data, err := readPrivate(filepath.Join(m.s.Store.Dir, "https", name), 1<<20)
	if err != nil {
		return err
	}
	return decode(data, value)
}
func (m *httpsManager) install(b tlsBundle) error {
	pair, err := parseTLSBundle(b, time.Now())
	if err != nil {
		return err
	}
	// 도메인이 발급 중 바뀌었다면 이전 도메인의 인증서로 덮어쓰지 않습니다.
	m.s.ops.Lock()
	defer m.s.ops.Unlock()
	if m.s.Store.Config().Web.BaseDomain != b.Domain {
		return errors.New("발급 중 웹 도메인이 변경되었습니다. 현재 도메인으로 다시 발급하세요")
	}
	if err = m.privateJSON("certificate.json", b); err != nil {
		return errors.New("인증서를 저장하지 못했습니다")
	}
	m.mu.Lock()
	m.cert = pair
	m.domain = b.Domain
	m.certError = ""
	m.mu.Unlock()
	return nil
}
func (m *httpsManager) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cert == nil || m.domain != m.s.active.Web.BaseDomain {
		return nil, errors.New("HTTPS 인증서가 준비되지 않았습니다")
	}
	if time.Now().Before(m.cert.Leaf.NotBefore) || !time.Now().Before(m.cert.Leaf.NotAfter) {
		return nil, errors.New("HTTPS 인증서가 만료되었습니다")
	}
	if hello.ServerName == "" || m.cert.Leaf.VerifyHostname(hello.ServerName) != nil {
		return nil, errors.New("HTTPS 도메인이 일치하지 않습니다")
	}
	return m.cert, nil
}
func (m *httpsManager) start() {
	var b tlsBundle
	err := m.readJSON("certificate.json", &b)
	if err == nil {
		m.cert, err = parseTLSBundle(b, time.Now())
		if err == nil {
			m.domain = b.Domain
		}
	}
	if err != nil {
		m.certError = "인증서를 등록하거나 자동 발급하세요"
	}
	c := m.s.active.Web
	if c.Enabled && c.TLSMode == "native" {
		ln, e := net.Listen("tcp", httpsListen(c))
		if e != nil {
			m.listenError = "HTTPS 수신 실패: " + httpsListen(c) + " · 포트가 다른 프로그램에서 사용 중인지 확인하세요"
			m.s.event("https", m.listenError)
		} else {
			m.listener = ln
			m.server = m.s.makeWebServer()
			m.server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: m.getCertificate}
			// 잘못된 외부 TLS 접속으로 무제한 로그를 기록하지 않습니다.
			m.server.ErrorLog = log.New(io.Discard, "", 0)
			m.workers.Add(1)
			go func() {
				defer m.workers.Done()
				e := m.server.ServeTLS(ln, "", "")
				if e != nil && !errors.Is(e, http.ErrServerClosed) {
					m.mu.Lock()
					m.listenError = "HTTPS 수신이 중단되었습니다. 서버를 다시 시작하세요"
					m.mu.Unlock()
					m.s.event("https", "HTTPS 수신 중단")
				}
			}()
			m.s.event("https", "내장 HTTPS 수신 시작: "+ln.Addr().String())
		}
	}
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		m.renewIfNeeded()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-m.s.ctx.Done():
				return
			case <-ticker.C:
				m.renewIfNeeded()
			}
		}
	}()
}
func (m *httpsManager) close() {
	m.mu.Lock()
	m.closing = true
	srv := m.server
	m.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	m.workers.Wait()
}
func (m *httpsManager) ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listener != nil && m.listenError == "" && m.cert != nil && m.domain == m.s.active.Web.BaseDomain && !time.Now().Before(m.cert.Leaf.NotBefore) && time.Now().Before(m.cert.Leaf.NotAfter)
}
func (m *httpsManager) status() map[string]any {
	cfg := m.s.Store.Config().Web
	m.mu.Lock()
	out := map[string]any{"mode": cfg.TLSMode, "listen": httpsListen(cfg), "listening": m.listener != nil && m.listenError == "", "listenerError": m.listenError, "certificateError": m.certError, "busy": m.busy, "message": m.jobMessage, "certificateDomain": m.domain, "certificatePresent": m.cert != nil, "restartRequired": cfg != m.s.active.Web}
	if m.cert != nil {
		out["expires"] = m.cert.Leaf.NotAfter
		out["issuer"] = m.cert.Leaf.Issuer.String()
		out["certificateMatches"] = m.domain == cfg.BaseDomain && time.Now().Before(m.cert.Leaf.NotAfter)
	}
	m.mu.Unlock()
	out["ready"] = m.ready()
	var a tlsAutomation
	if m.readJSON("automation.json", &a) == nil {
		out["automation"] = map[string]any{"zone": a.Zone, "email": a.Email, "enabled": a.Enabled, "tokenSaved": a.Token != "", "domain": a.Domain}
	}
	if cfg.Scheme == "https" && cfg.TLSMode != "native" {
		out["hint"] = "현재 외부 HTTPS 프록시 방식입니다. 프록시가 없다면 서버 설정에서 내장 HTTPS를 선택하세요"
	}
	return out
}
func (m *httpsManager) admin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/https", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, m.status()) })
	mux.HandleFunc("POST /api/https/certificate", func(w http.ResponseWriter, r *http.Request) {
		var b tlsBundle
		if body(w, r, &b) != nil {
			writeJSON(w, 400, map[string]string{"error": "인증서 입력 형식을 확인하세요"})
			return
		}
		m.mu.Lock()
		if m.busy || m.closing {
			m.mu.Unlock()
			writeJSON(w, 409, map[string]string{"error": "인증서 작업이 진행 중입니다"})
			return
		}
		m.busy = true
		m.workers.Add(1)
		m.mu.Unlock()
		defer func() { m.mu.Lock(); m.busy = false; m.mu.Unlock(); m.workers.Done() }()
		b.Domain = m.s.Store.Config().Web.BaseDomain
		if b.Domain == "" {
			writeJSON(w, 400, map[string]string{"error": "웹 기본 도메인을 먼저 저장하세요"})
			return
		}
		if err := m.install(b); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		// 수동 등록 후 기존 자동화가 인증서를 덮어쓰지 않도록 갱신을 중지합니다.
		var a tlsAutomation
		if m.readJSON("automation.json", &a) == nil {
			a.Enabled = false
			if err := m.privateJSON("automation.json", a); err != nil {
				writeJSON(w, 500, map[string]string{"error": "인증서는 등록되었으나 자동 갱신 중지 저장에 실패했습니다"})
				return
			}
		}
		m.s.event("https", "HTTPS 인증서 등록 완료")
		writeJSON(w, 200, m.status())
	})
	mux.HandleFunc("POST /api/https/issue", func(w http.ResponseWriter, r *http.Request) {
		var a tlsAutomation
		if body(w, r, &a) != nil {
			writeJSON(w, 400, map[string]string{"error": "자동 발급 설정 형식을 확인하세요"})
			return
		}
		a.Domain = m.s.Store.Config().Web.BaseDomain
		a.Zone = strings.ToLower(strings.TrimSpace(a.Zone))
		a.Email = strings.TrimSpace(a.Email)
		a.Token = strings.TrimSpace(a.Token)
		if a.Token == "" {
			var old tlsAutomation
			if m.readJSON("automation.json", &old) == nil && old.Zone == a.Zone {
				a.Token = old.Token
			}
		}
		if err := validateAutomation(a); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := m.beginIssue(a, true); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 202, map[string]bool{"started": true})
	})
	mux.HandleFunc("POST /api/https/forget-token", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.busy {
			writeJSON(w, 409, map[string]string{"error": "진행 중인 발급이 끝난 뒤 다시 시도하세요"})
			return
		}
		err := os.Remove(filepath.Join(m.s.Store.Dir, "https", "automation.json"))
		if err != nil && !os.IsNotExist(err) {
			writeJSON(w, 500, map[string]string{"error": "자동 발급 설정을 지우지 못했습니다"})
			return
		}
		writeJSON(w, 200, map[string]bool{"removed": true})
	})
}
