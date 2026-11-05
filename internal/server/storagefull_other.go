//go:build !windows

package server

import (
	"errors"
	"syscall"
)

func diskFull(err error) bool { return errors.Is(err, syscall.ENOSPC) }
