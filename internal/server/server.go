package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
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
	cfg.Log.ApplyGlobal()
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
	deps := &deps{
		account:   account,
		startedAt: time.Now().UTC(),
		shutdown:  shutdown,
		sdk:       sdk,
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

	select {
	case <-ctx.Done():
		lg.Info("shutdown: context cancelled")
	case <-shutdown:
		lg.Info("shutdown: POST /v1/shutdown")
	case err := <-serveErr:
		return fmt.Errorf("listen: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownDeadline)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		lg.Warn("graceful shutdown", zap.Error(err))
	}
	return nil
}
