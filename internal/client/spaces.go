package client

import (
	"context"
	"encoding/json"
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

// SpaceSync forces an immediate head-sync round (POST
// /v1/spaces/:id/sync → Space.SyncHeads). Blocks until the round
// completes server-side. Use to converge on demand instead of waiting
// for the periodic headsync timer.
func (c *Client) SpaceSync(ctx context.Context, spaceId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/sync", url.PathEscape(spaceId))
	return c.do(ctx, http.MethodPost, path, nil, nil)
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

// SpaceListQuery runs a windowed snapshot over the account's space list
// (POST /v1/spaces/query → Service.Query on the `spaces` dataset). body
// carries the standard query fields (filter / sort / limit / offset /
// includeTotal) plus an optional `dataset` override.
func (c *Client) SpaceListQuery(ctx context.Context, body []byte) (*api.QueryResponse, error) {
	var out api.QueryResponse
	// json.RawMessage round-trips through do's json.Marshal unchanged, so
	// the pre-built body ships verbatim. nil body → no request payload.
	var reqBody any
	if len(body) > 0 {
		reqBody = json.RawMessage(body)
	}
	if err := c.do(ctx, http.MethodPost, "/v1/spaces/query", reqBody, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Datasets fetches the account's tech-space system dataset schemas
// (GET /v1/datasets → Service.Datasets).
func (c *Client) Datasets(ctx context.Context) (*api.DatasetsResponse, error) {
	var out api.DatasetsResponse
	if err := c.do(ctx, http.MethodGet, "/v1/datasets", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceDatasets fetches one space's dataset schemas (GET
// /v1/spaces/:id/datasets → Space.Datasets).
func (c *Client) SpaceDatasets(ctx context.Context, spaceId string) (*api.DatasetsResponse, error) {
	var out api.DatasetsResponse
	path := fmt.Sprintf("/v1/spaces/%s/datasets", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
