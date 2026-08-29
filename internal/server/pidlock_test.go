package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/config"
)

func TestAcquire_CreatesAndReleases(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := os.Stat(config.LockPath(dir)); err != nil {
		t.Fatalf("lock file missing after acquire: %v", err)
	}
	if _, err := os.Stat(config.PIDPath(dir)); err != nil {
		t.Fatalf("pid file missing after acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// A second Acquire of a held dir must fail. The OS lock conflicts across
// distinct open file descriptions even inside one process — flock(2) on
// unix, byte-range locks on Windows — so one process proves it.
func TestAcquire_SecondAcquireBlocked(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer func() { _ = lock.Release() }()

	_, err = Acquire(dir)
	var locked *ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if locked.PID != os.Getpid() {
		t.Fatalf("expected holder PID %d, got %d", os.Getpid(), locked.PID)
	}
}

func TestAcquire_ReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	again, err := Acquire(dir)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	_ = again.Release()
}

// Release must never unlink: under an OS lock a removed file lets the
// next process create a fresh inode and lock THAT, so both would hold
// "the" lock.
func TestAcquire_ReleaseKeepsFiles(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	for _, path := range []string{config.LockPath(dir), config.PIDPath(dir)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s must survive release: %v", filepath.Base(path), err)
		}
	}
}

// The pid is advisory: losing it costs the holder's name in the error,
// never the refusal itself.
func TestAcquire_MissingPIDFileTolerated(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = lock.Release() }()

	if err := os.Remove(config.PIDPath(dir)); err != nil {
		t.Fatal(err)
	}

	_, err = Acquire(dir)
	var locked *ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if locked.PID != 0 {
		t.Fatalf("expected PID 0 with no pid file, got %d", locked.PID)
	}
	if locked.Error() == "" {
		t.Fatal("ErrLocked.Error() must render without a pid")
	}
}

// A stale pid file — left by a crashed holder — must not block: the
// kernel released the lock when that process died.
func TestAcquire_StalePIDFileDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(config.PIDPath(dir), []byte("4194303"), 0o600); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("a stale pid file must not block acquisition: %v", err)
	}
	defer func() { _ = lock.Release() }()

	if got := readHolderPID(config.PIDPath(dir)); got != os.Getpid() {
		t.Fatalf("expected pid file rewritten to %d, got %d", os.Getpid(), got)
	}
}
