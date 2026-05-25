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

	registerUIRoutes(e)

	e.Any("/debug/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	return e
}
