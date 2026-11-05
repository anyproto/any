//go:build windows

package server

import (
	"errors"

	"golang.org/x/sys/windows"
)

// diskFull matches the two Win32 codes a full volume produces;
// syscall.ENOSPC is never returned on Windows.
func diskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
