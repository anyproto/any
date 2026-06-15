package server

import (
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync/app/logger"
)

// httpLogMiddleware emits one info-level line per request via the any-sync
// named logger "http" — keeping a single log stream across the process.
// It logs only method/path/status/duration/request_id; request and
// response BODIES are never logged. Keep it that way — POST /v1/auth
// carries the generated backup mnemonic in its response body, and the
// loopback trust boundary is not an excuse to leak it into server logs.
func httpLogMiddleware() echo.MiddlewareFunc {
	lg := logger.NewNamed("http")
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			if err != nil {
				// Let the error handler render the response; we still log
				// the outcome with whatever status was set (or 500 default).
				c.Error(err)
			}
			req := c.Request()
			res := c.Response()
			lg.Info("http",
				zap.String("method", req.Method),
				zap.String("path", req.URL.Path),
				zap.Int("status", res.Status),
				zap.Duration("duration", time.Since(start)),
				zap.String("request_id", res.Header().Get(echo.HeaderXRequestID)),
			)
			// Returning nil because we already forwarded to c.Error. Returning
			// err would cause echo to invoke the error handler twice.
			return nil
		}
	}
}
