package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// SyncStatusSpace fetches the per-space rolled-up sync state. Cheap;
// safe to poll on render ticks. Use StreamSyncStatusAccount for a
// push stream covering every known space.
func (c *Client) SyncStatusSpace(ctx context.Context, spaceId string) (*api.SpaceSyncStatusResponse, error) {
	var out api.SpaceSyncStatusResponse
	path := fmt.Sprintf("/v1/spaces/%s/sync-status", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SyncStatusObject fetches the per-object sync state. Unknown ids
// return State = "unknown" — the SDK is forgiving here; callers that
// need existence checks should use the object catalog endpoints.
func (c *Client) SyncStatusObject(ctx context.Context, spaceId, objectId string) (*api.ObjectSyncStatusResponse, error) {
	var out api.ObjectSyncStatusResponse
	path := fmt.Sprintf("/v1/spaces/%s/sync-status/objects/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamSyncStatusAccount opens GET /v1/sync-status/subscribe — one
// SSE stream covering every known space's rollup transitions.
// Blocks until the server sends `closed`, the body returns EOF, or
// ctx is canceled.
func (c *Client) StreamSyncStatusAccount(ctx context.Context, fn func(SSEFrame) error) error {
	return c.streamSSE(ctx, http.MethodGet, "/v1/sync-status/subscribe", nil, fn)
}

// StreamSyncStatusObject opens
// GET /v1/spaces/:spaceId/sync-status/objects/:objectId/subscribe.
// Per-object state transitions for the given (spaceId, objectId).
func (c *Client) StreamSyncStatusObject(ctx context.Context, spaceId, objectId string, fn func(SSEFrame) error) error {
	path := fmt.Sprintf("/v1/spaces/%s/sync-status/objects/%s/subscribe",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	return c.streamSSE(ctx, http.MethodGet, path, nil, fn)
}
