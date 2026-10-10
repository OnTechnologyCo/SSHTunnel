//go:build !windows

package main

func runService(dir string) (bool, error) { return false, nil }
