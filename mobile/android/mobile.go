//go:build android || gomobile

// Package mobile is the gomobile bind surface for embedding the any HTTP
// server inside an Android (or, in principle, iOS) process. Only basic
// types cross the bridge — keep the surface flat: top-level funcs over
// strings and errors, plus StartResult, a struct of basic fields. All
// lifecycle logic lives in internal/embedded; this
// package is a thin host-idiom adapter over it so both mobile shims (this
// gomobile one and the iOS c-archive at mobile/ios) share one tested core.
//
// The package is NAMED mobile while its directory is mobile/android:
// gomobile derives the generated Java class from the package name, so the
// name is the AAR's public API and the directory is free to move. Do not
// "fix" the mismatch.
package mobile

import (
	"github.com/anyproto/any/internal/embedded"
)

// Start boots the any server. dataDir maps to Context.getFilesDir() on Android.
// listenAddr is typically "127.0.0.1:2046"; pass "127.0.0.1:0" to let the OS
// pick a free port (read it back with Address()). nodeconfYAML overrides
// the network: pass "" for the embedded PRODUCTION nodeconf that ships in
// the AAR, or a conf's YAML text to join another network (staging, local
// infra). Hosts targeting production need not vendor a copy of the conf —
// an AAR bump carries a new one.
//
// Returns once the server has bound the listener (success) or failed during
// wallet / SDK / listener setup (error). The error string is suitable for
// surfacing to the host app — it carries the underlying Go error message.
// Subsequent calls while a server is running return an error.
//
// The gomobile bind surface keeps the bound address out of the return tuple
// (a flat `error`-only signature); read it back with Address(). Index policy:
// BM25 on, embedder "none".
func Start(dataDir, listenAddr, nodeconfYAML string) error {
	return StartWithPush(dataDir, listenAddr, nodeconfYAML, "", "")
}

// StartWithPush is Start plus the push-notification node. The
// push node is a direct out-of-band peer, not part of nodeconfYAML — it
// pairs with the nodeconf choice, so a host overriding the network passes
// both from the same place. pushPeerId is the node's peer id; pushAddrs
// its dial addresses, comma-separated (same format ANY_PUSH_ADDRS
// parses, e.g. "quic://host:port" or "host:port"). Empty strings are the
// Start behavior: the production push node when nodeconfYAML is empty
// too, push off (409 push.disabled) when it is not.
func StartWithPush(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs string) error {
	return StartWithMode(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, "", "")
}

// StartWithMode is StartWithPush plus the ownership mode. mode is
// "standalone" (or "" — the Start behavior: the account resolves from
// the wallet on disk) or "managed": the host states the account over
// POST /v1/auth on every launch, keys never touch disk, and
// DELETE /v1/auth, account switch and POST /v1/shutdown are accepted
// only with controlToken (X-Any-Control-Token). A managed start without
// a token is an error (embedded.ErrBadOptions).
func StartWithMode(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken string) error {
	_, err := start(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken)
	return err
}

// StartResult is the outcome of StartWithResult. Code is the start code
// (embedded.Code, the iOS shim's set): 0 means up with Address bound,
// anything else means not, and Message says why.
type StartResult struct {
	Code    int32
	Address string
	Message string
}

// StartWithResult is StartWithMode with a typed outcome: it never throws,
// and a failed start carries its code in the result.
func StartWithResult(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken string) *StartResult {
	addr, err := start(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken)
	if err != nil {
		return &StartResult{Code: embedded.Code(err), Message: err.Error()}
	}
	return &StartResult{Code: embedded.CodeOK, Address: addr}
}

// start is the one call into the core. The index policy (FTS on, no
// embedder) is not a bind parameter.
func start(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken string) (string, error) {
	return embedded.Start(embedded.Options{
		DataDir:      dataDir,
		ListenAddr:   listenAddr,
		NodeconfYAML: nodeconfYAML,
		PushPeerId:   pushPeerId,
		PushAddrs:    pushAddrs,
		Mode:         mode,
		ControlToken: controlToken,
	})
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
