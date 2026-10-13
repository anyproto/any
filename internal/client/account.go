package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

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

// AccountSetIcon uploads r as the account avatar — the raw request body
// IS the image, streamed without buffering. PUT /v1/account/icon.
func (c *Client) AccountSetIcon(ctx context.Context, r io.Reader, name, mimeType string) (*api.AccountMetadata, error) {
	path := "/v1/account/icon"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	req, err := c.newRequest(ctx, http.MethodPut, path, r)
	if err != nil {
		return nil, err
	}
	if mimeType != "" {
		req.Header.Set("Content-Type", mimeType)
	}
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return nil, &TransportError{Addr: c.base, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, parseServerError(resp)
	}
	var out api.AccountMetadata
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode set-icon reply: %w", err)
	}
	return &out, nil
}

// AccountIcon opens GET /v1/account/icon — the avatar's bytes as a raw
// HTTP body. The caller streams and closes Body.
func (c *Client) AccountIcon(ctx context.Context) (*FileDownloadResult, error) {
	return c.openDownload(ctx, "/v1/account/icon")
}

// AccountDeleteIcon removes the avatar. DELETE /v1/account/icon.
func (c *Client) AccountDeleteIcon(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/v1/account/icon", nil, nil)
}
