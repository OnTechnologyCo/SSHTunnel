//go:build windows

// 실제 로그인 세션에서 트레이 등록과 숨김 창을 확인하는 테스트 전용 도구입니다.
package main

import (
	"encoding/json"
	"flag"
	"golang.org/x/sys/windows"
	"os"
	"time"
	"unsafe"
)

type rect struct{ Left, Top, Right, Bottom int32 }
type identifier struct {
	Size   uint32
	Window uintptr
	ID     uint32
	GUID   windows.GUID
}

func main() {
	pid := flag.Uint("pid", 0, "owned test server process ID")
	out := flag.String("out", "", "JSON report")
	flag.Parse()
	user := windows.NewLazySystemDLL("user32.dll")
	shell := windows.NewLazySystemDLL("shell32.dll")
	enum := user.NewProc("EnumWindows")
	getPID := user.NewProc("GetWindowThreadProcessId")
	visible := user.NewProc("IsWindowVisible")
	class := user.NewProc("GetClassNameW")
	var host uintptr
	visibleCount := 0
	callback := windows.NewCallback(func(hwnd, lparam uintptr) uintptr {
		var owner uint32
		getPID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != uint32(*pid) {
			return 1
		}
		v, _, _ := visible.Call(hwnd)
		if v != 0 {
			visibleCount++
		}
		var name [256]uint16
		class.Call(hwnd, uintptr(unsafe.Pointer(&name[0])), 256)
		if windows.UTF16ToString(name[:]) == "OnTech.SSHTunnel.TrayHost.v1" {
			host = hwnd
		}
		return 1
	})
	var bounds rect
	result := uintptr(1)
	for i := 0; i < 30; i++ {
		host = 0
		visibleCount = 0
		enum.Call(callback, 0)
		if host != 0 {
			n := identifier{Window: host, ID: 1}
			n.Size = uint32(unsafe.Sizeof(n))
			result, _, _ = shell.NewProc("Shell_NotifyIconGetRect").Call(uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&bounds)))
			if result == 0 {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	success := host != 0 && result == 0 && visibleCount == 0
	posted := uintptr(0)
	if host != 0 {
		posted, _, _ = user.NewProc("PostMessageW").Call(host, 0x10, 0, 0)
	}
	report := map[string]any{"success": success, "processID": *pid, "hostWindowFound": host != 0, "visibleWindows": visibleCount, "iconRegistered": result == 0, "iconHRESULT": uint32(result), "iconBounds": bounds, "gracefulClosePosted": posted != 0}
	data, _ := json.MarshalIndent(report, "", "  ")
	if e := os.WriteFile(*out, data, 0600); e != nil {
		os.Exit(2)
	}
	if !success {
		os.Exit(1)
	}
}
