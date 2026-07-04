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

// SpaceDelete deletes a space (DELETE /v1/spaces/:id → Service.Delete).
// Offline-first: the server writes the synced `deleted` tombstone and
// offloads local state immediately, while the signed coordinator delete
// is driven in the background. The tech-space row stays in
// `Service.List` with `status:"deleted"` (sticky tombstone). Returns 204
// on success.
func (c *Client) SpaceDelete(ctx context.Context, spaceId string) error {
	path := fmt.Sprintf("/v1/spaces/%s", url.PathEscape(spaceId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
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

// SpaceList fetches the account's spaces (GET /v1/spaces → Service.List,
// mapped to []SpaceInfo). An empty status keeps the server default
// (active-only); pass "all" for every status or a specific status string
// (e.g. "one_to_one_pending") to filter.
func (c *Client) SpaceList(ctx context.Context, status string) (*api.SpaceListResponse, error) {
	var out api.SpaceListResponse
	path := "/v1/spaces"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceOneToOne opens (initiates or explicitly accepts) a 1-1 (direct)
// space with the given account identity — POST /v1/spaces/one-to-one →
// Service.OneToOne. Activates locally; returns the space info.
func (c *Client) SpaceOneToOne(ctx context.Context, req api.SpaceOneToOneRequest) (*api.SpaceInfo, error) {
	var out api.SpaceInfo
	if err := c.do(ctx, http.MethodPost, "/v1/spaces/one-to-one", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceOneToOneAccept approves an incoming pending 1-1 by space id — POST
// /v1/spaces/:id/one-to-one/accept → Service.AcceptOneToOne.
func (c *Client) SpaceOneToOneAccept(ctx context.Context, spaceId string) (*api.SpaceInfo, error) {
	var out api.SpaceInfo
	path := fmt.Sprintf("/v1/spaces/%s/one-to-one/accept", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceOneToOneDecline rejects an incoming pending 1-1 by space id — POST
// /v1/spaces/:id/one-to-one/decline → Service.DeclineOneToOne. Returns 204.
func (c *Client) SpaceOneToOneDecline(ctx context.Context, spaceId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/one-to-one/decline", url.PathEscape(spaceId))
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

// SpaceRegisterIncoming records an out-of-band incoming 1-1 request as a
// pending row — POST /v1/spaces/one-to-one/register-incoming →
// Service.RegisterIncoming. Returns 204.
func (c *Client) SpaceRegisterIncoming(ctx context.Context, req api.SpaceRegisterIncomingRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/spaces/one-to-one/register-incoming", req, nil)
}

// SpaceInviteAccept approves a direct-add invite by space id — POST
// /v1/spaces/:id/invite/accept → Service.AcceptInvite. 200 returns the
// loaded space; 202 means accepted with the load continuing in the
// background (the returned info carries the current status).
func (c *Client) SpaceInviteAccept(ctx context.Context, spaceId string) (*api.SpaceInfo, error) {
	var out api.SpaceInfo
	path := fmt.Sprintf("/v1/spaces/%s/invite/accept", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SpaceInviteDecline rejects a direct-add invite by space id — POST
// /v1/spaces/:id/invite/decline → Service.DeclineInvite. Returns 204.
func (c *Client) SpaceInviteDecline(ctx context.Context, spaceId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/invite/decline", url.PathEscape(spaceId))
	return c.do(ctx, http.MethodPost, path, nil, nil)
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
