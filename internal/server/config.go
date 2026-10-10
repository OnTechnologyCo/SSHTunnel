package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

const Version = "1.2.0"

type User struct {
	Name               string   `json:"name"`
	Enabled            bool     `json:"enabled"`
	PublicKeys         []string `json:"publicKeys"`
	PasswordHash       string   `json:"passwordHash,omitempty"`
	AccessKeyHash      string   `json:"accessKeyHash,omitempty"`
	AllowTunnel        bool     `json:"allowTunnel"`
	AllowLocal         bool     `json:"allowLocal"`
	AllowReverse       bool     `json:"allowReverse"`
	AllowPublicReverse bool     `json:"allowPublicReverse"`
	Targets            []string `json:"targets"`
	ListenPorts        []string `json:"listenPorts"`
	MaxConnections     int      `json:"maxConnections"`
}
type Config struct {
	Version        int          `json:"version"`
	Listen         string       `json:"listen"`
	AdminListen    string       `json:"adminListen"`
	Open           bool         `json:"open"`
	MaxConnections int          `json:"maxConnections"`
	MaxChannels    int          `json:"maxChannelsPerConnection"`
	MaxForwards    int          `json:"maxReverseListenersPerConnection"`
	Users          []User       `json:"users"`
	Broker         BrokerConfig `json:"broker"`
}
type Store struct {
	mu        sync.RWMutex
	Dir       string
	config    Config
	Signer    ssh.Signer
	adminHash [32]byte
	lock      *os.File
	closeOnce sync.Once
}

