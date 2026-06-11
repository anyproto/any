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
	v1.GET("/health", d.health)
	v1.POST("/shutdown", d.shutdownHandler)
	v1.GET("/openapi.json", serveOpenAPI)

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

	registerUIRoutes(e)

	e.Any("/debug/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	return e
}
