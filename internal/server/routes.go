package server

import (
	"net/http"
	_ "net/http/pprof"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"
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
		// (GET /v1/spaces/:spaceId/files/:fileId/content).
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAccept, "Range"},
	}))
	// Global body cap for the JSON API. The one exemption is the file
	// attach route — its raw body IS the file, streamed straight into
	// the SDK without buffering, so a byte cap would truncate uploads.
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
		Skipper: func(c echo.Context) bool {
			return c.Path() == "/v1/spaces/:spaceId/objects/:objectId/files"
		},
		Limit: "1M",
	}))
	e.Use(httpLogMiddleware())

	v1 := e.Group("/v1")
	// Unauthorized guard: until an engine is live (deps.ready) every
	// /v1 route except the meta set and /v1/auth itself rejects with
	// 401 auth.required, so SDK-backed handlers never observe a nil
	// sdk. Group middleware applies to routes registered after Use —
	// keep this above the route registrations.
	v1.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if d.ready.Load() {
				return next(c)
			}
			switch c.Path() {
			case "/v1/health", "/v1/shutdown", "/v1/openapi.json", "/v1/auth":
				return next(c)
			case "/v1/*":
				// Unmatched route — echo's not-found pattern. Pass it
				// through so it renders a 404 rather than masking
				// unknown paths as auth.required.
				return next(c)
			}
			return writeError(c, http.StatusUnauthorized, "auth.required",
				"no account authorized — POST /v1/auth first", nil)
		}
	})
	v1.GET("/health", d.health)
	v1.POST("/shutdown", d.shutdownHandler)
	v1.GET("/openapi.json", serveOpenAPI)

	v1.Use(d.techSpaceRouteGuard)

	registerAuthRoutes(v1, d)
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
	// false (deps.local == nil). See docs/26-local-store.md.
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
	// so /ui and /ui/ 404 there. See docs/05-config.md § webUI, IOS-116.
	if d.cfg.WebUI.Enabled {
		registerUIRoutes(e)
	}

	e.Any("/debug/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	return e
}
