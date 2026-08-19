// Package embedded is the shared, build-tag-free lifecycle core for
// embedding the `any` HTTP server inside a host process. Both mobile
// binding shims sit on top of it: the gomobile `mobile` package (Android)
// and the c-archive `cmd/anyserver` shim (iOS). Each shim is a thin
// host-idiom adapter; this package owns the real logic so it is exercised
// once by a single host-runnable test suite (IOS-6169).
//
// What it owns:
//   - a mutex-guarded, single-server-per-process singleton with a
//     double-start guard;
//   - a drain (`stopping`) channel so a fast restart waits for a prior
//     hard-stop's teardown before re-booting on the same data dir
//     (otherwise a background->foreground restart could run two engines
//     against one data dir);
//   - the GOMEMLIMIT soft cap via debug.SetMemoryLimit (iOS jetsam AND
//     Android's low-memory killer);
//   - config assembly: config.Defaults() + DataDir + Listen.Addr +
//     Network.Nodeconf + index policy (Embedder="none", Index.Enabled =
//     the compiled FTS cap) + headless (WebUI.Enabled=false, IOS-116) +
//     the push node peer (Options.PushPeerId/PushAddrs → cfg.Push,
//     SYN-83);
//   - the run via main's canonical embedder seam, server.RunWith.
//
// The embedded path never reads config.yaml or ANY_* env — the host owns
// every input and passes it explicitly through Options. Env in a mobile
// app process is not a configuration channel.
//
// It installs NO os/signal handlers — the caller owns the lifecycle.
package embedded

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/server"
	"github.com/anyproto/any/internal/version"
)

// Exported sentinel errors. Both shims map from this one source so the
// host error contract has a single definition. The iOS c-archive shim
// maps each to a negative C error code the Swift side mirrors:
//
//	ErrAlreadyRunning -> -1
//	ErrBadDataDir     -> -2
//	a boot failure    -> -3 (see BootError)
//
// An empty nodeconfYAML is not an error: it selects the embedded
// production nodeconf, the same default the CLI and desktop sidecar boot
// on. Hosts pass YAML only to override the network.
var (
	// ErrAlreadyRunning: a server is already started in this process. The
	// caller must Stop the current one before starting again.
	ErrAlreadyRunning = errors.New("embedded: server already running")
	// ErrBadDataDir: the data directory argument is empty, or could not be
	// resolved / created (bad path, unwritable parent).
	ErrBadDataDir = errors.New("embedded: bad data directory")
)

// BootError wraps a failure that occurred while bringing the server up
// after the config validated — engine/account boot, listener bind, or a
// config the runtime rejected. The iOS shim maps any BootError to -3. The
// distinct type lets a caller tell "the config was fine but boot failed"
// from "the input was bad" (ErrBadDataDir) without string matching.
type BootError struct{ Err error }

func (e *BootError) Error() string {
	if e.Err == nil {
		return "embedded: server boot failed"
	}
	return "embedded: server boot failed: " + e.Err.Error()
}

func (e *BootError) Unwrap() error { return e.Err }

// gomemlimitBytes is the GOMEMLIMIT soft cap applied at the start of
// Start to keep Go's GC pacing from letting RSS drift into iOS jetsam
// (and Android low-memory-killer) territory.
//
// Basis: Phase 0 measured ~90 MB steady-state RSS on the host with one
// space and embedder="none" (FTS-only). 256 MiB gives ~2.8x headroom over
// that baseline — enough that transient allocation spikes (a query
// fan-out, an SSE burst) don't thrash GC against the limit, while still
// well under the per-app memory ceiling iOS enforces before jetsam on
// modern devices. It is a SOFT limit: Go runs GC harder as it approaches
// the cap rather than failing. This number is a host-derived starting
// point — on-device profiling may justify re-tuning it (a device under
// memory pressure has a tighter real budget than the host).
const gomemlimitBytes = 256 << 20 // 256 MiB

// handle is the package-level, mutex-guarded server handle. Only one
// server runs per process; Start guards against a double-start, and the
// stop paths reach the running server through this.
var (
	handleMu sync.Mutex
	handle   *serverHandle
	// stopping is the done channel of a hard-stopped server whose run
	// goroutine may still be tearing down. StopNow returns before the
	// drain completes, so the next Start waits on this before booting —
	// otherwise a fast background->foreground restart could run two
	// engines against one data dir.
	stopping chan struct{}
)

