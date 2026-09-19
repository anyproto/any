// Package main is the C-archive surface that embeds the `any` engine in
// an iOS app. Built with `-tags mobile -buildmode=c-archive` it
// links the full embedded dependency graph (any-sync + libp2p + QUIC +
// the modernc SQLite shim) and exposes five C entry points the Swift
// side calls over its lifecycle: AnyLibStart, AnyLibStartWithMode,
// AnyLibStop, AnyLibStopNow and AnyLibVersion.
//
// It lives under mobile/ rather than cmd/ because cmd/ is Go's convention
// for runnable binaries and a c-archive is not one — the Android gomobile
// shim sits beside it at mobile/android.
//
// All real lifecycle logic lives in internal/embedded — the shared,
// build-tag-free core both mobile shims sit on. This package is a thin
// host-idiom adapter: the //export wrappers only marshal C<->Go types and
// translate the core's typed errors to the C codes the Swift side
// mirrors. The plain Go functions (startEngine / stopEngine / errCode /
// copyBounded) carry that translation and are what the host-build test
// suite exercises; the //export wrappers themselves cannot be called from
// `go test`, and neither can anything typed in terms of C (cgo is not
// permitted in _test.go). That's why the core below is pure Go and the
// cgo layer is a two-line adapter over it.
package main

/*
#include <stdint.h>

// AnyLibStartResult carries the three things a host needs out of a boot
// attempt: whether it worked, where to talk to it, and — when it didn't
// work — the engine's own explanation.
//
// Fixed-size buffers returned BY VALUE, deliberately: no malloc, no
// free, no lifetime contract for the caller to get wrong. Both buffers
// are always NUL-terminated at the boundary, so a long message loses
// its tail rather than the terminator (512 bytes holds the observed
// boot errors).
//
// code:
//   0  ok
//   1  already running (stop the current instance first)
//   2  bad data directory (empty, or not creatable)
//   3  boot failed
//   4  the on-disk search index must be deleted and rebuilt
//   5  bad options (unknown mode, or managed without a control token)
typedef struct {
    int32_t code;
    char    address[64];  // "127.0.0.1:53421" when code == 0, empty otherwise
    char    message[512]; // the engine's own detail when code != 0, empty on success
} AnyLibStartResult;
*/
import "C"

import (
	"errors"
	"unicode/utf8"
	"unsafe"

	"github.com/anyproto/any/internal/embedded"
	"github.com/anyproto/any/internal/indexer"
)

// Codes returned in AnyLibStartResult.code. The Swift side mirrors this
// set and maps each to an error case, so the contract is: 0 means it is
// up and `address` is bound; anything else means it is not, and
// `message` says why.
//
// They are positive (and 0 means ok) because the code no longer shares a
// return slot with the port — the old surface packed both into one int,
// which is what forced the negative codes and left no room for a
// message. This is also the shape Android can adopt.
const (
	// codeOK: the engine is running and `address` holds the bound
	// host:port.
	codeOK = 0
	// codeAlreadyRunning: an instance is already started in this process.
	// The caller must stop the current one before starting again.
	// Maps from embedded.ErrAlreadyRunning.
	codeAlreadyRunning = 1
	// codeBadDataDir: the data directory argument is empty, or could not
	// be resolved / created (bad path, unwritable parent). Maps from
	// embedded.ErrBadDataDir.
	codeBadDataDir = 2
	// codeBootFailed: boot failed after the config validated — engine /
	// account boot, listener bind, or a config the runtime rejected.
	// Maps from a *embedded.BootError.
	codeBootFailed = 3
	// codeIndexRebuildRequired: the on-disk search index can't be opened
	// by this build and has to be deleted to rebuild. Distinct from
	// codeBootFailed because the host has a specific recovery to offer
	// ("reset local data") rather than a generic retry. Maps from
	// indexer.ErrIndexRebuildRequired, which arrives wrapped in a
	// *embedded.BootError — hence the ordering in errCode.
	codeIndexRebuildRequired = 4
	// codeBadOptions: an argument the host controls is invalid — an
	// unknown mode, or "managed" without a control token. A programming
	// error on the host side, never retryable and never a "reset local
	// data" case. Maps from embedded.ErrBadOptions.
	codeBadOptions = 5
)

