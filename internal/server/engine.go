package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/localstore"
	"github.com/anyproto/any/internal/push"
	"github.com/anyproto/any/internal/version"
)

// engine bundles everything that exists only while an account is
// booted: the per-account instance lock, the SDK, the indexer, the push
// service, and every goroutine and stream working against them. One at
// a time per process; built by server.Run (an identity resolved at
// boot) or by POST /v1/auth, torn down by shutdown, DELETE /v1/auth or
// an account switch — the listener outlives it.
type engine struct {
	lock    *Lock
	sdk     *anysyncsdk.SDK
	indexer *indexer.Indexer
	push    *push.Service
	// local is the local store over the SDK's own DB (handlers_local.go);
	// nil when local.enabled is false. No lifecycle of its own — the
	// SDK opens and closes the file.
	local   *localstore.Store
	account string
	// dir is the account dir the lock, pid and addr files live in.
	dir string
	// derived is the derived-space registry resolved against this
	// account (see derivedspaces.go).
	derived []resolvedDerivedSpace
	// chunkers is the index chunker registry driven by this engine's
	// indexer. Per engine: the schema chunker keeps per-space catalog
	// state that must not carry over to the next account.
	chunkers *index.Registry

	// ctx bounds every goroutine and SSE stream that belongs to this
	// engine; cancelling it is the first step of teardown. Streams
	// select on it and emit their terminal frame with closeReason.
	ctx    context.Context
	cancel context.CancelFunc
	// wg tracks the engine's own goroutines (registerDevice, the push
	// kick, the standing process interest, the derived-setup pass,
	// bundle loser retries) so teardown can join them before closing
	// the SDK they read.
	wg sync.WaitGroup

	mu sync.Mutex
	// closeReason is the `closed{reason}` streams emit once ctx is
	// cancelled: server_shutdown for process exit, deauthorized for an
	// in-place logout or switch. Written before cancel.
	closeReason string
	// procRelease drops the standing account-scope pub/sub interest
	// for process.* broadcasts (acquired by holdProcessInterest).
	procRelease func()
	// done flips once the engine's resources are closed; late indexer
	// frames from a torn-down engine are dropped past it.
	done atomic.Bool
}

// newEngine allocates an engine with its lifecycle context; the
// resources are filled in by bootEngine.
func newEngine() *engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &engine{ctx: ctx, cancel: cancel, closeReason: api.SubscribeClosedServerShutdown}
}

// spawn runs fn as one of the engine's goroutines, bounded by its ctx
// and joined at teardown.
func (e *engine) spawn(fn func(ctx context.Context)) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		fn(e.ctx)
	}()
}

func (e *engine) setCloseReason(reason string) {
	e.mu.Lock()
	e.closeReason = reason
	e.mu.Unlock()
}

func (e *engine) getCloseReason() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closeReason
}

// closeResources releases everything the engine holds, in dependency
// order: cancel first so goroutines and streams unwind, join the
// goroutines, then indexer and push (their workers read the SDK), the
// SDK, and finally the instance lock. Nil-tolerant for a partially
// built engine (boot failure, hand-built test fixtures).
func (e *engine) closeResources(lg logger.CtxLogger) {
	e.cancel()
	if !waitWG(&e.wg, gracefulShutdownDeadline) {
		lg.Warn("engine goroutines did not finish in time")
	}
	e.mu.Lock()
	release := e.procRelease
	e.procRelease = nil
	e.mu.Unlock()
	if release != nil {
		release()
	}
	if e.indexer != nil {
		if err := e.indexer.Close(); err != nil {
			lg.Warn("indexer close", zap.Error(err))
		}
	}
	if e.push != nil {
		if err := e.push.Close(); err != nil {
			lg.Warn("push close", zap.Error(err))
		}
	}
	if e.sdk != nil {
		if err := e.sdk.Close(); err != nil {
			lg.Warn("sdk close", zap.Error(err))
		}
	}
	if err := e.lock.Release(); err != nil {
		lg.Warn("release instance lock", zap.Error(err))
	}
	e.done.Store(true)
}

