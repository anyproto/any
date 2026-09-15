//go:build !windows

package client

import (
	"errors"
	"syscall"
)

func connRefused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }
