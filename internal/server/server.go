package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

const gracefulShutdownDeadline = 10 * time.Second

// Run starts the HTTP server and blocks until ctx is cancelled or
// POST /v1/shutdown is called. Caller is responsible for installing
// signal handlers and cancelling ctx accordingly.
//
// The wallet is opened before the listener binds so the mnemonic can be
// printed to stderr before any request is accepted. The SDK is opened
// next; failures here surface before the server starts accepting traffic.
func Run(ctx context.Context, cfg config.Config) error {
	logConfigOnce.Do(cfg.Log.ApplyGlobal)
	lg := logger.NewNamed("server")

	if err := ValidateLoopback(cfg.Listen.Addr); err != nil {
		return err
	}

	dataDir, err := config.EnsureDataDir(cfg.DataDir)
	if err != nil {
		return err
	}

	lock, err := Acquire(config.PIDPath(dataDir))
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil {
			lg.Warn("release pid lock", zap.Error(err))
		}
	}()

	passkey, err := config.ResolvePasskey(cfg, false)
	if err != nil {
		return err
	}
	walletPath := config.WalletPath(cfg, dataDir)
	provider, firstRun, err := OpenWallet(walletPath, passkey)
	if err != nil {
		return err
	}
	if firstRun {
		PrintMnemonic(provider.Mnemonic())
	}

	account, err := AccountID(ctx, provider)
	if err != nil {
		return fmt.Errorf("derive account id: %w", err)
	}

	sdk, err := OpenSDK(ctx, cfg, dataDir, provider)
	if err != nil {
		return fmt.Errorf("open sdk: %w", err)
	}
	defer func() {
		if err := sdk.Close(); err != nil {
			lg.Warn("sdk close", zap.Error(err))
		}
	}()

	shutdown := make(chan struct{}, 1)
	streamsCtx, cancelStreams := context.WithCancel(context.Background())
	defer cancelStreams()

	chunkers := NewIndexRegistry()

	// Indexer: deferred Close registered after sdk's so it runs first —
	// workers stop reading from the SDK before the SDK closes. Cursors
	// persist per batch, so no flush is needed beyond Close.
	var ix *indexer.Indexer
	if cfg.Index.Enabled {
		ix, err = OpenIndexer(ctx, cfg.Index, dataDir, sdk, chunkers, lg)
		if err != nil {
			return fmt.Errorf("open indexer: %w", err)
		}
		defer func() {
			if err := ix.Close(); err != nil {
				lg.Warn("indexer close", zap.Error(err))
			}
		}()
		ix.Start(streamsCtx)
	}

	deps := &deps{
		account:        account,
		startedAt:      time.Now().UTC(),
		shutdown:       shutdown,
		sdk:            sdk,
		chunkers:       chunkers,
		indexer:        ix,
		shutdownCtx:    streamsCtx,
		cancelShutdown: cancelStreams,
		streamsWG:      &sync.WaitGroup{},
	}
	e := buildEcho(deps)

	serveErr := make(chan error, 1)
	go func() {
		if err := e.Start(cfg.Listen.Addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()
	lg.Info("listening", zap.String("addr", cfg.Listen.Addr), zap.String("account", account))
	lg.Info("web ui", zap.String("url", "http://"+cfg.Listen.Addr+"/ui"))

	select {
	case <-ctx.Done():
		lg.Info("shutdown: context cancelled")
	case <-shutdown:
		lg.Info("shutdown: POST /v1/shutdown")
	case err := <-serveErr:
		return fmt.Errorf("listen: %w", err)
	}

	// Signal SSE/streaming handlers to wrap up so their final `closed`
	// frame lands before the listener tears the socket down. Then bound
	// the rest of the shutdown by gracefulShutdownDeadline; e.Shutdown
	// stops accepting new connections and waits for in-flight handlers
	// to return.
	deps.cancelShutdown()
	if !waitTimeout(deps.streamsWG, gracefulShutdownDeadline) {
		lg.Warn("graceful shutdown: streaming handlers did not finish in time")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownDeadline)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		lg.Warn("graceful shutdown", zap.Error(err))
	}
	return nil
}

// waitTimeout returns true if wg drains within d, false otherwise.
// The streaming-handler drain races the overall shutdown deadline —
// if a handler is wedged on a slow client write it gets cut off
// rather than blocking the shutdown indefinitely.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	if wg == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}
