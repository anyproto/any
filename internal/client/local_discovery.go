package client

import (
	"context"
	"net/http"

	"github.com/anyproto/any/internal/api"
)

// LocalDiscovery reads the local-discovery (mDNS) switch. Works before
// auth: with no engine up it is what the next boot starts with.
func (c *Client) LocalDiscovery(ctx context.Context) (*api.LocalDiscoveryResponse, error) {
	var out api.LocalDiscoveryResponse
	if err := c.do(ctx, http.MethodGet, "/v1/local-discovery", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetLocalDiscovery switches mDNS announce and browse on or off and
// returns the resulting state. A managed server needs the control
// token (WithControlToken).
func (c *Client) SetLocalDiscovery(ctx context.Context, enabled bool) (*api.LocalDiscoveryResponse, error) {
	var out api.LocalDiscoveryResponse
	req := api.LocalDiscoveryRequest{Enabled: &enabled}
	if err := c.do(ctx, http.MethodPut, "/v1/local-discovery", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
