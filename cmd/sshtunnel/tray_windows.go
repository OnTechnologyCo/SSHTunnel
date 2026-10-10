//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"ontech/sshtunnel/internal/server"
)

// 보이는 창 없이 메시지 전용 호스트와 알림 영역 아이콘만 만듭니다.
// 최상위 숨김 창을 사용해야 Explorer 재시작의 TaskbarCreated 메시지를 받습니다.
const trayMutexName = `Local\OnTech.SSHTunnelServer.Tray`
const (
	trayCallback = 0x8001
	trayUpdate   = 0x8002
	menuAdmin    = 1001
	menuRestart  = 1002
	menuFolder   = 1003
	menuError    = 1004
	menuExit     = 1005
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	registerClass    = user32.NewProc("RegisterClassExW")
	unregisterClass  = user32.NewProc("UnregisterClassW")
	createWindow     = user32.NewProc("CreateWindowExW")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	getMessage       = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	postMessage      = user32.NewProc("PostMessageW")
	postQuit         = user32.NewProc("PostQuitMessage")
	loadIcon         = user32.NewProc("LoadIconW")
	registerMessage  = user32.NewProc("RegisterWindowMessageW")
	createMenu       = user32.NewProc("CreatePopupMenu")
	appendMenu       = user32.NewProc("AppendMenuW")
	destroyMenu      = user32.NewProc("DestroyMenu")
	trackMenu        = user32.NewProc("TrackPopupMenu")
	setForeground    = user32.NewProc("SetForegroundWindow")
	getCursorPos     = user32.NewProc("GetCursorPos")
	setTimer         = user32.NewProc("SetTimer")
	killTimer        = user32.NewProc("KillTimer")
	messageBox       = user32.NewProc("MessageBoxW")
	notifyIcon       = shell32.NewProc("Shell_NotifyIconW")
	shellExecute     = shell32.NewProc("ShellExecuteW")
)

type windowClass struct {
	Size, Style                        uint32
	Callback                           uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type point struct{ X, Y int32 }
type winMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type notificationIcon struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         uintptr
}
type trayEvent struct {
	ready  bool
	tunnel *server.Server
	err    error
}
type trayHost struct {
	window, icon                                      uintptr
	taskbarCreated                                    uint32
	dir, adminAddress                                 string
	events                                            chan trayEvent
	cancel                                            context.CancelFunc
	tunnel                                            *server.Server
	active, stopping, quitting, restarting, iconAdded bool
	lastError                                         error
}

