//go:build !windows

package server

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Lock is an exclusive advisory lock held by writing a PID file at a known
// path. A live PID in the file blocks acquisition; a stale PID (process
// no longer exists) is reclaimed silently.
type Lock struct {
	path string
}

// Acquire takes the PID lock. Returns ErrLocked when another live process
// holds the file.
func Acquire(path string) (*Lock, error) {
	if existing, ok, err := readPID(path); err != nil {
		return nil, err
	} else if ok && alive(existing) {
		return nil, &ErrLocked{PID: existing, Path: path}
	}

	// Either no file, unreadable PID, or stale — overwrite.
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, fmt.Errorf("write pid file %s: %w", path, err)
	}
	return &Lock{path: path}, nil
}

// Release removes the PID file. Safe to call from a defer — no-op if the
// file has already been deleted.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	err := os.Remove(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ErrLocked is returned when Acquire finds an active process holding the
// lock.
type ErrLocked struct {
	PID  int
	Path string
}

func (e *ErrLocked) Error() string {
	return fmt.Sprintf("pidfile %s is locked by PID %d", e.Path, e.PID)
}

func readPID(path string) (int, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, false, nil
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, false, nil
	}
	return pid, true, nil
}

// alive probes whether the given PID names a running process on POSIX.
// kill(pid, 0) is the standard, race-free test.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
