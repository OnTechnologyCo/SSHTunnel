package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ontech/sshtunnel/pkg/tunnel"
)

func waitWebOffline(t *testing.T, s *Server, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.brokerMu.Lock()
		_, ok := s.tunnels[id]
		s.brokerMu.Unlock()
		if !ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("relay did not release live connection")
}
func TestPersistentWebAddressReconnectRestartAndOwnership(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("persistent-service")) }))
	defer target.Close()
	s, opts := webFixture(t, target.URL)
	opts.Ephemeral = false
	opts.KeyDirectory = t.TempDir()
	opts.Identity = "service-a"
	first := sdkOpen(t, opts)
	before := first.Endpoint()
	key := first.Key()
	if _, err := tunnel.Open(context.Background(), opts); err == nil {
		t.Fatal("duplicate active identity accepted")
	}
	first.Close()
	waitWebOffline(t, s, before.ID)
	if got := s.WebAddressViews(); len(got) != 1 || got[0].Online {
		t.Fatalf("offline reservation missing: %+v", got)
	}
	second := sdkOpen(t, opts)
	if second.Endpoint().URL != before.URL || second.Key() != key {
		t.Fatal("identity changed on reconnect")
	}
	response := webRequest(t, s, before.Host, "GET", "/", nil, nil)
	if readWeb(t, response) != "persistent-service" {
		t.Fatal("reconnected route failed")
	}
	second.Close()
	waitWebOffline(t, s, before.ID)
	other := opts
	other.Identity = "service-b"
	third := sdkOpen(t, other)
	if third.Endpoint().URL == before.URL {
		t.Fatal("different services share address")
	}
	third.Close()
	waitWebOffline(t, s, third.Endpoint().ID)
	dir := s.Store.Dir
	s.Close()
	s.Store.Close()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(store)
	if err = restarted.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { restarted.Close(); store.Close() }()
	opts.Server = restarted.Addr()
	fourth := sdkOpen(t, opts)
	if fourth.Endpoint().URL != before.URL {
		t.Fatalf("restart changed address: %s -> %s", before.URL, fourth.Endpoint().URL)
	}
	fourth.Close()
	waitWebOffline(t, restarted, before.ID)
	// Knowing a different account's key does not transfer its reservation.
	if _, err = store.reserveWebAddress("other", hashSecret(key), "", nil); err == nil {
		t.Fatal("reservation transferred to another account")
	}
	// Access credential rotation and IP/port changes are not part of client identity.
	opts.AccessKey, _ = tunnel.NewKey()
	cfg := store.Config()
	cfg.Users[0].AccessKeyHash = hashSecret(opts.AccessKey)
	if err = store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	fifth := sdkOpen(t, opts)
	if fifth.Endpoint().URL != before.URL {
		t.Fatal("access credential rotation changed address")
	}
	fifth.Close()
	waitWebOffline(t, restarted, before.ID)
	data, err := os.ReadFile(filepath.Join(dir, webAddressesFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), key) {
		t.Fatal("plaintext ownership key persisted at relay")
	}
}
func TestWebAddressRotateOfflineAndRetainOldID(t *testing.T) {
	s, opts := webFixture(t, "http://127.0.0.1:9")
	opts.TunnelKey, _ = tunnel.NewKey()
	first := sdkOpen(t, opts)
	old := first.Endpoint().ID
	if _, err := s.rotateWebAddress(old); err == nil {
		t.Fatal("rotated online address")
	}
	first.Close()
	waitWebOffline(t, s, old)
	next, err := s.rotateWebAddress(old)
	if err != nil || next == old {
		t.Fatalf("rotation: %q %v", next, err)
	}
	again := sdkOpen(t, opts)
	if again.Endpoint().ID != next {
		t.Fatal("same key did not get rotated address")
	}
	if !s.Store.addresses[old].Retired || s.Store.addresses[old].KeyHash != "" {
		t.Fatal("old ID not retired")
	}
	if _, err = s.rotateWebAddress(old); err == nil {
		t.Fatal("stale rotation accepted")
	}
	b, _ := json.Marshal(s.WebAddressViews())
	if strings.Contains(string(b), hashSecret(opts.TunnelKey)) || strings.Contains(string(b), "keyHash") {
		t.Fatal("admin view leaked ownership hash")
	}
}
func TestWebAddressSaveFailureDoesNotAllocate(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// A directory at the destination fails atomic replacement on every supported OS.
	if err = os.Mkdir(filepath.Join(store.Dir, webAddressesFile), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = store.reserveWebAddress("web", hashSecret(secret()), "", nil); err == nil {
		t.Fatal("allocation survived failed persistence")
	}
	if len(store.addresses) != 0 {
		t.Fatal("failed write committed in memory")
	}
}
func TestWebAddressCorruptionIsNotOverwritten(t *testing.T) {
	for _, input := range []string{`{`, `{"version":2,"addresses":[]}`, `{"version":1,"addresses":[{"id":"abc123","user":"web","keyHash":"bad","created":"2026-10-11T00:00:00Z"}]}`} {
		t.Run(input[:1]+string(rune(len(input))), func(t *testing.T) {
			dir := t.TempDir()
			s, err := NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			path := filepath.Join(dir, webAddressesFile)
			if err = os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			if reopened, err := NewStore(dir); err == nil {
				reopened.Close()
				t.Fatal("corrupt reservation data accepted")
			}
			b, _ := os.ReadFile(path)
			if string(b) != input {
				t.Fatal("corrupt original changed")
			}
		})
	}
}
func TestConcurrentWebAddressReservationsAndLimit(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const n = 12
	ids := make(chan string, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, e := store.reserveWebAddress("web", hashSecret(secret()), "", nil)
			ids <- id
			errs <- e
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("duplicate reservation")
		}
		seen[id] = true
	}
	hash := hashSecret(secret())
	for i := 0; len(store.addresses) < maxUserWebReservations; i++ {
		id := fmtWebID(i)
		if _, exists := store.addresses[id]; exists {
			continue
		}
		store.addresses[id] = webReservation{ID: id, User: "web", Retired: true}
	}
	// A previously assigned address still works at quota; new ones fail.
	firstHash := ""
	firstID := ""
	for id, a := range store.addresses {
		if !a.Retired {
			firstHash = a.KeyHash
			firstID = id
			break
		}
	}
	if _, err = store.reserveWebAddress("web", hash, "", nil); err == nil {
		t.Fatal("quota ignored")
	}
	if id, err := store.reserveWebAddress("web", firstHash, "", nil); err != nil || id != firstID {
		t.Fatal("quota denied existing identity")
	}
}
func fmtWebID(n int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := []byte("000000")
	for i := 5; i >= 0; i-- {
		b[i] = digits[n%36]
		n /= 36
	}
	return string(b)
}