func defaultCommand() string { return "tray" }
func defaultTrayData() string {
	if p := os.Getenv("LOCALAPPDATA"); p != "" {
		return filepath.Join(p, "SSHTunnel", "data")
	}
	if p, e := os.UserConfigDir(); e == nil {
		return filepath.Join(p, "SSHTunnel", "data")
	}
	return defaultData()
}
func utf16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	return p
}
func setTip(target *[128]uint16, value string) {
	b, _ := windows.UTF16FromString(value)
	*target = [128]uint16{}
	if len(b) > 127 {
		b = b[:127]
		if b[len(b)-1] >= 0xD800 && b[len(b)-1] <= 0xDBFF {
			b = b[:len(b)-1]
		}
	}
	copy(target[:], b)
}
func shellOpen(owner uintptr, value string) error {
	r, _, _ := shellExecute.Call(owner, uintptr(unsafe.Pointer(utf16("open"))), uintptr(unsafe.Pointer(utf16(value))), 0, 0, 1)
	if r <= 32 {
		return fmt.Errorf("열기 실패 (Windows 코드 %d)", r)
	}
	return nil
}
func adminURL(dir, address string) (string, error) {
	if address == "" {
		b, e := os.ReadFile(filepath.Join(dir, "server.json"))
		if e != nil {
			return "", e
		}
		var c server.Config
		if e = json.Unmarshal(b, &c); e != nil {
			return "", e
		}
		address = c.AdminListen
	}
	host, port, e := net.SplitHostPort(address)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || port == "" {
		return "", errors.New("관리 주소는 루프백 주소여야 합니다")
	}
	b, e := os.ReadFile(filepath.Join(dir, "admin.token"))
	if e != nil {
		return "", e
	}
	token := strings.TrimSpace(string(b))
	if len(token) != 64 || strings.IndexFunc(token, func(c rune) bool { return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') }) >= 0 {
		return "", errors.New("관리 토큰 형식 오류")
	}
	return "http://" + address + "/#token=" + token, nil
}
func openAdmin(dir string) error {
	url, e := adminURL(dir, "")
	if e != nil {
		return e
	}
	return shellOpen(0, url)
}
func startupLog(dir string, err error) {
	if err == nil {
		return
	}
	if server.PrivateDir(dir) != nil {
		return
	}
	p := filepath.Join(dir, "startup.log")
	if st, e := os.Stat(p); e == nil && st.Size() > 256<<10 {
		_ = os.Remove(p + ".1")
		_ = os.Rename(p, p+".1")
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e == nil {
		defer f.Close()
		fmt.Fprintf(f, "%s %v\n", time.Now().Format(time.RFC3339), err)
	}
}
func runTray(dir string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mutex, e := windows.CreateMutex(nil, false, utf16(trayMutexName))
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	if e != nil {
		return e
	}
	instance, _, e := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	if instance == 0 {
		return e
	}
	t := &trayHost{dir: dir, events: make(chan trayEvent, 8)}
	t.icon, _, _ = loadIcon.Call(uintptr(instance), 1)
	if t.icon == 0 {
		t.icon, _, _ = loadIcon.Call(0, 32512)
	}
	name := utf16("OnTech.SSHTunnel.TrayHost.v1")
	wc := windowClass{Instance: uintptr(instance), Name: name, Icon: t.icon, SmallIcon: t.icon}
	wc.Size = uint32(unsafe.Sizeof(wc))
	wc.Callback = windows.NewCallback(t.windowProc)
	atom, _, e := registerClass.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return fmt.Errorf("트레이 창 등록: %w", e)
	}
	defer unregisterClass.Call(uintptr(unsafe.Pointer(name)), uintptr(instance))
	// WS_EX_TOOLWINDOW, WS_OVERLAPPED. WS_VISIBLE와 ShowWindow는 사용하지 않습니다.
	t.window, _, e = createWindow.Call(0x80, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(utf16("SSHTunnel Tray Host"))), 0, 0, 0, 0, 0, 0, 0, uintptr(instance), 0)
	if t.window == 0 {
		return fmt.Errorf("트레이 생성: %w", e)
	}
	msg, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))
	t.taskbarCreated = uint32(msg)
	t.updateIcon(true)
	defer t.removeIcon()
	setTimer.Call(t.window, 1, 3000, 0)
	t.start()
	var m winMessage
	for {
		r, _, err := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == -1 {
			if t.cancel != nil {
				t.cancel()
			}
			return err
		}
		if r == 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}