// startResult is the Go-side mirror of C.AnyLibStartResult. Keeping the
// outcome in a plain Go value is what makes the whole translation
// testable on the host: the cgo layer below does nothing but copy these
// three fields into the C struct.
type startResult struct {
	code    int32
	address string
	message string
}

// startEngine boots the embedded engine and blocks until the listener
// binds (returning codeOK and the bound host:port) or boot fails
// (returning a code and the underlying error's message). It is the
// testable core behind the //export AnyLibStart wrapper.
//
// The search index is not a host parameter: FTS on, no embedder, same as
// the Android bind. pushPeerId/pushAddrs configure the
// push node (addrs comma-separated, see embedded.Options) — empty strings
// keep push off. mode/controlToken select the ownership mode ("" =
// standalone; "managed" needs a non-empty controlToken).
func startEngine(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken string) startResult {
	addr, err := embedded.Start(embedded.Options{
		DataDir:      dataDir,
		ListenAddr:   listenAddr,
		NodeconfYAML: nodeconfYAML,
		PushPeerId:   pushPeerId,
		PushAddrs:    pushAddrs,
		Mode:         mode,
		ControlToken: controlToken,
	})
	if err != nil {
		return startResult{code: errCode(err), message: err.Error()}
	}
	return startResult{code: codeOK, address: addr}
}

// errCode maps an internal/embedded error to its C code.
//
// The indexer arm comes FIRST and that ordering is load-bearing: a
// rebuild-required failure reaches us as
// &BootError{Err: fmt.Errorf("open indexer: %w", ErrIndexRebuildRequired)}
// (internal/server/engine.go), so the boot-class arm would swallow it if
// it ran first. Anything unrecognised funnels to codeBootFailed rather
// than crossing the C boundary untyped.
func errCode(err error) int32 {
	switch {
	case errors.Is(err, indexer.ErrIndexRebuildRequired):
		return codeIndexRebuildRequired
	case errors.Is(err, embedded.ErrAlreadyRunning):
		return codeAlreadyRunning
	case errors.Is(err, embedded.ErrBadDataDir):
		return codeBadDataDir
	case errors.Is(err, embedded.ErrBadOptions):
		return codeBadOptions
	default:
		// *embedded.BootError (and any unforeseen error) are all
		// boot-class config/boot failures.
		return codeBootFailed
	}
}

// stopEngine tears down the running engine via the core. graceful waits
// for the drain (embedded.Stop(true)); non-graceful returns promptly
// (embedded.StopNow). It is the testable core behind the //export
// AnyLibStop / AnyLibStopNow wrappers.
func stopEngine(graceful bool) {
	if graceful {
		embedded.Stop(true)
		return
	}
	embedded.StopNow()
}

// copyBounded writes s into dst, truncating so dst always ends
// NUL-terminated within its own bounds. It is the whole safety argument
// for the fixed-size buffers, so it is pure Go and directly tested.
//
// Truncation lands on a UTF-8 rune boundary: the strings that reach here
// are Go error texts and several carry an em dash, so a byte-exact cut
// could leave a half-encoded rune that Swift's
// String(validatingCString:) rejects outright. Dropping the partial rune
// costs at most three bytes of an already-truncated message.
func copyBounded(dst []byte, s string) {
	if len(dst) == 0 {
		return
	}
	if lim := len(dst) - 1; len(s) > lim {
		s = s[:lim]
		for len(s) > 0 {
			// DecodeLastRuneInString reports (RuneError, 1) only for an
			// invalid encoding; a genuine U+FFFD decodes with size 3, so
			// this trims the partial rune without eating a real one.
			if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size > 1 {
				break
			}
			s = s[:len(s)-1]
		}
	}
	dst[copy(dst, s)] = 0
}

