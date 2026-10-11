package tunnel

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestIdentityConcurrentPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identities", "service.key")
	var wg sync.WaitGroup
	keys := make(chan string, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); k, e := LoadOrCreateKey(path); keys <- k; errs <- e }()
	}
	wg.Wait()
	close(keys)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ""
	for k := range keys {
		if !validKey(k) {
			t.Fatal("invalid identity")
		}
		if first == "" {
			first = k
		}
		if k != first {
			t.Fatal("concurrent callers received different keys")
		}
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || again != first {
		t.Fatal("identity not persisted")
	}
}
func TestIdentityCorruptionPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.key")
	for _, bad := range []string{"", "invalid", "ABCDEF"} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateKey(path); err == nil {
			t.Fatal("invalid key silently replaced")
		}
		b, _ := os.ReadFile(path)
		if string(b) != bad {
			t.Fatal("original overwritten")
		}
	}
}
func TestIdentityScopeAndEphemeral(t *testing.T) {
	opts := Options{Protocol: "http", Fingerprint: "SHA256:fixed", User: "one", Target: "127.0.0.1:9000", KeyDirectory: t.TempDir()}
	first, err := optionKey(opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.AccessKey = "changed"
	opts.Name = "renamed"
	opts.Server = "new.example:3333"
	if k, e := optionKey(opts); e != nil || k != first {
		t.Fatal("display/network/credential change lost identity")
	}
	opts.Identity = "my-service"
	stable, err := optionKey(opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Target = "127.0.0.1:9001"
	if k, e := optionKey(opts); e != nil || k != stable {
		t.Fatal("explicit identity lost on port change")
	}
	opts.User = "two"
	if k, e := optionKey(opts); e != nil || k == stable {
		t.Fatal("different account shared identity")
	}
	opts.Ephemeral = true
	a, _ := optionKey(opts)
	b, _ := optionKey(opts)
	if a == b {
		t.Fatal("ephemeral key reused")
	}
	opts.Ephemeral = false
	opts.Protocol = "tcp"
	a, _ = optionKey(opts)
	b, _ = optionKey(opts)
	if a == b {
		t.Fatal("TCP behavior changed")
	}
}
