//go:build windows

package server

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func privateACL(path string, dir bool) error {
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sid := token.User.Sid.String()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	trusted := map[string]bool{sid: true, "S-1-5-18": true, "S-1-5-32-544": true, "S-1-5-19": true}
	if !trusted[owner.String()] {
		return errors.New("데이터 파일의 소유자를 신뢰할 수 없습니다")
	}
	flags := ""
	if dir {
		flags = "OICI"
	}
	sddl := "D:P(A;" + flags + ";FA;;;SY)(A;" + flags + ";FA;;;BA)(A;" + flags + ";FA;;;LS)"
	if sid != "S-1-5-18" && sid != "S-1-5-32-544" && sid != "S-1-5-19" {
		sddl += "(A;" + flags + ";FA;;;" + sid + ")"
	}
	next, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	acl, _, err := next.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
func checkPath(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		ptr, e := windows.UTF16PtrFromString(p)
		if e != nil {
			return e
		}
		attrs, e := windows.GetFileAttributes(ptr)
		if e == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("재분석 지점 데이터 경로를 사용할 수 없습니다")
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
func PrivateDir(path string) error {
	if err := checkPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return privateACL(path, true)
}
func PrivateFile(path string) error {
	if err := checkPath(path); err != nil {
		return err
	}
	return privateACL(path, false)
}
func atomicReplace(from, to string) error {
	a, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func lockData(path string) (*os.File, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(h), path), nil
}
