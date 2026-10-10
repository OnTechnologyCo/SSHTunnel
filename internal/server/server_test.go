package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().String()
}

type fixture struct {
	s       *Server
	store   *Store
	key     ssh.Signer
	private ed25519.PrivateKey
}

func setup(t *testing.T) fixture {
	t.Helper()
	store, e := NewStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(key)
	if e != nil {
		t.Fatal(e)
	}
	c := store.Config()
	c.Listen = freePort(t)
	c.AdminListen = freePort(t)
	c.Users = []User{{Name: "tunnel", Enabled: true, PublicKeys: []string{strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))}, AllowLocal: true, AllowReverse: true, Targets: []string{"*:*"}, ListenPorts: []string{"*"}, MaxConnections: 8}}
	if e = store.Save(c); e != nil {
		t.Fatal(e)
	}
	s := New(store)
	if e = s.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); store.Close() })
	return fixture{s, store, signer, key}
}
func (f fixture) client(t *testing.T) *ssh.Client {
	t.Helper()
	c, e := ssh.Dial("tcp", f.s.Addr(), &ssh.ClientConfig{User: "tunnel", Auth: []ssh.AuthMethod{ssh.PublicKeys(f.key)}, HostKeyCallback: ssh.FixedHostKey(f.store.Signer.PublicKey()), Timeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func halfServer(t *testing.T) net.Listener {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				b, _ := io.ReadAll(c)
				time.Sleep(35 * time.Millisecond)
				c.Write(append([]byte("reply:"), b...))
			}()
		}
	}()
	return l
}
func wait(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timeout")
}

