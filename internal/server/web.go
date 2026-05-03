package server

import (
	_ "embed"
	"net/http"

	"github.com/labstack/echo/v4"
)

// uiIndexHTML is the legacy single-page harness for poking the API
// against the same process serving it. The real SPA takes /ui via
// webapp.go. Drop /ui-legacy once the SPA covers the same
// query/object/markdown exercises.
//
//go:embed web/index.html
var uiIndexHTML []byte

// registerUIRoutes mounts the legacy test harness at GET /ui-legacy
// and the new SPA at /ui (via registerWebappRoutes, called from this
// function for symmetry).
func registerUIRoutes(e *echo.Echo) {
	e.GET("/ui-legacy", func(c echo.Context) error {
		return c.HTMLBlob(http.StatusOK, uiIndexHTML)
	})
	registerWebappRoutes(e)
}
