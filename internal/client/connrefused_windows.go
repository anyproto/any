//go:build windows

package client

import (
	"errors"

	"golang.org/x/sys/windows"
)

// connRefused matches Winsock's refusal: on Windows a refused dial is
// WSAECONNREFUSED, which syscall.ECONNREFUSED does not match.
func connRefused(err error) bool { return errors.Is(err, windows.WSAECONNREFUSED) }
