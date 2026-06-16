//go:build mobile

package server

import (
	"path/filepath"
	"testing"
)

// TestAcquirePIDLock_MobileNoOp proves the pidlock is bypassed under the
// mobile build at the single funnel (bootEngine -> acquirePIDLock), which
// covers BOTH bootAccount call sites (startup + POST /v1/auth). It returns
// a nil lock and never blocks a second acquire of the same path — the exact
// opposite of the host build (TestAcquirePIDLock_HostHoldsLock).
func TestAcquirePIDLock_MobileNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.pid")

	lock, err := acquirePIDLock(path)
	if err != nil {
		t.Fatalf("mobile acquirePIDLock must not error: %v", err)
	}
	if lock != nil {
		t.Fatalf("mobile acquirePIDLock must return a nil lock (no-op), got %v", lock)
	}
	// Release on the nil lock is safe (nil-tolerant).
	if err := lock.Release(); err != nil {
		t.Fatalf("nil lock Release: %v", err)
	}

	// A second acquire of the same path also no-ops — never blocks.
	if _, err := acquirePIDLock(path); err != nil {
		t.Fatalf("second mobile acquirePIDLock must not error: %v", err)
	}
}
