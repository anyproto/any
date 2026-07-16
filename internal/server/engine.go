package server

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	anysyncsdk "github.com/anyproto/any-sync-sdk"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/push"
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
}

// errAlreadyAuthorized guards double-boot via POST /v1/auth.
var errAlreadyAuthorized = errors.New("already authorized")

// walletSeed carries the inputs for CREATING a wallet from a known
// phrase: the BIP-39 mnemonic and its account-derivation index. Both
// zero for the load-an-existing-wallet path and for fresh generation
// (the SDK then generates the phrase at index 0). The index is paired
// with the mnemonic here because the two are meaningless apart and the
// Identity (which/where) deliberately doesn't carry derivation inputs.
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
func bootEngine(ctx context.Context, cfg config.Config, root string, id *Identity, seed walletSeed, streamsCtx context.Context, chunkers *index.Registry) (eng *engine, err error) {
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

	var ix *indexer.Indexer
	if cfg.Index.Enabled {
		ix, err = OpenIndexer(ctx, cfg.Index, id.Dir, config.ModelsDir(root), sdk, chunkers)
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
	}

	return &engine{lock: lock, sdk: sdk, indexer: ix, push: ps, account: account}, nil
}

// bootAccount boots an engine and publishes it on d. Serialized by
// authMu; exactly one engine per process lifetime.
func (d *deps) bootAccount(id *Identity, seed walletSeed) (*engine, error) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	if d.ready.Load() {
		return nil, errAlreadyAuthorized
	}
	eng, err := bootEngine(d.runCtx, d.cfg, d.root, id, seed, d.shutdownCtx, d.chunkers)
	if err != nil {
		return nil, err
	}
	d.eng = eng
	d.sdk = eng.sdk
	d.indexer = eng.indexer
	d.push = eng.push
	d.account = eng.account
	d.ready.Store(true)
	return eng, nil
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
