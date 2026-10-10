//go:build windows

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"ontech/sshtunnel/internal/server"
)

func TestTrayNativeABI(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("Windows amd64 ABI")
	}
	if unsafe.Sizeof(notificationIcon{}) != 976 || unsafe.Sizeof(windowClass{}) != 80 || unsafe.Sizeof(winMessage{}) != 48 {
		t.Fatal("Win32 native structure layout mismatch")
	}
}
func TestAdminURLKeepsTokenInFragmentAndUsesRunningAddress(t *testing.T) {
	dir := t.TempDir()
	s, e := server.NewStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	text, e := adminURL(dir, "127.0.0.1:17777")
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(text)
	if e != nil {
		t.Fatal(e)
	}
	token, _ := os.ReadFile(filepath.Join(dir, "admin.token"))
	if u.Host != "127.0.0.1:17777" || u.RawQuery != "" || u.Fragment != "token="+string(token) {
		t.Fatal("wrong running address or token exposed in request URL")
	}
	if _, e = adminURL(dir, "0.0.0.0:17893"); e == nil {
		t.Fatal("non-loopback management allowed")
	}
	if e = os.WriteFile(filepath.Join(dir, "admin.token"), []byte("invalid-token"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = adminURL(dir, ""); e == nil {
		t.Fatal("malformed token allowed")
	}
}
