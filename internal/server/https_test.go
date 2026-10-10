package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ontech/sshtunnel/pkg/tunnel"
)

func testTLSBundle(t *testing.T, domain string) (tlsBundle, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "TLS test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"*." + domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caCert, leafKey.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	private, _ := x509.MarshalPKCS8PrivateKey(leafKey)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert = append(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return tlsBundle{Domain: domain, Certificate: string(cert), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))}, roots
}
func nativeFixture(t *testing.T, target string, certificate bool) (*Server, tunnel.Options, *x509.CertPool) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Config()
	cfg.Listen = freePort(t)
	cfg.AdminListen = freePort(t)
	cfg.Broker.Enabled = true
	cfg.Web = WebConfig{Enabled: true, Listen: freePort(t), HTTPSListen: freePort(t), TLSMode: "native", Scheme: "https", BaseDomain: "tunnel.example.test", MaxBodyMB: 1}
	key, _ := tunnel.NewKey()
	cfg.Users = []User{{Name: "web", Enabled: true, AllowTunnel: true, AccessKeyHash: hashSecret(key), MaxConnections: 8}}
	if err = store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := New(store)
	var roots *x509.CertPool
	if certificate {
		var bundle tlsBundle
		bundle, roots = testTLSBundle(t, cfg.Web.BaseDomain)
		if err = s.https.install(bundle); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Start(); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); store.Close() })
	return s, tunnel.Options{Server: s.Addr(), User: "web", AccessKey: key, Fingerprint: s.Fingerprint(), Target: strings.TrimPrefix(target, "http://"), Protocol: "http"}, roots
}
func nativeClient(s *Server, roots *x509.CertPool) *http.Client {
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, s.https.listener.Addr().String())
	}}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}