// waitWG waits for the group with a deadline; false on timeout.
func waitWG(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	return waitClosed(done, d)
}

// Boot verdicts decided under authMu — the authoritative ones, since
// the handler's pre-check reads the account outside the lock:
// errAlreadyAuthorized means the engine already serves the requested
// account, errAccountMismatch that it serves another one.
var (
	errAlreadyAuthorized = errors.New("already authorized")
	errAccountMismatch   = errors.New("another account is running")
)

// walletSeed carries the inputs for CREATING a wallet: the BIP-39
// mnemonic and its account-derivation index. Both zero for the
// load-an-existing-wallet path (an existing wallet's stored values
// win); the fresh-generate and restore paths pass the resolved index
// explicitly (auth.DefaultAccountIndex unless the caller chose one).
// The index is paired with the mnemonic here because the two are
// meaningless apart and the Identity (which/where) deliberately
// doesn't carry derivation inputs.
type walletSeed struct {
	mnemonic string
	index    uint32
}

// processHook builds the indexer's process-update sink for an engine;
// nil disables process reporting (tests).
type processHook func(*engine) func(indexer.ProcessUpdate)

// linksHook builds the engine's link-index liveness publisher —
// nil = no bus event.
type linksHook func(*engine) func(spaceId string, targets []string)

