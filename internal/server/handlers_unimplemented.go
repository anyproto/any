package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// notImplemented returns a handler that responds with the canonical
// 501 envelope. surface is the SDK method name surfaced in the message
// (e.g. "Spaces.Join"); it is informational only and not parsed by
// clients — the stable signal is the sdk.not_implemented code.
func notImplemented(surface string) echo.HandlerFunc {
	return func(c echo.Context) error {
		msg := "not implemented in this SDK build"
		var details map[string]any
		if surface != "" {
			details = map[string]any{"surface": surface}
		}
		return writeError(c, http.StatusNotImplemented, "sdk.not_implemented", msg, details)
	}
}
