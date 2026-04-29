package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// writeError emits the canonical error envelope. Always emits the same
// shape regardless of status code so clients can rely on it.
func writeError(c echo.Context, status int, code, msg string, details map[string]any) error {
	return c.JSON(status, api.ErrorEnvelope{
		Error: api.APIError{Code: code, Message: msg, Details: details},
	})
}

// errorHandler is registered as echo.Echo.HTTPErrorHandler so built-in
// 404s, body-limit rejections, and any handler-returned echo.HTTPError
// land in the shared envelope.
func errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	status := http.StatusInternalServerError
	code := "internal"
	msg := "internal error"

	var he *echo.HTTPError
	if errors.As(err, &he) {
		status = he.Code
		code = codeForStatus(status)
		if m, ok := he.Message.(string); ok && m != "" {
			msg = m
		} else {
			msg = http.StatusText(status)
		}
	}

	_ = writeError(c, status, code, msg, nil)
}

func codeForStatus(s int) string {
	switch s {
	case http.StatusBadRequest:
		return "request.bad"
	case http.StatusNotFound:
		return "request.not_found"
	case http.StatusMethodNotAllowed:
		return "request.method_not_allowed"
	case http.StatusRequestEntityTooLarge:
		return "request.too_large"
	case http.StatusNotImplemented:
		return "sdk.not_implemented"
	case http.StatusServiceUnavailable:
		return "server.unavailable"
	default:
		return "internal"
	}
}
