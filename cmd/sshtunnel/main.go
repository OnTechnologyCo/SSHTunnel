package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"ontech/sshtunnel/internal/server"
)

func main() {
	if e := entry(); e != nil {
		fmt.Fprintln(os.Stderr, "SSHTunnel:", e)
		os.Exit(1)
	}
}
func entry() error {
	command := defaultCommand()
	args := os.Args[1:]
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		command = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	defaultDir := defaultData()
	if command == "tray" || command == "open-admin" {
		defaultDir = defaultTrayData()
	}
	data := flags.String("data-dir", defaultDir, "설정·호스트 키 저장 폴더")
	flags.Usage = func() {
		fmt.Println("SSHTunnel Server " + server.Version + "\nsshtunnel.exe [tray|run|open-admin|init|check|version] --data-dir <folder>")
		flags.PrintDefaults()
	}
	if e := flags.Parse(args); e != nil {
		return e
	}
	if command == "tray" {
		return runTray(*data)
	}
	if command == "open-admin" {
		return openAdmin(*data)
	}
	if command == "version" {
		fmt.Println(server.Version)
		return nil
	}
	if command != "run" && command != "init" && command != "check" {
		return fmt.Errorf("지원하지 않는 명령: %s", command)
	}
	if command == "run" {
		if handled, e := runService(*data); handled {
			return e
		}
	}
	store, e := server.NewStore(*data)
	if e != nil {
		return e
	}
	defer store.Close()
	if command == "init" || command == "check" {
		fmt.Println("설정 확인 완료:", store.Dir)
		fmt.Println("SSH:", store.Config().Listen, "관리:", store.Config().AdminListen)
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx, store, nil)
}
func defaultData() string {
	if p := os.Getenv("ProgramData"); p != "" {
		return filepath.Join(p, "SSHTunnel")
	}
	return "data"
}
func run(ctx context.Context, store *server.Store, ready func(*server.Server)) error {
	tunnel := server.New(store)
	defer tunnel.Close()
	// 두 포트를 모두 확보한 뒤 실행 상태를 알립니다.
	admin, e := net.Listen("tcp", store.Config().AdminListen)
	if e != nil {
		return fmt.Errorf("관리 포트: %w", e)
	}
	defer admin.Close()
	if e = tunnel.Start(); e != nil {
		return fmt.Errorf("SSH 포트: %w", e)
	}
	web := tunnel.HTTPServer()
	fail := make(chan error, 1)
	go func() { fail <- web.Serve(admin) }()
	fmt.Println("OnTech SSHTunnel Server", server.Version)
	fmt.Println("SSH:", tunnel.Addr())
	fmt.Println("관리: http://" + admin.Addr().String())
	fmt.Println("호스트 지문:", tunnel.Fingerprint())
	fmt.Println("처음 사용: Open-Admin.cmd에서 접속 계정과 공개키를 등록하세요.")
	if ready != nil {
		ready(tunnel)
	}
	select {
	case <-ctx.Done():
	case err := <-fail:
		if err != http.ErrServerClosed {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = web.Shutdown(shutdown)
	return nil
}
