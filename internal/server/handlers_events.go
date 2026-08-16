package server

import (
	"context"
	"net/http"
	"regexp"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// eventDataLimit caps the marshaled `data` payload of one event.
// Matches the SDK pub/sub per-message cap so a device-scope producer
// doesn't break when it switches to a network scope. (The network cap
// applies to the whole envelope, so the effective data budget there is
// slightly smaller — the SDK's own rejection maps to the same code.)
const eventDataLimit = 64 * 1024

// Grammar of the event vocabulary. Type segments are lowercase slugs
// joined by dots; the charset keeps the SYN-152 topic mapping (dots →
// `/` segments, target appended as one segment) collision-free, and
// the length bounds fit the pub/sub topic budget (256 bytes total).
var (
	eventTypeRe   = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)
	eventFilterRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*(\.\*)?$`)
	eventTargetRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

const eventTypeMaxLen = 128

// eventsPublish handles POST /v1/events. It validates the envelope,
// stamps the sender, and routes by scope: device fans out to local
// subscribers over the in-memory hub; account and space answer 501
// until the SDK pub/sub bridge lands. Fire-and-forget: the response
// reports how many local subscribers matched (0 = nobody listening)
// but a valid publish always succeeds. See docs/21-events.md.
//
//	@Summary	Publish an event
//	@Tags		events
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.EventPublishRequest	true	"event"
//	@Success	200		{object}	api.EventPublishResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Router		/events [post]
func (d *deps) eventsPublish(c echo.Context) error {
	req, ok := bindBodyStrict[api.EventPublishRequest](c, "")
	if !ok {
		return nil
	}
	if req.Type == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "type required", nil)
	}
	if len(req.Type) > eventTypeMaxLen || !eventTypeRe.MatchString(req.Type) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"type must be dotted lowercase slugs (e.g. process.progress)", nil)
	}
	switch req.Scope {
	case api.EventScopeDevice, api.EventScopeAccount, api.EventScopeSpace:
	case "":
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"scope required (device, account or space)", nil)
	default:
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"scope must be device, account or space", nil)
	}
	if req.Scope == api.EventScopeSpace && req.SpaceId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"spaceId required for scope space", nil)
	}
	if req.Scope != api.EventScopeSpace && req.SpaceId != "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"spaceId only valid with scope space", nil)
	}
	if req.Target != "" && !eventTargetRe.MatchString(req.Target) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"target must be 1-128 chars of [A-Za-z0-9._-]", nil)
	}
	if len(req.Data) > eventDataLimit {
		return writeError(c, http.StatusBadRequest, "events.payload_too_large",
			"data exceeds the 64 KiB event payload cap", nil)
	}

	ev := api.Event{
		Type:    req.Type,
		Scope:   req.Scope,
		SpaceId: req.SpaceId,
		Target:  req.Target,
		Data:    req.Data,
		Sender:  &api.EventSender{Identity: d.sdk.Account().Id(), Self: true},
	}

	switch ev.Scope {
	case api.EventScopeDevice:
		n := d.eventsHub().publish(ev)
		return c.JSON(http.StatusOK, api.EventPublishResponse{Subscribers: n})
	default:
		// account/space ride the SDK pub/sub bridge (SYN-152).
		return notImplemented("Space.PubSub")(c)
	}
}

// eventsSubscribe handles GET /v1/events/subscribe. It streams
// matching events to one connected client over SSE: an initial `ready`
// frame, then one `event` frame per delivery, `: keepalive` comments
// while idle, and a terminal `closed` frame on server shutdown (reason
// server_shutdown) or per-subscriber overflow (reason overflow). There
// is no snapshot — the bus is in-memory and at-most-once, so a
// subscriber only sees events published after it connects.
//
// Filter params are all repeatable and AND across dimensions, OR
// within one: scope, spaceId, target match exactly; type matches
// exactly or by prefix with a trailing `.*` (`type=process.*`). No
// params = everything. Driven by streamStatusSSE (nil dropped counter —
// the hub signals overflow by closing the channel instead of lag
// frames).
//
//	@Summary	Subscribe to events (SSE)
//	@Tags		events
//	@Produce	text/event-stream
//	@Param		scope	query	string	false	"scope filter (repeatable)"
//	@Param		spaceId	query	string	false	"spaceId filter (repeatable)"
//	@Param		type	query	string	false	"type filter, exact or prefix `x.*` (repeatable)"
//	@Param		target	query	string	false	"target filter (repeatable)"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Router		/events/subscribe [get]
func (d *deps) eventsSubscribe(c echo.Context) error {
	qp := c.QueryParams()
	f := eventFilter{
		scopes:   qp["scope"],
		spaceIds: qp["spaceId"],
		types:    qp["type"],
		targets:  qp["target"],
	}
	for _, s := range f.scopes {
		switch s {
		case api.EventScopeDevice, api.EventScopeAccount, api.EventScopeSpace:
		default:
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"scope must be device, account or space", nil)
		}
	}
	for _, t := range f.types {
		if len(t) > eventTypeMaxLen || !eventFilterRe.MatchString(t) {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"type filter must be dotted lowercase slugs, optionally ending in .* (e.g. process.*)", nil)
		}
	}
	for _, t := range f.targets {
		if !eventTargetRe.MatchString(t) {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"target must be 1-128 chars of [A-Za-z0-9._-]", nil)
		}
	}

	id, ch := d.eventsHub().subscribe(f)
	defer d.eventsHub().unsubscribe(id)

	return d.streamStatusSSE(c, nil, func(ctx context.Context, emit func(string, any) error) error {
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					// Hub dropped us: our buffer filled. Tell the client to
					// reconnect for a fresh stream.
					return emit("closed", api.SubscribeClosed{Reason: api.SubscribeClosedOverflow})
				}
				if err := emit("event", ev); err != nil {
					return err
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}
