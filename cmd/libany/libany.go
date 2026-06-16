// Command libany is the C-archive surface that embeds the `any` server
// in an iOS app (IOS-6169). Built with `-tags mobile -buildmode=c-archive`
// it links the full embedded-server dependency graph (any-sync + libp2p +
// QUIC + the modernc SQLite shim) and exposes three C entry points the
// Swift side calls over its lifecycle: AnyServerStart, AnyServerStop, and
// AnyServerStopNow.
//
// The //export wrappers are intentionally thin — they only marshal
// C<->Go types and call the plain Go functions (startServer / stopServer)
// that carry the real logic. The Go functions are what the host-build
// test suite exercises; the //export wrappers cannot be called from
// `go test`.
package main

import "C"

import (
	"context"
	"net"
	"runtime/debug"
	"strconv"
	"sync"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

// Negative error codes returned by AnyServerStart (and startServer). The
// Swift side (Phase 3.3) mirrors this set and maps each to an
// AnyServiceError, so the contract is: a successful start returns the
// chosen port (a positive int); any failure returns one of these. Keep
// the set minimal — only add a code a caller actually distinguishes.
const (
	// errAlreadyRunning: a server is already started in this process.
	// The caller must Stop the current one before starting again.
	errAlreadyRunning = -1
	// errBadDataDir: the data directory argument is empty, or could not
	// be resolved / created (bad path, unwritable parent).
	errBadDataDir = -2
	// errBoot: boot failed before the listener bound (engine/account
	// boot error, or config validation). The data dir was fine; the
	// server could not come up.
	errBoot = -3
)

// gomemlimitBytes is the GOMEMLIMIT soft cap applied at the start of
// startServer to keep Go's GC pacing from letting RSS drift into iOS
// jetsam territory.
//
// Basis: Phase 0 measured ~90 MB steady-state RSS on the host with one
// space and embedder="none" (FTS-only). 256 MiB gives ~2.8x headroom
// over that baseline — enough that transient allocation spikes (a query
// fan-out, an SSE burst) don't thrash GC against the limit, while still
// well under the per-app memory ceiling iOS enforces before jetsam on
// modern devices. It is a SOFT limit: Go runs GC harder as it approaches
// the cap rather than failing. This number is a host-derived starting
// point — on-device profiling may justify re-tuning it (a device under
// memory pressure has a tighter real budget than the host).
const gomemlimitBytes = 256 << 20 // 256 MiB

// handle is the package-level, mutex-guarded server handle. Only one
// server runs per process; start guards against a double-start, and the
// stop paths reach the running server through this.
var (
	handleMu sync.Mutex
	handle   *serverHandle
)

type serverHandle struct {
	cancel context.CancelFunc // cancels the RunWithListener ctx
	done   chan struct{}      // closed when RunWithListener returns
	port   int                // the bound port
}

// startServer boots the embedded server on a background goroutine and
// blocks until the listener binds (returning the chosen port, positive)
// or boot fails (returning a negative error code). It is the testable
// core behind the //export AnyServerStart wrapper.
func startServer(dataDir string, requestedPort int) int {
	debug.SetMemoryLimit(gomemlimitBytes)

	handleMu.Lock()
	defer handleMu.Unlock()

	if handle != nil {
		return errAlreadyRunning
	}

	if code := validateDataDir(dataDir); code != 0 {
		return code
	}

	cfg := config.Defaults()
	cfg.DataDir = dataDir
	cfg.Listen.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(requestedPort))
	cfg.Index.Enabled = true
	// FTS-only: the local llama.cpp embedder is compiled out under
	// -tags mobile, and "none" makes NewEmbedder return a true-nil so
	// the compiled-out NewLocal is never reached.
	cfg.Index.Embedder = "none"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	listened := make(chan int, 1) // delivers the bound port once
	runErr := make(chan error, 1) // delivers RunWithListener's return

	go func() {
		defer close(done)
		err := server.RunWithListener(ctx, cfg, func(addr string) {
			listened <- portOf(addr)
		})
		runErr <- err
	}()

	// Block until the listener binds (success) or boot errors out before
	// binding.
	select {
	case port := <-listened:
		handle = &serverHandle{cancel: cancel, done: done, port: port}
		return port
	case <-runErr:
		// RunWithListener returned before onListen fired: boot failed
		// before the listener bound. Drain the goroutine and report.
		// (A serve-loop failure AFTER binding can't reach here — onListen
		// has already delivered the port and start has already returned
		// it; that failure surfaces to the stop path via h.done.)
		cancel()
		<-done
		return errBoot
	}
}

// stopServer tears down the running server. graceful cancels the ctx and
// waits for RunWithListener to return (echo's graceful drain); non-graceful
// cancels the ctx and returns without waiting for a clean drain, so the
// hard-stop path stays bounded for the iOS background-task expiration
// handler. Either way the handle is cleared so a subsequent start is clean.
func stopServer(graceful bool) {
	handleMu.Lock()
	h := handle
	handle = nil
	handleMu.Unlock()

	if h == nil {
		return
	}

	h.cancel()
	if graceful {
		// Wait for RunWithListener to fully return — echo's graceful
		// shutdown closed the listener and drained in-flight handlers.
		<-h.done
	}
	// Non-graceful: ctx is cancelled (which unblocks RunWithListener's
	// select and triggers e.Shutdown); we return immediately without
	// waiting on h.done so the caller's deadline is never at our mercy.
}

// validateDataDir returns 0 if the data dir is usable, or errBadDataDir
// otherwise. It pre-resolves and creates the directory so a bad path is
// reported as errBadDataDir rather than folded into errBoot.
func validateDataDir(dataDir string) int {
	if dataDir == "" {
		return errBadDataDir
	}
	if _, err := config.EnsureDataDir(dataDir); err != nil {
		return errBadDataDir
	}
	return 0
}

// portOf extracts the port from a "host:port" address. Returns 0 if it
// can't be parsed (not expected — the address comes from net.Listen).
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
// listening on 127.0.0.1:requestedPort (pass 0 for an OS-assigned
// ephemeral port). It blocks until the listener binds, then returns the
// chosen port (positive) or a negative error code (see the const block).
//
//export AnyServerStart
func AnyServerStart(dataDir *C.char, requestedPort C.int) C.int {
	return C.int(startServer(C.GoString(dataDir), int(requestedPort)))
}

// AnyServerStop gracefully stops the server: it cancels the run context,
// waits for the graceful drain to complete, and clears the handle. It
// may block briefly. This is the async-stop path the Swift stop(graceful:)
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
