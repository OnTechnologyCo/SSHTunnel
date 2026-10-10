package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

type BrokerConfig struct {
	Enabled    bool   `json:"enabled"`
	PublicHost string `json:"publicHost"`
	BindIP     string `json:"bindIP"`
	PortMin    int    `json:"portMin"`
	PortMax    int    `json:"portMax"`
	MaxTunnels int    `json:"maxTunnels"`
}

func DefaultBrokerConfig() BrokerConfig {
	return BrokerConfig{BindIP: "0.0.0.0", PortMin: 20000, PortMax: 20199, MaxTunnels: 128}
}
func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return !net.ParseIP(host).IsUnspecified() && !net.ParseIP(host).IsMulticast()
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func validateBroker(c BrokerConfig) error {
	if c.PublicHost != "" && !validHost(c.PublicHost) {
		return errors.New("자동 터널의 공개 주소에는 IP 또는 호스트 이름만 입력하세요")
	}
	if net.ParseIP(c.BindIP) == nil || net.ParseIP(c.BindIP).IsMulticast() {
		return errors.New("자동 터널 수신 IP를 확인하세요")
	}
	if c.PortMin < 1024 || c.PortMax > 65535 || c.PortMax < c.PortMin || c.PortMax-c.PortMin >= 4096 || c.MaxTunnels < 1 || c.MaxTunnels > 1024 {
		return errors.New("자동 터널 포트 범위는 1024~65535 내 최대 4096개, 터널 수는 1~1024개입니다")
	}
	return nil
}
func validSecret(value string) bool {
	if len(value) != 64 {
		return false
	}
	b, e := hex.DecodeString(value)
	return e == nil && len(b) == 32 && value == strings.ToLower(value)
}
func hashSecret(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
func CheckAccessKey(hash, key string) bool {
	if !validSecret(hash) || !validSecret(key) {
		return false
	}
	actual := hashSecret(key)
	return subtle.ConstantTimeCompare([]byte(hash), []byte(actual)) == 1
}

type TunnelInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
	URL      string `json:"url,omitempty"`
}
type TunnelView struct {
	TunnelInfo
	User        string    `json:"user"`
	Started     time.Time `json:"started"`
	Connections int64     `json:"connections"`
}
type lease struct {
	info         TunnelInfo
	keyHash      string
	owner        *peer
	listener     net.Listener
	ctx          context.Context
	cancel       context.CancelFunc
	started      time.Time
	closed       atomic.Bool
	connections  atomic.Int64
	closeOnce    sync.Once
	webTransport *http.Transport
	webMu        sync.Mutex
	webStreams   map[net.Conn]bool
	webWorkers   sync.WaitGroup
}

