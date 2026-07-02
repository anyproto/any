package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// IdentitiesList fetches the account-global identities directory —
// every account identity this account has encountered (across spaces,
// 1-1s, inbox invites), with the last resolved profile and the spaces
// where each was seen. GET /v1/identities.
func (c *Client) IdentitiesList(ctx context.Context) (*api.IdentitiesListResponse, error) {
	var out api.IdentitiesListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/identities", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IdentityGet fetches a single directory entry by account address.
// Returns a 404 (identity.not_found) when never encountered.
// GET /v1/identities/:identity.
func (c *Client) IdentityGet(ctx context.Context, identity string) (*api.IdentityInfo, error) {
	var out api.IdentityInfo
	path := "/v1/identities/" + url.PathEscape(identity)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamIdentities opens GET /v1/identities/subscribe — one SSE stream
// of directory changes (added / updated / removed batches). Blocks until
// the server sends `closed`, the body returns EOF, or ctx is canceled.
func (c *Client) StreamIdentities(ctx context.Context, fn func(SSEFrame) error) error {
	return c.streamSSE(ctx, http.MethodGet, "/v1/identities/subscribe", nil, fn)
}
