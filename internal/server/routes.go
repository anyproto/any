package server

import (
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

	registerAccountRoutes(v1, d)
	registerSpaceRoutes(v1, d)

	registerUIRoutes(e)

	return e
}