// bootEngine opens the identity's keys through open and brings up the
// SDK and the indexer for it, refusing a network the account dir is
// pinned against (networkpin.go). On any failure everything already
// opened is torn back down; if THIS call freshly created the account
// dir, the orphan is removed too — otherwise a half-initialized
// account (especially a generated one whose phrase was never surfaced
// to the caller) would linger and be auto-selected on the next start.
// A pre-existing dir (an account reached under the other custody, or
// one holding data from an earlier boot) is left untouched.
func bootEngine(ctx context.Context, cfg config.Config, root string, id *Identity, open credential, onProcess processHook, onLinks linksHook) (_ *engine, err error) {
	// One read of the nodeconf feeds both the pin and the SDK, so the
	// pinned network is the one the SDK joins.
	nodeconf, err := config.LoadNodeconf(cfg.Network)
	if err != nil {
		return nil, err
	}
	networkId, err := config.NetworkId(nodeconf)
	if err != nil {
		return nil, err
	}
	cfg.Network = config.Network{Nodeconf: string(nodeconf)}

	_, statErr := os.Stat(id.Dir)
	dirExisted := statErr == nil
	if err := os.MkdirAll(id.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create account dir %s: %w", id.Dir, err)
	}
	// A held lock means another process owns this account dir — bail
	// before touching the keys, and never clean up on this path. Goes
	// through acquirePIDLock so the mobile build (one in-process instance)
	// can no-op it at both bootAccount sites; see pidlock_acquire*.go.
	lock, err := acquirePIDLock(id.Dir)
	if err != nil {
		return nil, err
	}
	// Checked under the lock and before the keys: a refused network
	// leaves the dir exactly as it was.
	pinned, err := checkNetworkPin(id.Dir, networkId)
	if err != nil {
		_ = lock.Release()
		return nil, err
	}

	provider, createdKeys, err := open(ctx)
	if err != nil {
		_ = lock.Release()
		if createdKeys && !dirExisted && id.Dir != root {
			_ = os.RemoveAll(id.Dir)
		}
		return nil, err
	}

	eng := newEngine()
	eng.lock = lock
	eng.dir = id.Dir
	eng.chunkers = NewIndexRegistry()
	// Everything opened from here belongs to the engine: on failure
	// closeResources releases it in order (goroutines, indexer, push,
	// SDK, lock), and a dir this call created for freshly minted keys
	// leaves with it. Scoped to per-account dirs: never the legacy
	// flat root or an explicit --wallet path.
	defer func() {
		if err != nil {
			eng.closeResources(engineLog)
			if createdKeys && !dirExisted && id.Dir != root {
				_ = os.RemoveAll(id.Dir)
			}
		}
	}()

	account, err := AccountID(ctx, provider)
	if err != nil {
		return nil, fmt.Errorf("derive account id: %w", err)
	}
	if id.Account != "" && id.Account != account {
		return nil, fmt.Errorf("keys under %s are account %s, not %s", id.Dir, account, id.Account)
	}
	eng.account = account

	sdk, err := OpenSDK(ctx, cfg, id.Dir, provider)
	if err != nil {
		return nil, fmt.Errorf("open sdk: %w", err)
	}
	eng.sdk = sdk
	// A dir without a pin (new, or from before pins) adopts this network.
	if !pinned {
		if err := writeNetworkPin(id.Dir, networkId); err != nil {
			return nil, fmt.Errorf("pin network: %w", err)
		}
		engineLog.Info("account pinned to network", zap.String("networkId", networkId))
	}

	// Refresh this device's registry row (SYN-165): os/version are
	// server-stamped so every boot keeps them current; the display name
	// is seeded from the hostname only while the row carries none so a
	// user-set name is never clobbered. Best-effort — the registry is
	// convenience metadata and must never block boot — and deferred
	// until the SDK's bootstrap pass completes (see registerDevice).
	eng.spawn(func(ctx context.Context) { registerDevice(ctx, sdk) })

	if cfg.Index.Enabled {
		var hook func(indexer.ProcessUpdate)
		if onProcess != nil {
			hook = onProcess(eng)
		}
		var links func(string, []string)
		if onLinks != nil {
			links = onLinks(eng)
		}
		ix, err := OpenIndexer(ctx, cfg.Index, id.Dir, config.ModelsDir(root), sdk, eng.chunkers, hook, links)
		if err != nil {
			return nil, fmt.Errorf("open indexer: %w", err)
		}
		eng.indexer = ix
		ix.Start(eng.ctx)
	}

	// Push notifications: only when config names a push node (the SDK
	// side was threaded by OpenSDK under the same gate). Token file
	// lives in the per-account dir; Start never fails boot — a
	// persisted token re-registers in the background.
	if cfg.Push.Active() {
		ps := push.New(sdk, id.Dir)
		eng.push = ps
		ps.Start(eng.ctx)
		// The first subscription sync can run before the SDK's background
		// boot pass materializes offline-created chats; kick a re-sync once
		// the pass completes so healing doesn't wait for the periodic tick.
		// sdk.Close also closes BootstrapDone, so re-check the ctx after
		// waking: a teardown must not kick a closing service.
		eng.spawn(func(ctx context.Context) {
			select {
			case <-sdk.BootstrapDone():
				if ctx.Err() == nil {
					ps.Kick()
				}
			case <-ctx.Done():
			}
		})
	}

	// Local store: device-local, non-CRDT collections in the SDK's
	// sdk.db under the "l_" tag. Borrows the SDK's handle — nothing to
	// open, nothing to close.
	if cfg.Local.Enabled {
		eng.local = localstore.New(sdk.Store())
	}

	derived, err := resolveDerivedSpaces(ctx, sdk)
	if err != nil {
		return nil, fmt.Errorf("resolve derived spaces: %w", err)
	}
	eng.derived = derived
	return eng, nil
}

// engineLog covers engine lifecycle noise that has no request context.
var engineLog = logger.NewNamed("engine")

// registerDevice upserts THIS device's row in the account's tech-space
// devices registry (docs/23-devices.md): os and version are stamped
// fresh on every boot, the display name is seeded from the hostname
// only while the row has no name (PUT /v1/devices/me owns it
// afterwards). It waits for the SDK's bootstrap pass first: before
// offline catch-up an empty local projection is indistinguishable from
// a truly unregistered device, and seeding the hostname then would
// clobber a user-set name account-wide once the synced row merges.
// Failures are logged, never fatal.
func registerDevice(ctx context.Context, sdk *anysyncsdk.SDK) {
	select {
	case <-sdk.BootstrapDone():
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil {
		return
	}
	up := space.DeviceUpsert{OS: runtime.GOOS, Version: version.Version}
	devices, err := sdk.Spaces().ListDevices(ctx)
	if err != nil {
		// Unavailability is not an empty registry: absence can't be
		// inferred, so skip the name seed and stamp os/version only.
		engineLog.Warn("device registry read failed", zap.Error(err))
	} else {
		self := sdk.PeerId()
		named := false
		for _, dev := range devices {
			if dev.PeerId == self {
				named = dev.Name != ""
				break
			}
		}
		if !named {
			if host, err := os.Hostname(); err == nil {
				up.Name = host
			}
		}
	}
	if err := sdk.Spaces().SetDevice(ctx, up); err != nil {
		engineLog.Warn("device registry upsert failed", zap.Error(err))
	}
}

// bootAccount boots an engine and publishes it on d. Serialized by
// authMu; at most one engine live at a time.
func (d *deps) bootAccount(id *Identity, open credential) (*engine, error) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	return d.bootAccountLocked(id, open)
}

