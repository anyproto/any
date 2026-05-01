package server

import (
	_ "embed"
	"net/http"

	"github.com/labstack/echo/v4"
)

// uiIndexHTML is a single-page browser harness for poking the API
// against the same process serving it. Same-origin so we keep the
// CORS-free localhost-only stance from CLAUDE.md / docs/00-overview.md.
//
//go:embed web/index.html
var uiIndexHTML []byte

// registerUIRoutes mounts the embedded test harness at GET /ui.
// Outside /v1/ on purpose — this is a debug surface, not part of the
// API contract.
func registerUIRoutes(e *echo.Echo) {
	e.GET("/ui", func(c echo.Context) error {
		return c.HTMLBlob(http.StatusOK, uiIndexHTML)
	})
}
