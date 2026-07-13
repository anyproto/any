// Command anyserver is the C-archive surface that embeds the `any` server
// in an iOS app (IOS-6169). Built with `-tags 'mobile fts'
// -buildmode=c-archive` it links the full embedded-server dependency graph
// (any-sync + libp2p + QUIC + the modernc SQLite shim) and exposes three C
// entry points the Swift side calls over its lifecycle: AnyServerStart,
// AnyServerStop, and AnyServerStopNow.
//
// All real lifecycle logic lives in internal/embedded — the shared,
// build-tag-free core both mobile shims sit on. This package is a thin
// host-idiom adapter: the //export wrappers only marshal C<->Go types and
// translate the core's typed errors to the negative C error codes the
// Swift side mirrors. The plain Go functions (startServer / stopServer)
// carry that translation and are what the host-build test suite exercises;
// the //export wrappers themselves cannot be called from `go test`.
package main

import "C"

import (
	"errors"
	"net"
	"strconv"

	"github.com/anyproto/any/internal/embedded"
)

// Negative error codes returned by AnyServerStart (and startServer). The
// Swift side (Phase C) mirrors this set and maps each to an
// AnyServiceError, so the contract is: a successful start returns the
// bound port (a positive int); any failure returns one of these. The codes
// mirror the internal/embedded sentinels one-to-one — see startServer for
// the mapping.
const (
	// errAlreadyRunning: a server is already started in this process.
	// The caller must Stop the current one before starting again.
	// Maps from embedded.ErrAlreadyRunning.
	errAlreadyRunning = -1
	// errBadDataDir: the data directory argument is empty, or could not
	// be resolved / created (bad path, unwritable parent). Maps from
	// embedded.ErrBadDataDir.
	errBadDataDir = -2
	// errBoot: boot failed after the config validated — engine/account
	// boot, listener bind, or a config the runtime rejected. Maps from a
	// *embedded.BootError and from
	// embedded.ErrNodeconfRequired (a boot-class config error: the host
	// supplied no nodeconf, so there is nothing to boot against).
	errBoot = -3
)

// startServer boots the embedded server and blocks until the listener
// binds (returning the bound port, positive) or boot fails (returning a
// negative error code; see the const block). It is the testable core
// behind the //export AnyServerStart wrapper: it calls embedded.Start and
// maps the returned addr->port and err->code, so both halves of the
// contract are exercised on the host.
func startServer(dataDir, listenAddr, nodeconfYAML string, indexEnabled bool) int {
	addr, err := embedded.Start(dataDir, listenAddr, nodeconfYAML, indexEnabled)
	if err != nil {
		return errCode(err)
	}
	return portOf(addr)
}

// errCode maps an internal/embedded error to its negative C error code.
// ErrAlreadyRunning and ErrBadDataDir map directly; ErrNodeconfRequired
// and any *BootError both fold into errBoot (a boot-class failure — the
// server could not come up). Any other error also funnels to errBoot
// rather than crossing the C boundary untyped.
func errCode(err error) int {
	switch {
	case errors.Is(err, embedded.ErrAlreadyRunning):
		return errAlreadyRunning
	case errors.Is(err, embedded.ErrBadDataDir):
		return errBadDataDir
	default:
		// embedded.ErrNodeconfRequired and *embedded.BootError (and any
		// unforeseen error) are all boot-class config/boot failures.
		return errBoot
	}
}

// stopServer tears down the running server via the core. graceful waits
// for the drain (embedded.Stop(true)); non-graceful returns promptly
// (embedded.StopNow). It is the testable core behind the //export
// AnyServerStop / AnyServerStopNow wrappers.
func stopServer(graceful bool) {
	if graceful {
		embedded.Stop(true)
		return
	}
	embedded.StopNow()
}

// portOf extracts the port from a "host:port" address. Returns 0 if it
// can't be parsed (not expected — the address comes from net.Listen via
// embedded.Start).
func portOf(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// AnyServerStart boots the embedded server with its data under dataDir,
// listening on listenAddr (pass "127.0.0.1:0" for an OS-assigned ephemeral
// port), joining the network described by nodeconfYAML. indexEnabled turns
// the FTS index on or off: pass false in the share extension (skip indexing
// for the memory headroom — nothing there searches), true in the app if it
// wants engine search. A build without the `fts` tag ignores it — the
// indexer stays dormant regardless. It blocks until the listener binds,
// then returns the bound port (positive) or a negative error code (see the
// const block).
//
//export AnyServerStart
func AnyServerStart(dataDir, listenAddr, nodeconfYAML *C.char, indexEnabled C._Bool) C.int {
	return C.int(startServer(C.GoString(dataDir), C.GoString(listenAddr), C.GoString(nodeconfYAML), bool(indexEnabled)))
}

// AnyServerStop gracefully stops the server: it cancels the run context,
// waits for the graceful drain to complete, and clears the handle. It may
// block briefly. This is the async-stop path the Swift stop(graceful:)
// calls.
//
//export AnyServerStop
func AnyServerStop() {
	stopServer(true)
}

// AnyServerStopNow hard-stops the server and returns promptly (well under
// 1s): it cancels the run context to force the listener closed but does
// NOT wait for a clean drain. The iOS beginBackgroundTask expiration
// handler calls this synchronously, so it must not block on shutdown.
//
//export AnyServerStopNow
func AnyServerStopNow() {
	stopServer(false)
}

func main() {}