func TestWebAddressAdminAuthenticationAndRotation(t *testing.T) {
	s, opts := webFixture(t, "http://127.0.0.1:9")
	live := sdkOpen(t, opts)
	id := live.Endpoint().ID
	path := "/api/web-addresses/" + id + "/rotate"
	for _, request := range []struct{ path, method string }{{"/api/web-addresses", "GET"}, {path, "POST"}} {
		r := tlsAdminRequest(t, s, request.path, request.method, nil, false)
		if r.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", request.path, r.Code)
		}
	}
	if r := tlsAdminRequest(t, s, path, "POST", nil, true); r.Code != 409 {
		t.Fatal("online rotation accepted")
	}
	live.Close()
	waitWebOffline(t, s, id)
	r := tlsAdminRequest(t, s, path, "POST", nil, true)
	if r.Code != 200 {
		t.Fatalf("rotation failed: %d %s", r.Code, r.Body.String())
	}
	list := tlsAdminRequest(t, s, "/api/web-addresses", "GET", nil, true)
	if list.Code != 200 || strings.Contains(list.Body.String(), "keyHash") || strings.Contains(list.Body.String(), live.Key()) {
		t.Fatal("invalid reservation view")
	}
	if r := tlsAdminRequest(t, s, path, "POST", nil, true); r.Code != 409 {
		t.Fatal("stale mutation accepted")
	}
}
