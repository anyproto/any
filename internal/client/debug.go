package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// DebugSpace fetches the per-space diagnostic snapshot — outbound
// headsync counters since boot. In-memory on the server; resets on
// restart.
func (c *Client) DebugSpace(ctx context.Context, spaceId string) (*api.SpaceDebugResponse, error) {
	var out api.SpaceDebugResponse
	path := fmt.Sprintf("/v1/spaces/%s/debug", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DebugObject fetches the per-object diagnostic snapshot. Triggers a
// full tree walk on the server (and a cold-restore on first touch);
// not a hot-poll endpoint.
func (c *Client) DebugObject(ctx context.Context, spaceId, objectId string) (*api.ObjectDebugResponse, error) {
	var out api.ObjectDebugResponse
	path := fmt.Sprintf("/v1/spaces/%s/debug/objects/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
