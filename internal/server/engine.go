package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/push"
	"github.com/anyproto/any/internal/version"
)

// engine bundles everything that exists only while an account is
// booted: the per-account pid lock, the SDK, the indexer and the push
// service. One per process; built either directly by server.Run (an
// identity resolved at boot) or later by POST /v1/auth.
type engine struct {
	lock    *Lock
	sdk     *anysyncsdk.SDK
	indexer *indexer.Indexer
	push    *push.Service
	account string
	// derived is the derived-space registry resolved against this
	// account (see derivedspaces.go).
	derived []resolvedDerivedSpace
	// procRelease drops the standing account-scope pub/sub interest
	// for process.* broadcasts (acquired in bootAccount, best-effort).
	procRelease func()
}

// errAlreadyAuthorized guards double-boot via POST /v1/auth.
var errAlreadyAuthorized = errors.New("already authorized")

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

// bootEngine opens the identity's wallet and brings up the SDK and the
// indexer for it. A non-zero seed.mnemonic seeds wallet creation
// (restore path) at seed.index. On any failure everything already
// opened is torn back down; if THIS call freshly created a per-account
// wallet, the orphaned account dir is removed too — otherwise a
// half-initialized account (especially a generated one whose phrase
// was never surfaced to the caller) would linger and be auto-selected
// on the next start.
func bootEngine(ctx context.Context, cfg config.Config, root string, id *Identity, seed walletSeed, streamsCtx context.Context, chunkers *index.Registry, onProcess func(indexer.ProcessUpdate)) (eng *engine, err error) {
	if err := os.MkdirAll(id.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create account dir %s: %w", id.Dir, err)
	}
	// A held lock means another process owns this account dir — bail
	// before touching the wallet, and never clean up on this path. Goes
	// through acquirePIDLock so the mobile build (one in-process instance)
	// can no-op it at both bootAccount sites; see pidlock_acquire*.go.
	lock, err := acquirePIDLock(config.PIDPath(id.Dir))
	if err != nil {
		return nil, err
	}

	passkey, err := config.ResolvePasskey(cfg, false)
	if err != nil {
		_ = lock.Release()
		return nil, err
	}
	provider, createdWallet, err := OpenWallet(id.WalletPath, passkey, seed.mnemonic, seed.index)
	if err != nil {
		_ = lock.Release()
		return nil, err
	}
	// Hold the lock from here. On failure release it, and if we just
	// created a per-account wallet, remove the orphan dir. Registered
	// before the sdk-close defer so (LIFO) sdk.Close runs first, then
	// release, then the dir removal — the pid file is gone cleanly
	// before RemoveAll. Scoped to per-account dirs: never the legacy
	// flat root or an explicit --wallet path.
	defer func() {
		if err != nil {
			_ = lock.Release()
			if createdWallet && id.Dir != root {
				_ = os.RemoveAll(id.Dir)
			}
		}
	}()

	account, err := AccountID(ctx, provider)
	if err != nil {
		return nil, fmt.Errorf("derive account id: %w", err)
	}
	if id.Account != "" && id.Account != account {
		return nil, fmt.Errorf("wallet at %s is account %s, not %s", id.WalletPath, account, id.Account)
	}

	sdk, err := OpenSDK(ctx, cfg, id.Dir, provider)
	if err != nil {
		return nil, fmt.Errorf("open sdk: %w", err)
	}
	defer func() {
		if err != nil {
			_ = sdk.Close()
		}
	}()

	// Refresh this device's registry row (SYN-165): os/version are
	// server-stamped so every boot keeps them current; the display name
	// is seeded from the hostname only while the row carries none so a
	// user-set name is never clobbered. Best-effort — the registry is
	// convenience metadata and must never block boot — and deferred
	// until the SDK's bootstrap pass completes (see registerDevice).
	go registerDevice(streamsCtx, sdk)

	var ix *indexer.Indexer
	if cfg.Index.Enabled {
		ix, err = OpenIndexer(ctx, cfg.Index, id.Dir, config.ModelsDir(root), sdk, chunkers, onProcess)
		if err != nil {
			return nil, fmt.Errorf("open indexer: %w", err)
		}
		ix.Start(streamsCtx)
	}

	// Push notifications: only when config names a push node (the SDK
	// side was threaded by OpenSDK under the same gate). Token file
	// lives in the per-account dir; Start never fails boot — a
	// persisted token re-registers in the background.
	var ps *push.Service
	if cfg.Push.Active() {
		ps = push.New(sdk, id.Dir)
		ps.Start(streamsCtx)
		// The first subscription sync can run before the SDK's background
		// boot pass materializes offline-created chats; kick a re-sync once
		// the pass completes so healing doesn't wait for the periodic tick.
		go func() {
			select {
			case <-sdk.BootstrapDone():
				ps.Kick()
			case <-streamsCtx.Done():
			}
		}()
	}

	derived, err := resolveDerivedSpaces(ctx, sdk)
	if err != nil {
		return nil, fmt.Errorf("resolve derived spaces: %w", err)
	}

	return &engine{lock: lock, sdk: sdk, indexer: ix, push: ps, account: account, derived: derived}, nil
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
// authMu; exactly one engine per process lifetime.
func (d *deps) bootAccount(id *Identity, seed walletSeed) (*engine, error) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	if d.ready.Load() {
		return nil, errAlreadyAuthorized
	}
	eng, err := bootEngine(d.runCtx, d.cfg, d.root, id, seed, d.shutdownCtx, d.chunkers, d.indexerProcess)
	if err != nil {
		return nil, err
	}
	d.eng = eng
	d.sdk = eng.sdk
	d.indexer = eng.indexer
	d.push = eng.push
	d.account = eng.account
	d.derived = eng.derived
	d.ready.Store(true)
	go d.holdProcessInterest()
	// Restore side of the setup split: adopt the installs of the
	// well-known derived spaces this account already has
	// (derivedsetup.go). Never creates a space; never blocks serving.
	go d.bootstrapDerivedSetups(d.shutdownCtx)
	// Declaration side: ensure the server-owned account-level bundles
	// (favorites/v1) on the tech space — idempotent, offline-capable,
	// never blocks serving (builtinbundles.go).
	go d.ensureBuiltinBundles(d.shutdownCtx)
	return eng, nil
}

