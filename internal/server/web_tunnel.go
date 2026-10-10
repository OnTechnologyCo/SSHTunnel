package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WebConfig는 모든 웹 터널이 공유하는 HTTP 게이트웨이입니다.
// HTTPS 인증서는 앞단 프록시에서 처리하고 Host를 그대로 전달합니다.
type WebConfig struct {
	Enabled    bool   `json:"enabled"`
	Listen     string `json:"listen"`
	BaseDomain string `json:"baseDomain"`
	Scheme     string `json:"scheme"`
	MaxBodyMB  int    `json:"maxBodyMB"`
}

func DefaultWebConfig() WebConfig {
	return WebConfig{Listen: "127.0.0.1:17894", Scheme: "https", MaxBodyMB: 128}
}
func validateWeb(c WebConfig) error {
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil || net.ParseIP(host) == nil || !validPort(port, false) {
		return errors.New("웹 게이트웨이 수신 주소는 IP:포트 형식이어야 합니다")
	}
	if c.Scheme != "https" && c.Scheme != "http" {
		return errors.New("웹 공개 주소 방식은 https 또는 http입니다")
	}
	if c.MaxBodyMB < 1 || c.MaxBodyMB > 2048 {
		return errors.New("웹 업로드 한도는 1~2048 MB입니다")
	}
	if c.BaseDomain != "" && (!validHost(c.BaseDomain) || net.ParseIP(c.BaseDomain) != nil || !strings.Contains(c.BaseDomain, ".") || len(c.BaseDomain) > 220 || c.BaseDomain != strings.ToLower(c.BaseDomain)) {
		return errors.New("웹 기본 도메인은 소문자 도메인만 입력하세요. IP·포트·와일드카드는 제외합니다")
	}
	if c.Enabled && c.BaseDomain == "" {
		return errors.New("웹 터널의 기본 도메인을 설정하세요")
	}
	return nil
}
func (s *Server) WebAddr() string {
	if s.webListener == nil {
		return ""
	}
	return s.webListener.Addr().String()
}
func (s *Server) makeWebServer() *http.Server {
	return &http.Server{Handler: s.WebHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
}

// WebHandler는 임의 목적지로 연결하지 않고, 인증된 계정이 연 터널만 찾습니다.
func (s *Server) WebHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodConnect || r.Method == http.MethodTrace || r.URL.IsAbs() {
			http.Error(w, "Unsupported request", http.StatusBadRequest)
			return
		}
		cfg := s.Store.Config().Web
		if !cfg.Enabled || !s.open.Load() || cfg != s.active.Web {
			http.NotFound(w, r)
			return
		}
		host := strings.ToLower(r.Host)
		if strings.Contains(host, ":") {
			h, p, err := net.SplitHostPort(host)
			if err != nil || (cfg.Scheme == "https" && p != "443") || (cfg.Scheme == "http" && p != "80") {
				http.NotFound(w, r)
				return
			}
			host = h
		}
		suffix := "." + cfg.BaseDomain
		if !strings.HasSuffix(host, suffix) {
			http.NotFound(w, r)
			return
		}
		id := strings.TrimSuffix(host, suffix)
		if len(id) != 32 || strings.Contains(id, ".") {
			http.NotFound(w, r)
			return
		}
		s.brokerMu.Lock()
		l := s.tunnels[id]
		s.brokerMu.Unlock()
		if l == nil || l.info.Protocol != "http" || l.info.Host != host || l.closed.Load() {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(l.ctx, cancel)
		defer stop()
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, int64(cfg.MaxBodyMB)<<20)
		proxy := &httputil.ReverseProxy{
			Transport:     l.webTransport,
			FlushInterval: -1,
			ErrorLog:      log.New(io.Discard, "", 0),
			Rewrite: func(pr *httputil.ProxyRequest) {
				// 경로와 쿼리를 보존하고 사용자 입력으로 외부 목적지를 선택하지 않습니다.
				pr.Out.URL.Scheme = "http"
				pr.Out.URL.Host = l.info.Host
				pr.Out.Host = l.info.Host
				// 위조된 프록시/인증 힌트는 전달하지 않습니다. 쿠키·Authorization은 원본 앱용으로 유지합니다.
				for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-IP", "X-Forwarded-Prefix"} {
					pr.Out.Header.Del(name)
				}
				pr.SetXForwarded()
				pr.Out.Header.Set("X-Forwarded-Proto", cfg.Scheme)
				if cfg.Scheme == "https" {
					pr.Out.Header.Set("X-Forwarded-Port", "443")
				} else {
					pr.Out.Header.Set("X-Forwarded-Port", "80")
				}
			},
			ModifyResponse: func(resp *http.Response) error {
				// 한 터널이 부모 도메인 쿠키로 다른 터널의 로그인 세션을 덮어쓰지 못하게 합니다.
				cookies := resp.Header.Values("Set-Cookie")
				resp.Header.Del("Set-Cookie")
				for _, cookie := range cookies {
					parts := strings.Split(cookie, ";")
					kept := parts[:1]
					for _, part := range parts[1:] {
						name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
						if !strings.EqualFold(name, "domain") {
							kept = append(kept, part)
						}
					}
					resp.Header.Add("Set-Cookie", strings.Join(kept, ";"))
				}
				if location, err := url.Parse(resp.Header.Get("Location")); err == nil && location.Host == l.info.Host && (location.Scheme == "http" || location.Scheme == "https") {
					location.Scheme = cfg.Scheme
					resp.Header.Set("Location", location.String())
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				if r.Context().Err() != nil {
					return
				}
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					http.Error(w, "Upload limit exceeded", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "Tunnel target unavailable", http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

func (l *lease) newWebTransport() *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: l.dialWeb, DisableKeepAlives: true, DisableCompression: true, ResponseHeaderTimeout: 30 * time.Second, MaxResponseHeaderBytes: 64 << 10, MaxConnsPerHost: 64}
}
func (l *lease) dialWeb(ctx context.Context, network, address string) (net.Conn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	l.webMu.Lock()
	if l.closed.Load() || l.ctx.Err() != nil || !l.owner.take() {
		l.webMu.Unlock()
		return nil, errors.New("tunnel closed or channel limit")
	}
	// net.Pipe의 deadline을 HTTP transport에서 사용할 수 있고 OS 포트는 소비하지 않습니다.
	client, upstream := net.Pipe()
	l.webStreams[client] = true
	l.webWorkers.Add(1)
	l.connections.Add(1)
	l.webMu.Unlock()
	go func() {
		defer l.webWorkers.Done()
		defer l.owner.release()
		defer l.connections.Add(-1)
		defer func() {
			client.Close()
			upstream.Close()
			l.webMu.Lock()
			delete(l.webStreams, client)
			l.webMu.Unlock()
		}()
		l.forward(upstream)
	}()
	return client, nil
}
func (l *lease) closeWebStreams() {
	if l.webTransport == nil {
		return
	}
	l.webTransport.CloseIdleConnections()
	l.webMu.Lock()
	for c := range l.webStreams {
		_ = c.Close()
	}
	l.webMu.Unlock()
	l.webWorkers.Wait()
}

// DeploymentConfig는 관리 화면에서 미리 확인할 수 있는 설정 예시만 반환합니다.
// 기존 프록시·DNS·방화벽은 자동 변경하지 않습니다.
func (s *Server) WebDeployment() map[string]string {
	c := s.Store.Config().Web
	domain := c.BaseDomain
	if domain == "" {
		domain = "tunnel.example.com"
	}
	_, port, _ := net.SplitHostPort(c.Listen)
	upstream := net.JoinHostPort("127.0.0.1", port)
	if host, _, e := net.SplitHostPort(c.Listen); e == nil && net.ParseIP(host).IsLoopback() {
		upstream = c.Listen
	}
	tls := ""
	address := "http://*." + domain
	if c.Scheme == "https" {
		address = "https://*." + domain
		tls = "    tls C:/certs/tunnel-fullchain.pem C:/certs/tunnel-private.key\n"
	}
	return map[string]string{"dns": "*." + domain + " → 터널 중계 서버의 공개 IP", "certificate": "*." + domain, "urlExample": c.Scheme + "://" + strings.Repeat("a", 32) + "." + domain, "caddy": fmt.Sprintf("# 기존 사이트와 분리된 추가 사이트 블록 예시입니다.\n%s {\n%s    reverse_proxy %s\n}\n", address, tls, upstream), "maxBodyMB": strconv.Itoa(c.MaxBodyMB)}
}
