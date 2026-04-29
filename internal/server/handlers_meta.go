package server

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	anysyncsdk "github.com/anyproto/any-sync-sdk"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/version"
)

// deps is the per-process bundle of objects routes need: an account id
// for /health, the start time, the shutdown signal, and a live SDK
// handle once the SDK slice landed. It is built once in server.Run and
// shared across all routes via closures registered on echo.
type deps struct {
	account   string
	startedAt time.Time
	// shutdown signals server.Run to begin graceful teardown. Receives at
	// most one value; subsequent sends are dropped by the non-blocking send.
	shutdown chan<- struct{}
	sdk      *anysyncsdk.SDK
}

func (d *deps) health(c echo.Context) error {
	return c.JSON(http.StatusOK, api.HealthResponse{
		Status:    "ok",
		Version:   version.String(),
		StartedAt: d.startedAt,
		Account:   d.account,
	})
}

func (d *deps) shutdownHandler(c echo.Context) error {
	select {
	case d.shutdown <- struct{}{}:
	default:
	}
	return c.NoContent(http.StatusNoContent)
}
