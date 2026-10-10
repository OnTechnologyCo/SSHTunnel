//go:build !windows

package server

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func PrivateDir(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		if f, e := os.Lstat(p); e == nil && f.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic link data path refused")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	return os.Chmod(path, 0700)
}
func PrivateFile(path string) error       { return os.Chmod(path, 0600) }
func atomicReplace(from, to string) error { return os.Rename(from, to) }
func lockData(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
