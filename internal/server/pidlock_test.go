//go:build !windows

package server

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAcquire_CreatesAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.pid")

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pid file missing after acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pid file still present after release: %v", err)
	}
}

func TestAcquire_LivePIDBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.pid")

	// Our own PID is, by definition, alive.
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Acquire(path)
	var locked *ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if locked.PID != os.Getpid() {
		t.Fatalf("expected PID %d, got %d", os.Getpid(), locked.PID)
	}
}

func TestAcquire_ReclaimsStalePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.pid")

	// PID 1 is init/systemd — always alive on Linux. We need something
	// that's definitely dead. Spawn true(1), wait for it, then use its
	// recycled PID, which is ~certainly not live by the time we probe.
	// Simpler: write an obviously-out-of-range PID. On Linux kernel.pid_max
	// caps at 4194304; 4194303 is beyond that on default installs.
	if err := os.WriteFile(path, []byte("4194303"), 0o644); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("expected stale PID to be reclaimed, got %v", err)
	}
	defer lock.Release()

	// The file should now hold OUR pid.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatalf("parse reclaimed pid: %v", err)
	}
	if got != os.Getpid() {
		t.Fatalf("expected reclaimed file to hold PID %d, got %d", os.Getpid(), got)
	}
}

func TestAcquire_EmptyFileReclaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.pid")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("expected empty pidfile to be reclaimed, got %v", err)
	}
	_ = lock.Release()
}
