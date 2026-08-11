package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// EmailMailbox resolves (creating on first use) the deterministic
// mailbox object id for an address. Reads go through /query on that
// object with dataset=email_messages.
func (c *Client) EmailMailbox(ctx context.Context, spaceId, address string) (*api.EmailMailboxResponse, error) {
	var out api.EmailMailboxResponse
	path := fmt.Sprintf("/v1/spaces/%s/email/mailbox?address=%s",
		url.PathEscape(spaceId), url.QueryEscape(address))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EmailIngest upserts one batch of messages (≤ 256) onto the mailbox
// object's email_messages dataset — one ModifyBatch, idempotent by
// provider message id.
func (c *Client) EmailIngest(ctx context.Context, spaceId, objectId string, req api.EmailIngestRequest) (*api.EmailIngestResult, error) {
	var out api.EmailIngestResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/email/messages",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EmailPatch sets a message's mutable fields (labelIds, historyId).
// 403 if the caller is not the ingesting account.
func (c *Client) EmailPatch(ctx context.Context, spaceId, objectId, msgId string, req api.EmailPatchRequest) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/email/messages/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(msgId))
	if err := c.do(ctx, http.MethodPatch, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EmailDelete tombstones a message (provider-side delete/expunge).
// 403 if the caller is not the ingesting account.
func (c *Client) EmailDelete(ctx context.Context, spaceId, objectId, msgId string) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/email/messages/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(msgId))
	if err := c.do(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