// holdProcessInterest acquires the standing account-scope interest for
// process.* broadcasts (ev/process/>), so this account's other
// devices' processes materialize in the view with no local SSE
// subscriber. Space-scope coverage stays subscriber-driven
// (docs/22-processes.md § Remote visibility). Retries with backoff
// until acquired or shutdown — unlike an SSE client, this interest
// has no reconnect path, so a one-shot attempt could silently cost
// remote visibility for the whole process lifetime.
func (d *deps) holdProcessInterest() {
	ctx := d.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	var done <-chan struct{}
	if d.shutdownCtx != nil {
		done = d.shutdownCtx.Done()
	}
	backoff := 2 * time.Second
	for {
		release, err := d.eventsNet().acquire(ctx, true, nil, []string{eventTopicPrefix + "process/>"})
		if err == nil {
			d.authMu.Lock()
			if d.eng != nil {
				d.eng.procRelease = release
				d.authMu.Unlock()
				return
			}
			d.authMu.Unlock()
			release() // engine already torn down
			return
		}
		handlerLog.Warn("process event interest not acquired; retrying", zap.Error(err))
		select {
		case <-done:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// indexerProcess bridges indexer lifecycle updates onto the process
// view as device-scope processes — each device indexes its own copy,
// other devices don't care. Kinds: index.fts.<spaceId> (chunk/advance
// backlog), index.embed.<spaceId> (vector drain, total from the
// pending count), index.model_download (embedding-model fetch,
// done/total bytes, target = model file name). Updates arriving
// before the engine is published (the boot pass can start work first)
// are dropped — every frame carries the full descriptor, so the view
// recovers from any later one. Cancel requests are ignored by these
// producers; work stopped mid-pass (space dropped, shutdown) reports
// cancelled. Failure messages are deliberately generic — indexer
// errors carry filesystem paths and upstream response bodies, which
// never go on the wire; the detail is in the server log.
func (d *deps) indexerProcess(u indexer.ProcessUpdate) {
	if !d.ready.Load() {
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
		Sender: &api.EventSender{Identity: d.account, Self: true},
	})
}

// closeEngine tears down the live engine, if any: indexer and push
// first so their workers stop reading from the SDK, then the SDK,
// then the pid lock.
func (d *deps) closeEngine(lg logger.CtxLogger) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	eng := d.eng
	if eng == nil {
		return
	}
	if eng.procRelease != nil {
		eng.procRelease()
	}
	if eng.indexer != nil {
		if err := eng.indexer.Close(); err != nil {
			lg.Warn("indexer close", zap.Error(err))
		}
	}
	if eng.push != nil {
		if err := eng.push.Close(); err != nil {
			lg.Warn("push close", zap.Error(err))
		}
	}
	if err := eng.sdk.Close(); err != nil {
		lg.Warn("sdk close", zap.Error(err))
	}
	if err := eng.lock.Release(); err != nil {
		lg.Warn("release pid lock", zap.Error(err))
	}
	d.eng = nil
}
