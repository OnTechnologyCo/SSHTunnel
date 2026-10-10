//go:build !windows

package main

import "errors"

func defaultCommand() string   { return "run" }
func defaultTrayData() string  { return defaultData() }
func runTray(string) error     { return errors.New("트레이 모드는 Windows에서 지원합니다") }
func openAdmin(string) error   { return errors.New("관리 URL을 브라우저에서 직접 여세요") }
func startupLog(string, error) {}
