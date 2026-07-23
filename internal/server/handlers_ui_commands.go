package server

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// uiCommandPublish handles POST /v1/ui/commands. It validates the
// command and fans it out to every connected subscriber over the
// account-wide in-memory hub. Fire-and-forget: the response reports how
// many UI windows received it (0 = nobody listening) but a valid
// publish always succeeds. See docs/15-ui-commands.md.
//
//	@Summary	Publish a UI command
//	@Tags		ui
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.UICommand	true	"command"
//	@Success	200		{object}	api.UICommandPublishResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Router		/ui/commands [post]
func (d *deps) uiCommandPublish(c echo.Context) error {
	var cmd api.UICommand
	if err := c.Bind(&cmd); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if cmd.Action == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "action required", nil)
	}
	if cmd.SpaceId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "spaceId required", nil)
	}
	if cmd.Action == api.UICommandOpenObject && cmd.ObjectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"objectId required for open_object", nil)
	}
	n := d.uiHub().publish(cmd)
	return c.JSON(http.StatusOK, api.UICommandPublishResponse{Subscribers: n})
}

// uiCommandSubscribe handles GET /v1/ui/commands/subscribe. It streams
// UI commands to one connected client over SSE: an initial `ready`
// frame, then one `command` frame per published command, `: keepalive`
// comments while idle, and a terminal `closed` frame on server shutdown
// (reason server_shutdown) or per-subscriber overflow (reason
// overflow). There is no snapshot — the channel is in-memory and
// at-most-once, so a subscriber only sees commands published after it
// connects. Modeled on streamStatusSSE but simpler (no lag counter).
//
//	@Summary	Subscribe to UI commands (SSE)
//	@Tags		ui
//	@Produce	text/event-stream
//	@Success	200
//	@Router		/ui/commands/subscribe [get]
func (d *deps) uiCommandSubscribe(c echo.Context) error {
	if d.streamsWG != nil {
		d.streamsWG.Add(1)
		defer d.streamsWG.Done()
	}

	id, ch := d.uiHub().subscribe()
	defer d.uiHub().unsubscribe(id)

	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := writeSSEEvent(w, "ready", "", api.SubscribeReady{}); err != nil {
		return nil
	}
	w.Flush()

	waitCtx, cancelWait := mergeCtx(c.Request().Context(), d.shutdownCtx)
	defer cancelWait()

	// Keepalive: commands are sparse, so an idle stream would otherwise
	// sit silent past middlebox timeouts. Stop alongside the loop.
	stopKeepalive := make(chan struct{})
	go func() {
		t := time.NewTicker(keepaliveInterval)
		defer t.Stop()
		for {
			select {
			case <-stopKeepalive:
				return
			case <-waitCtx.Done():
				return
			case <-t.C:
				if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
					return
				}
				flush(w)
			}
		}
	}()
	defer close(stopKeepalive)

	for {
		select {
		case cmd, ok := <-ch:
			if !ok {
				// Hub dropped us: our buffer filled. Tell the client to
				// reconnect for a fresh stream.
				_ = writeSSEEvent(w, "closed", "", api.SubscribeClosed{Reason: api.SubscribeClosedOverflow})
				flush(w)
				return nil
			}
			if err := writeSSEEvent(w, "command", "", cmd); err != nil {
				return nil
			}
			flush(w)
		case <-waitCtx.Done():
			// Shutdown writes a terminal frame; a client disconnect does
			// not (the peer is already gone).
			if d.shutdownCtx != nil && d.shutdownCtx.Err() != nil {
				_ = writeSSEEvent(w, "closed", "", api.SubscribeClosed{Reason: api.SubscribeClosedServerShutdown})
				flush(w)
			}
			return nil
		}
	}
}
