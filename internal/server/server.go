package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
)

const gracefulShutdownDeadline = 10 * time.Second

// RunOptions tweaks server.Run behaviour without bloating its signature.
// All fields are optional and zero-valued by default.
type RunOptions struct {
	// Ready, if non-nil, is invoked once the TCP listener has bound but
	// before Echo starts serving. It receives the actually-bound address
	// (resolving "127.0.0.1:0" to the OS-picked host:port) so embedders
	// like the gomobile wrapper can hand the real port back to the
	// caller. The hook runs on the goroutine that called Run, so keep it
	// quick — long work blocks server startup.
	Ready func(addr string)
}

// Run starts the HTTP server and blocks until ctx is cancelled or
// POST /v1/shutdown is called. Caller is responsible for installing
// signal handlers and cancelling ctx accordingly.
//
// The wallet is opened before the listener binds so the mnemonic can be
// printed to stderr before any request is accepted. The SDK is opened
// next; failures here surface before the server starts accepting traffic.
func Run(ctx context.Context, cfg config.Config) error {
	return RunWith(ctx, cfg, RunOptions{})
}

// RunWith is Run plus a hook surface. Embedders (gomobile) use this to
// learn the bound address synchronously and surface bind / wallet / SDK
// errors before returning.
func RunWith(ctx context.Context, cfg config.Config, opts RunOptions) error {
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
	deps := &deps{
		account:        account,
		startedAt:      time.Now().UTC(),
		shutdown:       shutdown,
		sdk:            sdk,
		shutdownCtx:    streamsCtx,
		cancelShutdown: cancelStreams,
		streamsWG:      &sync.WaitGroup{},
	}
	e := buildEcho(deps)

	// Pre-bind the listener so listen errors (port in use, EACCES,
	// non-loopback after validation slipped) surface synchronously to
	// the caller, and so embedders can read back the real port when
	// cfg.Listen.Addr asked for ":0".
	ln, err := net.Listen("tcp", cfg.Listen.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	e.Listener = ln
	boundAddr := ln.Addr().String()
	if opts.Ready != nil {
		opts.Ready(boundAddr)
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := e.Start(""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()
	lg.Info("listening", zap.String("addr", boundAddr), zap.String("account", account))
	lg.Info("web ui", zap.String("url", "http://"+boundAddr+"/ui"))

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
