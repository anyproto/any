//go:build llamacpp && !android && !ios && windows

package indexer

import (
	"errors"
	"syscall"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259 // GetExitCodeProcess of a process that runs
)

// processAlive reports whether a process with this pid runs, another
// user's included.
func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	return syscall.GetExitCodeProcess(h, &code) != nil || code == stillActive
}
