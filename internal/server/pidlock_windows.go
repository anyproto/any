//go:build windows

package server

import "errors"

type Lock struct{}

type ErrLocked struct {
	PID  int
	Path string
}

func (e *ErrLocked) Error() string { return "pidfile is locked" }

func Acquire(path string) (*Lock, error) {
	return nil, errors.New("PID lock is not implemented on Windows yet (see docs/07-roadmap.md open question #8)")
}

func (l *Lock) Release() error { return nil }