type serverHandle struct {
	cancel context.CancelFunc // cancels the RunWith ctx
	done   chan struct{}      // closed when RunWith returns
	addr   string             // the bound listen address (host:port)
}

// Options are the host-supplied inputs for Start. The embedded path has
// no config file and no env — every knob a host can turn crosses this
// struct explicitly.
type Options struct {
	// DataDir is the server's data root (Context.getFilesDir() on
	// Android, the app container on iOS). Required.
	DataDir string
	// ListenAddr is the loopback listen address; "127.0.0.1:0" lets the
	// OS pick a free port (read it back with Address()).
	ListenAddr string
	// NodeconfYAML is the any-sync network config contents (staging/prod
	// yml). Required — there is no filesystem fallback on this path.
	NodeconfYAML string
	// IndexEnabled requests the FTS index (see the index-policy block in
	// Start); a build without the `fts` tag ignores it.
	IndexEnabled bool
	// PushPeerId is the push node's peer id (SYN-83). The push node is a
	// direct out-of-band peer, deliberately NOT part of NodeconfYAML —
	// but it pairs with the nodeconf choice (staging vs prod), so the
	// host supplies both from the same place. Empty = push stays off
	// (every /v1/push endpoint returns 409 push.disabled).
	PushPeerId string
	// PushAddrs are the push node's dial addresses, comma-separated —
	// the same format ANY_PUSH_ADDRS parses, e.g.
	// "quic://host:port" or "host:port,host2:port2". Push activates only
	// when both PushPeerId and PushAddrs are non-empty.
	PushAddrs string
}

// assembleConfig builds the embedded boot config from validated Options.
// Split from Start so the assembly rules (index policy, headless,
// push-node threading) are unit-testable without booting an engine.
func assembleConfig(opts Options) config.Config {
	cfg := config.Defaults()
	cfg.DataDir = opts.DataDir
	cfg.Listen.Addr = opts.ListenAddr
	// Trimmed so a whitespace-only string counts as "not supplied" and
	// falls through to config's embedded production default, rather than
	// reaching any-sync as an unparseable conf.
	cfg.Network.Nodeconf = strings.TrimSpace(opts.NodeconfYAML)
	// FTS-only index policy. "none" makes the embedder factory return a
	// true-nil so the compiled-out local llama.cpp embedder is never
	// reached. Index.Enabled is gated on BOTH the compiled FTS cap and the
	// caller's IndexEnabled request: config.Defaults() ships
	// Index.Enabled=true, so this is a LOAD-BEARING override. FTS runs only
	// when the `fts` tag is compiled in AND the caller opts in — a build
	// without the tag leaves CompiledCaps()'s fts bit false and the dormant
	// indexer never starts regardless of IndexEnabled; with `fts` the caller
	// decides. The iOS share extension passes IndexEnabled=false to keep the
	// indexer dormant for the memory headroom (it never searches), the app
	// passes true if it wants engine search. (capFTS is unexported;
	// CompiledCaps is the exported reader.)
	cfg.Index.Embedder = "none"
	fts, _ := indexer.CompiledCaps()
	cfg.Index.Enabled = opts.IndexEnabled && fts
	// IOS-116: every in-process boot (iOS/iPadOS app, future sharing
	// extension) is headless — no /ui debug harness, no advertising log.
	cfg.WebUI.Enabled = false
	// Push node (SYN-83): plain field fill — the config.Push tristate does
	// the enablement on its own (nil Enabled + non-empty PeerId + addrs ⇒
	// Active). Empty inputs leave the defaults and push stays off.
	cfg.Push.PeerId = opts.PushPeerId
	cfg.Push.Addrs = splitAddrs(opts.PushAddrs)
	return cfg
}

