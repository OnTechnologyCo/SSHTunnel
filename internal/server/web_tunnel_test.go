package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

func webFixture(t *testing.T, target string) (*Server, tunnel.Options) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := tunnel.NewKey()
	cfg := store.Config()
	cfg.Listen = freePort(t)
	cfg.AdminListen = freePort(t)
	cfg.Broker.Enabled = true
	cfg.Web = WebConfig{Enabled: true, Listen: freePort(t), BaseDomain: "tunnel.example.test", Scheme: "https", MaxBodyMB: 1}
	cfg.Users = []User{{Name: "web", Enabled: true, AllowTunnel: true, AccessKeyHash: hashSecret(key), MaxConnections: 8}}
	if err = store.Save(cfg); err != nil {
		store.Close()
		t.Fatal(err)
	}
	s := New(store)
	if err = s.Start(); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); store.Close() })
	return s, tunnel.Options{Server: s.Addr(), User: "web", AccessKey: key, Fingerprint: s.Fingerprint(), Target: strings.TrimPrefix(target, "http://"), Protocol: "http"}
}
func webRequest(t *testing.T, s *Server, host, method, path string, body io.Reader, headers http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+s.WebAddr()+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if headers != nil {
		req.Header = headers
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
func readWeb(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestWebTunnelRandomURLRoutingCookiesAndHeaders(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "session=one; Domain=.tunnel.example.test; Path=/; HttpOnly; Secure; SameSite=Lax")
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "http://"+r.Host+"/login")
			w.WriteHeader(302)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"host": r.Host, "path": r.URL.EscapedPath(), "query": r.URL.RawQuery, "message": string(b), "proto": r.Header.Get("X-Forwarded-Proto"), "for": r.Header.Get("X-Forwarded-For"), "auth": r.Header.Get("Authorization"), "cookie": r.Header.Get("Cookie"), "forwarded": r.Header.Get("Forwarded")})
	}))
	defer target.Close()
	s, opts := webFixture(t, target.URL)
	a := sdkOpen(t, opts)
	b := sdkOpen(t, opts)
	if a.Endpoint().URL == b.Endpoint().URL || a.Endpoint().Port != 0 || strings.Contains(a.Endpoint().Address, ":") || !strings.HasPrefix(a.Endpoint().URL, "https://") {
		t.Fatal("not a port-free random URL")
	}
	s.brokerMu.Lock()
	for _, l := range s.tunnels {
		if l.listener != nil {
			t.Error("web tunnel opened an OS listener")
		}
	}
	s.brokerMu.Unlock()
	headers := http.Header{"Authorization": {"Bearer application-token"}, "Cookie": {"session=old"}, "X-Forwarded-For": {"evil"}, "X-Forwarded-Proto": {"http"}, "Forwarded": {"for=evil"}}
	resp := webRequest(t, s, a.Endpoint().Host, "POST", "/api/a%2Fb?q=%ED%95%9C%EA%B8%80&x=1", strings.NewReader("메시지"), headers)
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || got["host"] != a.Endpoint().Host || got["path"] != "/api/a%2Fb" || got["query"] != "q=%ED%95%9C%EA%B8%80&x=1" || got["message"] != "메시지" || got["proto"] != "https" || strings.Contains(got["for"], "evil") || got["forwarded"] != "" || got["auth"] != "Bearer application-token" || got["cookie"] != "session=old" {
		t.Fatalf("routing/headers: %+v", got)
	}
	cookie := resp.Header.Get("Set-Cookie")
	if strings.Contains(strings.ToLower(cookie), "domain=") || !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "Secure") {
		t.Fatal("cookie isolation failed", cookie)
	}
	redirect := webRequest(t, s, a.Endpoint().Host, "GET", "/redirect", nil, nil)
	redirect.Body.Close()
	if redirect.Header.Get("Location") != a.Endpoint().URL+"/login" {
		t.Fatal("redirect scheme mismatch")
	}
	a.Close()
	wait(t, func() bool { return len(s.TunnelViews()) == 1 })
	closed := webRequest(t, s, a.Endpoint().Host, "GET", "/", nil, nil)
	closed.Body.Close()
	if closed.StatusCode != 404 {
		t.Fatal("closed host remained routable")
	}
	active := webRequest(t, s, b.Endpoint().Host, "GET", "/", nil, nil)
	active.Body.Close()
	if active.StatusCode != 200 {
		t.Fatal("other web tunnel was affected")
	}
}
func TestWebTunnelWebSocketUpgradeAndClose(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer conn.Close()
		hash := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(hash[:]))
		rw.Flush()
		frame := make([]byte, 11)
		if _, e = io.ReadFull(rw, frame); e != nil {
			return
		}
		for i := 6; i < len(frame); i++ {
			frame[i] ^= frame[2+(i-6)%4]
		}
		conn.Write(append([]byte{0x81, 5}, frame[6:]...))
		io.Copy(io.Discard, conn)
	}))
	defer target.Close()
	s, opts := webFixture(t, target.URL)
	session := sdkOpen(t, opts)
	conn, e := net.DialTimeout("tcp", s.WebAddr(), 3*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", session.Endpoint().Host)
	reader := bufio.NewReader(conn)
	resp, e := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != 101 || resp.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatal("upgrade failed", resp.Status)
	}
	frame := []byte{0x81, 0x85, 1, 2, 3, 4, 'h' ^ 1, 'e' ^ 2, 'l' ^ 3, 'l' ^ 4, 'o' ^ 1}
	conn.Write(frame)
	reply := make([]byte, 7)
	if _, e = io.ReadFull(reader, reply); e != nil || string(reply[2:]) != "hello" {
		t.Fatal("websocket echo failed", e, reply)
	}
	if !s.CloseTunnel(session.Endpoint().ID) {
		t.Fatal("close failed")
	}
	_, e = reader.ReadByte()
	if e == nil {
		t.Fatal("websocket still open")
	}
	if ne, ok := e.(net.Error); ok && ne.Timeout() {
		t.Fatal("websocket did not close")
	}
}
func TestWebTunnelStreamingAndClientCancellation(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer target.Close()
	s, opts := webFixture(t, target.URL)
	session := sdkOpen(t, opts)
	resp := webRequest(t, s, session.Endpoint().Host, "GET", "/events", nil, nil)
	first := make([]byte, 13)
	if _, e := io.ReadFull(resp.Body, first); e != nil || string(first) != "data: first\n\n" {
		t.Fatal("SSE not flushed", e)
	}
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not receive client cancellation")
	}
}
func TestWebTunnelRejectsUnknownHostsAndLargeBodies(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, e := io.Copy(io.Discard, r.Body)
		if e != nil {
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()
	s, opts := webFixture(t, target.URL)
	session := sdkOpen(t, opts)
	for _, host := range []string{"localhost", session.Endpoint().Host + ".evil", session.Endpoint().Host + ":9999", strings.Repeat("b", 32) + ".tunnel.example.test"} {
		resp := webRequest(t, s, host, "GET", "/", nil, nil)
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("host accepted", host)
		}
	}
	resp := webRequest(t, s, session.Endpoint().Host, "POST", "/upload", bytes.NewReader(make([]byte, 2<<20)), nil)
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatal("upload limit status", resp.StatusCode)
	}
	bad := opts
	bad.Protocol = "udp"
	if c, e := tunnel.Open(context.Background(), bad); e == nil {
		c.Close()
		t.Fatal("unsupported protocol accepted")
	}
}
func TestWebTunnelConfigChangeNeedsRestart(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer target.Close()
	s, opts := webFixture(t, target.URL)
	session := sdkOpen(t, opts)
	cfg := s.Store.Config()
	cfg.Web.BaseDomain = "next.example.test"
	if e := s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("configuration did not close old session")
	}
	if c, e := tunnel.Open(context.Background(), opts); e == nil {
		c.Close()
		t.Fatal("new domain used before listener restart")
	}
}
func TestWebTunnelLegacyDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	store, e := NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	cfg := store.Config()
	store.Close()
	raw, _ := json.Marshal(cfg)
	var m map[string]any
	json.Unmarshal(raw, &m)
	delete(m, "web")
	raw, _ = json.Marshal(m)
	os.WriteFile(filepath.Join(dir, "server.json"), raw, 0600)
	store, e = NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if store.Config().Web != DefaultWebConfig() {
		t.Fatal("missing web defaults")
	}
	for _, domain := range []string{"*.example.com", "127.0.0.1", "example.com:443", "https://example.com", "UPPER.example.com"} {
		c := DefaultWebConfig()
		c.Enabled = true
		c.BaseDomain = domain
		if validateWeb(c) == nil {
			t.Fatal("invalid base accepted", domain)
		}
	}
}
func TestWebTunnelListenerConflictRollsBackSSH(t *testing.T) {
	existing, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer existing.Close()
	store, e := NewStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	cfg := store.Config()
	cfg.Listen = freePort(t)
	cfg.Web = DefaultWebConfig()
	cfg.Web.Enabled = true
	cfg.Web.BaseDomain = "test.example"
	cfg.Web.Listen = existing.Addr().String()
	if e = store.Save(cfg); e != nil {
		t.Fatal(e)
	}
	s := New(store)
	defer s.Close()
	if e = s.Start(); e == nil {
		t.Fatal("overwrote an occupied port")
	}
	l, e := net.Listen("tcp", cfg.Listen)
	if e != nil {
		t.Fatal("SSH listener leaked after startup failure", e)
	}
	l.Close()
	conn, e := net.DialTimeout("tcp", existing.Addr().String(), time.Second)
	if e != nil {
		t.Fatal("existing listener changed")
	}
	conn.Close()
}
