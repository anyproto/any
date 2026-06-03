package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// ChatSend posts a new message to the chat object's message stream.
// Body shape mirrors api.ChatSendRequest. Returns the modify result —
// recordIds[0] is the server-derived message id; read the record back
// via /query with dataset=chat_messages.
func (c *Client) ChatSend(ctx context.Context, spaceId, objectId string, req api.ChatSendRequest) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/chat/messages",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChatEdit replaces the text of an existing message. Server-side
// returns 403 if the caller is not the original author. Returns the
// modify result (versionId of the edit change).
func (c *Client) ChatEdit(ctx context.Context, spaceId, objectId, msgId string, req api.ChatEditRequest) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/chat/messages/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(msgId))
	if err := c.do(ctx, http.MethodPatch, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChatDelete tombstones a message. 403 if not the original author.
// Returns the modify result (versionId of the tombstone change).
func (c *Client) ChatDelete(ctx context.Context, spaceId, objectId, msgId string) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/chat/messages/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(msgId))
	if err := c.do(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChatReact toggles the caller's emoji reaction on a message. Returns
// the modify result (versionId of the toggle change); read the updated
// reactions back via /query with dataset=chat_messages.
func (c *Client) ChatReact(ctx context.Context, spaceId, objectId, msgId, emoji string) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/chat/messages/%s/reactions/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(msgId), url.PathEscape(emoji))
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
