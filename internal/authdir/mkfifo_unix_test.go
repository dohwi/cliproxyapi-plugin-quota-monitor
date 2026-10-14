//go:build unix

package authdir

import "syscall"

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }
