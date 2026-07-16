//go:build android || gomobile

// Package mobile is the gomobile bind surface for embedding the any HTTP
// server inside an Android (or, in principle, iOS) process. Only basic
// types cross the bridge — keep the surface flat (top-level funcs,
// string/error only). All lifecycle logic lives in internal/embedded; this
// package is a thin host-idiom adapter over it so both mobile shims (this
// gomobile one and the iOS c-archive) share one tested core (IOS-6169).
package mobile

import (
	"github.com/anyproto/any/internal/embedded"
)

// Start boots the any server. dataDir maps to Context.getFilesDir() on Android.
// listenAddr is typically "127.0.0.1:7001"; pass "127.0.0.1:0" to let the OS
// pick a free port (read it back with Address()). nodeconfYAML is the
// staging.yml contents — required, because the AAR has no filesystem
// fallback for any-sync's nodeconf.
//
// Returns once the server has bound the listener (success) or failed during
// wallet / SDK / listener setup (error). The error string is suitable for
// surfacing to the host app — it carries the underlying Go error message.
// Subsequent calls while a server is running return an error.
//
// The gomobile bind surface keeps the bound address out of the return tuple
// (a flat `error`-only signature); read it back with Address(). The core's
// index policy ("none" embedder, FTS off under gomobile) reduces to the same
// runtime as before — no embedder is linked on the gomobile bind.
func Start(dataDir, listenAddr, nodeconfYAML string) error {
	// indexEnabled=true is a no-op here: gomobile forces capFTS off at
	// compile time (no `fts` tag), so the core gate stays false regardless.
	_, err := embedded.Start(dataDir, listenAddr, nodeconfYAML, true)
	return err
}

// Stop signals graceful shutdown and waits for the server goroutine to exit.
// Safe to call when the server is not running.
func Stop() error {
	embedded.Stop(true)
	return nil
}

// StopNow hard-stops the server and returns promptly without waiting for a
// clean drain. Use it from a deadline-bounded teardown (e.g. an OS-imposed
// background-task expiration). Safe to call when the server is not running.
func StopNow() {
	embedded.StopNow()
}

// Address returns the actually-bound listen address (host:port). Differs
// from the listenAddr passed to Start when ":0" was used and the OS picked
// a port. Empty when the server is not running.
func Address() string {
	return embedded.Address()
}

// Version returns the build-stamped version string ("any <version> (commit
// <sha>, built <ts>)").
func Version() string {
	return embedded.Version()
}