// bootAccountLocked is bootAccount with authMu held by the caller — the
// switch path tears the previous engine down and boots the next one
// under a single hold. With an engine already live it decides by
// account: the same one is errAlreadyAuthorized (returned with that
// engine — a no-op for the caller), any other errAccountMismatch. An
// id with no known account (the legacy root wallet before it is
// opened) never matches a live engine.
func (d *deps) bootAccountLocked(id *Identity, open credential) (*engine, error) {
	if d.eng != nil {
		if id.Account != "" && d.eng.account == id.Account {
			return d.eng, errAlreadyAuthorized
		}
		return nil, errAccountMismatch
	}
	eng, err := bootEngine(d.runCtx, d.cfg, d.root, id, open, d.indexerProcessFor, d.indexerLinksFor)
	if err != nil {
		return nil, err
	}
	d.publishEngine(eng)
	return eng, nil
}

// switchAccount replaces the live engine with one for id under a single
// authMu hold: teardown (streams end with deauthorized) then boot. A
// same-account race — the target already came up meanwhile — reports
// errAlreadyAuthorized with that engine, like bootAccountLocked. A
// boot failure after the teardown leaves the server unauthorized; the
// caller reports the boot error and clients re-read GET /v1/auth.
func (d *deps) switchAccount(id *Identity, open credential) (*engine, error) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	if d.eng != nil && d.eng.account != id.Account {
		engineLog.Info("switching account", zap.String("to", id.Account))
		d.teardownEngineLocked(engineLog, api.SubscribeClosedDeauthorized)
	}
	eng, err := d.bootAccountLocked(id, open)
	if err != nil && !errors.Is(err, errAlreadyAuthorized) {
		engineLog.Error("account switch: boot failed — server is unauthorized", zap.Error(err))
	}
	return eng, err
}

// publishEngine installs a booted engine on d and opens the gate. The
// field stores happen before ready flips; the guard's atomic load is
// the acquire edge that makes them visible to handlers.
func (d *deps) publishEngine(eng *engine) {
	d.eng = eng
	d.sdk = eng.sdk
	d.indexer = eng.indexer
	d.push = eng.push
	d.local = eng.local
	d.account = eng.account
	d.derived = eng.derived
	d.shutdownCtx = eng.ctx
	d.gate.reset()
	d.ready.Store(true)
	if d.boundAddr != "" && eng.lock != nil {
		writeAddrFile(eng.dir, d.boundAddr)
	}
	eng.spawn(func(context.Context) { d.holdProcessInterest(eng) })
	// Restore side of the setup split: adopt the installs of the
	// well-known derived spaces this account already has
	// (derivedsetup.go). Never creates a space; never blocks serving.
	eng.spawn(func(ctx context.Context) { d.bootstrapDerivedSetups(ctx, eng) })
}

// teardownEngine tears the live engine down in place and returns d to
// the unauthorized state, keeping the listener up. reason is the
// terminal `closed{reason}` every open stream receives. No-op without
// an engine.
func (d *deps) teardownEngine(lg logger.CtxLogger, reason string) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	d.teardownEngineLocked(lg, reason)
}

