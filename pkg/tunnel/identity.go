package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"ontech/sshtunnel/internal/securefile"
)

func optionKey(opts Options) (string, error) {
	if opts.Protocol != "http" || opts.Ephemeral {
		return NewKey()
	}
	identity := opts.Identity
	if identity == "" {
		identity = opts.Target
	}
	if len(identity) > 256 || strings.IndexFunc(identity, unicode.IsControl) >= 0 {
		return "", errors.New("Identity must be at most 256 bytes without control characters")
	}
	dir := opts.KeyDirectory
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "OnTech", "SSHTunnel", "identities")
	}
	// No IP, access credential or display name: changing those does not change identity.
	data, _ := json.Marshal([]string{opts.Fingerprint, opts.User, identity})
	hash := sha256.Sum256(data)
	return LoadOrCreateKey(filepath.Join(dir, hex.EncodeToString(hash[:])+".key"))
}

// LoadOrCreateKey atomically creates a private 256-bit identity file or reuses it.
// Use a dedicated private parent directory: permissions are restricted to its owner.
// A malformed/unreadable file is never replaced with a new identity.
func LoadOrCreateKey(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err = securefile.PrivateDir(filepath.Dir(path)); err != nil {
		return "", err
	}
	// Serialize concurrent first starts, including separate processes.
	var lock *os.File
	for attempt := 0; attempt < 50; attempt++ {
		lock, err = securefile.Lock(path + ".lock")
		if err == nil {
			break
		}
		if attempt < 49 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if err != nil {
		return "", fmt.Errorf("cannot lock tunnel identity: %w", err)
	}
	defer lock.Close()
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 128 {
			return "", errors.New("invalid tunnel identity file; original preserved")
		}
		if err = securefile.PrivateFile(path); err != nil {
			return "", err
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return "", e
		}
		key := strings.TrimSpace(string(data))
		if !validKey(key) {
			return "", errors.New("invalid tunnel identity key; restore its backup instead of deleting it")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	key, err := NewKey()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".identity-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = securefile.PrivateFile(tmp); err != nil {
		f.Close()
		return "", err
	}
	_, err = f.WriteString(key + "\n")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = securefile.AtomicReplace(tmp, path); err != nil {
		return "", err
	}
	return key, nil
}