func DefaultConfig() Config {
	return Config{Version: 1, Listen: "0.0.0.0:2222", AdminListen: "127.0.0.1:17893", Open: true, MaxConnections: 256, MaxChannels: 64, MaxForwards: 16, Users: []User{}, Broker: DefaultBrokerConfig()}
}
func NewStore(dir string) (*Store, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = PrivateDir(dir); err != nil {
		return nil, err
	}
	lock, err := lockData(filepath.Join(dir, "server.lock"))
	if err != nil {
		return nil, fmt.Errorf("데이터 폴더를 사용할 수 없습니다. 이미 실행 중인지 확인하세요: %w", err)
	}
	s := &Store{Dir: dir, config: DefaultConfig(), lock: lock}
	success := false
	defer func() {
		if !success {
			s.Close()
		}
	}()
	path := filepath.Join(dir, "server.json")
	if data, e := readPrivate(path, 2<<20); e == nil {
		if err = decode(data, &s.config); err != nil {
			return nil, fmt.Errorf("server.json: %w", err)
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if err = ValidateConfig(s.config); err != nil {
		return nil, err
	}
	if _, err = os.Stat(path); os.IsNotExist(err) {
		if err = s.saveLocked(s.config); err != nil {
			return nil, err
		}
	}
	keyPath := filepath.Join(dir, "host_ed25519.pem")
	key, err := readPrivate(keyPath, 64<<10)
	if os.IsNotExist(err) {
		_, private, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return nil, e
		}
		der, e := x509.MarshalPKCS8PrivateKey(private)
		if e != nil {
			return nil, e
		}
		key = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		err = writeNew(keyPath, key)
	}
	if err != nil {
		return nil, err
	}
	s.Signer, err = ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, errors.New("저장된 SSH 호스트 키를 읽을 수 없습니다. 원본 파일을 보존합니다")
	}
	tokenPath := filepath.Join(dir, "admin.token")
	token, err := readPrivate(tokenPath, 256)
	if os.IsNotExist(err) {
		token = []byte(secret())
		err = writeNew(tokenPath, token)
	}
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(string(token))
	if len(value) != 64 {
		return nil, errors.New("관리 토큰 형식이 올바르지 않습니다")
	}
	if _, err = hex.DecodeString(value); err != nil {
		return nil, err
	}
	s.adminHash = sha256.Sum256([]byte(value))
	success = true
	return s, nil
}
func (s *Store) Close() {
	s.closeOnce.Do(func() {
		if s.lock != nil {
			_ = s.lock.Close()
		}
	})
}
func secret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func readPrivate(path string, max int64) ([]byte, error) {
	f, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !f.Mode().IsRegular() || f.Size() > max {
		return nil, errors.New("일반 파일이 아니거나 크기 제한을 초과했습니다")
	}
	if err = PrivateFile(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	e := f.Close()
	if err == nil {
		err = e
	}
	return err
}
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("JSON 문서 뒤에 추가 데이터가 있습니다")
	}
	return nil
}
func (s *Store) saveLocked(c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir, ".config-"+secret()+".tmp")
	if err = writeNew(tmp, append(b, '\n')); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err = atomicReplace(tmp, filepath.Join(s.Dir, "server.json")); err != nil {
		return err
	}
	s.config = c
	return nil
}
func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.config)
	var c Config
	_ = json.Unmarshal(b, &c)
	return c
}
func (s *Store) Save(c Config) error {
	if err := ValidateConfig(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(c)
}
func (s *Store) SetOpen(open bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.config
	c.Open = open
	return s.saveLocked(c)
}
func (s *Store) User(name string) (User, bool) {
	for _, u := range s.Config().Users {
		if u.Name == name && u.Enabled {
			return u, true
		}
	}
	return User{}, false
}
func (s *Store) AdminOK(token string) bool {
	if len(token) != 64 {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(hash[:], s.adminHash[:]) == 1
}
func NewPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 72 {
		return "", errors.New("암호는 UTF-8 기준 12~72바이트여야 합니다")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(b), err
}
func CheckPassword(hash, password string) bool {
	return len(password) <= 72 && hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

var userName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

func ValidateConfig(c Config) error {
	if c.Version != 1 {
		return errors.New("지원하지 않는 설정 버전입니다")
	}
	for _, addr := range []string{c.Listen, c.AdminListen} {
		host, port, e := net.SplitHostPort(addr)
		if e != nil || host == "" || net.ParseIP(host) == nil || !validPort(port, false) {
			return errors.New("수신 주소는 IP:포트 형식이어야 합니다")
		}
	}
	host, _, _ := net.SplitHostPort(c.AdminListen)
	if !net.ParseIP(host).IsLoopback() {
		return errors.New("관리 화면은 루프백 주소만 허용합니다")
	}
	if c.Listen == c.AdminListen {
		return errors.New("SSH와 관리 화면 주소는 달라야 합니다")
	}
	if c.MaxConnections < 1 || c.MaxConnections > 4096 || c.MaxChannels < 1 || c.MaxChannels > 1024 || c.MaxForwards < 1 || c.MaxForwards > 256 || len(c.Users) > 256 {
		return errors.New("연결 또는 계정 수 제한을 확인하세요")
	}
	if err := validateBroker(c.Broker); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, u := range c.Users {
		if !userName.MatchString(u.Name) || seen[u.Name] || u.MaxConnections < 1 || u.MaxConnections > c.MaxConnections {
			return errors.New("계정 이름·중복·동시 연결 수를 확인하세요")
		}
		seen[u.Name] = true
		if len(u.PublicKeys) > 32 || len(u.Targets) > 128 || len(u.ListenPorts) > 128 {
			return errors.New("계정 정책 항목이 너무 많습니다")
		}
		if u.Enabled && len(u.PublicKeys) == 0 && u.PasswordHash == "" && u.AccessKeyHash == "" {
			return fmt.Errorf("%s: 공개키·암호·API 인증키 중 하나가 필요합니다", u.Name)
		}
		if u.AccessKeyHash != "" && !validSecret(u.AccessKeyHash) {
			return errors.New("API 인증키 해시 형식이 올바르지 않습니다")
		}
		if u.PasswordHash != "" {
			cost, e := bcrypt.Cost([]byte(u.PasswordHash))
			if e != nil || cost < 10 || cost > 14 {
				return errors.New("암호 해시 형식 또는 비용이 올바르지 않습니다")
			}
		}
		for _, line := range u.PublicKeys {
			if len(line) > 16384 {
				return errors.New("공개키가 너무 큽니다")
			}
			key, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(line))
			if e != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 {
				return errors.New("옵션 없는 SSH 공개키 한 줄을 입력하세요")
			}
			if _, ok := key.(*ssh.Certificate); ok {
				return errors.New("SSH 인증서 대신 일반 공개키를 등록하세요")
			}
		}
		for _, target := range u.Targets {
			if _, _, e := parseTarget(target); e != nil {
				return e
			}
		}
		for _, ports := range u.ListenPorts {
			if !validPortRule(ports) {
				return errors.New("Reverse 포트는 *, 포트 또는 시작-끝 형식입니다")
			}
		}
	}
	return nil
}