// teardownEngineLocked is teardownEngine with authMu held. Order:
//
//  1. ready off — the guard refuses new requests with 401 — and the
//     engine stops reporting (late indexer frames are dropped);
//  2. cancel the engine ctx — streams write their terminal frame and
//     unwind, engine goroutines return, and every gated request's
//     context is cancelled with it (routes.go), so SDK calls in flight
//     return instead of holding the drain;
//  3. drain the gate — every in-flight handler and stream leaves
//     (bounded: a handler wedged past the deadline is abandoned, and
//     the resources are released only once it leaves — never closed
//     underneath it);
//  4. detach the pub/sub interests — the next engine builds its own;
//  5. close the engine's resources (goroutines joined, indexer, push,
//     SDK, lock), THEN drop the process view and bundle observations,
//     so nothing the joined goroutines emitted survives;
//  6. clear the engine fields. The gate stays closed until the next
//     publishEngine reopens it behind the fresh fields — a request that
//     read ready=true just before step 1 is refused at the gate rather
//     than admitted against cleared fields.
//
// Nothing here takes the gate or blocks on a handler that holds it, so
// a handler must never take authMu — see the guard in routes.go.
func (d *deps) teardownEngineLocked(lg logger.CtxLogger, reason string) {
	eng := d.eng
	if eng == nil {
		return
	}
	d.ready.Store(false)
	eng.done.Store(true)
	eng.setCloseReason(reason)
	eng.cancel()
	drained := d.gate.close()
	ok := waitClosed(drained, gracefulShutdownDeadline)
	d.eventsNet().detach()
	if ok {
		eng.closeResources(lg)
		d.processes().reset()
		d.bundleResolver().Reset()
		d.sdk = nil
		d.indexer = nil
		d.push = nil
		d.local = nil
		d.account = ""
		d.derived = nil
	} else {
		// A straggler still executes against this engine. Its fields
		// stay in place (the next publishEngine overwrites them) and
		// the resources — including the instance lock — are released
		// once it leaves; a boot of the same account before that is
		// refused as account_in_use, which is the truth.
		lg.Error("engine teardown: in-flight requests did not finish in time; resources are released once they leave")
		d.processes().reset()
		d.bundleResolver().Reset()
		go func() {
			<-drained
			eng.closeResources(lg)
		}()
	}
	// Cleared unconditionally so a second teardown (server.Run's
	// deferred one) never closes the SDK twice, and spawnEngine stops
	// attaching work to a torn-down engine.
	d.eng = nil
	d.shutdownCtx = nil
}

