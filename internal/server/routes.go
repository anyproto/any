package server

import (
	"net/http"
	_ "net/http/pprof"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func buildEcho(d *deps) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = errorHandler

	e.Use(middleware.Recover())
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
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAccept},
	}))
	e.Use(middleware.BodyLimit("1M"))
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

	registerAuthRoutes(v1, d)
	registerAccountRoutes(v1, d)
	registerSpaceRoutes(v1, d)

	// Account-wide sync-status subscribe sits outside the space group:
	// the SDK's Service.SubscribeStatus delivers every known space's
	// rollup transitions on one stream, so it has no :spaceId scope.
	v1.GET("/sync-status/subscribe", d.syncStatusSubscribe)

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

	// Account-wide UI command channel: an in-memory broadcast from the
	// agent to connected UI windows ("open this space/object"). Not
	// space data — no :spaceId scope, no SDK/dataset backing, nothing
	// stored. Sits outside the space group like sync-status/subscribe.
	// See docs/15-ui-commands.md.
	v1.POST("/ui/commands", d.uiCommandPublish)
	v1.GET("/ui/commands/subscribe", d.uiCommandSubscribe)

	registerUIRoutes(e)

	e.Any("/debug/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	return e
}
