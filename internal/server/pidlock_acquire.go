//go:build !mobile

package server

// acquirePIDLock is the desktop/server path: a real single-process lock
// on the account dir. bootEngine calls this (covering BOTH bootAccount
// sites — startup and POST /v1/auth — since both funnel through
// bootEngine), so the lock behaves the same on every non-mobile build.
func acquirePIDLock(dir string) (*Lock, error) {
	return Acquire(dir)
}