// splitAddrs splits a comma-separated address list into trimmed,
// non-empty entries — the same semantics config's ANY_PUSH_ADDRS
// parsing applies, kept here so both mobile shims share one
// implementation instead of each parsing host input.
func splitAddrs(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Start boots the embedded server with its data under opts.DataDir,
// listening on opts.ListenAddr, joining the network described by
// opts.NodeconfYAML (see Options for the full input contract). It blocks
// until the listener binds — returning the bound address — or boot fails,
// returning one of the package's typed errors (ErrAlreadyRunning,
// ErrBadDataDir or a *BootError).
func Start(opts Options) (string, error) {
	// Soft memory cap, applied unconditionally at start so it is in force
	// before the engine allocates. SetMemoryLimit's argument is a soft
	// GOMEMLIMIT in bytes; -1 leaves it unchanged (used to read it back).
	debug.SetMemoryLimit(gomemlimitBytes)

	handleMu.Lock()
	if handle != nil {
		handleMu.Unlock()
		return "", ErrAlreadyRunning
	}
	prev := stopping
	handleMu.Unlock()

	// A prior hard stop (StopNow) returns before its run goroutine has
	// finished tearing down — the engine and SQLite still hold the data
	// dir. Wait for that teardown before booting a new engine on the same
	// dir, so a fast restart can't run two engines on one data dir.
	if prev != nil {
		<-prev
	}

	if opts.DataDir == "" {
		return "", ErrBadDataDir
	}
	if _, err := config.EnsureDataDir(opts.DataDir); err != nil {
		return "", fmt.Errorf("%w: %w", ErrBadDataDir, err)
	}

	cfg := assembleConfig(opts)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	ready := make(chan string, 1) // delivers the bound addr once
	runErr := make(chan error, 1) // delivers RunWith's return

	go func() {
		defer close(done)
		err := server.RunWith(ctx, cfg, server.RunOptions{
			Ready: func(addr string) { ready <- addr },
		})
		runErr <- err
	}()

	// Block until the listener binds (success) or boot errors out before
	// binding.
	select {
	case addr := <-ready:
		handleMu.Lock()
		handle = &serverHandle{cancel: cancel, done: done, addr: addr}
		handleMu.Unlock()
		return addr, nil
	case err := <-runErr:
		// RunWith returned before Ready fired: boot failed before the
		// listener bound. Drain the goroutine and report. (A serve-loop
		// failure AFTER binding can't reach here — Ready has already
		// delivered the addr and Start has returned it.)
		cancel()
		<-done
		if err == nil {
			err = errors.New("server exited without binding")
		}
		return "", &BootError{Err: err}
	}
}

// Stop tears down the running server. When graceful, it cancels the run
// context and waits for RunWith to return (Echo's graceful drain); when
// not graceful, it cancels the context and returns without waiting for a
// clean drain so the hard-stop path stays bounded for the iOS background-
// task expiration handler. Either way the handle is cleared so a
// subsequent Start is clean.
//
// The non-graceful path returns while the run goroutine is still
// draining, so it records that teardown in `stopping`; the next Start
// waits on it before booting to avoid running two engines against one
// data dir. Safe to call when no server is running.
func Stop(graceful bool) {
	handleMu.Lock()
	h := handle
	handle = nil
	if h == nil {
		handleMu.Unlock()
		return
	}
	if !graceful {
		stopping = h.done
		go func() {
			<-h.done
			handleMu.Lock()
			if stopping == h.done {
				stopping = nil
			}
			handleMu.Unlock()
		}()
	}
	handleMu.Unlock()

	h.cancel()
	if graceful {
		// Wait for RunWith to fully return — Echo's graceful shutdown
		// closed the listener and drained in-flight handlers.
		<-h.done
	}
	// Non-graceful: ctx is cancelled (which unblocks RunWith's select and
	// triggers shutdown); we return immediately without waiting on h.done
	// so the caller's deadline is never at our mercy. The teardown
	// continues in the background and is tracked via `stopping`.
}

// StopNow hard-stops the server and returns promptly (well under 1s): it
// cancels the run context to force the listener closed but does NOT wait
// for a clean drain. The iOS beginBackgroundTask expiration handler calls
// this synchronously, so it must not block on shutdown.
func StopNow() { Stop(false) }

// Address returns the actually-bound listen address (host:port). Differs
// from the listenAddr passed to Start when ":0" was used and the OS
// picked a port. Empty when the server is not running.
func Address() string {
	handleMu.Lock()
	defer handleMu.Unlock()
	if handle == nil {
		return ""
	}
	return handle.addr
}

// Version returns the build-stamped version string ("any <version>
// (commit <sha>, built <ts>)").
func Version() string { return version.String() }
