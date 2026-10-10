package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ontech/sshtunnel/pkg/tunnel"
)

func brokerFixture(t *testing.T) (fixture, tunnel.Options) {
	t.Helper()
	f := setup(t)
	target := halfServer(t)
	addr := freePort(t)
	_, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	if n > 65525 {
		n -= 10
	}
	key, e := tunnel.NewKey()
	if e != nil {
		t.Fatal(e)
	}
	cfg := f.store.Config()
	cfg.Broker = BrokerConfig{Enabled: true, PublicHost: "127.0.0.1", BindIP: "127.0.0.1", PortMin: n, PortMax: n + 5, MaxTunnels: 8}
	cfg.Users[0].AllowTunnel = true
	cfg.Users[0].AccessKeyHash = hashSecret(key)
	if e = f.s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	return f, tunnel.Options{Server: f.s.Addr(), User: "tunnel", AccessKey: key, Fingerprint: f.s.Fingerprint(), Target: target.Addr().String(), Name: "test-service"}
}
func sdkOpen(t *testing.T, opts tunnel.Options) *tunnel.Session {
	t.Helper()
	session, e := tunnel.Open(context.Background(), opts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
func exchange(address string, payload []byte) error {
	c, e := net.DialTimeout("tcp", address, 3*time.Second)
	if e != nil {
		return e
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, e = c.Write(payload); e != nil {
		return e
	}
	if e = c.(*net.TCPConn).CloseWrite(); e != nil {
		return e
	}
	reply, e := io.ReadAll(c)
	if e != nil {
		return e
	}
	if !bytes.Equal(reply, append([]byte("reply:"), payload...)) {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func TestBrokerAddressHalfCloseConcurrentClientsAndIsolation(t *testing.T) {
	f, opts := brokerFixture(t)
	a := sdkOpen(t, opts)
	b := sdkOpen(t, opts)
	if a.Endpoint().Address == b.Endpoint().Address || a.Key() == b.Key() || len(a.Key()) != 64 {
		t.Fatal("address/key allocation collision")
	}
	payload := bytes.Repeat([]byte{0, 1, 2, 128, 255, 10, 13}, 32768)
	errors := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- exchange(a.Endpoint().Address, payload) }()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	if !f.s.CloseTunnel(a.Endpoint().ID) {
		t.Fatal("missing tunnel")
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SDK missed relay close notification")
	}
	if e := exchange(b.Endpoint().Address, []byte("independent")); e != nil {
		t.Fatal("other tunnel affected:", e)
	}
	b.Close()
	wait(t, func() bool { return len(f.s.TunnelViews()) == 0 })
}
func TestBrokerWrongKeyHostPinPermissionAndOwnership(t *testing.T) {
	f, opts := brokerFixture(t)
	bad := opts
	bad.AccessKey = strings.Repeat("0", 64)
	if c, e := tunnel.Open(context.Background(), bad); e == nil {
		c.Close()
		t.Fatal("wrong access key accepted")
	}
	bad = opts
	bad.Fingerprint = "SHA256:incorrect"
	if c, e := tunnel.Open(context.Background(), bad); e == nil {
		c.Close()
		t.Fatal("wrong host key accepted")
	}
	cfg := f.store.Config()
	cfg.Users[0].AllowTunnel = false
	if e := f.s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	if c, e := tunnel.Open(context.Background(), opts); e == nil {
		c.Close()
		t.Fatal("tunnel permission bypass")
	}
	cfg.Users[0].AllowTunnel = true
	if e := f.s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	opts.TunnelKey, _ = tunnel.NewKey()
	a := sdkOpen(t, opts)
	if c, e := tunnel.Open(context.Background(), opts); e == nil {
		c.Close()
		t.Fatal("duplicate key accepted")
	}
	intruder := f.client(t)
	payload, _ := json.Marshal(map[string]string{"id": a.Endpoint().ID})
	ok, _, e := intruder.SendRequest("tunnel-close@ontech.dev", true, payload)
	if e != nil || ok {
		t.Fatal("other connection closed tunnel", e)
	}
	if e = exchange(a.Endpoint().Address, []byte("still-owned")); e != nil {
		t.Fatal(e)
	}
}
func TestBrokerPortExhaustionAndReuse(t *testing.T) {
	f, opts := brokerFixture(t)
	cfg := f.store.Config()
	cfg.Broker.PortMax = cfg.Broker.PortMin
	if e := f.s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	opts.TunnelKey, _ = tunnel.NewKey()
	a := sdkOpen(t, opts)
	other := opts
	other.TunnelKey = ""
	if c, e := tunnel.Open(context.Background(), other); e == nil {
		c.Close()
		t.Fatal("allocated beyond configured port range")
	}
	a.Close()
	wait(t, func() bool { return len(f.s.TunnelViews()) == 0 })
	again := sdkOpen(t, opts)
	if again.Endpoint().Port != cfg.Broker.PortMin {
		t.Fatal("released port not reused")
	}
}
func TestBrokerCancellationClosesActiveStreamsAndPort(t *testing.T) {
	f, opts := brokerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, e := tunnel.Open(ctx, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	c, e := net.Dial("tcp", session.Endpoint().Address)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte("pending"))
	cancel()
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SDK cancellation blocked")
	}
	wait(t, func() bool { return len(f.s.TunnelViews()) == 0 })
	_, e = c.Read(make([]byte, 1024))
	if e == nil {
		t.Fatal("active stream still open")
	}
	if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
		t.Fatal("stream did not close on cancellation")
	}
	if probe, e := net.DialTimeout("tcp", session.Endpoint().Address, 200*time.Millisecond); e == nil {
		probe.Close()
		t.Fatal("public port still listening")
	}
}
func TestBrokerNormalHTTPClientWithoutTunnelSoftware(t *testing.T) {
	f, opts := brokerFixture(t)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ordinary-server-response")) }))
	defer web.Close()
	opts.Target = strings.TrimPrefix(web.URL, "http://")
	session := sdkOpen(t, opts)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	r, e := client.Get("http://" + session.Endpoint().Address + "/hello")
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	if e != nil || string(b) != "ordinary-server-response" {
		t.Fatal("HTTP relay failed", e)
	}
	if len(f.s.TunnelViews()) != 1 {
		t.Fatal("missing visible tunnel")
	}
}
func TestBrokerLegacyConfigMigration(t *testing.T) {
	dir := t.TempDir()
	s, e := NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	cfg := s.Config()
	s.Close()
	b, _ := json.Marshal(cfg)
	var m map[string]any
	json.Unmarshal(b, &m)
	delete(m, "broker")
	b, _ = json.Marshal(m)
	if e = os.WriteFile(filepath.Join(dir, "server.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	s, e = NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Config().Broker != DefaultBrokerConfig() {
		t.Fatal("legacy configuration did not receive defaults")
	}
}
func TestBrokerAdminKeyIssueRotateAndRedaction(t *testing.T) {
	f, _ := brokerFixture(t)
	token, _ := os.ReadFile(filepath.Join(f.store.Dir, "admin.token"))
	handler := f.s.AdminHandler()
	call := func(method, path string, input any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(input)
		r := httptest.NewRequest(method, "http://"+f.store.Config().AdminListen+path, bytes.NewReader(b))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+string(token))
		r.Header.Set("X-SSHTunnel", "1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := call("POST", "/api/agents", map[string]any{"name": "app-api", "maxConnections": 2, "revision": revision(f.store.Config())})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var issued map[string]string
	if e := json.Unmarshal(w.Body.Bytes(), &issued); e != nil {
		t.Fatal(e)
	}
	old := issued["accessKey"]
	u, _ := f.store.User("app-api")
	if !CheckAccessKey(u.AccessKeyHash, old) || u.AllowLocal || u.AllowReverse || !u.AllowTunnel {
		t.Fatal("wrong key or excessive default permissions")
	}
	w = call("GET", "/api/config", nil)
	if strings.Contains(w.Body.String(), old) || strings.Contains(w.Body.String(), u.AccessKeyHash) {
		t.Fatal("key/hash leaked in config")
	}
	var safe struct {
		Config   Config `json:"config"`
		Revision string `json:"revision"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &safe); e != nil {
		t.Fatal(e)
	}
	w = call("PUT", "/api/config", map[string]any{"config": safe.Config, "revision": safe.Revision})
	if w.Code != 200 {
		t.Fatal("saving redacted config lost credentials", w.Body.String())
	}
	u, _ = f.store.User("app-api")
	if !CheckAccessKey(u.AccessKeyHash, old) {
		t.Fatal("key not preserved")
	}
	w = call("POST", "/api/users/app-api/access-key", map[string]string{"revision": revision(f.store.Config())})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &issued)
	u, _ = f.store.User("app-api")
	if CheckAccessKey(u.AccessKeyHash, old) || !CheckAccessKey(u.AccessKeyHash, issued["accessKey"]) {
		t.Fatal("rotation did not revoke old key")
	}
	log, _ := os.ReadFile(filepath.Join(f.store.Dir, "events.jsonl"))
	if strings.Contains(string(log), old) || strings.Contains(string(log), issued["accessKey"]) {
		t.Fatal("secret logged")
	}
}