func TestLocalForwardHalfCloseAndIndependentConnections(t *testing.T) {
	f := setup(t)
	target := halfServer(t)
	a := f.client(t)
	b := f.client(t)
	for _, client := range []*ssh.Client{a, b} {
		stream, e := client.Dial("tcp", target.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		stream.Write([]byte("data"))
		if e = stream.(interface{ CloseWrite() error }).CloseWrite(); e != nil {
			t.Fatal(e)
		}
		reply, e := io.ReadAll(stream)
		stream.Close()
		if e != nil || string(reply) != "reply:data" {
			t.Fatalf("half-close: %q %v", reply, e)
		}
	}
	wait(t, func() bool { return len(f.s.Views()) == 2 })
	var victim string
	for _, v := range f.s.Views() {
		if v.Remote == a.LocalAddr().String() {
			victim = v.ID
		}
	}
	if !f.s.Disconnect(victim) {
		t.Fatal("target connection not found")
	}
	stream, e := b.Dial("tcp", target.Addr().String())
	if e != nil {
		t.Fatal("other connection affected:", e)
	}
	stream.Close()
}
func TestReverseForwardDynamicPortAndHalfClose(t *testing.T) {
	f := setup(t)
	client := f.client(t)
	l, e := client.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		b, e := io.ReadAll(c)
		if e == nil {
			_, e = c.Write(append([]byte("reverse:"), b...))
			if e == nil {
				e = c.(interface{ CloseWrite() error }).CloseWrite()
			}
		}
		done <- e
	}()
	c, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte("test"))
	c.(*net.TCPConn).CloseWrite()
	b, e := io.ReadAll(c)
	if e != nil || string(b) != "reverse:test" {
		t.Fatalf("reverse response %q %v", b, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	l.Close()
	wait(t, func() bool { v := f.s.Views(); return len(v) == 1 && v[0].ReverseListeners == 0 })
}
func TestRejectShellWrongKeyAndPublicReverse(t *testing.T) {
	f := setup(t)
	client := f.client(t)
	if session, e := client.NewSession(); e == nil {
		session.Close()
		t.Fatal("shell allowed")
	}
	if l, e := client.Listen("tcp", "0.0.0.0:0"); e == nil {
		l.Close()
		t.Fatal("public reverse allowed")
	}
	_, wrong, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(wrong)
	if c, e := ssh.Dial("tcp", f.s.Addr(), &ssh.ClientConfig{User: "tunnel", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(f.store.Signer.PublicKey()), Timeout: 3 * time.Second}); e == nil {
		c.Close()
		t.Fatal("wrong key accepted")
	}
}
func TestDestinationAndReversePolicies(t *testing.T) {
	f := setup(t)
	target := halfServer(t)
	cfg := f.store.Config()
	cfg.Users[0].Targets = []string{"127.0.0.0/8:1"}
	cfg.Users[0].ListenPorts = []string{"20000-21000"}
	if e := f.s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	client := f.client(t)
	if c, e := client.Dial("tcp", target.Addr().String()); e == nil {
		c.Close()
		t.Fatal("target policy bypass")
	}
	if l, e := client.Listen("tcp", "127.0.0.1:0"); e == nil {
		l.Close()
		t.Fatal("port policy bypass")
	}
	if !matchTarget([]string{"127.0.0.0/8:80"}, "localhost", net.ParseIP("127.0.0.1"), 80) || matchTarget([]string{"127.0.0.0/8:80"}, "localhost", net.ParseIP("10.0.0.1"), 80) {
		t.Fatal("CIDR policy")
	}
}
func TestPersistentIdentityAndClose(t *testing.T) {
	f := setup(t)
	client := f.client(t)
	fingerprint := f.s.Fingerprint()
	token, _ := os.ReadFile(filepath.Join(f.store.Dir, "admin.token"))
	if e := f.s.SetOpen(false); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- client.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not disconnect")
	}
	f.s.Close()
	f.store.Close()
	second, e := NewStore(f.store.Dir)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if second.Config().Open || ssh.FingerprintSHA256(second.Signer.PublicKey()) != fingerprint || !second.AdminOK(strings.TrimSpace(string(token))) {
		t.Fatal("state or identity not persisted")
	}
}
func TestAdminHostOriginAuthAndSecretRedaction(t *testing.T) {
	f := setup(t)
	token, _ := os.ReadFile(filepath.Join(f.store.Dir, "admin.token"))
	handler := f.s.AdminHandler()
	call := func(path, host, auth, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://"+f.store.Config().AdminListen+path, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Host = host
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	host := f.store.Config().AdminListen
	if w := call("/api/config", host, "", ""); w.Code != 401 {
		t.Fatal("anonymous management")
	}
	if w := call("/api/config", "evil.example:17893", string(token), ""); w.Code != 403 {
		t.Fatal("Host allowed")
	}
	if w := call("/api/config", host, string(token), "https://evil.example"); w.Code != 403 {
		t.Fatal("Origin allowed")
	}
	if w := call("/api/config", host, string(token), ""); w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE KEY") || strings.Contains(w.Body.String(), string(token)) {
		t.Fatal("management leak or failure")
	}
	for i := 0; i < 75; i++ {
		call("/api/status", host, "incorrect", "")
	}
	if w := call("/api/status", host, string(token), ""); w.Code != 200 {
		t.Fatal("invalid token attempts blocked legitimate management")
	}
}
func TestPasswordAndConfigurationValidation(t *testing.T) {
	hash, e := NewPassword("correct-horse-secret")
	if e != nil || !CheckPassword(hash, "correct-horse-secret") || CheckPassword(hash, "wrong-password") {
		t.Fatal("password hash")
	}
	f := setup(t)
	cfg := f.store.Config()
	cfg.AdminListen = "0.0.0.0:17893"
	if ValidateConfig(cfg) == nil {
		t.Fatal("public admin allowed")
	}
	cfg = f.store.Config()
	cfg.Users[0].PublicKeys = []string{"command=\"cmd\" " + cfg.Users[0].PublicKeys[0]}
	if ValidateConfig(cfg) == nil {
		t.Fatal("authorized key option accepted")
	}
	cfg = f.store.Config()
	cfg.Users = append(cfg.Users, cfg.Users[0])
	if ValidateConfig(cfg) == nil {
		t.Fatal("duplicate account accepted")
	}
}
func TestChannelLimitAndCloseActiveStreams(t *testing.T) {
	f := setup(t)
	cfg := f.store.Config()
	cfg.MaxChannels = 1
	if e := f.s.Apply(cfg); e != nil {
		t.Fatal(e)
	}
	client := f.client(t)
	target := halfServer(t)
	a, e := client.Dial("tcp", target.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if b, e := client.Dial("tcp", target.Addr().String()); e == nil {
		b.Close()
		t.Fatal("channel cap exceeded")
	}
	if e = f.s.SetOpen(false); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := a.Read(make([]byte, 1)); done <- e }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("active stream not closed")
	}
}
func TestConfigAPIRevisionAndPasswordUpdate(t *testing.T) {
	f := setup(t)
	token, _ := os.ReadFile(filepath.Join(f.store.Dir, "admin.token"))
	cfg := f.store.Config()
	rev := revision(cfg)
	payload, _ := json.Marshal(map[string]any{"config": cfg, "revision": rev, "passwords": map[string]string{"tunnel": "new-password-for-test"}})
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "http://"+cfg.AdminListen+"/api/config", bytes.NewReader(payload))
		r.RemoteAddr = "127.0.0.1:1000"
		r.Header.Set("Authorization", "Bearer "+string(token))
		r.Header.Set("X-SSHTunnel", "1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.s.AdminHandler().ServeHTTP(w, r)
		return w
	}
	if w := call(); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	u, _ := f.store.User("tunnel")
	if !CheckPassword(u.PasswordHash, "new-password-for-test") {
		t.Fatal("password not updated")
	}
	if w := call(); w.Code != 409 {
		t.Fatal("stale config not rejected")
	}
}

