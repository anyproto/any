package server

import (
	"context"
	"net/http"

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
	cmd, ok := bindBody[api.UICommand](c)
	if !ok {
		return nil
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
	n := d.uiHub().publish(*cmd)
	return c.JSON(http.StatusOK, api.UICommandPublishResponse{Subscribers: n})
}

// uiCommandSubscribe handles GET /v1/ui/commands/subscribe. It streams
// UI commands to one connected client over SSE: an initial `ready`
// frame, then one `command` frame per published command, `: keepalive`
// comments while idle, and a terminal `closed` frame on server shutdown
// (reason server_shutdown) or per-subscriber overflow (reason
// overflow). There is no snapshot — the channel is in-memory and
// at-most-once, so a subscriber only sees commands published after it
// connects. Driven by streamStatusSSE (nil dropped counter — the hub
// signals overflow by closing the channel instead of lag frames).
//
//	@Summary	Subscribe to UI commands (SSE)
//	@Tags		ui
//	@Produce	text/event-stream
//	@Success	200
//	@Router		/ui/commands/subscribe [get]
func (d *deps) uiCommandSubscribe(c echo.Context) error {
	id, ch := d.uiHub().subscribe()
	defer d.uiHub().unsubscribe(id)

	return d.streamStatusSSE(c, nil, func(ctx context.Context, emit func(string, any) error) error {
		for {
			select {
			case cmd, ok := <-ch:
				if !ok {
					// Hub dropped us: our buffer filled. Tell the client to
					// reconnect for a fresh stream.
					return emit("closed", api.SubscribeClosed{Reason: api.SubscribeClosedOverflow})
				}
				if err := emit("command", cmd); err != nil {
					return err
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}
