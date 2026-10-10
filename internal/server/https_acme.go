package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

func validateAutomation(a tlsAutomation) error {
	if !validHost(a.Domain) || net.ParseIP(a.Domain) != nil || !strings.Contains(a.Domain, ".") {
		return errors.New("웹 기본 도메인을 먼저 저장하세요")
	}
	if !validHost(a.Zone) || net.ParseIP(a.Zone) != nil || !strings.Contains(a.Zone, ".") || !(a.Domain == a.Zone || strings.HasSuffix(a.Domain, "."+a.Zone)) {
		return errors.New("Cloudflare 영역 도메인을 확인하세요. 예: ontechdev.com")
	}
	if a.Email != "" {
		address, err := mail.ParseAddress(a.Email)
		if err != nil || address.Address != a.Email {
			return errors.New("이메일 주소 형식을 확인하세요")
		}
	}
	if len(a.Token) < 20 || len(a.Token) > 512 || strings.ContainsAny(a.Token, "\r\n\t ") {
		return errors.New("Cloudflare API 토큰을 입력하세요")
	}
	if !a.AcceptTerms {
		return errors.New("Let's Encrypt 이용약관에 동의해야 인증서를 발급할 수 있습니다")
	}
	return nil
}
func (m *httpsManager) beginIssue(a tlsAutomation, persist bool) error {
	m.mu.Lock()
	if m.busy || m.closing || m.s.ctx.Err() != nil {
		m.mu.Unlock()
		return errors.New("인증서 작업이 진행 중이거나 서버가 종료 중입니다")
	}
	if !m.lastAttempt.IsZero() && time.Since(m.lastAttempt) < time.Minute {
		m.mu.Unlock()
		return errors.New("인증서 발급 재시도는 1분 뒤 가능합니다")
	}
	if persist {
		if err := m.privateJSON("automation.json", a); err != nil {
			m.mu.Unlock()
			return errors.New("자동 발급 설정을 저장하지 못했습니다")
		}
	}
	m.busy = true
	m.lastAttempt = time.Now()
	m.jobMessage = "Cloudflare DNS 인증을 준비하고 있습니다"
	m.workers.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.workers.Done()
		ctx, cancel := context.WithTimeout(m.s.ctx, 12*time.Minute)
		defer cancel()
		err := m.issue(ctx, a)
		message := "인증서 발급 완료 · HTTPS에 즉시 적용했습니다"
		if !m.ready() {
			message = "인증서 발급 완료 · 내장 HTTPS 설정을 저장하고 서버를 다시 시작하세요"
		}
		if err != nil {
			message = "인증서 발급 실패: " + err.Error()
		}
		m.mu.Lock()
		m.busy = false
		m.jobMessage = message
		m.mu.Unlock()
		m.s.event("https", message)
	}()
	return nil
}
func (m *httpsManager) renewIfNeeded() {
	if !m.s.active.Web.Enabled || m.s.active.Web.TLSMode != "native" {
		return
	}
	var a tlsAutomation
	if m.readJSON("automation.json", &a) != nil || !a.Enabled || a.Domain != m.s.active.Web.BaseDomain || validateAutomation(a) != nil {
		return
	}
	m.mu.Lock()
	due := m.cert == nil || m.domain != a.Domain
	if m.cert != nil {
		leaf := m.cert.Leaf
		window := leaf.NotAfter.Sub(leaf.NotBefore) / 3
		due = due || time.Until(leaf.NotAfter) <= window
	}
	recent := !m.lastAttempt.IsZero() && time.Since(m.lastAttempt) < 6*time.Hour
	m.mu.Unlock()
	if due && !recent {
		_ = m.beginIssue(a, false)
	}
}
func (m *httpsManager) progress(message string) { m.mu.Lock(); m.jobMessage = message; m.mu.Unlock() }

type cloudflareDNS struct {
	token   string
	client  *http.Client
	baseURL string
}

