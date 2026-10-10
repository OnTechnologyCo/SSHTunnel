// HTTP 서버 프로그램 안에서 터널을 직접 개설하는 예제입니다.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"ontech/sshtunnel/pkg/tunnel"
	"os"
	"os/signal"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	local, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer local.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "임의 서버 프로그램의 응답입니다.")
	})
	web := &http.Server{Handler: mux}
	defer web.Close()
	go web.Serve(local)
	session, e := tunnel.Open(ctx, tunnel.Options{Protocol: "http", Server: os.Getenv("TUNNEL_SERVER"), User: os.Getenv("TUNNEL_USER"), AccessKey: os.Getenv("TUNNEL_ACCESS_KEY"), Fingerprint: os.Getenv("TUNNEL_FINGERPRINT"), Target: local.Addr().String(), Name: "HTTP example"})
	if e != nil {
		return e
	}
	defer session.Close()
	fmt.Println("클라이언트 접속 URL: " + session.Endpoint().URL)
	e = session.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return e
}
