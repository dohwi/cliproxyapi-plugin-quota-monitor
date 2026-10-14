//go:build !unix

package authdir

import "errors"

func mkfifo(string) error { return errors.New("unsupported") }
