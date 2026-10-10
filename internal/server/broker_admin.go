package server

import (
	"net"
	"net/http"
)

func (s *Server) brokerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tunnels", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.TunnelViews()) })
	mux.HandleFunc("DELETE /api/tunnels/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.CloseTunnel(r.PathValue("id")) {
			writeJSON(w, 404, map[string]string{"error": "이미 종료된 터널입니다"})
			return
		}
		w.WriteHeader(204)
	})
	issue := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				Name           string `json:"name"`
				MaxConnections int    `json:"maxConnections"`
				Revision       string `json:"revision"`
			}
			if e := body(w, r, &input); e != nil {
				writeJSON(w, 400, map[string]string{"error": "인증키 발급 요청 형식을 확인하세요"})
				return
			}
			s.ops.Lock()
			defer s.ops.Unlock()
			cfg := s.Store.Config()
			if input.Revision != revision(cfg) {
				writeJSON(w, 409, map[string]string{"error": "설정이 변경되었습니다. 관리 화면을 새로고침하세요"})
				return
			}
			key := secret()
			name := input.Name
			if create {
				for _, u := range cfg.Users {
					if u.Name == name {
						writeJSON(w, 409, map[string]string{"error": "이미 존재하는 계정입니다"})
						return
					}
				}
				if input.MaxConnections == 0 {
					input.MaxConnections = 8
				}
				cfg.Users = append(cfg.Users, User{Name: name, Enabled: true, AllowTunnel: true, AccessKeyHash: hashSecret(key), MaxConnections: input.MaxConnections, PublicKeys: []string{}, Targets: []string{}, ListenPorts: []string{}})
			} else {
				name = r.PathValue("name")
				found := false
				for i := range cfg.Users {
					if cfg.Users[i].Name == name {
						cfg.Users[i].AccessKeyHash = hashSecret(key)
						cfg.Users[i].AllowTunnel = true
						found = true
						break
					}
				}
				if !found {
					writeJSON(w, 404, map[string]string{"error": "계정을 찾을 수 없습니다"})
					return
				}
			}
			if e := s.Store.Save(cfg); e != nil {
				writeJSON(w, 400, map[string]string{"error": e.Error()})
				return
			}
			s.DisconnectAll()
			s.event("credential", name+" 자동 터널 인증키 발급/교체")
			address := ""
			_, port, _ := net.SplitHostPort(s.Addr())
			if cfg.Broker.PublicHost != "" {
				address = net.JoinHostPort(cfg.Broker.PublicHost, port)
			}
			writeJSON(w, 200, map[string]string{"user": name, "accessKey": key, "serverAddress": address, "fingerprint": s.Fingerprint()})
		}
	}
	mux.HandleFunc("POST /api/agents", issue(true))
	mux.HandleFunc("POST /api/users/{name}/access-key", issue(false))
}
