package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync/util/crypto"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bundles"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/localstore"
	"github.com/anyproto/any/internal/push"
	"github.com/anyproto/any/internal/version"
)

// deps is the per-process bundle of objects routes need: an account id
// for /health, the start time, the shutdown signal, and a live SDK
// handle once the SDK slice landed. It is built once in server.Run and
// shared across all routes via closures registered on echo.
//
// The engine gate + shutdownCtx pair coordinates engine teardown
// (process exit, DELETE /v1/auth, an account switch): every request
// and stream runs inside the gate, teardown cancels shutdownCtx so
// they unwind — streams emit their terminal frame — and drains the
// gate before the engine's resources are released (engine.go).
type deps struct {
	account   string
	startedAt time.Time
	// networkId is the pinned nodeconf's networkId (pinNodeconf), fixed
	// for the process lifetime.
	networkId string
	// shutdown signals server.Run to begin graceful teardown. Receives at
	// most one value; subsequent sends are dropped by the non-blocking send.
	shutdown chan<- struct{}
	sdk      *anysyncsdk.SDK
	// signKey is the booted account's signing key (handlers_access.go).
	signKey crypto.PrivKey
	// derived is the derived-space registry resolved against the booted
	// account (derivedspaces.go) — set with sdk, published by ready.
	derived []resolvedDerivedSpace

	// installs drives the per-space bundle installs and carries their
	// loser verdicts across requests (derivedsetup.go). Created lazily
	// via bundleResolver() — it has no engine/SDK dependency, so every
	// deps construction path gets one with no explicit wiring.
	installsOnce sync.Once
	installs     *bundles.Resolver
	// catalog overrides the embedded usecase catalog — tests only;
	// nil means the compiled embedded one (server/catalog.go).
	catalog *compiledCatalog

	// localDiscovery is the host's answer to PUT /v1/local-discovery
	// (local_discovery.go). On deps rather than on the engine so it
	// survives logout and account switches, and can be given before the
	// first boot.
	localDiscovery localDiscoverySwitch

	// events is the account-wide in-memory event bus hub
	// (POST /v1/events → GET /v1/events/subscribe). Created lazily via
	// eventsHub() so every deps construction path gets one with no
	// explicit wiring — it has no engine/SDK dependency.
	eventsOnce sync.Once
	events     *eventHub
	// procs is the live process view over process.* events
	// (GET /v1/processes) — created in the same once as the hub so its
	// tap never misses a publish. Access via processes().
	procs *processRegistry

	// bridge refcounts SSE event filters into SDK pub/sub interests for
	// the account/space scopes (created lazily via eventsNet — only
	// reached behind the ready guard, so sdk is non-nil).
	bridgeOnce sync.Once
	bridge     *eventsBridge

	// indexer is the search indexer (FTS + vector over the chunker
	// feed). Nil when index.enabled is false — the search endpoint then
	// returns index.disabled.
	indexer *indexer.Indexer

	// push is the push-notification service (device token +
	// subscription sync + notify queue). Nil when push is disabled or
	// no push node is configured — the /v1/push endpoints then return
	// 409 push.disabled.
	push *push.Service

	// local is the device-local, non-CRDT collection store sharing the
	// SDK's sdk.db under the "l_" tag (handlers_local.go). Nil when
	// local.enabled is false — the /v1/local endpoints then return 409
	// local.disabled; existing collections stay untouched on disk.
	local *localstore.Store

	// shutdownCtx is the LIVE ENGINE's context: it cancels when that
	// engine's teardown begins (process exit, logout, account switch).
	// Streaming handlers select on Done to write their final `closed`
	// frame and exit; engine goroutines return on it. Nil while
	// unauthorized and in tests that never boot — SSE handlers fall
	// back to a never-cancel context. Swapped only between gate
	// drains, so a handler inside the gate sees one value.
	shutdownCtx context.Context

	// ready flips to true once an engine (wallet + SDK + indexer) is
	// live and false as the first step of its teardown. Until then the
	// /v1 guard middleware rejects every route except
	// health/shutdown/auth with 401 auth.required, so handlers never
	// observe a nil sdk. The store happens after the engine fields
	// above are populated; the middleware's atomic load is the acquire
	// edge that makes them visible. Tests building deps by hand must
	// set it (newTestDeps does).
	ready atomic.Bool
	// gate counts the requests and streams executing against the live
	// engine; teardown closes and drains it before touching the
	// fields above (gate.go). Zero value = open.
	gate engineGate

	// authMu serializes engine lifecycle: boot (POST /v1/auth vs.
	// server.Run), teardown (shutdown, DELETE /v1/auth) and the switch
	// that chains the two. Never taken by a request handler — teardown
	// holds it while draining the gate. eng is the live engine.
	authMu sync.Mutex
	eng    *engine

	// Boot inputs for the deferred-auth path: the resolved root data
	// dir, the effective config and the server's run context (engine
	// lifetime exceeds any single request, so boots don't run on
	// request contexts).
	root   string
	cfg    config.Config
	runCtx context.Context

	// controlToken gates the managed-mode control operations (auth
	// verbs + shutdown); empty on a standalone server, where those
	// operations are refused by mode instead. See control.go.
	controlToken string
	// boundAddr is the listener's resolved address, set before serving
	// starts; an engine booted afterwards records it beside its pid
	// file (server.addr) for the CLI.
	boundAddr string
}

