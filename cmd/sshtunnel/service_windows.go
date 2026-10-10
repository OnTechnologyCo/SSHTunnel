//go:build windows

package main

import (
	"context"
	"golang.org/x/sys/windows/svc"
	"ontech/sshtunnel/internal/server"
)

type serviceHandler struct{ dir string }

func runService(dir string) (bool, error) {
	is, e := svc.IsWindowsService()
	if e != nil {
		return true, e
	}
	if !is {
		return false, nil
	}
	return true, svc.Run("SSHTunnelServer", &serviceHandler{dir})
}
func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending, WaitHint: 15000}
	store, e := server.NewStore(h.dir)
	if e != nil {
		return true, 1
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() { done <- run(ctx, store, func(*server.Server) { close(ready) }) }()
	select {
	case <-ready:
	case <-done:
		return true, 2
	}
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	status <- current
	for {
		select {
		case e := <-done:
			if e != nil {
				return true, 3
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- current
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 15000}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}
