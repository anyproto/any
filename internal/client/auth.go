package client

import (
	"context"
	"net/http"

	"github.com/anyproto/any/internal/api"
)

// AuthStatus reports the server's authorization state and the locally
// available accounts. GET /v1/auth.
func (c *Client) AuthStatus(ctx context.Context) (*api.AuthStatusResponse, error) {
	var out api.AuthStatusResponse
	if err := c.do(ctx, http.MethodGet, "/v1/auth", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Authorize creates, restores or selects an account on a server that
// started unauthorized. POST /v1/auth.
func (c *Client) Authorize(ctx context.Context, req api.AuthRequest) (*api.AuthResponse, error) {
	var out api.AuthResponse
	if err := c.do(ctx, http.MethodPost, "/v1/auth", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