func newCloudflareDNS(token string) *cloudflareDNS {
	return &cloudflareDNS{token: token, baseURL: "https://api.cloudflare.com/client/v4", client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Cloudflare API redirect refused") }}}
}
func (c *cloudflareDNS) request(ctx context.Context, method, path string, input, output any) error {
	var data []byte
	if input != nil {
		data, _ = json.Marshal(input)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("Cloudflare API 요청 생성 실패")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return errors.New("Cloudflare API 연결 실패 · 인터넷 연결을 확인하세요")
	}
	defer resp.Body.Close()
	var result struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return errors.New("Cloudflare API 응답 형식 오류")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.Success {
		code := 0
		if len(result.Errors) > 0 {
			code = result.Errors[0].Code
		}
		// 원격 오류 본문은 토큰 등의 값을 포함할 수 있어 코드만 표시합니다.
		return fmt.Errorf("Cloudflare API 거부 (HTTP %d / 코드 %d) · 해당 영역의 Zone Read·DNS Edit 권한을 확인하세요", resp.StatusCode, code)
	}
	if output != nil {
		if err = json.Unmarshal(result.Result, output); err != nil {
			return errors.New("Cloudflare 결과 형식 오류")
		}
	}
	return nil
}
func (c *cloudflareDNS) zoneID(ctx context.Context, name string) (string, error) {
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.request(ctx, "GET", "/zones?name="+url.QueryEscape(name), nil, &zones); err != nil {
		return "", err
	}
	for _, z := range zones {
		if z.Name == name && validCFID(z.ID) {
			return z.ID, nil
		}
	}
	return "", errors.New("Cloudflare 영역을 찾지 못했습니다. 영역 이름과 토큰 권한을 확인하세요")
}
func validCFID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}
func (c *cloudflareDNS) createTXT(ctx context.Context, zone, name, value string) (string, error) {
	var record struct {
		ID string `json:"id"`
	}
	err := c.request(ctx, "POST", "/zones/"+zone+"/dns_records", map[string]any{"type": "TXT", "name": name, "content": value, "ttl": 60, "comment": "OnTech SSHTunnel certificate validation"}, &record)
	if err != nil {
		return "", err
	}
	if !validCFID(record.ID) {
		return "", errors.New("Cloudflare TXT 레코드 ID 오류")
	}
	return record.ID, nil
}
func (c *cloudflareDNS) deleteTXT(ctx context.Context, zone, id string) error {
	return c.request(ctx, "DELETE", "/zones/"+zone+"/dns_records/"+id, nil, nil)
}
func waitTXT(ctx context.Context, name, value string) error {
	for {
		for _, address := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
			}}
			records, err := resolver.LookupTXT(probe, name)
			cancel()
			if err == nil {
				for _, record := range records {
					if record == value {
						return nil
					}
				}
			}
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("DNS 인증 레코드 전파를 확인하지 못했습니다. DNS 연결·위임·CNAME 설정을 확인하세요")
		case <-timer.C:
		}
	}
}
func (m *httpsManager) accountKey() (*ecdsa.PrivateKey, error) {
	var saved struct {
		Key string `json:"key"`
	}
	err := m.readJSON("acme-account.json", &saved)
	if os.IsNotExist(err) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return nil, e
		}
		der, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return nil, e
		}
		saved.Key = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		if e = m.privateJSON("acme-account.json", saved); e != nil {
			return nil, e
		}
		return key, nil
	}
	if err != nil {
		return nil, errors.New("ACME 계정 키를 읽지 못했습니다")
	}
	block, _ := pem.Decode([]byte(saved.Key))
	if block == nil {
		return nil, errors.New("ACME 계정 키 형식 오류")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("ACME 계정 키 형식 오류")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("ACME 계정 키 형식 오류")
	}
	return key, nil
}
func acmeFailure(stage string, err error) error {
	var ae *acme.Error
	if errors.As(err, &ae) {
		return fmt.Errorf("%s (ACME HTTP %d) · DNS·CAA·발급 제한을 확인하세요", stage, ae.StatusCode)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s · 작업 취소 또는 시간 초과", stage)
	}
	return fmt.Errorf("%s · DNS 인증 또는 인터넷 연결을 확인하세요", stage)
}
func (m *httpsManager) issue(ctx context.Context, a tlsAutomation) error {
	cf := newCloudflareDNS(a.Token)
	key, err := m.accountKey()
	if err != nil {
		return err
	}
	client := &acme.Client{Key: key, DirectoryURL: acme.LetsEncryptURL, UserAgent: "OnTech-SSHTunnel/" + Version, HTTPClient: &http.Client{Timeout: 45 * time.Second}}
	return m.issueWith(ctx, a, cf, client, waitTXT)
}

