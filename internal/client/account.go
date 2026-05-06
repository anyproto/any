package client

import (
	"context"
	"net/http"

	"github.com/anyproto/any/internal/api"
)

// AccountUpdateMetadata updates the caller's account profile across
// every space they're a member of. PUT /v1/account/metadata.
func (c *Client) AccountUpdateMetadata(ctx context.Context, meta api.AccountMetadata) error {
	return c.do(ctx, http.MethodPut, "/v1/account/metadata", meta, nil)
}