func (t *trayHost) start() {
	t.lastError = nil
	t.stopping = false
	t.restarting = false
	t.active = true
	t.tunnel = nil
	t.adminAddress = ""
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	go func() {
		store, e := server.NewStore(t.dir)
		if e == nil {
			e = run(ctx, store, func(tunnel *server.Server) { t.send(trayEvent{ready: true, tunnel: tunnel}) })
			store.Close()
		}
		if e != nil {
			startupLog(t.dir, e)
		}
		t.send(trayEvent{err: e})
	}()
	t.updateIcon(false)
}
func (t *trayHost) send(e trayEvent) { t.events <- e; postMessage.Call(t.window, trayUpdate, 0, 0) }
func (t *trayHost) state() string {
	if t.stopping {
		return "서버 종료 중"
	}
	if t.lastError != nil {
		return "시작 실패 · 메뉴에서 오류 확인"
	}
	if t.tunnel != nil {
		state := "접속 허용"
		if !t.tunnel.Store.Config().Open {
			state = "접속 중지"
		}
		return state + " · " + t.tunnel.Addr()
	}
	return "서버 시작 중"
}
func (t *trayHost) updateIcon(add bool) {
	n := notificationIcon{Window: t.window, ID: 1, Flags: 1 | 2 | 4 | 0x80, Callback: trayCallback, Icon: t.icon}
	n.Size = uint32(unsafe.Sizeof(n))
	setTip(&n.Tip, "SSHTunnel Server\n"+t.state())
	mode := uintptr(1)
	if add || !t.iconAdded {
		mode = 0
	}
	r, _, _ := notifyIcon.Call(mode, uintptr(unsafe.Pointer(&n)))
	t.iconAdded = r != 0
	if mode == 0 && r != 0 {
		n.Version = 4
		notifyIcon.Call(4, uintptr(unsafe.Pointer(&n)))
	}
}
func (t *trayHost) removeIcon() {
	n := notificationIcon{Window: t.window, ID: 1}
	n.Size = uint32(unsafe.Sizeof(n))
	notifyIcon.Call(2, uintptr(unsafe.Pointer(&n)))
	t.iconAdded = false
}
func (t *trayHost) stop(quit bool) {
	if t.stopping {
		if quit {
			t.quitting = true
			t.restarting = false
		}
		return
	}
	t.quitting = quit
	t.restarting = !quit
	t.stopping = true
	t.updateIcon(false)
	if t.cancel != nil {
		t.cancel()
	}
	if !t.active {
		if quit {
			t.finish()
		} else {
			t.start()
		}
	}
}
func (t *trayHost) finish() {
	killTimer.Call(t.window, 1)
	t.removeIcon()
	destroyWindow.Call(t.window)
}
func (t *trayHost) processEvents() {
	for {
		select {
		case e := <-t.events:
			if e.ready {
				t.tunnel = e.tunnel
				t.adminAddress = e.tunnel.Store.Config().AdminListen
			} else {
				t.active = false
				t.tunnel = nil
				t.lastError = e.err
				if t.quitting {
					t.finish()
					return
				}
				if t.restarting {
					t.start()
					continue
				}
				t.stopping = false
			}
			t.updateIcon(false)
		default:
			return
		}
	}
}
func (t *trayHost) showError(err error) {
	if err == nil {
		return
	}
	messageBox.Call(t.window, uintptr(unsafe.Pointer(utf16(err.Error()))), uintptr(unsafe.Pointer(utf16("SSHTunnel Server"))), 0x10)
}
func (t *trayHost) command(id uintptr) {
	switch id {
	case menuAdmin:
		if t.tunnel != nil && !t.stopping {
			url, e := adminURL(t.dir, t.adminAddress)
			if e == nil {
				e = shellOpen(t.window, url)
			}
			t.showError(e)
		}
	case menuRestart:
		t.stop(false)
	case menuFolder:
		t.showError(shellOpen(t.window, t.dir))
	case menuError:
		t.showError(t.lastError)
	case menuExit:
		t.stop(true)
	}
}
func (t *trayHost) menu() {
	menu, _, _ := createMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	item := func(id uintptr, label string, disabled bool) {
		flags := uintptr(0)
		if disabled {
			flags = 3
		}
		appendMenu.Call(menu, flags, id, uintptr(unsafe.Pointer(utf16(label))))
	}
	item(0, "SSHTunnel Server "+server.Version+" · "+t.state(), true)
	appendMenu.Call(menu, 0x800, 0, 0)
	item(menuAdmin, "관리 화면 열기", t.tunnel == nil || t.stopping)
	item(menuRestart, "서버 다시 시작", t.stopping)
	item(menuFolder, "설정 / 로그 폴더 열기", false)
	if t.lastError != nil {
		item(menuError, "오류 상세 보기", false)
	}
	appendMenu.Call(menu, 0x800, 0, 0)
	item(menuExit, "서버 종료", false)
	var p point
	getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	setForeground.Call(t.window)
	// TPM_RETURNCMD | TPM_RIGHTBUTTON. 종료는 실제 서버 정리 완료 뒤 처리합니다.
	id, _, _ := trackMenu.Call(menu, 0x100|2, uintptr(p.X), uintptr(p.Y), 0, t.window, 0)
	postMessage.Call(t.window, 0, 0, 0)
	if id != 0 {
		t.command(id)
	}
}
func (t *trayHost) windowProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	if t.taskbarCreated != 0 && message == t.taskbarCreated {
		t.iconAdded = false
		t.updateIcon(true)
		return 0
	}
	switch message {
	case trayCallback:
		switch uint32(lparam) & 0xffff {
		case 0x7b, 0x400, 0x401:
			t.menu()
		}
		return 0
	case trayUpdate:
		t.processEvents()
		return 0
	case 0x111:
		t.command(wparam & 0xffff)
		return 0
	case 0x113:
		t.updateIcon(false)
		return 0
	case 0x10:
		t.stop(true)
		return 0
	case 0x11:
		return 1 // WM_QUERYENDSESSION
	case 0x16:
		if wparam != 0 {
			t.stop(true)
		}
		return 0
	case 2:
		postQuit.Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}
