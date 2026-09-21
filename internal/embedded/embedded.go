// Package embedded is the shared, build-tag-free lifecycle core for
// embedding the `any` HTTP server inside a host process. Both mobile
// binding shims sit on top of it: the gomobile `mobile` package
// (`mobile/android`) and the c-archive shim (`mobile/ios`). Each shim is a thin
// host-idiom adapter; this package owns the real logic so it is exercised
// once by a single host-runnable test suite.
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
//     Network.Nodeconf + index policy (Embedder="none": FTS only) +
//     headless (WebUI.Enabled=false) +
//     the push node peer (Options.PushPeerId/PushAddrs → cfg.Push);
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
	"github.com/anyproto/any/internal/server"
	"github.com/anyproto/any/internal/version"
)

// Exported sentinel errors. Both shims map from this one source so the
// host error contract has a single definition. The iOS c-archive shim
// (mobile/ios) maps each to a C code the Swift side mirrors:
//
//	ErrAlreadyRunning               -> 1
//	ErrBadDataDir                   -> 2
//	a boot failure                  -> 3 (see BootError)
//	indexer.ErrIndexRebuildRequired -> 4 (inside a BootError; checked first)
//	ErrBadOptions                   -> 5 (a host programming error, never retryable)
//
// The Android gomobile shim (mobile/android) surfaces the error string
// and no code today; converging it is Android-owned work.
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
	// ErrBadOptions: an Options field the host controls is invalid — an
	// unknown Mode, managed mode without a ControlToken, or a NodeconfYAML
	// that isn't YAML or names no networkId.
	ErrBadOptions = errors.New("embedded: bad options")
)

// BootError wraps a failure that occurred while bringing the server up
// after the config validated — engine/account boot, listener bind, or a
// config the runtime rejected. The iOS shim maps a BootError to code 3,
// EXCEPT when it wraps indexer.ErrIndexRebuildRequired, which gets its own
// code 4 because the host has a specific recovery to offer. The distinct
// type lets a caller tell "the config was fine but boot failed" from "the
// input was bad" (ErrBadDataDir) without string matching.
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
	// PushPeerId is the push node's peer id. The push node is a
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
	// GlobalP2PEnabled overrides the internet-wide p2p layer: an iroh
	// UDP endpoint, a permanent relay session, and a dialable ticket
	// published into the records of every space this account holds.
	// nil takes the same default the CLI gets — on when the host
	// supplied no NodeconfYAML, i.e. the production network. A host
	// that does not want the endpoint sets this to false; there is no
	// config file or env on this path, so this struct is the only way
	// to say so. Publishing a ticket is one-way (docs/30-global-p2p.md
	// § Advertising is a one-way door), so a host that is unsure
	// should decide before the first boot, not after.
	GlobalP2PEnabled *bool

	// Mode is the server ownership mode: "" / "standalone" (the account
	// resolves from the wallet on disk) or "managed" (the host states
	// the account over POST /v1/auth on every boot, keys never touch
	// disk, and logout / switch / shutdown are allowed behind
	// ControlToken). Fixed for the server's lifetime.
	Mode string
	// LocalDiscovery is p2p.localDiscovery: whether mDNS announce and
	// browse start with the engine. nil resolves per platform and mode
	// (config.LocalDiscoveryEnabled). An embedded host that owns a
	// Local Network permission flow passes false here rather than over
	// HTTP: a standalone server with an account on disk boots before the
	// listener is up, so a PUT /v1/local-discovery could not precede it.
	LocalDiscovery *bool
	// ControlToken gates the managed control operations (POST/DELETE
	// /v1/auth, POST /v1/shutdown, PUT /v1/local-discovery). Required
	// when Mode is "managed":
	// an in-process host has no stdout handshake to receive a minted
	// one, and without a token any other process on the device could
	// switch the account. Ignored in standalone.
	ControlToken string
}

// validateOptions checks the host-controlled fields that have no
// filesystem side to fail on. Split from assembleConfig so the
// assembly stays infallible.
func validateOptions(opts Options) error {
	mode, err := config.ParseMode(opts.Mode)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadOptions, err)
	}
	if mode == config.ModeManaged && strings.TrimSpace(opts.ControlToken) == "" {
		return fmt.Errorf("%w: managed mode requires ControlToken", ErrBadOptions)
	}
	if nc := strings.TrimSpace(opts.NodeconfYAML); nc != "" {
		if _, err := config.NodeconfNetworkId([]byte(nc)); err != nil {
			return fmt.Errorf("%w: %w", ErrBadOptions, err)
		}
	}
	return nil
}

// assembleConfig builds the embedded boot config from validated Options.
// Split from Start so the assembly rules (index policy, headless,
// push-node threading) are unit-testable without booting an engine.
func assembleConfig(opts Options) config.Config {
	cfg := config.Defaults()
	cfg.DataDir = opts.DataDir
	cfg.Listen.Addr = opts.ListenAddr
	// Already validated by Start; "" normalizes to standalone.
	cfg.Mode, _ = config.ParseMode(opts.Mode)
	// Trimmed so a whitespace-only string counts as "not supplied" and
	// falls through to config's embedded production default, rather than
	// reaching any-sync as an unparseable conf.
	cfg.Network.Nodeconf = strings.TrimSpace(opts.NodeconfYAML)
	// FTS-only index policy, not a host parameter: "none" makes the
	// embedder factory return a true-nil, so no vector pipeline runs
	// in-process. Index.Enabled keeps its config.Defaults() value (on).
	cfg.Index.Embedder = "none"
	// Every in-process boot is headless — no /ui debug harness, no
	// advertising log.
	cfg.WebUI.Enabled = false
	// Push node: plain field fill — the config.Push tristate does
	// the enablement on its own (nil Enabled + non-empty PeerId + addrs ⇒
	// Active). Empty inputs leave the defaults and push stays off.
	cfg.Push.PeerId = opts.PushPeerId
	cfg.Push.Addrs = splitAddrs(opts.PushAddrs)
	cfg.P2P.LocalDiscovery = opts.LocalDiscovery
	// Same rule the CLI gets from config.Load: a host that supplied
	// neither a push node nor a network lands on the production pair.
	config.ApplyPushDefaults(&cfg)
	// Global p2p rides the same pairing, so it reaches a host that
	// supplied no NodeconfYAML — naming a network is what marks a boot
	// as "not production". The shipped shims pass one today, so for
	// them this fills nothing; a host on the production default gets
	// the layer, and GlobalP2PEnabled is how it says otherwise, since
	// this path reads neither a config file nor ANY_* env.
	config.ApplyGlobalP2PDefaults(&cfg)
	if opts.GlobalP2PEnabled != nil {
		v := *opts.GlobalP2PEnabled
		cfg.P2P.Global.Enabled = &v
	}
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
	if err := validateOptions(opts); err != nil {
		return "", err
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
			Ready:        func(addr string) { ready <- addr },
			ControlToken: strings.TrimSpace(opts.ControlToken),
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
