// 브라우저로 양방향 터널 통신을 확인하는 테스트 프로그램입니다.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"time"

	"ontech/sshtunnel/pkg/tunnel"
)

//go:embed web/*
var assets embed.FS

type config struct {
	Server      string `json:"server"`
	User        string `json:"user"`
	AccessKey   string `json:"accessKey"`
	Fingerprint string `json:"fingerprint"`
}

func main() {
	path := flag.String("config", "demo.json", "테스트 접속 설정 파일")
	flag.Parse()
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(*path), "demo.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	if err := run(*path); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg config
	if err = json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	started := time.Now().UTC()
	var count atomic.Uint64
	var endpoint atomic.Value
	endpoint.Store("")
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"status": "connected", "serverTime": time.Now().UTC().Format(time.RFC3339Nano), "started": started, "requests": count.Load(), "address": endpoint.Load()})
	})
	mux.HandleFunc("POST /api/echo", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		defer r.Body.Close()
		var input struct {
			Message string `json:"message"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || len(input.Message) > 2048 {
			http.Error(w, "메시지는 2048바이트 이내로 입력하세요.", 400)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "잘못된 JSON 요청", 400)
			return
		}
		writeJSON(w, map[string]any{"message": input.Message, "serverTime": time.Now().UTC().Format(time.RFC3339Nano), "via": "SSHTunnel", "request": count.Load()})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name, ct := "", ""
		switch r.URL.Path {
		case "/":
			name, ct = "index.html", "text/html; charset=utf-8"
		case "/app.js":
			name, ct = "app.js", "application/javascript; charset=utf-8"
		case "/style.css":
			name, ct = "style.css", "text/css; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := assets.ReadFile("web/" + name)
		if err != nil {
			http.Error(w, "읽기 실패", 500)
			return
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(data)
	})
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	defer web.Close()
	webErrors := make(chan error, 1)
	go func() { webErrors <- web.Serve(listener) }()
	session, err := tunnel.Open(ctx, tunnel.Options{Server: cfg.Server, User: cfg.User, AccessKey: cfg.AccessKey, Fingerprint: cfg.Fingerprint, Target: listener.Addr().String(), Name: "브라우저 통신 테스트", MaxConnections: 32})
	if err != nil {
		return fmt.Errorf("터널 연결 실패: %w", err)
	}
	defer session.Close()
	endpoint.Store(session.Endpoint().Address)
	state := map[string]any{"url": "http://" + session.Endpoint().Address, "port": session.Endpoint().Port, "tunnelId": session.Endpoint().ID, "pid": os.Getpid(), "localTarget": listener.Addr().String(), "started": started}
	stateBytes, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "endpoint.json"), stateBytes, 0600); err != nil {
		return err
	}
	log.Printf("준비 완료: http://%s", session.Endpoint().Address)
	select {
	case <-ctx.Done():
		return nil
	case <-session.Done():
		return session.Err()
	case err := <-webErrors:
		return err
	}
}
