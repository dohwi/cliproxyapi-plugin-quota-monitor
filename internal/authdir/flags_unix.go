//go:build unix

package authdir

import "syscall"

// nonBlockFlag stops a FIFO swapped in after the Lstat check from blocking open(2).
const nonBlockFlag = syscall.O_NONBLOCK