func TestNativeHTTPSRoundTripAndCertificateReplacement(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Origin-Proto", r.Header.Get("X-Forwarded-Proto"))
		io.Copy(w, r.Body)
	}))
	defer target.Close()
	s, options, roots := nativeFixture(t, target.URL, true)
	session := sdkOpen(t, options)
	client := nativeClient(s, roots)
	resp, err := client.Post(session.Endpoint().URL+"/upload", "text/plain", strings.NewReader("HTTPS 왕복 통신"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("X-Origin-Proto") != "https" || readWeb(t, resp) != "HTTPS 왕복 통신" {
		t.Fatal("HTTPS tunnel failed")
	}
	if !s.https.ready() {
		t.Fatal("TLS not ready")
	}
	replacement, newRoots := testTLSBundle(t, s.active.Web.BaseDomain)
	if err = s.https.install(replacement); err != nil {
		t.Fatal(err)
	}
	resp, err = nativeClient(s, newRoots).Get(session.Endpoint().URL)
	if err != nil {
		t.Fatal("new certificate not hot-loaded", err)
	}
	resp.Body.Close()
	// 잘못된 인증서 교체는 기존 인증서 파일과 실행 상태를 보존합니다.
	path := filepath.Join(s.Store.Dir, "https", "certificate.json")
	before, _ := os.ReadFile(path)
	replacement.PrivateKey = "invalid"
	if s.https.install(replacement) == nil {
		t.Fatal("invalid key accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || !s.https.ready() {
		t.Fatal("failed import damaged active certificate")
	}
	resp, err = nativeClient(s, newRoots).Get("https://wrong.example.test/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("unrelated SNI accepted")
	}
}
func TestNativeHTTPSMissingCertificateAndPortConflict(t *testing.T) {
	s, options, _ := nativeFixture(t, "http://127.0.0.1:1", false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := tunnel.Open(ctx, options)
	if err == nil {
		session.Close()
		t.Fatal("URL issued without a certificate")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Fatal(err)
	}
	if s.https.status()["certificatePresent"] != false {
		t.Fatal("missing certificate not reported")
	}
	s.Close()
	listener, err := net.Listen("tcp", s.active.Web.HTTPSListen)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	next := New(s.Store)
	if err = next.Start(); err != nil {
		t.Fatal("SSH/admin should survive HTTPS conflict", err)
	}
	defer next.Close()
	status := next.https.status()
	if status["listenerError"] == "" || next.https.ready() {
		t.Fatal("port conflict hidden", status)
	}
	conn, err := net.DialTimeout("tcp", next.Addr(), time.Second)
	if err != nil {
		t.Fatal("SSH unavailable", err)
	}
	conn.Close()
}
func TestTLSCertificateValidationAndLegacyConfig(t *testing.T) {
	bundle, _ := testTLSBundle(t, "tunnel.example.test")
	if _, err := parseTLSBundle(bundle, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := parseTLSBundle(bundle, time.Now().Add(72*time.Hour)); err == nil {
		t.Fatal("expired certificate accepted")
	}
	bundle.Domain = "different.example.test"
	if _, err := parseTLSBundle(bundle, time.Now()); err == nil {
		t.Fatal("wrong domain accepted")
	}
	cfg := DefaultConfig()
	cfg.Web.Enabled = true
	cfg.Web.BaseDomain = "tunnel.example.test"
	if err := ValidateConfig(cfg); err != nil {
		t.Fatal("legacy proxy config rejected", err)
	}
	cfg.Web.TLSMode = "native"
	cfg.Web.HTTPSListen = cfg.AdminListen
	if ValidateConfig(cfg) == nil {
		t.Fatal("admin port conflict accepted")
	}
	cfg.Web.HTTPSListen = "0.0.0.0:443"
	cfg.Web.Listen = "0.0.0.0:17894"
	if ValidateConfig(cfg) == nil {
		t.Fatal("public plaintext accepted in native mode")
	}
}
func tlsAdminRequest(t *testing.T, s *Server, path, method string, value any, authenticate bool) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(value)
	req := httptest.NewRequest(method, "http://"+s.active.AdminListen+path, strings.NewReader(string(data)))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SSHTunnel", "1")
	if authenticate {
		token, err := os.ReadFile(filepath.Join(s.Store.Dir, "admin.token"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	recorder := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(recorder, req)
	return recorder
}
func TestHTTPSAdminSecretsAndAuthentication(t *testing.T) {
	s, _, _ := nativeFixture(t, "http://127.0.0.1:1", false)
	s.https.mu.Lock()
	s.https.lastAttempt = time.Now()
	s.https.mu.Unlock()
	a := tlsAutomation{Domain: s.active.Web.BaseDomain, Zone: "example.test", Token: strings.Repeat("secret", 8), Enabled: true, AcceptTerms: true}
	if err := s.https.privateJSON("automation.json", a); err != nil {
		t.Fatal(err)
	}
	status := tlsAdminRequest(t, s, "/api/https", "GET", nil, true)
	if status.Code != 200 || strings.Contains(status.Body.String(), a.Token) {
		t.Fatal("API leaked secret", status.Code)
	}
	bundle, _ := testTLSBundle(t, s.active.Web.BaseDomain)
	unauth := tlsAdminRequest(t, s, "/api/https/certificate", "POST", bundle, false)
	if unauth.Code != 401 {
		t.Fatal("unauthenticated import allowed")
	}
	imported := tlsAdminRequest(t, s, "/api/https/certificate", "POST", bundle, true)
	if imported.Code != 200 {
		t.Fatal(imported.Body.String())
	}
	if strings.Contains(imported.Body.String(), "PRIVATE KEY") {
		t.Fatal("private key exposed")
	}
	var saved tlsAutomation
	if err := s.https.readJSON("automation.json", &saved); err != nil || saved.Enabled {
		t.Fatal("manual import did not disable renewal")
	}
	forgotten := tlsAdminRequest(t, s, "/api/https/forget-token", "POST", nil, true)
	if forgotten.Code != 200 {
		t.Fatal(forgotten.Body.String())
	}
	if err := s.https.readJSON("automation.json", &saved); !os.IsNotExist(err) {
		t.Fatal("token file remains", err)
	}
	if !s.https.ready() {
		t.Fatal("removing token removed certificate")
	}
}
