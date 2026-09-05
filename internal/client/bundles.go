package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// bundlePath builds a per-bundle path; bundle ids carry a slash, so the
// segment is percent-encoded.
func bundlePath(spaceId, bundleId, suffix string) string {
	return fmt.Sprintf("/v1/spaces/%s/bundles/%s%s", url.PathEscape(spaceId), url.PathEscape(bundleId), suffix)
}

// BundleEnsure installs or adopts a bundle in a space.
func (c *Client) BundleEnsure(ctx context.Context, spaceId string, req api.BundleEnsureRequest) (*api.BundleEnsureResponse, error) {
	var out api.BundleEnsureResponse
	path := fmt.Sprintf("/v1/spaces/%s/bundles", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BundleList lists the space's bundles registry.
func (c *Client) BundleList(ctx context.Context, spaceId string) (*api.BundleListResponse, error) {
	var out api.BundleListResponse
	path := fmt.Sprintf("/v1/spaces/%s/bundles", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BundleGet reads one bundle row.
func (c *Client) BundleGet(ctx context.Context, spaceId, bundleId string) (*api.BundleGetResponse, error) {
	var out api.BundleGetResponse
	if err := c.do(ctx, http.MethodGet, bundlePath(spaceId, bundleId, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BundleResolve deletes a losing root after the caller merged it.
// Returns nil on success (204).
func (c *Client) BundleResolve(ctx context.Context, spaceId, bundleId, loserRootId string) error {
	return c.do(ctx, http.MethodPost, bundlePath(spaceId, bundleId, "/resolve"),
		api.BundleResolveRequest{LoserRootId: loserRootId}, nil)
}
