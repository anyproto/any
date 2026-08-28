//go:build !mobile

package server

import (
	"testing"
)

// TestAcquirePIDLock_HostHoldsLock proves the !mobile shim delegates to the
// real Acquire: a second acquire of the same path fails while the first is
// held (the lock is real on desktop/server builds). The mobile counterpart
// (TestAcquirePIDLock_MobileNoOp, pidlock_acquire_mobile_test.go) asserts
// the opposite — that under -tags mobile the lock is bypassed.
func TestAcquirePIDLock_HostHoldsLock(t *testing.T) {
	dir := t.TempDir()
	lock, err := acquirePIDLock(dir)
	if err != nil {
		t.Fatalf("first acquirePIDLock: %v", err)
	}
	if lock == nil {
		t.Fatal("host acquirePIDLock returned nil lock; expected a real lock")
	}
	defer func() { _ = lock.Release() }()

	if _, err := acquirePIDLock(dir); err == nil {
		t.Fatal("second acquirePIDLock on a held path must fail on the host build")
	}
}
