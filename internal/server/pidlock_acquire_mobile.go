//go:build mobile

package server

// acquirePIDLock is a NO-OP under the mobile build: it returns a nil
// *Lock (whose Release is already nil-tolerant) and never touches the
// filesystem. bootEngine calls this in place of Acquire, so BOTH
// bootAccount sites — startup (server.go) and POST /v1/auth
// (handlers_auth.go) — bypass the lock through the single funnel.
//
// Why bypass on mobile:
//   - In one in-process instance the inter-process PID lock is pointless:
//     there is no second process competing for the account dir.
//   - On iOS the lock's stale-PID liveness check (kill(pid,0)) false-
//     positives. The OS recycles PIDs aggressively, and jetsam can kill
//     the app with no chance to run Release(), leaving a stale pid file
//     whose recorded PID now names an unrelated live process — so the
//     next launch reads it as "locked" and refuses to boot.
func acquirePIDLock(path string) (*Lock, error) {
	return nil, nil
}
