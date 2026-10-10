package server

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

//go:embed web/*
var web embed.FS

func revision(c Config) string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	s.brokerAdmin(mux)
	s.https.admin(mux)
	mux.HandleFunc("GET /api/web-deployment", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.WebDeployment()) })
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "running", "version": Version})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		saved := s.Store.Config()
		writeJSON(w, 200, map[string]any{"version": Version, "started": s.started, "sshAddress": s.Addr(), "webAddress": s.WebAddr(), "adminAddress": s.active.AdminListen, "open": s.open.Load(), "fingerprint": s.Fingerprint(), "connections": s.Views(), "tunnels": s.TunnelViews(), "restartRequired": saved.Listen != s.active.Listen || saved.AdminListen != s.active.AdminListen || saved.Web != s.active.Web})
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		c := s.Store.Config()
		rev := revision(c)
		passwordUsers := []string{}
		accessKeyUsers := []string{}
		for i := range c.Users {
			if c.Users[i].PasswordHash != "" {
				passwordUsers = append(passwordUsers, c.Users[i].Name)
			}
			if c.Users[i].AccessKeyHash != "" {
				accessKeyUsers = append(accessKeyUsers, c.Users[i].Name)
			}
			c.Users[i].PasswordHash = ""
			c.Users[i].AccessKeyHash = ""
		}
		writeJSON(w, 200, map[string]any{"config": c, "revision": rev, "passwordUsers": passwordUsers, "accessKeyUsers": accessKeyUsers})
	})
	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Config    Config            `json:"config"`
			Passwords map[string]string `json:"passwords"`
			Revision  string            `json:"revision"`
		}
		if err := body(w, r, &input); err != nil {
			writeJSON(w, 400, map[string]string{"error": "설정 JSON 형식을 확인하세요"})
			return
		}
		s.ops.Lock()
		defer s.ops.Unlock()
		old := s.Store.Config()
		if input.Revision != revision(old) {
			writeJSON(w, 409, map[string]string{"error": "다른 곳에서 설정이 변경되었습니다. 다시 불러온 뒤 저장하세요"})
			return
		}
		for i := range input.Config.Users {
			u := &input.Config.Users[i]
			if u.PasswordHash != "" || u.AccessKeyHash != "" {
				writeJSON(w, 400, map[string]string{"error": "암호 해시를 직접 전달할 수 없습니다"})
				return
			}
			for _, prior := range old.Users {
				if prior.Name == u.Name {
					u.PasswordHash = prior.PasswordHash
					u.AccessKeyHash = prior.AccessKeyHash
				}
			}
			if password, exists := input.Passwords[u.Name]; exists {
				if password == "" {
					u.PasswordHash = ""
				} else {
					hash, e := NewPassword(password)
					if e != nil {
						writeJSON(w, 400, map[string]string{"error": e.Error()})
						return
					}
					u.PasswordHash = hash
				}
			}
		}
		if e := s.Store.Save(input.Config); e != nil {
			writeJSON(w, 400, map[string]string{"error": e.Error()})
			return
		}
		s.open.Store(input.Config.Open)
		s.DisconnectAll()
		s.event("config", "관리 화면에서 설정 저장 · 기존 연결 종료")
		writeJSON(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("POST /api/open", func(w http.ResponseWriter, r *http.Request) {
		if e := s.SetOpen(true); e != nil {
			writeJSON(w, 500, map[string]string{"error": "공유 상태를 저장하지 못했습니다"})
			return
		}
		writeJSON(w, 200, map[string]bool{"open": true})
	})
	mux.HandleFunc("POST /api/close", func(w http.ResponseWriter, r *http.Request) {
		if e := s.SetOpen(false); e != nil {
			writeJSON(w, 500, map[string]string{"error": "현재 연결은 닫았지만 중지 상태 저장에 실패했습니다. 재시작 전에 저장 경로를 확인하세요"})
			return
		}
		writeJSON(w, 200, map[string]bool{"open": false})
	})
	mux.HandleFunc("DELETE /api/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.Disconnect(r.PathValue("id")) {
			writeJSON(w, 404, map[string]string{"error": "이미 종료된 연결입니다"})
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Events()) })
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		if name != "/index.html" && name != "/app.js" && name != "/style.css" {
			http.NotFound(w, r)
			return
		}
		data, e := web.ReadFile("web" + name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		ct := "text/html; charset=utf-8"
		if name == "/app.js" {
			ct = "application/javascript; charset=utf-8"
		}
		if name == "/style.css" {
			ct = "text/css; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.Write(data)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		remote, _, e := net.SplitHostPort(r.RemoteAddr)
		host, port, e2 := net.SplitHostPort(r.Host)
		_, expectedPort, _ := net.SplitHostPort(s.active.AdminListen)
		hostIP := net.ParseIP(host)
		if e != nil || e2 != nil || !net.ParseIP(remote).IsLoopback() || port != expectedPort || !(host == "localhost" || hostIP != nil && hostIP.IsLoopback()) {
			writeJSON(w, 403, map[string]string{"error": "로컬 관리 주소만 허용합니다"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			writeJSON(w, 403, map[string]string{"error": "요청 출처가 올바르지 않습니다"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || !s.Store.AdminOK(strings.TrimPrefix(auth, "Bearer ")) {
				writeJSON(w, 401, map[string]string{"error": "관리 토큰이 필요합니다. Open-Admin.cmd로 관리 화면을 여세요"})
				return
			}
			if r.Method != "GET" && r.Header.Get("X-SSHTunnel") != "1" {
				writeJSON(w, 403, map[string]string{"error": "요청 확인 헤더가 필요합니다"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func body(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("JSON required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	data, e := io.ReadAll(r.Body)
	if e != nil {
		return e
	}
	return decode(data, v)
}
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{Addr: s.Store.Config().AdminListen, Handler: s.AdminHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}