// fillC is the only bridge between the pure-Go core and the C buffers:
// it reinterprets the C char array as bytes and hands it to copyBounded.
func fillC(dst []C.char, s string) {
	copyBounded(unsafe.Slice((*byte)(unsafe.Pointer(&dst[0])), len(dst)), s)
}

// versionString is allocated ONCE, at package init, and never freed.
// That is the whole lifetime contract for AnyLibVersion: the version is
// a build-stamped constant, so one allocation per process is honest and
// the caller has nothing to release. Do not hand this pointer to free().
var versionString = C.CString(embedded.Version())

// AnyLibStart boots the embedded engine with its data under dataDir,
// listening on listenAddr (pass "127.0.0.1:0" for an OS-assigned
// ephemeral port), joining the network described by nodeconfYAML.
//
// pushPeerId / pushAddrs configure the push-notification node. It is a
// direct out-of-band peer, not part of nodeconfYAML, but it pairs with
// the nodeconf choice (staging vs prod), so the host passes both from
// the same place. pushAddrs is comma-separated, the same format
// ANY_PUSH_ADDRS parses (e.g. "quic://host:port"). Push activates only
// when both are non-empty; empty strings mean every /v1/push endpoint
// returns 409 push.disabled.
//
// It blocks until the listener binds, then returns code 0 with the bound
// address, or a non-zero code with the engine's own error message. See
// the AnyLibStartResult typedef and the code constants.
//
// Any argument may be NULL and reads as an empty string (C.GoString
// treats NULL as ""), which spares the Swift caller a strdup for the
// push pair it usually leaves off.
//
//export AnyLibStart
func AnyLibStart(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs *C.char) C.AnyLibStartResult {
	return AnyLibStartWithMode(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, nil, nil)
}

// AnyLibStartWithMode is AnyLibStart plus the ownership mode. mode is
// "standalone" (or NULL/empty — the AnyLibStart behavior: the account
// resolves from the wallet on disk) or "managed": the host states the
// account over POST /v1/auth on every launch, keys never touch disk,
// and DELETE /v1/auth, account switch and POST /v1/shutdown are
// accepted only with controlToken (X-Any-Control-Token). A managed
// start without a token, or an unknown mode, fails with code 5 (see
// embedded.ErrBadOptions) before anything touches the data dir.
//
//export AnyLibStartWithMode
func AnyLibStartWithMode(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken *C.char) C.AnyLibStartResult {
	res := startEngine(
		C.GoString(dataDir),
		C.GoString(listenAddr),
		C.GoString(nodeconfYAML),
		C.GoString(pushPeerId),
		C.GoString(pushAddrs),
		C.GoString(mode),
		C.GoString(controlToken),
	)
	var out C.AnyLibStartResult
	out.code = C.int32_t(res.code)
	fillC(out.address[:], res.address)
	fillC(out.message[:], res.message)
	return out
}

// AnyLibStop gracefully stops the engine: it cancels the run context,
// waits for the graceful drain to complete, and clears the handle. It may
// block briefly. This is the async-stop path the Swift stop(graceful:)
// calls.
//
//export AnyLibStop
func AnyLibStop() {
	stopEngine(true)
}

// AnyLibStopNow hard-stops the engine and returns promptly (well under
// 1s): it cancels the run context to force the listener closed but does
// NOT wait for a clean drain. The iOS beginBackgroundTask expiration
// handler calls this synchronously, so it must not block on shutdown.
//
//export AnyLibStopNow
func AnyLibStopNow() {
	stopEngine(false)
}

// AnyLibVersion returns the build-stamped version string of the linked
// archive ("any <version> (commit <sha>, built <ts>)"), so the host can
// report which one it is actually running.
//
// The pointer is valid for the process lifetime and must NOT be freed —
// it is allocated once at init (see versionString). Callable before
// AnyLibStart.
//
//export AnyLibVersion
func AnyLibVersion() *C.char {
	return versionString
}

func main() {}
