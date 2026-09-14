package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/anyproto/any-sync-sdk/auth"
	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
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
	// ControlToken is the managed-mode control token an in-process host
	// supplies. Empty on a managed server means "mint one and announce
	// it on stdout" (the CLI / sidecar path). Ignored in standalone.
	ControlToken string
}

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
	return RunWith(ctx, cfg, RunOptions{})
}

// RunWith is Run plus a hook surface. Embedders (gomobile) use this to
// learn the bound address synchronously and surface bind / wallet / SDK
// errors before returning.
func RunWith(ctx context.Context, cfg config.Config, opts RunOptions) error {
	logConfigOnce.Do(cfg.Log.ApplyGlobal)
	lg := logger.NewNamed("server")

	// The search index is gated twice: build tags decide what is compiled
	// (fts / vector — vector is always off on gomobile), config decides
	// what runs. Warn once if the index is enabled but neither leg was
	// compiled in, so the empty-result state is observable, not silent.
	if cfg.Index.Enabled {
		if fts, vec := indexer.CompiledCaps(); !fts && !vec {
			lg.Warn("search index enabled (index.enabled) but built without the fts/vector tags — search returns no results; rebuild with -tags 'fts vector' (docs/13-index.md)")
		}
	}

	if err := ValidateLoopback(cfg.Listen.Addr); err != nil {
		return err
	}

	root, err := config.EnsureDataDir(cfg.DataDir)
	if err != nil {
		return err
	}

	// The embedded path assembles its config directly (no config.Load),
	// so normalize the mode here as well.
	if cfg.Mode, err = config.ParseMode(cfg.Mode); err != nil {
		return err
	}
	networkId, err := pinNodeconf(&cfg)
	if err != nil {
		return err
	}
	// A managed server never resolves an account from disk: the host
	// states it on POST /v1/auth every boot. A minted control token is
	// announced after LISTENING so only the spawning parent can read it.
	var (
		controlToken string
		mintedToken  bool
	)
	if cfg.Managed() {
		controlToken = opts.ControlToken
		if controlToken == "" {
			if controlToken, err = mintControlToken(); err != nil {
				return fmt.Errorf("mint control token: %w", err)
			}
			mintedToken = true
		}
	}

	var identity *Identity
	var noIdentity *ErrNoIdentity
	if !cfg.Managed() {
		identity, err = ResolveIdentity(cfg, root)
		if errors.As(err, &noIdentity) {
			identity = nil
		} else if err != nil {
			return err
		}
	}

	// The embedded usecase catalog is validated before anything boots:
	// a broken entry is a build defect, not a runtime condition to serve
	// around (`make catalog-validate` catches it in CI).
	if _, err := embeddedUsecaseCatalog(); err != nil {
		return fmt.Errorf("usecase catalog: %w", err)
	}

	shutdown := make(chan struct{}, 1)

	deps := &deps{
		startedAt:    time.Now().UTC(),
		networkId:    networkId,
		shutdown:     shutdown,
		root:         root,
		cfg:          cfg,
		runCtx:       ctx,
		controlToken: controlToken,
	}
	// Covers every early return past a successful boot (a bind
	// failure, for one): the engine's goroutines are joined and its
	// resources released. A no-op after the explicit teardown below.
	defer deps.teardownEngine(lg, api.SubscribeClosedServerShutdown)

	switch {
	case identity != nil:
		// Existing wallets ignore the seed entirely; the index matters
		// only for the wallet-override path, where a missing file is
		// freshly generated — at the any default, consistent with init.
		if _, err := deps.bootAccount(identity, fileCredential(cfg, identity.WalletPath, walletSeed{index: auth.DefaultAccountIndex})); err != nil {
			return err
		}
	case cfg.Managed():
		lg.Info("managed mode — starting unauthorized, waiting for the host's POST /v1/auth")
	default:
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
	// Record the address for the CLI: now for an engine booted before
	// the bind, and in publishEngine for every later boot (the field is
	// set before serving starts, so handlers observe it).
	deps.boundAddr = boundAddr
	if deps.eng != nil {
		writeAddrFile(deps.eng.dir, boundAddr)
	}
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
	// Do not change this line: the desktop shell (any-ui PR-095 / PR #162)
	// parses it as its port handshake + readiness gate.
	fmt.Printf("LISTENING %s\n", boundAddr)
	if mintedToken {
		// Second line of the same handshake: the parent owns the pipe,
		// so nobody else can read the token. Never logged.
		fmt.Printf("CONTROL_TOKEN %s\n", controlToken)
	}
	lg.Info("listening", zap.String("addr", boundAddr), zap.String("account", deps.accountID()), zap.String("mode", cfg.Mode))
	// Gated by the same flag as the /ui route mount (routes.go), so the
	// advertised URL never outlives the handler. Silent on headless
	// (app-embedded) boots — IOS-116.
	if deps.cfg.WebUI.Enabled {
		lg.Info("web ui", zap.String("url", "http://"+boundAddr+"/ui"))
	}

	select {
	case <-ctx.Done():
		lg.Info("shutdown: context cancelled")
	case <-shutdown:
		lg.Info("shutdown: POST /v1/shutdown")
	case err := <-serveErr:
		return fmt.Errorf("listen: %w", err)
	}

	// Tear the engine down first so streaming handlers write their
	// final `closed` frame and every in-flight request drains before
	// the listener tears the socket down (bounded by
	// gracefulShutdownDeadline inside). Then e.Shutdown stops accepting
	// new connections and waits for whatever is left.
	deps.teardownEngine(lg, api.SubscribeClosedServerShutdown)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownDeadline)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		lg.Warn("graceful shutdown", zap.Error(err))
	}
	return nil
}