func TestStandardOpenSSHSOCKS5(t *testing.T) {
	sshPath, e := exec.LookPath("ssh")
	if e != nil {
		t.Skip("OpenSSH client not installed")
	}
	f := setup(t)
	target := halfServer(t)
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "client.pem")
	block, e := ssh.MarshalPrivateKey(f.private, "integration test")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); e != nil {
		t.Fatal(e)
	}
	// OpenSSH의 Windows 키 검사에는 현재 사용자·SYSTEM·Administrators만 허용합니다.
	if runtime.GOOS == "windows" {
		if e = privateClientKey(keyPath); e != nil {
			t.Fatal(e)
		}
	}
	_, port, _ := net.SplitHostPort(f.s.Addr())
	known := filepath.Join(dir, "known_hosts")
	line := "[127.0.0.1]:" + port + " " + string(ssh.MarshalAuthorizedKey(f.store.Signer.PublicKey()))
	os.WriteFile(known, []byte(line), 0600)
	local := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, sshPath, "-N", "-T", "-F", os.DevNull, "-i", keyPath, "-p", port, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+known, "-o", "ExitOnForwardFailure=yes", "-D", local, "tunnel@127.0.0.1")
	var output bytes.Buffer
	cmd.Stderr = &output
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cancel()
		cmd.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	}()
	var conn net.Conn
	wait(t, func() bool {
		var err error
		conn, err = net.DialTimeout("tcp", local, 100*time.Millisecond)
		return err == nil
	})
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte{5, 1, 0})
	b := make([]byte, 2)
	if _, e = io.ReadFull(conn, b); e != nil || !bytes.Equal(b, []byte{5, 0}) {
		t.Fatal("SOCKS greeting", e)
	}
	dst := target.Addr().(*net.TCPAddr)
	request := []byte{5, 1, 0, 1, 127, 0, 0, 1, byte(dst.Port >> 8), byte(dst.Port)}
	conn.Write(request)
	b = make([]byte, 10)
	if _, e = io.ReadFull(conn, b); e != nil || b[1] != 0 {
		t.Fatal("SOCKS connect", e, b)
	}
	conn.Write([]byte("socks"))
	conn.(*net.TCPConn).CloseWrite()
	reply, e := io.ReadAll(conn)
	if e != nil || string(reply) != "reply:socks" {
		t.Fatalf("SOCKS half-close: %q %v", reply, e)
	}
}

func TestDataDirectoryExclusiveLock(t *testing.T) {
	f := setup(t)
	if second, e := NewStore(f.store.Dir); e == nil {
		second.Close()
		t.Fatal("duplicate data directory allowed")
	}
}
func TestCorruptHostKeyFailsWithoutReplacement(t *testing.T) {
	dir := t.TempDir()
	s, e := NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	path := filepath.Join(dir, "host_ed25519.pem")
	if e = os.WriteFile(path, []byte("broken-key"), 0600); e != nil {
		t.Fatal(e)
	}
	if next, e := NewStore(dir); e == nil {
		next.Close()
		t.Fatal("corrupt key accepted")
	}
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "broken-key" {
		t.Fatal("existing key overwritten")
	}
}
