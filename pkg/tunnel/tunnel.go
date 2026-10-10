// Package tunnel은 서버 프로그램이 중계 서버에 먼저 연결하여 공개 TCP 주소를
// 발급받게 합니다. 외부 클라이언트는 일반 TCP만 사용합니다.
package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Options의 AccessKey는 관리 화면에서 발급한 인증키입니다.
// TunnelKey는 터널 식별용 키이며 비우면 Open이 임의 생성합니다.
// Fingerprint는 관리 화면의 SSH 호스트 지문(SHA256:...)입니다.
type Options struct {
	Server         string
	User           string
	AccessKey      string
	Fingerprint    string
	Target         string
	Name           string
	TunnelKey      string
	Protocol       string // "http": 랜덤 웹 주소, "tcp" 또는 빈 값: 기존 포트 방식
	MaxConnections int
}
type Endpoint struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
	URL      string `json:"url,omitempty"`
}
type Session struct {
	endpoint             Endpoint
	key, target          string
	client               *ssh.Client
	ctx                  context.Context
	cancel               context.CancelFunc
	stopOnce             sync.Once
	done                 chan struct{}
	slots                chan struct{}
	mu                   sync.Mutex
	err, lastStreamError error
}

func NewKey() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func validKey(s string) bool {
	b, e := hex.DecodeString(s)
	return len(s) == 64 && e == nil && len(b) == 32 && s == strings.ToLower(s)
}

