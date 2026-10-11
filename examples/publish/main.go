// 기존 TCP 서버를 공개합니다. 인증키는 환경 변수로 전달합니다.
package main

import (
	"context"
	"flag"
	"fmt"
	"ontech/sshtunnel/pkg/tunnel"
	"os"
	"os/signal"
)

func main() {
	relay := flag.String("server", "", "중계 서버 IP:SSH포트")
	user := flag.String("user", "", "관리 화면에서 만든 계정")
	target := flag.String("target", "127.0.0.1:9000", "공개할 기존 TCP 서비스")
	fingerprint := flag.String("fingerprint", "", "중계 서버 SHA256 호스트 지문")
	name := flag.String("name", "my-service", "터널 이름")
	protocol := flag.String("protocol", "tcp", "tcp: 포트 주소, http: 브라우저용 랜덤 URL")
	identity := flag.String("identity", "", "고정 서비스 식별명 · 생략하면 target 사용")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	session, e := tunnel.Open(ctx, tunnel.Options{Protocol: *protocol, Identity: *identity, Server: *relay, User: *user, AccessKey: os.Getenv("TUNNEL_ACCESS_KEY"), Fingerprint: *fingerprint, Target: *target, Name: *name})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer session.Close()
	if session.Endpoint().URL != "" {
		fmt.Println("브라우저 접속 URL:", session.Endpoint().URL)
	} else {
		fmt.Println("클라이언트 접속 주소:", session.Endpoint().Address)
	}
	if e = session.Wait(); e != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
