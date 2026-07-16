package client

import (
	"context"
	"net/http"

	"github.com/anyproto/any/internal/api"
)

// PushTokenSet registers this device's mobile push token (POST
// /v1/push/token). platform is "ios" or "android"; call again on token
// rotation. Returns 204 on success; 409 push.disabled when the server
// has no push node configured.
func (c *Client) PushTokenSet(ctx context.Context, req api.PushTokenSetRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/push/token", req, nil)
}

// PushTokenRevoke drops this device's token (DELETE /v1/push/token).
// The local persist is deleted even when the push node is unreachable
// (best-effort forward). Returns 204.
func (c *Client) PushTokenRevoke(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/v1/push/token", nil, nil)
}

// PushTokenStatus reads the LOCAL registration state (GET
// /v1/push/token) — whether a persisted token exists on this device
// and for which platform. No push-node round trip.
func (c *Client) PushTokenStatus(ctx context.Context) (*api.PushTokenStatus, error) {
	var out api.PushTokenStatus
	if err := c.do(ctx, http.MethodGet, "/v1/push/token", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PushSubscriptions lists the account's topic set as the push server
// holds it (GET /v1/push/subscriptions) — raw {spaceKey, topic} rows,
// unsigned; spaceKey is the base58 space push public key, not a
// spaceId.
func (c *Client) PushSubscriptions(ctx context.Context) (*api.PushSubscriptionsResponse, error) {
	var out api.PushSubscriptionsResponse
	if err := c.do(ctx, http.MethodGet, "/v1/push/subscriptions", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