// Open은 SSH 암호화 연결 안에서 터널 개설 API를 호출하고 접속 주소를 반환합니다.
// 반환된 Session은 Target으로 데이터를 중계합니다. ctx 취소 또는 Close 시
// 공개 주소와 활성 연결을 모두 닫습니다. 연결 손실 후에는 새 Open이 필요합니다.
func Open(ctx context.Context, opts Options) (*Session, error) {
	if opts.Protocol == "" {
		opts.Protocol = "tcp"
	}
	if opts.Protocol != "tcp" && opts.Protocol != "http" {
		return nil, errors.New("Protocol must be tcp or http")
	}
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if _, _, e := net.SplitHostPort(opts.Server); e != nil {
		return nil, fmt.Errorf("invalid relay address: %w", e)
	}
	host, port, e := net.SplitHostPort(opts.Target)
	if e != nil || host == "" || port == "" || port == "0" {
		return nil, errors.New("Target must be host:port of the existing TCP server")
	}
	if opts.User == "" || !validKey(opts.AccessKey) {
		return nil, errors.New("User and a 64-character AccessKey are required")
	}
	if !strings.HasPrefix(opts.Fingerprint, "SHA256:") {
		return nil, errors.New("relay SHA256 host fingerprint is required")
	}
	if opts.TunnelKey == "" {
		opts.TunnelKey, e = NewKey()
		if e != nil {
			return nil, e
		}
	}
	if !validKey(opts.TunnelKey) {
		return nil, errors.New("TunnelKey must be 64 lowercase hex characters")
	}
	if opts.MaxConnections == 0 {
		opts.MaxConnections = 64
	}
	if opts.MaxConnections < 1 || opts.MaxConnections > 1024 {
		return nil, errors.New("MaxConnections must be 1..1024")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	raw, e := dialer.DialContext(ctx, "tcp", opts.Server)
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			raw.Close()
		}
	}()
	stopOpening := context.AfterFunc(ctx, func() { raw.Close() })
	defer stopOpening()
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	cfg := &ssh.ClientConfig{User: opts.User, Auth: []ssh.AuthMethod{ssh.Password(opts.AccessKey)}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if subtle.ConstantTimeCompare([]byte(actual), []byte(opts.Fingerprint)) != 1 {
			return errors.New("relay host fingerprint mismatch")
		}
		return nil
	}}
	connection, channels, requests, e := ssh.NewClientConn(raw, opts.Server, cfg)
	if e != nil {
		return nil, e
	}
	// 전역 요청을 직접 처리하여 종료 알림과 keepalive를 수신합니다.
	ignored := make(chan *ssh.Request)
	close(ignored)
	client := ssh.NewClient(connection, channels, ignored)
	incoming := client.HandleChannelOpen("tunnel-connection@ontech.dev")
	request := map[string]string{"key": opts.TunnelKey, "name": opts.Name}
	if opts.Protocol == "http" {
		request["protocol"] = "http"
	}
	payload, _ := json.Marshal(request)
	ok, reply, e := client.SendRequest("tunnel-open@ontech.dev", true, payload)
	if e != nil {
		client.Close()
		return nil, e
	}
	if !ok {
		client.Close()
		var response struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(reply, &response)
		if response.Error == "" {
			response.Error = "relay refused tunnel request"
		}
		return nil, errors.New(response.Error)
	}
	var endpoint Endpoint
	if e = json.Unmarshal(reply, &endpoint); e != nil || endpoint.ID == "" || endpoint.Host == "" {
		client.Close()
		return nil, errors.New("invalid tunnel endpoint response")
	}
	if opts.Protocol == "http" {
		u, err := url.Parse(endpoint.URL)
		if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != endpoint.Host || u.Port() != "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || endpoint.Protocol != "http" || endpoint.Port != 0 || endpoint.Address != endpoint.Host {
			client.Close()
			return nil, errors.New("invalid web endpoint response")
		}
	} else {
		if _, _, e = net.SplitHostPort(endpoint.Address); e != nil || endpoint.Port < 1 || endpoint.Port > 65535 {
			client.Close()
			return nil, errors.New("invalid public address response")
		}
	}
	if e = ctx.Err(); e != nil {
		client.Close()
		return nil, e
	}
	_ = raw.SetDeadline(time.Time{})
	live, cancel := context.WithCancel(ctx)
	s := &Session{endpoint: endpoint, key: opts.TunnelKey, target: opts.Target, client: client, ctx: live, cancel: cancel, done: make(chan struct{}), slots: make(chan struct{}, opts.MaxConnections)}
	stopContext := context.AfterFunc(live, s.stop)
	go func() { defer stopContext(); s.loop(incoming, requests) }()
	success = true
	return s, nil
}
func (s *Session) Endpoint() Endpoint    { return s.endpoint }
func (s *Session) Key() string           { return s.key }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// LastStreamError는 로컬 대상 접속 실패 등 최근 개별 연결 오류입니다.
func (s *Session) LastStreamError() error { s.mu.Lock(); defer s.mu.Unlock(); return s.lastStreamError }
func (s *Session) setError(e error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = e
	}
}
func (s *Session) stop()        { s.stopOnce.Do(func() { s.cancel(); _ = s.client.Close() }) }
func (s *Session) Close() error { s.stop(); <-s.done; return nil }
func (s *Session) Wait() error  { <-s.done; return s.Err() }
func (s *Session) loop(incoming <-chan ssh.NewChannel, requests <-chan *ssh.Request) {
	defer close(s.done)
	requestDone := make(chan struct{})
	go func() { defer close(requestDone); s.requests(requests) }()
	keepaliveDone := make(chan struct{})
	go func() { defer close(keepaliveDone); s.keepalive() }()
	var workers sync.WaitGroup
	for ch := range incoming {
		if s.ctx.Err() != nil {
			_ = ch.Reject(ssh.Prohibited, "tunnel closed")
			continue
		}
		select {
		case s.slots <- struct{}{}:
			workers.Add(1)
			go func(ch ssh.NewChannel) { defer workers.Done(); defer func() { <-s.slots }(); s.forward(ch) }(ch)
		default:
			_ = ch.Reject(ssh.ResourceShortage, "local connection limit")
		}
	}
	if s.ctx.Err() == nil {
		e := s.client.Wait()
		if e == nil {
			e = io.EOF
		}
		s.setError(e)
	}
	s.stop()
	workers.Wait()
	<-requestDone
	<-keepaliveDone
}
func (s *Session) requests(requests <-chan *ssh.Request) {
	for r := range requests {
		switch r.Type {
		case "keepalive@openssh.com":
			_ = r.Reply(true, nil)
		case "tunnel-closed@ontech.dev":
			var msg struct {
				ID string `json:"id"`
			}
			if len(r.Payload) < 4096 && json.Unmarshal(r.Payload, &msg) == nil && msg.ID == s.endpoint.ID {
				_ = r.Reply(true, nil)
				s.setError(errors.New("relay closed the tunnel"))
				s.stop()
			} else {
				_ = r.Reply(false, nil)
			}
		default:
			_ = r.Reply(false, nil)
		}
	}
}
func (s *Session) keepalive() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			done := make(chan error, 1)
			go func() { _, _, e := s.client.SendRequest("keepalive@openssh.com", true, nil); done <- e }()
			timer := time.NewTimer(10 * time.Second)
			select {
			case e := <-done:
				timer.Stop()
				if e != nil {
					s.setError(e)
					s.stop()
					return
				}
			case <-timer.C:
				s.setError(errors.New("relay keepalive timeout"))
				s.stop()
				return
			case <-s.ctx.Done():
				timer.Stop()
				return
			}
		}
	}
}
func (s *Session) forward(incoming ssh.NewChannel) {
	var meta struct {
		TunnelID string `json:"tunnelId"`
		Origin   string `json:"origin"`
	}
	if len(incoming.ExtraData()) > 4096 || json.Unmarshal(incoming.ExtraData(), &meta) != nil || meta.TunnelID != s.endpoint.ID {
		_ = incoming.Reject(ssh.Prohibited, "unknown tunnel")
		return
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	tcp, e := dialer.DialContext(s.ctx, "tcp", s.target)
	if e != nil {
		s.mu.Lock()
		s.lastStreamError = fmt.Errorf("local target: %w", e)
		s.mu.Unlock()
		_ = incoming.Reject(ssh.ConnectionFailed, "local target unavailable")
		return
	}
	defer tcp.Close()
	channel, requests, e := incoming.Accept()
	if e != nil {
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	stop := context.AfterFunc(s.ctx, func() { tcp.Close(); channel.Close() })
	defer stop()
	done := make(chan error, 2)
	go func() {
		_, e := io.Copy(channel, tcp)
		if e == nil {
			e = channel.CloseWrite()
		}
		done <- e
	}()
	go func() {
		_, e := io.Copy(tcp, channel)
		if e == nil {
			e = tcp.(*net.TCPConn).CloseWrite()
		}
		done <- e
	}()
	if e := <-done; e != nil {
		tcp.Close()
		channel.Close()
	}
	<-done
}
