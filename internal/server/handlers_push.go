package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// pushDisabled is the shared 409 for "no push service on this server"
// — deps.push is nil when config.Push isn't active (indexer's
// index.disabled pattern).
func pushDisabled(c echo.Context) error {
	return writeError(c, http.StatusConflict, "push.disabled",
		"push notifications are not configured on this server (push.peerId / push.addrs)", nil)
}

// pushError maps push-service errors to the canonical envelope. The
// SDK's ErrPushNotConfigured collapses onto the same 409 as a nil
// service — from the caller's view both mean "this server does no
// push".
func pushError(c echo.Context, err error) error {
	if errors.Is(err, space.ErrPushNotConfigured) {
		return pushDisabled(c)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
	}
	return writeError(c, http.StatusInternalServerError, "internal", err.Error(), nil)
}

// pushTokenSet handles POST /v1/push/token — persist this device's
// mobile push token and register it with the push node. The persist
// is durable; a transient forward failure is retried in the
// background, so a slow push node never fails the call.
//
//	@Summary	Register this device's push token
//	@Tags		push
//	@Accept		json
//	@Param		body	body	api.PushTokenSetRequest	true	"platform (ios|android) + token"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/push/token [post]
func (d *deps) pushTokenSet(c echo.Context) error {
	// Body validation before the availability gate so 400s stay 400s
	// regardless of server config.
	var req api.PushTokenSetRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.Platform == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "platform required", nil)
	}
	switch req.Platform {
	case api.PushPlatformIOS, api.PushPlatformAndroid:
	default:
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"platform must be ios or android", map[string]any{"platform": req.Platform})
	}
	if req.Token == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "token required", nil)
	}
	if d.push == nil {
		return pushDisabled(c)
	}
	if err := d.push.SetToken(c.Request().Context(), space.PushPlatform(req.Platform), req.Token); err != nil {
		return pushError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// pushTokenRevoke handles DELETE /v1/push/token — drop this device's
// token from the push node and delete the persisted file. The local
// delete wins even when the push node is unreachable (best-effort
// forward).
//
//	@Summary	Revoke this device's push token
//	@Tags		push
//	@Success	204
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/push/token [delete]
func (d *deps) pushTokenRevoke(c echo.Context) error {
	if d.push == nil {
		return pushDisabled(c)
	}
	if err := d.push.RevokeToken(c.Request().Context()); err != nil {
		return pushError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// pushTokenStatus handles GET /v1/push/token — the LOCAL registration
// state (persisted token present? which platform?). No push-node
// round trip.
//
//	@Summary	This device's push-token status
//	@Tags		push
//	@Produce	json
//	@Success	200	{object}	api.PushTokenStatus
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Router		/push/token [get]
func (d *deps) pushTokenStatus(c echo.Context) error {
	if d.push == nil {
		return pushDisabled(c)
	}
	registered, platform := d.push.TokenStatus()
	return c.JSON(http.StatusOK, api.PushTokenStatus{Registered: registered, Platform: platform})
}

// pushSubscriptions handles GET /v1/push/subscriptions — the
// account's topic set as the push server holds it (raw
// {spaceKey, topic} rows, unsigned; spaceKey is the base58 space push
// public key, not a spaceId).
//
//	@Summary	List the account's push topic subscriptions
//	@Tags		push
//	@Produce	json
//	@Success	200	{object}	api.PushSubscriptionsResponse
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/push/subscriptions [get]
func (d *deps) pushSubscriptions(c echo.Context) error {
	if d.push == nil {
		return pushDisabled(c)
	}
	subs, err := d.push.Subscriptions(c.Request().Context())
	if err != nil {
		return pushError(c, err)
	}
	out := make([]api.PushSubscription, 0, len(subs))
	for _, s := range subs {
		out = append(out, api.PushSubscription{SpaceKey: s.SpaceKey, Topic: s.Topic})
	}
	return c.JSON(http.StatusOK, api.PushSubscriptionsResponse{Subscriptions: out})
}