// 의존성을 인자로 받아 실제 DNS/인증기관에 접근하지 않는 통합 테스트를 지원합니다.
func (m *httpsManager) issueWith(ctx context.Context, a tlsAutomation, cf *cloudflareDNS, client *acme.Client, waitDNS func(context.Context, string, string) error) error {
	zone, err := cf.zoneID(ctx, a.Zone)
	if err != nil {
		return err
	}
	account := &acme.Account{}
	if a.Email != "" {
		account.Contact = []string{"mailto:" + a.Email}
	}
	_, err = client.Register(ctx, account, func(string) bool { return a.AcceptTerms })
	if err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return acmeFailure("Let's Encrypt 계정 등록 실패", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs("*."+a.Domain))
	if err != nil {
		return acmeFailure("인증서 주문 실패", err)
	}
	for _, authURL := range order.AuthzURLs {
		auth, err := client.GetAuthorization(ctx, authURL)
		if err != nil {
			return acmeFailure("DNS 인증 준비 실패", err)
		}
		if auth.Identifier.Type != "dns" || auth.Identifier.Value != a.Domain || !auth.Wildcard {
			return errors.New("인증 기관의 도메인 응답이 요청과 일치하지 않습니다")
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		var challenge *acme.Challenge
		for _, ch := range auth.Challenges {
			if ch.Type == "dns-01" {
				challenge = ch
				break
			}
		}
		if challenge == nil {
			return errors.New("DNS-01 인증을 사용할 수 없습니다")
		}
		value, err := client.DNS01ChallengeRecord(challenge.Token)
		if err != nil {
			return acmeFailure("DNS 인증 값 생성 실패", err)
		}
		name := "_acme-challenge." + a.Domain
		record, err := cf.createTXT(ctx, zone, name, value)
		if err != nil {
			return err
		}
		// 이번 작업에서 만든 레코드 ID만 제거하며 기존 DNS 레코드는 수정하지 않습니다.
		defer func() {
			clean, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if cf.deleteTXT(clean, zone, record) != nil {
				m.s.event("https", "임시 DNS 인증 TXT 정리 실패 · Cloudflare에서 _acme-challenge 레코드를 확인하세요")
			}
		}()
		m.progress("DNS 인증 레코드 전파 대기 중 · 몇 분 걸릴 수 있습니다")
		if err = waitDNS(ctx, name, value); err != nil {
			return err
		}
		if _, err = client.Accept(ctx, challenge); err != nil {
			return acmeFailure("DNS 인증 요청 실패", err)
		}
		if _, err = client.WaitAuthorization(ctx, authURL); err != nil {
			return acmeFailure("도메인 소유권 인증 실패", err)
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return acmeFailure("인증서 주문 준비 실패", err)
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return errors.New("인증서 키 생성 실패")
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{"*." + a.Domain}}, certKey)
	if err != nil {
		return errors.New("인증서 서명 요청 생성 실패")
	}
	m.progress("도메인 인증 완료 · 인증서 발급 중")
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return acmeFailure("인증서 발급 실패", err)
	}
	var certPEM []byte
	for _, der := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(certKey)
	if err != nil {
		return errors.New("인증서 키 저장 준비 실패")
	}
	return m.install(tlsBundle{Domain: a.Domain, Certificate: string(certPEM), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))})
}
