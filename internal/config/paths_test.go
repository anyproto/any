package config

import (
	"path/filepath"
	"testing"
)

// The single-instance lock splits its two files across the account dir:
// server.lock carries the OS lock, server.pid names the holder. They must
// never collide, and both must land inside the dir on every platform.
func TestLockAndPIDPaths(t *testing.T) {
	dir := filepath.Join("tmp", "acct")

	lock, pid := LockPath(dir), PIDPath(dir)
	if lock == pid {
		t.Fatal("LockPath and PIDPath must be different files")
	}
	for name, got := range map[string]string{"LockPath": lock, "PIDPath": pid} {
		if d := filepath.Dir(got); d != dir {
			t.Errorf("%s(%q) = %q, want it inside %q", name, dir, got, dir)
		}
	}
	if base := filepath.Base(lock); base != "server.lock" {
		t.Errorf("LockPath base = %q, want server.lock", base)
	}
	if base := filepath.Base(pid); base != "server.pid" {
		t.Errorf("PIDPath base = %q, want server.pid", base)
	}
}