func (p *peer) forwardCount() int {
	p.mu.Lock()
	n := len(p.forwards)
	p.mu.Unlock()
	p.server.brokerMu.Lock()
	defer p.server.brokerMu.Unlock()
	for _, l := range p.server.tunnels {
		if l.owner == p {
			n++
		}
	}
	return n
}
func (p *peer) openLease(key, name string) (*lease, error) { return p.openLeaseMode(key, name, "tcp") }
func (p *peer) openLeaseMode(key, name, protocol string) (*lease, error) {
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "http" {
		return nil, errors.New("터널 protocol은 tcp 또는 http입니다")
	}
	cfg := p.server.Store.Config().Broker
	if !cfg.Enabled || !p.user.AllowTunnel || !p.server.open.Load() || p.ctx.Err() != nil {
		return nil, errors.New("자동 터널이 비활성화되어 있거나 계정 권한이 없습니다")
	}
	if !validSecret(key) || len(name) > 160 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, errors.New("터널 키는 64자리 소문자 hex, 이름은 제어문자 없는 최대 160바이트입니다")
	}
	if p.forwardCount() >= p.server.Store.Config().MaxForwards {
		return nil, errors.New("연결당 터널 수 제한입니다")
	}
	s := p.server
	s.brokerMu.Lock()
	defer s.brokerMu.Unlock()
	if len(s.tunnels) >= cfg.MaxTunnels {
		return nil, errors.New("서버 터널 수 제한입니다")
	}
	hash := hashSecret(key)
	for _, existing := range s.tunnels {
		if existing.keyHash == hash {
			return nil, errors.New("이미 사용 중인 터널 키입니다")
		}
	}
	if protocol == "http" {
		web := s.Store.Config().Web
		if !web.Enabled || web != s.active.Web {
			return nil, errors.New("웹 터널 설정을 활성화한 뒤 서버를 재시작하세요")
		}
		if web.TLSMode == "native" && !s.https.ready() {
			return nil, errors.New("내장 HTTPS가 준비되지 않았습니다. 관리 화면의 HTTPS 상태·인증서·포트를 확인하세요")
		}
		// brokerMu를 잡은 상태에서 발급·등록하여 동시 요청도 기존 주소를 덮어쓰지 않습니다.
		id, err := newWebID(s.tunnels, rand.Reader)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(p.ctx)
		host := id + "." + web.BaseDomain
		l := &lease{info: TunnelInfo{ID: id, Name: name, Address: host, Host: host, Protocol: "http", URL: web.Scheme + "://" + host}, keyHash: hash, owner: p, ctx: ctx, cancel: cancel, started: time.Now().UTC(), webStreams: map[net.Conn]bool{}}
		l.webTransport = l.newWebTransport()
		s.tunnels[id] = l
		return l, nil
	}
	if cfg.PublicHost == "" {
		return nil, errors.New("TCP 터널 공개 IP/도메인이 필요합니다")
	}
	span := cfg.PortMax - cfg.PortMin + 1
	random, e := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if e != nil {
		return nil, e
	}
	var listener net.Listener
	for i := 0; i < span; i++ {
		port := cfg.PortMin + (int(random.Int64())+i)%span
		listener, e = net.Listen("tcp", net.JoinHostPort(cfg.BindIP, strconv.Itoa(port)))
		if e == nil {
			break
		}
	}
	if listener == nil {
		return nil, errors.New("사용 가능한 공개 포트가 없습니다")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(p.ctx)
	l := &lease{info: TunnelInfo{ID: secret()[:24], Name: name, Address: net.JoinHostPort(cfg.PublicHost, strconv.Itoa(port)), Host: cfg.PublicHost, Port: port, Protocol: "tcp"}, keyHash: hash, owner: p, listener: listener, ctx: ctx, cancel: cancel, started: time.Now().UTC()}
	s.tunnels[l.info.ID] = l
	return l, nil
}
func (p *peer) brokerRequest(r *ssh.Request) {
	fail := func(err error) { b, _ := json.Marshal(map[string]string{"error": err.Error()}); _ = r.Reply(false, b) }
	if !r.WantReply || len(r.Payload) > 4096 {
		fail(errors.New("응답 요청이 필요하거나 요청 크기가 너무 큽니다"))
		return
	}
	if r.Type == "tunnel-open@ontech.dev" {
		var input struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			Protocol string `json:"protocol,omitempty"`
		}
		if e := decode(r.Payload, &input); e != nil {
			fail(errors.New("터널 요청 JSON 형식 오류"))
			return
		}
		l, e := p.openLeaseMode(input.Key, input.Name, input.Protocol)
		if e != nil {
			fail(e)
			return
		}
		b, _ := json.Marshal(l.info)
		if e = r.Reply(true, b); e != nil {
			l.Close()
			return
		}
		p.server.event("tunnel", fmt.Sprintf("%s 자동 터널 개설: %s", p.user.Name, l.info.Address))
		if l.listener != nil {
			p.workers.Add(1)
			go func() { defer p.workers.Done(); l.accept() }()
		}
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if e := decode(r.Payload, &input); e != nil {
		fail(errors.New("종료 요청 JSON 형식 오류"))
		return
	}
	p.server.brokerMu.Lock()
	l := p.server.tunnels[input.ID]
	p.server.brokerMu.Unlock()
	if l == nil || l.owner != p {
		fail(errors.New("이 연결에 속한 터널이 없습니다"))
		return
	}
	l.Close()
	_ = r.Reply(true, []byte(`{"closed":true}`))
}
func (l *lease) Close() {
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.cancel()
		if l.listener != nil {
			_ = l.listener.Close()
		}
		l.closeWebStreams()
		s := l.owner.server
		s.brokerMu.Lock()
		delete(s.tunnels, l.info.ID)
		s.brokerMu.Unlock()
		s.event("tunnel", fmt.Sprintf("%s 자동 터널 종료: %s", l.owner.user.Name, l.info.Address))
		if l.owner.ctx.Err() == nil {
			go l.notifyClosed()
		}
	})
}
func (l *lease) notifyClosed() {
	b, _ := json.Marshal(map[string]string{"id": l.info.ID})
	done := make(chan struct{}, 1)
	go func() { _, _, _ = l.owner.conn.SendRequest("tunnel-closed@ontech.dev", false, b); done <- struct{}{} }()
	select {
	case <-done:
	case <-l.owner.ctx.Done():
	case <-time.After(5 * time.Second):
		_ = l.owner.conn.Close()
	}
}
func (p *peer) closeLeases() {
	s := p.server
	s.brokerMu.Lock()
	var own []*lease
	for _, l := range s.tunnels {
		if l.owner == p {
			own = append(own, l)
		}
	}
	s.brokerMu.Unlock()
	for _, l := range own {
		l.Close()
	}
}
func (s *Server) TunnelViews() []TunnelView {
	s.brokerMu.Lock()
	defer s.brokerMu.Unlock()
	views := []TunnelView{}
	for _, l := range s.tunnels {
		views = append(views, TunnelView{l.info, l.owner.user.Name, l.started, l.connections.Load()})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Started.Before(views[j].Started) })
	return views
}
func (s *Server) CloseTunnel(id string) bool {
	s.brokerMu.Lock()
	l := s.tunnels[id]
	s.brokerMu.Unlock()
	if l == nil {
		return false
	}
	l.Close()
	return true
}
func (l *lease) accept() {
	defer l.Close()
	for {
		tcp, e := l.listener.Accept()
		if e != nil {
			return
		}
		if l.closed.Load() || !l.owner.server.open.Load() || !l.owner.take() {
			tcp.Close()
			continue
		}
		l.owner.workers.Add(1)
		l.connections.Add(1)
		go func() {
			defer l.owner.workers.Done()
			defer l.owner.release()
			defer l.connections.Add(-1)
			l.forward(tcp)
		}()
	}
}
func (l *lease) forward(tcp net.Conn) {
	defer tcp.Close()
	payload, _ := json.Marshal(map[string]string{"tunnelId": l.info.ID, "origin": tcp.RemoteAddr().String()})
	type result struct {
		channel  ssh.Channel
		requests <-chan *ssh.Request
		err      error
	}
	done := make(chan result, 1)
	go func() {
		ch, requests, e := l.owner.conn.OpenChannel("tunnel-connection@ontech.dev", payload)
		done <- result{ch, requests, e}
	}()
	var opened result
	select {
	case opened = <-done:
	case <-time.After(10 * time.Second):
		_ = l.owner.conn.Close()
		opened = <-done
	}
	if opened.err != nil {
		return
	}
	defer opened.channel.Close()
	go ssh.DiscardRequests(opened.requests)
	if l.ctx.Err() != nil {
		return
	}
	l.owner.bridgeContext(l.ctx, tcp, opened.channel)
}
