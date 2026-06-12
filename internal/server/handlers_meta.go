package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	anysyncsdk "github.com/anyproto/any-sync-sdk"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/version"
)

// deps is the per-process bundle of objects routes need: an account id
// for /health, the start time, the shutdown signal, and a live SDK
// handle once the SDK slice landed. It is built once in server.Run and
// shared across all routes via closures registered on echo.
//
// The shutdownCtx / streamsWG pair coordinates graceful teardown for
// long-running streaming handlers (SSE subscribe). server.Run cancels
// shutdownCtx when teardown begins; handlers select on it and exit;
// streamsWG lets Run wait for them to finish before returning.
type deps struct {
	account   string
	startedAt time.Time
	// shutdown signals server.Run to begin graceful teardown. Receives at
	// most one value; subsequent sends are dropped by the non-blocking send.
	shutdown chan<- struct{}
	sdk      *anysyncsdk.SDK

	// chunkers is the index chunker registry, built once at boot via
	// NewIndexRegistry and driven by the indexer.
	chunkers *index.Registry

	// indexer is the search indexer (FTS + vector over the chunker
	// feed). Nil when index.enabled is false — the search endpoint then
	// returns index.disabled.
	indexer *indexer.Indexer

	// shutdownCtx cancels when graceful teardown begins. Streaming
	// handlers select on Done to write their final `closed` frame and
	// exit. Nil-tolerant: tests that don't go through server.Run leave
	// it unset and SSE handlers fall back to never-cancel context.
	// cancelShutdown is the matching CancelFunc; server.Run trips it
	// before draining streamsWG so handlers wake up and emit their
	// terminal frame inside the shutdown deadline.
	shutdownCtx    context.Context
	cancelShutdown context.CancelFunc
	streamsWG      *sync.WaitGroup
}

// @Summary	Health check
// @Tags		system
// @Produce	json
// @Success	200	{object}	api.HealthResponse
// @Router		/health [get]
func (d *deps) health(c echo.Context) error {
	return c.JSON(http.StatusOK, api.HealthResponse{
		Status:    "ok",
		Version:   version.String(),
		StartedAt: d.startedAt,
		Account:   d.account,
	})
}

// @Summary	Graceful shutdown
// @Tags		system
// @Success	204
// @Router		/shutdown [post]
func (d *deps) shutdownHandler(c echo.Context) error {
	select {
	case d.shutdown <- struct{}{}:
	default:
	}
	return c.NoContent(http.StatusNoContent)
}
