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

// AccountRedeemAccessCode sends an invite code to the invite service on
// the account's behalf. POST /v1/account/access-code.
func (c *Client) AccountRedeemAccessCode(ctx context.Context, code string) (api.AccessCodeResponse, error) {
	var out api.AccessCodeResponse
	err := c.do(ctx, http.MethodPost, "/v1/account/access-code", api.AccessCodeRequest{Code: code}, &out)
	return out, err
}
