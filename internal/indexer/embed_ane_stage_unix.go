//go:build llamacpp && !android && !ios && unix

package indexer

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with this pid runs, another
// user's included.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
