//go:build mobile

package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// serveOpenAPI is stubbed out under the mobile build: the swaggo/swag
// generator (and its os/exec dependency chain) is excluded from the
// embedded mobile binary. The route stays registered for parity but
// reports the spec is unavailable in this build.
func serveOpenAPI(c echo.Context) error {
	return c.JSON(http.StatusNotFound, map[string]string{
		"error": "openapi spec not available in mobile build",
	})
}
