package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// SpaceUpdate patches space metadata (name / description / iconCid).
// Pointer-to-string semantics — only non-nil fields are touched.
// Returns 204 on success; the spaceIndex mirror to the local
// tech-space row catches up asynchronously, so a follow-up GET may
// briefly show stale values.
func (c *Client) SpaceUpdate(ctx context.Context, spaceId string, req api.SpaceUpdateRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s", url.PathEscape(spaceId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// SpaceGet fetches one space's wire-shape info (including
// spaceIndexObjectId).
func (c *Client) SpaceGet(ctx context.Context, spaceId string) (*api.SpaceInfo, error) {
	var out api.SpaceInfo
	path := fmt.Sprintf("/v1/spaces/%s", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