// holdProcessInterest acquires the standing account-scope interest for
// process.* broadcasts (ev/process/>), so this account's other
// devices' processes materialize in the view with no local SSE
// subscriber. Space-scope coverage stays subscriber-driven
// (docs/22-processes.md § Remote visibility). Retries with backoff
// until acquired or the engine goes away — unlike an SSE client, this
// interest has no reconnect path, so a one-shot attempt could silently
// cost remote visibility for the engine's lifetime.
//
// Runs as an engine goroutine and must not take authMu: teardown joins
// it while holding that lock.
func (d *deps) holdProcessInterest(eng *engine) {
	backoff := 2 * time.Second
	for {
		release, err := d.eventsNet().acquire(eng.ctx, eng.sdk, true, nil, []string{eventTopicPrefix + "process/>"})
		if err == nil {
			eng.mu.Lock()
			if eng.ctx.Err() != nil {
				eng.mu.Unlock()
				release() // engine already tearing down
				return
			}
			eng.procRelease = release
			eng.mu.Unlock()
			return
		}
		if eng.ctx.Err() != nil {
			return
		}
		handlerLog.Warn("process event interest not acquired; retrying", zap.Error(err))
		select {
		case <-eng.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// indexerProcessFor bridges the engine's indexer lifecycle updates onto
// the process view as device-scope processes — each device indexes its
// own copy, other devices don't care. Kinds: index.fts.<spaceId>
// (chunk/advance backlog), index.embed.<spaceId> (vector drain, total
// from the pending count), index.model_download (embedding-model
// fetch, done/total bytes, target = model file name). Frames are
// stamped with the engine's own account, and dropped once its
// resources are closed — a straggling download goroutine from a
// torn-down engine must not report under the next account. Cancel
// requests are ignored by these producers; work stopped mid-pass
// (space dropped, teardown) reports cancelled. Failure messages are
// deliberately generic — indexer errors carry filesystem paths and
// upstream response bodies, which never go on the wire; the detail is
// in the server log.
func (d *deps) indexerProcessFor(eng *engine) func(indexer.ProcessUpdate) {
	return func(u indexer.ProcessUpdate) {
		if eng.done.Load() {
			return
		}
		var id string
		var data processEventData
		switch u.Kind {
		case indexer.ProcessKindFTS:
			id = "index.fts." + u.SpaceId
			data = processEventData{Kind: "index.fts", Title: "Indexing for search", Target: u.SpaceId}
		case indexer.ProcessKindEmbed:
			id = "index.embed." + u.SpaceId
			data = processEventData{Kind: "index.embed", Title: "Embedding search index", Target: u.SpaceId}
		case indexer.ProcessKindModelDownload:
			id = "index.model_download"
			data = processEventData{Kind: "index.model_download", Title: "Downloading embedding model", Target: u.Name}
		case indexer.ProcessKindLinksBackfill:
			id = "index.links_backfill." + u.SpaceId
			data = processEventData{Kind: "index.links_backfill", Title: "Rebuilding the link index", Target: u.SpaceId}
		default:
			return
		}
		data.Done, data.Total = u.Done, u.Total
		// Non-failed messages are producer-authored status lines (e.g. the
		// download's generic "retrying" note) and safe to relay; a failed
		// frame's message is the raw error — paths and response bodies —
		// and is replaced by the generic Error below.
		data.Message = u.Message
		var typ string
		switch u.Phase {
		case indexer.ProcessStarted:
			typ = api.EventProcessStarted
		case indexer.ProcessProgress:
			typ = api.EventProcessProgress
		case indexer.ProcessDone:
			typ = api.EventProcessDone
		case indexer.ProcessCancelled:
			typ = api.EventProcessCancelled
		case indexer.ProcessFailed:
			typ = api.EventProcessFailed
			data.Message = ""
			data.Error = &api.ProcessError{Code: data.Kind + "_failed",
				Message: "failed; retrying — see server log"}
		default:
			return
		}
		payload, err := json.Marshal(data)
		if err != nil {
			return
		}
		d.eventsHub().publish(api.Event{
			Type:   typ,
			Scope:  api.EventScopeDevice,
			Target: id,
			Data:   payload,
			Sender: &api.EventSender{Identity: eng.account, Self: true},
		})
	}
}

// indexerLinksFor bridges the link index's page signal onto the bus as
// a device-scope `links.updated` event (docs/21-events.md): the
// targets whose backlinks changed, so an open panel refreshes.
func (d *deps) indexerLinksFor(eng *engine) func(spaceId string, targets []string) {
	return func(spaceId string, targets []string) {
		if eng.done.Load() {
			return
		}
		data := api.EventLinksUpdatedData{SpaceId: spaceId, Targets: targets}
		if len(targets) > api.MaxEventLinksTargets {
			// Bounded like every bus payload; a panel showing an
			// unlisted target re-reads on Truncated.
			data.Targets, data.Truncated = targets[:api.MaxEventLinksTargets], true
		}
		payload, err := json.Marshal(data)
		if err != nil {
			return
		}
		d.eventsHub().publish(api.Event{
			Type:   api.EventLinksUpdated,
			Scope:  api.EventScopeDevice,
			Data:   payload,
			Sender: &api.EventSender{Identity: eng.account, Self: true},
		})
	}
}

// spawnEngine runs fn as a goroutine of the live engine (joined at
// teardown, bounded by its ctx). Callers hold the gate, so d.eng is
// stable; a caller that outlived a timed-out drain finds no engine and
// its work is dropped — nothing may attach to a torn-down engine.
func (d *deps) spawnEngine(fn func(ctx context.Context)) {
	if eng := d.eng; eng != nil {
		eng.spawn(fn)
	}
}
