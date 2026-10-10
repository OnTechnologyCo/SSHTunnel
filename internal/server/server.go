package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

type Event struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}
type ConnectionView struct {
	ID               string    `json:"id"`
	User             string    `json:"user"`
	Remote           string    `json:"remote"`
	Started          time.Time `json:"started"`
	Channels         int       `json:"channels"`
	ReverseListeners int       `json:"reverseListeners"`
	BytesUp          int64     `json:"bytesUp"`
	BytesDown        int64     `json:"bytesDown"`
}
type attempt struct {
	started time.Time
	count   int
}
type Server struct {
	Store         *Store
	started       time.Time
	ctx           context.Context
	cancel        context.CancelFunc
	listener      net.Listener
	active        Config
	open          atomic.Bool
	mu            sync.Mutex
	raw           map[net.Conn]bool
	peers         map[string]*peer
	attempts      map[string]attempt
	events        []Event
	logMu         sync.Mutex
	ops           sync.Mutex
	workers       sync.WaitGroup
	acceptDone    chan struct{}
	closeOnce     sync.Once
	passwordSlots chan struct{}
	brokerMu      sync.Mutex
	tunnels       map[string]*lease
}

func New(s *Store) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	v := &Server{Store: s, ctx: ctx, cancel: cancel, started: time.Now(), raw: map[net.Conn]bool{}, peers: map[string]*peer{}, attempts: map[string]attempt{}, acceptDone: make(chan struct{}), passwordSlots: make(chan struct{}, 4), tunnels: map[string]*lease{}}
	v.open.Store(s.Config().Open)
	return v
}
func (s *Server) Start() error {
	s.active = s.Store.Config()
	l, e := net.Listen("tcp", s.active.Listen)
	if e != nil {
		return e
	}
	s.listener = l
	s.event("server", "SSH 수신 시작: "+l.Addr().String())
	go s.accept()
	return nil
}
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}
func (s *Server) Fingerprint() string { return ssh.FingerprintSHA256(s.Store.Signer.PublicKey()) }
func (s *Server) event(kind, message string) {
	e := Event{time.Now().UTC(), kind, message}
	s.mu.Lock()
	s.events = append(s.events, e)
	if len(s.events) > 200 {
		s.events = s.events[len(s.events)-200:]
	}
	s.mu.Unlock()
	s.logMu.Lock()
	defer s.logMu.Unlock()
	p := filepath.Join(s.Store.Dir, "events.jsonl")
	if st, err := os.Stat(p); err == nil && st.Size() > 2<<20 {
		_ = os.Remove(p + ".1")
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		_ = json.NewEncoder(f).Encode(e)
		_ = f.Close()
	}
}
func (s *Server) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event{}, s.events...)
}
func (s *Server) admit(addr net.Addr) bool {
	host, _, e := net.SplitHostPort(addr.String())
	if e != nil {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, a := range s.attempts {
		if now.Sub(a.started) > time.Minute {
			delete(s.attempts, key)
		}
	}
	a, exists := s.attempts[host]
	if !exists {
		if len(s.attempts) >= 4096 {
			return false
		}
		a.started = now
	}
	if a.count >= 60 {
		return false
	}
	a.count++
	s.attempts[host] = a
	return len(s.raw)-len(s.peers) < 32 && len(s.raw) < s.Store.Config().MaxConnections+32
}
func (s *Server) accept() {
	defer close(s.acceptDone)
	for {
		c, e := s.listener.Accept()
		if e != nil {
			return
		}
		if !s.open.Load() || !s.admit(c.RemoteAddr()) {
			_ = c.Close()
			continue
		}
		s.mu.Lock()
		s.raw[c] = true
		s.mu.Unlock()
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			defer func() { _ = c.Close(); s.mu.Lock(); delete(s.raw, c); s.mu.Unlock() }()
			s.serve(c)
		}()
	}
}
func (s *Server) serve(raw net.Conn) {
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	algorithms := ssh.SupportedAlgorithms()
	cfg := &ssh.ServerConfig{ServerVersion: "SSH-2.0-OnTechTunnel_" + Version, MaxAuthTries: 4, Config: ssh.Config{KeyExchanges: algorithms.KeyExchanges, Ciphers: algorithms.Ciphers, MACs: algorithms.MACs}, PublicKeyAuthAlgorithms: algorithms.PublicKeyAuths}
	cfg.PublicKeyCallback = func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		u, ok := s.Store.User(meta.User())
		if !ok || !s.open.Load() {
			return nil, errors.New("authentication failed")
		}
		for _, line := range u.PublicKeys {
			allowed, _, _, _, e := ssh.ParseAuthorizedKey([]byte(line))
			if e == nil && bytes.Equal(allowed.Marshal(), key.Marshal()) {
				return &ssh.Permissions{}, nil
			}
		}
		return nil, errors.New("authentication failed")
	}
	cfg.PasswordCallback = func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		u, ok := s.Store.User(meta.User())
		if !ok || !s.open.Load() {
			return nil, errors.New("authentication failed")
		}
		if CheckAccessKey(u.AccessKeyHash, string(password)) {
			return &ssh.Permissions{}, nil
		}
		if u.PasswordHash == "" {
			return nil, errors.New("authentication failed")
		}
		select {
		case s.passwordSlots <- struct{}{}:
			defer func() { <-s.passwordSlots }()
		default:
			return nil, errors.New("authentication busy")
		}
		if CheckPassword(u.PasswordHash, string(password)) {
			return &ssh.Permissions{}, nil
		}
		return nil, errors.New("authentication failed")
	}
	cfg.AddHostKey(s.Store.Signer)
	conn, channels, requests, e := ssh.NewServerConn(raw, cfg)
	if e != nil {
		s.event("auth", "SSH 인증/연결 실패: "+raw.RemoteAddr().String())
		return
	}
	defer conn.Close()
	_ = raw.SetDeadline(time.Time{})
	u, ok := s.Store.User(conn.User())
	if !ok || !s.open.Load() {
		return
	}
	pctx, cancel := context.WithCancel(s.ctx)
	p := &peer{server: s, id: secret()[:24], user: u, conn: conn, ctx: pctx, cancel: cancel, started: time.Now().UTC(), forwards: map[string]net.Listener{}, slots: make(chan struct{}, s.Store.Config().MaxChannels)}
	s.mu.Lock()
	n := 0
	for _, other := range s.peers {
		if other.user.Name == u.Name {
			n++
		}
	}
	if n >= u.MaxConnections || len(s.peers) >= s.Store.Config().MaxConnections || !s.open.Load() {
		s.mu.Unlock()
		cancel()
		return
	}
	s.peers[p.id] = p
	s.mu.Unlock()
	s.event("connect", u.Name+" 연결: "+raw.RemoteAddr().String())
	requestDone := make(chan struct{})
	go func() { defer close(requestDone); p.requests(requests) }()
	go p.keepalive()
	defer func() {
		cancel()
		_ = conn.Close()
		<-requestDone
		p.closeLeases()
		p.mu.Lock()
		for _, l := range p.forwards {
			_ = l.Close()
		}
		p.mu.Unlock()
		p.workers.Wait()
		s.mu.Lock()
		delete(s.peers, p.id)
		s.mu.Unlock()
		s.event("disconnect", u.Name+" 연결 종료")
	}()
	for incoming := range channels {
		if incoming.ChannelType() != "direct-tcpip" {
			_ = incoming.Reject(ssh.Prohibited, "TCP forwarding only; shell and SFTP are disabled")
			continue
		}
		if !p.take() {
			_ = incoming.Reject(ssh.ResourceShortage, "channel limit")
			continue
		}
		p.workers.Add(1)
		go func(ch ssh.NewChannel) { defer p.workers.Done(); defer p.release(); p.direct(ch) }(incoming)
	}
}
func (s *Server) Views() []ConnectionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []ConnectionView{}
	for _, p := range s.peers {
		p.mu.Lock()
		nf := len(p.forwards)
		p.mu.Unlock()
		result = append(result, ConnectionView{p.id, p.user.Name, p.conn.RemoteAddr().String(), p.started, len(p.slots), nf, p.up.Load(), p.down.Load()})
	}
	return result
}
func (s *Server) Disconnect(id string) bool {
	s.mu.Lock()
	p, ok := s.peers[id]
	s.mu.Unlock()
	if ok {
		p.cancel()
		_ = p.conn.Close()
	}
	return ok
}
func (s *Server) DisconnectAll() {
	s.mu.Lock()
	raw := make([]net.Conn, 0, len(s.raw))
	for c := range s.raw {
		raw = append(raw, c)
	}
	s.mu.Unlock()
	for _, c := range raw {
		_ = c.Close()
	}
}
func (s *Server) SetOpen(open bool) error {
	s.ops.Lock()
	defer s.ops.Unlock()
	if !open {
		s.open.Store(false)
		s.DisconnectAll()
	}
	if e := s.Store.SetOpen(open); e != nil {
		return e
	}
	s.open.Store(open)
	s.event("sharing", fmt.Sprintf("터널 허용 상태: %v", open))
	return nil
}
func (s *Server) Apply(c Config) error {
	s.ops.Lock()
	defer s.ops.Unlock()
	if e := s.Store.Save(c); e != nil {
		return e
	}
	s.open.Store(c.Open)
	s.DisconnectAll()
	s.event("config", "설정을 저장하고 기존 SSH 연결을 종료했습니다")
	return nil
}
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		if s.listener != nil {
			_ = s.listener.Close()
			<-s.acceptDone
		}
		s.DisconnectAll()
		s.workers.Wait()
		s.event("server", "서버 종료")
	})
}