// accountID returns the booted account id, or "" while unauthorized.
// Health and auth status run outside the guard middleware, so the read
// takes the gate itself: a teardown in progress answers "".
func (d *deps) accountID() string {
	if !d.ready.Load() || !d.gate.enter() {
		return ""
	}
	defer d.gate.leave()
	return d.account
}

// bootstrapping reports whether the booted SDK's background boot pass
// (eager space loading + offline catch-up) is still running. False
// while unauthorized and once the pass completes. Gate-scoped like
// accountID: the sdk field is stable for the duration of the read.
func (d *deps) bootstrapping() bool {
	if !d.ready.Load() || !d.gate.enter() {
		return false
	}
	defer d.gate.leave()
	if d.sdk == nil {
		return false
	}
	select {
	case <-d.sdk.BootstrapDone():
		return false
	default:
		return true
	}
}

// @Summary	Health check
// @Tags		system
// @Produce	json
// @Success	200	{object}	api.HealthResponse
// @Router		/health [get]
func (d *deps) health(c echo.Context) error {
	return c.JSON(http.StatusOK, api.HealthResponse{
		Status:        "ok",
		Version:       version.String(),
		StartedAt:     d.startedAt,
		NetworkId:     d.networkId,
		Account:       d.accountID(),
		Bootstrapping: d.bootstrapping(),
		CRDTVersion:   d.crdtVersion(),
	})
}

// crdtVersion reports the booted SDK's account CRDT-version state;
// nil when unauthorized.
func (d *deps) crdtVersion() *api.CRDTVersionState {
	if !d.ready.Load() || !d.gate.enter() {
		return nil
	}
	defer d.gate.leave()
	if d.sdk == nil {
		return nil
	}
	st := d.sdk.CRDTVersion()
	return &api.CRDTVersionState{Supported: st.Supported, Stored: st.Stored, Newer: st.Newer}
}

// shutdownHandler handles POST /v1/shutdown. Lifetime belongs to the
// server's owner: a managed host stops it here with the control token;
// a standalone server is the user's, stopped with `any stop` or a
// signal, and refuses. Stays outside the auth guard so an unauthorized
// managed server is still stoppable.
//
//	@Summary	Graceful shutdown (managed servers; needs the control token)
//	@Tags		system
//	@Param		X-Any-Control-Token	header	string	false	"managed servers: the control token"
//	@Success	204
//	@Failure	403	{object}	api.ErrorEnvelope
//	@Router		/shutdown [post]
func (d *deps) shutdownHandler(c echo.Context) error {
	if !d.cfg.Managed() {
		return writeError(c, http.StatusForbidden, "shutdown.not_managed",
			"standalone server: stop it with `any stop` or a signal", nil)
	}
	if !d.requireControl(c) {
		return nil
	}
	select {
	case d.shutdown <- struct{}{}:
	default:
	}
	return c.NoContent(http.StatusNoContent)
}
