package server

import (
	"context"
	"net/http"
	_ "net/http/pprof"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/api"
)

// recoverLog surfaces recovered panics (which otherwise render as a bare
// "internal error" 500 with nothing in the log) through the app logger with
// the full stack.
var recoverLog = logger.NewNamed("recover")

func buildEcho(d *deps) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = errorHandler

	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			recoverLog.Error("panic recovered",
				zap.String("method", c.Request().Method),
				zap.String("path", c.Request().URL.Path),
				zap.Error(err),
				zap.ByteString("stack", stack),
			)
			return err
		},
	}))
	e.Use(middleware.RequestID())
	// Desktop-shell webview origins (any-ui PR-095 / PR #162): the bundled
	// SPA runs at a custom-scheme origin and must pass browser-side CORS to
	// reach this loopback API (REST + SSE). Fixed allowlist — no remote web
	// page can ever carry these origins (custom schemes are unclaimable by
	// web content; RFC 6761 pins `.localhost` to loopback), and local
	// processes were never gated by CORS, so the loopback-only listen stays
	// the trust boundary. This is the one named exception to the "no CORS
	// in v1" stance (docs/03-api.md § Middleware). No-op for
	// requests without an Origin header (curl / CLI / same-origin).
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{
			"tauri://localhost",      // macOS / Linux webview
			"http://tauri.localhost", // Windows webview
			"http://localhost:5173",  // tauri dev (Vite, plain http)
			"http://127.0.0.1:5173",
		},
		// Range lets the webview issue ranged file-content downloads
		// (GET /v1/spaces/:spaceId/files/:fileId/content); the control
		// token is what the shell's webview logs a managed server in
		// with (docs/08-clients.md § 14).
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAccept, "Range", api.ControlTokenHeader},
	}))
	// Global body cap for the JSON API. Two exemptions, both routes
	// whose raw body IS a file streamed without buffering, so a byte
	// cap would truncate it: file attach (into the SDK) and the local
	// store import (into any-store, 256 documents per transaction).
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
		Skipper: func(c echo.Context) bool {
			switch c.Path() {
			case "/v1/spaces/:spaceId/objects/:objectId/files", "/v1/local/import":
				return true
			}
			return false
		},
		Limit: "1M",
	}))
	e.Use(httpLogMiddleware())

	v1 := e.Group("/v1")
	// Unauthorized guard + engine gate: while an engine is live
	// (deps.ready) every request runs inside the gate, so a teardown
	// (shutdown, logout, account switch) drains it before the engine
	// fields change — SDK-backed handlers see one engine for their
	// whole lifetime and never a nil sdk. Without one, every /v1 route
	// except the meta set and /v1/auth itself rejects with 401
	// auth.required. Group middleware applies to routes registered
	// after Use — keep this above the route registrations.
	//
	// The exempt routes run OUTSIDE the gate on purpose: /v1/auth and
	// /v1/shutdown are the ones that initiate a teardown, and a
	// teardown waits for the gate to drain.
	v1.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			switch c.Path() {
			case "/v1/health", "/v1/shutdown", "/v1/openapi.json", "/v1/auth", "/v1/local-discovery":
				return next(c)
			case "/v1/*":
				// Unmatched route — echo's not-found pattern. Pass it
				// through so it renders a 404 rather than masking
				// unknown paths as auth.required.
				return next(c)
			}
			if d.ready.Load() && d.gate.enter() {
				defer d.gate.leave()
				// Bind the request to the engine: teardown cancels the
				// engine ctx, and every SDK call takes the request ctx,
				// so in-flight work unwinds instead of holding the
				// drain open. shutdownCtx is stable inside the gate.
				if engCtx := d.shutdownCtx; engCtx != nil {
					ctx, cancel := context.WithCancel(c.Request().Context())
					stop := context.AfterFunc(engCtx, cancel)
					defer stop()
					defer cancel()
					c.SetRequest(c.Request().WithContext(ctx))
				}
				return next(c)
			}
			return writeError(c, http.StatusUnauthorized, "auth.required",
				"no account authorized — POST /v1/auth first", nil)
		}
	})
	v1.GET("/health", d.health)
	v1.POST("/shutdown", d.shutdownHandler)
	v1.GET("/openapi.json", serveOpenAPI)
	// Registered before the tech-space guard: the auth routes take no
	// spaceId, and the guard's engine read belongs inside the gate.
	registerAuthRoutes(v1, d)
	// The local-discovery switch is exempt from the guard like /v1/auth:
	// the host states it BEFORE the first boot, so that boot starts
	// discovery in the right state (local_discovery.go). The handlers
	// enter the gate themselves when an engine is live.
	v1.GET("/local-discovery", d.localDiscoveryGet)
	v1.PUT("/local-discovery", d.localDiscoverySet)

	v1.Use(d.techSpaceRouteGuard)

	registerAccountRoutes(v1, d)
	registerSpaceRoutes(v1, d)
	// Per-space bundles registry (handlers_bundles.go) — what clients
	// have installed into a space.
	registerBundleRoutes(v1, d)

	// Account-wide sync-status subscribe sits outside the space group:
	// the SDK's Service.SubscribeStatus delivers every known space's
	// rollup transitions on one stream, so it has no :spaceId scope.
	v1.GET("/sync-status/subscribe", d.syncStatusSubscribe)

	// Account-wide p2p (local network) snapshot — listener, discovery
	// possibility, every known LAN peer. Account-scoped like
	// sync-status/subscribe.
	v1.GET("/debug/p2p", d.debugP2P)

	// Account-wide dataset discovery: the tech-space system datasets
	// (spaces, profile) that back the generic space-list query/subscribe.
	// Account-scoped, so like sync-status/subscribe it sits outside the
	// per-space group.
	v1.GET("/datasets", d.systemDatasets)

	// Account-global identities directory: every account identity this
	// account has encountered (profiles + the spaces where each was
	// seen), backed by SDK.Identities(). Account-scoped — no :spaceId —
	// so like sync-status/subscribe it sits outside the space group.
	registerIdentitiesRoutes(v1, d)

	// Account-global device registry: one tech-space row per device
	// (peer) of this account, with per-app install flags and the
	// active-instance claims (SYN-165). Account-scoped — no :spaceId —
	// so like sync-status/subscribe it sits outside the space group.
	// See docs/23-devices.md.
	registerDevicesRoutes(v1, d)

	// The usecase catalog: the server's embedded well-known bundles and
	// the one-call setup of a usecase into a space, dependencies
	// included. Account-scoped — the catalog belongs to the server —
	// so it sits outside the space group like /v1/devices. See
	// docs/28-well-known-bundles.md.
	registerCatalogRoutes(v1, d)

	// Account-wide file-cache controls: local bytes held by file
	// content across ALL spaces (SDK-level, not per-space), so like
	// sync-status/subscribe they sit outside the space group. See
	// docs/17-files.md § Cache.
	v1.GET("/files/cache", d.fileCacheGet)
	v1.POST("/files/cache/free", d.fileCacheFree)
	v1.POST("/files/cache/sweep", d.fileCacheSweep)

	// Account-wide event bus: an in-memory, at-most-once broadcast
	// channel (agent → UI navigation, process progress, …). Not space
	// data — no :spaceId scope, no SDK/dataset backing, nothing stored;
	// account/space scopes bridge onto the SDK pub/sub. Sits outside
	// the space group like sync-status/subscribe. See docs/21-events.md.
	v1.POST("/events", d.eventsPublish)
	v1.GET("/events/subscribe", d.eventsSubscribe)

	// Process helper over the event bus: register/progress/finish emit
	// process.* events on the caller's chosen scope, cancel addresses
	// the owner, GET is the in-memory last-event-wins view. Nothing
	// stored — same account-scoped placement as /events. Static
	// segments (a future /processes/subscribe) must register before
	// the :id wildcards. See docs/22-processes.md.
	v1.GET("/processes", d.processesList)
	v1.POST("/processes", d.processRegister)
	v1.POST("/processes/:id/progress", d.processProgress)
	v1.POST("/processes/:id/finish", d.processFinish)
	v1.POST("/processes/:id/cancel", d.processCancel)

	// Local store: device-local, non-CRDT any-store collections in the
	// SDK's sdk.db under the "l_" tag — query/aggregate power without
	// sync. Consumer-side like /search and /events, account-scoped (a
	// space-scoped collection is addressed in the body), so it sits
	// outside the space group. 409 local.disabled when local.enabled is
	// false (deps.local == nil).
	registerLocalRoutes(v1, d)

	// Account-wide push notifications: device-token registration and
	// the server-held topic subscriptions (SYN-47). The push server
	// identifies the caller by account on the secure channel, so the
	// surface has no :spaceId scope — it sits outside the space group
	// like sync-status/subscribe. 409 push.disabled when no push node
	// is configured (deps.push == nil).
	v1.POST("/push/token", d.pushTokenSet)
	v1.GET("/push/token", d.pushTokenStatus)
	v1.DELETE("/push/token", d.pushTokenRevoke)
	v1.GET("/push/subscriptions", d.pushSubscriptions)

	// The /ui debug harness is mounted only when enabled (default on).
	// App-embedded boots run headless (embedded.Start sets this false),
	// so /ui and /ui/ 404 there. See `webUI` in docs/05-config.md.
	if d.cfg.WebUI.Enabled {
		registerUIRoutes(e)
	}

	e.Any("/debug/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	return e
}