type peer struct {
	server   *Server
	id       string
	user     User
	conn     *ssh.ServerConn
	ctx      context.Context
	cancel   context.CancelFunc
	started  time.Time
	mu       sync.Mutex
	forwards map[string]net.Listener
	slots    chan struct{}
	workers  sync.WaitGroup
	up, down atomic.Int64
}
type forwardRequest struct {
	Host string
	Port uint32
}
type directRequest struct {
	Host       string
	Port       uint32
	Origin     string
	OriginPort uint32
}

func (p *peer) take() bool {
	select {
	case p.slots <- struct{}{}:
		return true
	default:
		return false
	}
}
func (p *peer) release() { <-p.slots }
func (p *peer) keepalive() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-t.C:
			done := make(chan error, 1)
			go func() { _, _, e := p.conn.SendRequest("keepalive@openssh.com", true, nil); done <- e }()
			select {
			case e := <-done:
				if e != nil {
					p.conn.Close()
					return
				}
			case <-time.After(10 * time.Second):
				p.conn.Close()
				return
			case <-p.ctx.Done():
				return
			}
		}
	}
}
func (p *peer) direct(incoming ssh.NewChannel) {
	var d directRequest
	if e := ssh.Unmarshal(incoming.ExtraData(), &d); e != nil {
		incoming.Reject(ssh.Prohibited, "invalid target")
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	tcp, e := dialTarget(ctx, p.user, d.Host, d.Port)
	if e != nil {
		incoming.Reject(ssh.Prohibited, "target denied or unavailable")
		return
	}
	channel, requests, e := incoming.Accept()
	if e != nil {
		tcp.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	p.bridge(tcp, channel)
}
func forwardKey(host string, port uint32) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}
func (p *peer) requests(requests <-chan *ssh.Request) {
	for r := range requests {
		switch r.Type {
		case "tunnel-open@ontech.dev", "tunnel-close@ontech.dev":
			p.brokerRequest(r)
		case "keepalive@openssh.com":
			r.Reply(true, nil)
		case "tcpip-forward":
			var f forwardRequest
			if ssh.Unmarshal(r.Payload, &f) != nil {
				r.Reply(false, nil)
				continue
			}
			host, e := reverseAddress(p.user, f.Host, f.Port)
			if e != nil {
				r.Reply(false, nil)
				continue
			}
			limit := p.forwardCount() >= p.server.Store.Config().MaxForwards
			if limit {
				r.Reply(false, nil)
				continue
			}
			l, e := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(int(f.Port))))
			if e != nil {
				r.Reply(false, nil)
				continue
			}
			bound := uint32(l.Addr().(*net.TCPAddr).Port)
			key := forwardKey(f.Host, bound)
			p.mu.Lock()
			p.forwards[key] = l
			p.mu.Unlock()
			payload := []byte(nil)
			if f.Port == 0 {
				payload = ssh.Marshal(struct{ Port uint32 }{bound})
			}
			if e = r.Reply(true, payload); e != nil {
				l.Close()
				continue
			}
			p.workers.Add(1)
			go func() { defer p.workers.Done(); p.reverse(l, f.Host, bound) }()
		case "cancel-tcpip-forward":
			var f forwardRequest
			if ssh.Unmarshal(r.Payload, &f) != nil {
				r.Reply(false, nil)
				continue
			}
			key := forwardKey(f.Host, f.Port)
			p.mu.Lock()
			l, ok := p.forwards[key]
			if ok {
				delete(p.forwards, key)
				l.Close()
			}
			p.mu.Unlock()
			r.Reply(ok, nil)
		default:
			r.Reply(false, nil)
		}
	}
}
func (p *peer) reverse(l net.Listener, host string, port uint32) {
	defer l.Close()
	defer func() {
		p.mu.Lock()
		key := forwardKey(host, port)
		if p.forwards[key] == l {
			delete(p.forwards, key)
		}
		p.mu.Unlock()
	}()
	for {
		c, e := l.Accept()
		if e != nil {
			return
		}
		if !p.take() {
			c.Close()
			continue
		}
		p.workers.Add(1)
		go func() {
			defer p.workers.Done()
			defer p.release()
			origin := c.RemoteAddr().(*net.TCPAddr)
			channel, requests, e := p.conn.OpenChannel("forwarded-tcpip", ssh.Marshal(directRequest{host, port, origin.IP.String(), uint32(origin.Port)}))
			if e != nil {
				c.Close()
				return
			}
			go ssh.DiscardRequests(requests)
			p.bridge(c, channel)
		}()
	}
}

// 한 방향 EOF는 반대 방향의 응답을 기다립니다. 오류/취소만 양방향을 즉시 종료합니다.
func (p *peer) bridge(tcp net.Conn, channel ssh.Channel) { p.bridgeContext(p.ctx, tcp, channel) }
func (p *peer) bridgeContext(ctx context.Context, tcp net.Conn, channel ssh.Channel) {
	defer tcp.Close()
	defer channel.Close()
	stop := context.AfterFunc(ctx, func() { tcp.Close(); channel.Close() })
	defer stop()
	done := make(chan error, 2)
	go func() {
		n, e := io.Copy(channel, tcp)
		p.down.Add(n)
		if e == nil {
			e = channel.CloseWrite()
		}
		done <- e
	}()
	go func() {
		n, e := io.Copy(tcp, channel)
		p.up.Add(n)
		if e == nil {
			if c, ok := tcp.(*net.TCPConn); ok {
				e = c.CloseWrite()
			}
		}
		done <- e
	}()
	if e := <-done; e != nil {
		tcp.Close()
		channel.Close()
	}
	<-done
}
