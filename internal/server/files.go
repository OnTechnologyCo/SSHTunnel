package server

import (
	"ontech/sshtunnel/internal/securefile"
	"os"
)

func PrivateDir(path string) error           { return securefile.PrivateDir(path) }
func PrivateFile(path string) error          { return securefile.PrivateFile(path) }
func atomicReplace(from, to string) error    { return securefile.AtomicReplace(from, to) }
func lockData(path string) (*os.File, error) { return securefile.Lock(path) }
