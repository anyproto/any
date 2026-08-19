package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync/commonspace/pubsub"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// eventDataLimit caps the marshaled `data` payload of one event.
// Matches the SDK pub/sub per-message cap so a device-scope producer
// doesn't break when it switches to a network scope. (The network cap
// applies to the whole envelope, so the effective data budget there is
// slightly smaller — the SDK's own rejection maps to the same code.)
const eventDataLimit = 64 * 1024

// Grammar of the event vocabulary. Type segments are lowercase slugs
// joined by dots; the charset keeps the topic mapping (dots → `/`
// segments, target appended as one segment) collision-free. Length is
// bounded per field here; the combined pub/sub topic budget (256
// bytes / 16 segments) is enforced on the rendered topic at publish.
var (
	eventTypeRe   = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)
	eventFilterRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*(\.\*)?$`)
	eventTargetRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

const eventTypeMaxLen = 128

// eventsPublish handles POST /v1/events. It validates the envelope,
// stamps the sender, and routes by scope: device fans out to local
// subscribers over the in-memory hub; account and space publish on the
// SDK pub/sub (tech space / target space) and reach local subscribers
// via its Self loopback through the bridge. Fire-and-forget: the
// response reports how many local subscribers matched (0 = nobody
// listening) but a valid publish always succeeds. See docs/21-events.md.
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
	if !validateEventScope(c, req.Scope, req.SpaceId) {
		return nil
	}
	if req.Target != "" && !validTargetToken(c, "target", req.Target) {
		return nil
	}
	if len(req.Data) > eventDataLimit {
		return writeError(c, http.StatusBadRequest, "events.payload_too_large",
			"data exceeds the 64 KiB event payload cap", nil)
	}
	// Enforce the combined topic budget on every scope — device included —
	// so a producer doesn't break when it switches to a network scope.
	if err := pubsub.ValidateTopic(eventTopic(req.Type, req.Target, d.sdk.Account().Id())); err != nil {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"type + target exceed the pub/sub topic budget (256 bytes / 16 segments)", nil)
	}

	return d.publishScoped(c, api.Event{
		Type:    req.Type,
		Scope:   req.Scope,
		SpaceId: req.SpaceId,
		Target:  req.Target,
		Data:    req.Data,
		Sender:  &api.EventSender{Identity: d.sdk.Account().Id(), Self: true},
	})
}

// validateEventScope enforces the scope/spaceId pairing shared by
// event publish and process register: scope is one of the closed set,
// spaceId present iff scope is space. Writes the 400 and returns
// false on violation.
func validateEventScope(c echo.Context, scope, spaceId string) bool {
	switch scope {
	case api.EventScopeDevice, api.EventScopeAccount, api.EventScopeSpace:
	case "":
		_ = writeError(c, http.StatusBadRequest, "request.missing_field",
			"scope required (device, account or space)", nil)
		return false
	default:
		_ = writeError(c, http.StatusBadRequest, "request.invalid_field",
			"scope must be device, account or space", nil)
		return false
	}
	if scope == api.EventScopeSpace && spaceId == "" {
		_ = writeError(c, http.StatusBadRequest, "request.missing_field",
			"spaceId required for scope space", nil)
		return false
	}
	if scope != api.EventScopeSpace && spaceId != "" {
		_ = writeError(c, http.StatusBadRequest, "request.invalid_field",
			"spaceId only valid with scope space", nil)
		return false
	}
	return true
}

// validTargetToken checks one field against the event-target grammar
// (topic-segment charset), writing the 400 on violation.
func validTargetToken(c echo.Context, name, v string) bool {
	if eventTargetRe.MatchString(v) {
		return true
	}
	_ = writeError(c, http.StatusBadRequest, "request.invalid_field",
		name+" must be 1-128 chars of [A-Za-z0-9._-]", nil)
	return false
}

// publishScoped routes one stamped event by scope: device fans out on
// the in-memory hub, account/space publish on the SDK pub/sub. The
// single dispatch point for everything the bus emits (events publish
// and the process helper) — bus-level policy belongs here, not in one
// caller's fork of this switch.
func (d *deps) publishScoped(c echo.Context, ev api.Event) error {
	switch ev.Scope {
	case api.EventScopeDevice:
		n := d.eventsHub().publish(ev)
		return c.JSON(http.StatusOK, api.EventPublishResponse{Subscribers: n})
	case api.EventScopeAccount:
		return d.publishNetworkEvent(c, ev, d.sdk.PubSub())
	default: // space
		sp, err := d.sdk.Spaces().Get(c.Request().Context(), ev.SpaceId)
		if err != nil {
			return spaceError(c, err, ev.SpaceId)
		}
		return d.publishNetworkEvent(c, ev, sp.PubSub())
	}
}

// publishNetworkEvent sends ev over the SDK pub/sub. Local delivery is
// NOT fanned out here — the SDK delivers the publish synchronously to
// matching local subscriptions (Self loopback), which the bridge feeds
// into the hub. The reply's subscribers count is the hub's current
// reachable-match count (matchCount excludes subscribers holding no
// interest on the event's space) — approximate by design
// (fire-and-forget).
func (d *deps) publishNetworkEvent(c echo.Context, ev api.Event, ps space.PubSubAPI) error {
	n, err := d.networkPublish(c.Request().Context(), ev, ps)
	if err != nil {
		return pubsubError(c, err)
	}
	// The Self loopback re-enters the hub (and its taps) only when a
	// local interest covers the topic — feed the process view directly
	// so a confirmed publish always lands in it, whichever endpoint
	// emitted it. apply ignores non-process types; the tap-side upsert
	// is idempotent, so a loopback double-apply is harmless.
	d.processes().apply(&ev)
	return c.JSON(http.StatusOK, api.EventPublishResponse{Subscribers: n})
}

// networkPublish is the transport core of publishNetworkEvent —
// marshal, topic render, pub/sub send — returning the hub's current
// reachable-match count.
func (d *deps) networkPublish(ctx context.Context, ev api.Event, ps space.PubSubAPI) (int, error) {
	payload, err := json.Marshal(wireEvent{Type: ev.Type, Target: ev.Target, Data: ev.Data})
	if err != nil {
		return 0, err
	}
	topic := eventTopic(ev.Type, ev.Target, d.sdk.Account().Id())
	if err := ps.Publish(ctx, topic, payload); err != nil {
		return 0, err
	}
	return d.eventsHub().matchCount(&ev), nil
}

// pubsubSentinels is the one list of SDK pub/sub sentinels this file
// maps — shared by pubsubError and errorsIsPubSub so the two can't
// drift.
var pubsubSentinels = []error{
	space.ErrPubSubPayloadTooLarge, space.ErrPubSubNoReadKey,
	space.ErrPubSubTooManyPatterns, space.ErrPubSubTopicNotOwned,
	space.ErrPubSubInvalidTopic,
}

// pubsubError maps the SDK pub/sub sentinels onto the canonical
// envelope; anything else falls through to the generic SDK-error
// mapper so shared sentinels (cancellation, ErrSpaceNotTracked, …)
// classify like every other surface.
func pubsubError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, space.ErrPubSubPayloadTooLarge):
		return writeError(c, http.StatusBadRequest, "events.payload_too_large",
			"event exceeds the 64 KiB pub/sub message cap", nil)
	case errors.Is(err, space.ErrPubSubNoReadKey):
		return writeError(c, http.StatusConflict, "events.no_read_key",
			"no read key for the space (keyless or guest access) — network events need full membership", nil)
	case errors.Is(err, space.ErrPubSubTooManyPatterns):
		return writeError(c, http.StatusConflict, "events.too_many_patterns",
			"the space's pub/sub subscription pattern budget is exhausted; narrow or share filters", nil)
	case errors.Is(err, space.ErrPubSubTopicNotOwned):
		return writeError(c, http.StatusForbidden, "events.topic_not_owned",
			"the type maps into another account's self-owned topic namespace", nil)
	case errors.Is(err, space.ErrPubSubInvalidTopic):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"type + target render an invalid pub/sub topic (256-byte / 16-segment budget)", nil)
	}
	return sdkOpError(c, err, nil)
}

// errorsIsPubSub distinguishes the pub/sub sentinels from space
// resolution errors on the subscribe path, where either can surface
// from acquire.
func errorsIsPubSub(err error) bool {
	for _, s := range pubsubSentinels {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
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
		if !validTargetToken(c, "target", t) {
			return nil
		}
	}

	// Network scopes pull through refcounted pub/sub interests. With no
	// scope filter the subscription is a catch-all: account interests
	// (unless a spaceId filter rules account events out — they carry no
	// spaceId), space interests for every explicitly listed spaceId
	// (there is no "all spaces" interest — pub/sub is per-space). An
	// explicit space-scope subscription therefore must name its spaces.
	catchAll := len(f.scopes) == 0
	hasSpaceScope := slices.Contains(f.scopes, api.EventScopeSpace)
	if hasSpaceScope && len(f.spaceIds) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"scope=space subscription requires at least one spaceId filter", nil)
	}
	// A spaceId filter admits space-scope events only (device/account
	// events carry no spaceId) — reject combinations that can never
	// match anything, mirroring the publish-side scope/spaceId check.
	if len(f.spaceIds) > 0 && !catchAll && !hasSpaceScope {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"spaceId filter matches space-scope events only — add scope=space or drop the spaceId filter", nil)
	}
	wantAccount := (catchAll || slices.Contains(f.scopes, api.EventScopeAccount)) && len(f.spaceIds) == 0
	wantSpace := catchAll || hasSpaceScope
	var netSpaceIds []string
	if wantSpace {
		netSpaceIds = f.spaceIds
	}
	if wantAccount || len(netSpaceIds) > 0 {
		release, err := d.eventsNet().acquire(c.Request().Context(), wantAccount, netSpaceIds, filterPatterns(&f))
		if err != nil {
			if len(netSpaceIds) > 0 && !errorsIsPubSub(err) {
				return spaceError(c, err, "")
			}
			return pubsubError(c, err)
		}
		defer release()
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
