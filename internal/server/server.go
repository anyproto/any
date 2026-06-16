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

// Run starts the HTTP server and blocks until ctx is cancelled or
// POST /v1/shutdown is called. Caller is responsible for installing
// signal handlers and cancelling ctx accordingly.
//
// When the data dir resolves to an account (root wallet, sole
// per-account dir, or an explicit selector) its engine — wallet, SDK,
// indexer — boots before the listener binds, so failures surface
// before any request is accepted. With no account to boot the server
// starts UNAUTHORIZED: every /v1 route except health/shutdown/auth
// returns 401 auth.required until POST /v1/auth creates or selects an
// account and boots the engine in place.
func Run(ctx context.Context, cfg config.Config) error {
	return RunWithListener(ctx, cfg, nil)
}

// RunWithListener is Run with an optional onListen callback invoked once
// with the RESOLVED bound address (host:port) the instant the listener
// is up, before the serve loop accepts traffic. It exists for in-process
// embedders (the iOS c-archive, IOS-6169) that ask for an ephemeral port
// via `Listen.Addr = "127.0.0.1:0"` and need the real port handed back
// WITHOUT scraping stdout — the `LISTENING %s` print is a desktop-shell
// contract, not a programmatic API. onListen runs on Run's goroutine
// (synchronously, before select); a nil onListen is the desktop/cobra
// path and changes nothing. Keep onListen cheap and non-blocking: it
// runs before the server starts serving.
func RunWithListener(ctx context.Context, cfg config.Config, onListen func(addr string)) error {
	logConfigOnce.Do(cfg.Log.ApplyGlobal)
	lg := logger.NewNamed("server")

	if err := ValidateLoopback(cfg.Listen.Addr); err != nil {
		return err
	}

	root, err := config.EnsureDataDir(cfg.DataDir)
	if err != nil {
		return err
	}

	identity, err := ResolveIdentity(cfg, root)
	var noIdentity *ErrNoIdentity
	if errors.As(err, &noIdentity) {
		identity = nil
	} else if err != nil {
		return err
	}

	shutdown := make(chan struct{}, 1)
	streamsCtx, cancelStreams := context.WithCancel(context.Background())
	defer cancelStreams()

	deps := &deps{
		startedAt:      time.Now().UTC(),
		shutdown:       shutdown,
		chunkers:       NewIndexRegistry(),
		shutdownCtx:    streamsCtx,
		cancelShutdown: cancelStreams,
		streamsWG:      &sync.WaitGroup{},
		root:           root,
		cfg:            cfg,
		runCtx:         ctx,
	}
	defer deps.closeEngine(lg)

	if identity != nil {
		if _, err := deps.bootAccount(identity, walletSeed{}); err != nil {
			return err
		}
	} else {
		lg.Info("no account selected — starting unauthorized, waiting for POST /v1/auth",
			zap.Strings("available", noIdentity.Accounts))
	}

	e := buildEcho(deps)

	// Bind explicitly so the RESOLVED address is known before serving —
	// `--addr 127.0.0.1:0` asks the kernel for an ephemeral port, and the
	// desktop shell needs the real one.
	ln, err := net.Listen("tcp", cfg.Listen.Addr)
	if err != nil {
		return err
	}
	e.Listener = ln
	boundAddr := ln.Addr().String()

	serveErr := make(chan error, 1)
	go func() {
		if err := e.Start(""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()
	// Do not change this line: the desktop shell (any-ui PR-095 / PR #162)
	// parses it as its port handshake + readiness gate.
	fmt.Printf("LISTENING %s\n", boundAddr)
	// Hand the resolved address to an in-process caller (iOS c-archive)
	// without it having to parse the print above. Nil on the desktop path.
	if onListen != nil {
		onListen(boundAddr)
	}
	lg.Info("listening", zap.String("addr", boundAddr), zap.String("account", deps.accountID()))
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
