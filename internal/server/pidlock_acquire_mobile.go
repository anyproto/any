//go:build mobile

package server

// acquirePIDLock is a NO-OP under the mobile build: it returns a nil
// *Lock (whose Release is already nil-tolerant) and never touches the
// filesystem. bootEngine calls this in place of Acquire, so BOTH
// bootAccount sites — startup (server.go) and POST /v1/auth
// (handlers_auth.go) — bypass the lock through the single funnel.
//
// Why bypass on mobile: in one in-process instance the inter-process
// lock is pointless — there is no second process competing for the
// account dir.
func acquirePIDLock(dir string) (*Lock, error) {
	return nil, nil
}
