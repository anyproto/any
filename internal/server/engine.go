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
)

// engine bundles everything that exists only while an account is
// booted: the per-account pid lock, the SDK and the indexer. One per
// process; built either directly by server.Run (an identity resolved
// at boot) or later by POST /v1/auth.
type engine struct {
	lock    *Lock
	sdk     *anysyncsdk.SDK
	indexer *indexer.Indexer
	account string
}

// errAlreadyAuthorized guards double-boot via POST /v1/auth.
var errAlreadyAuthorized = errors.New("already authorized")

// bootEngine opens the identity's wallet and brings up the SDK and the
// indexer for it. mnemonic, when non-empty, seeds wallet creation
// (restore path). On any failure everything already opened is torn
// back down.
func bootEngine(ctx context.Context, cfg config.Config, root string, id *Identity, mnemonic string, streamsCtx context.Context, chunkers *index.Registry) (eng *engine, err error) {
	if err := os.MkdirAll(id.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create account dir %s: %w", id.Dir, err)
	}
	lock, err := Acquire(config.PIDPath(id.Dir))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = lock.Release()
		}
	}()

	passkey, err := config.ResolvePasskey(cfg, false)
	if err != nil {
		return nil, err
	}
	provider, _, err := OpenWallet(id.WalletPath, passkey, mnemonic)
	if err != nil {
		return nil, err
	}
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

	return &engine{lock: lock, sdk: sdk, indexer: ix, account: account}, nil
}

// bootAccount boots an engine and publishes it on d. Serialized by
// authMu; exactly one engine per process lifetime.
func (d *deps) bootAccount(id *Identity, mnemonic string) (*engine, error) {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	if d.ready.Load() {
		return nil, errAlreadyAuthorized
	}
	eng, err := bootEngine(d.runCtx, d.cfg, d.root, id, mnemonic, d.shutdownCtx, d.chunkers)
	if err != nil {
		return nil, err
	}
	d.eng = eng
	d.sdk = eng.sdk
	d.indexer = eng.indexer
	d.account = eng.account
	d.ready.Store(true)
	return eng, nil
}

// closeEngine tears down the live engine, if any: indexer first so its
// workers stop reading from the SDK, then the SDK, then the pid lock.
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
	if err := eng.sdk.Close(); err != nil {
		lg.Warn("sdk close", zap.Error(err))
	}
	if err := eng.lock.Release(); err != nil {
		lg.Warn("release pid lock", zap.Error(err))
	}
	d.eng = nil
}
