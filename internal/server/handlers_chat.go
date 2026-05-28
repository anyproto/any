package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/chat"
)

// chatSend handles POST /v1/spaces/:spaceId/objects/:objectId/chat/messages.
//
//	@Summary	Send a chat message
//	@Tags		chat
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string				true	"Space ID"
//	@Param		objectId	path		string				true	"Chat object ID"
//	@Param		body		body		api.ChatSendRequest	true	"Message body"
//	@Success	201			{object}	api.ChatMessage
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/chat/messages [post]
func (d *deps) chatSend(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	var req api.ChatSendRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.Text == "" {
		return writeError(c, http.StatusBadRequest, api.ErrChatTextRequired, "text required", nil)
	}
	if len(req.Text) > chat.MaxTextBytes {
		return writeError(c, http.StatusBadRequest, api.ErrChatTextTooLong, "text too long",
			map[string]any{"max_bytes": chat.MaxTextBytes, "got_bytes": len(req.Text)})
	}
	if req.ReplyToMessageId != "" && len(req.ReplyToMessageId) > chat.MaxReplyIdBytes {
		return writeError(c, http.StatusBadRequest, api.ErrChatReplyIdInvalid,
			"replyToMessageId too long", nil)
	}
	if req.FromAgent != "" && len(req.FromAgent) > chat.MaxFromAgentBytes {
		return writeError(c, http.StatusBadRequest, api.ErrChatFromAgentInvalid,
			"fromAgent too long",
			map[string]any{"max_bytes": chat.MaxFromAgentBytes, "got_bytes": len(req.FromAgent)})
	}

	msg, err := chat.Send(c.Request().Context(), sp, objectId, chat.SendOpts{
		Text:             req.Text,
		ReplyToMessageId: req.ReplyToMessageId,
		FromAgent:        req.FromAgent,
	})
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusCreated, msg)
}

// chatEdit handles PATCH .../chat/messages/:msgId.
//
//	@Summary	Edit a chat message (author only)
//	@Tags		chat
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string				true	"Space ID"
//	@Param		objectId	path		string				true	"Chat object ID"
//	@Param		msgId		path		string				true	"Message ID"
//	@Param		body		body		api.ChatEditRequest	true	"New text"
//	@Success	200			{object}	api.ChatMessage
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	403			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/chat/messages/{msgId} [patch]
func (d *deps) chatEdit(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	msgId := c.Param("msgId")
	if objectId == "" || msgId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId and msgId required", nil)
	}

	var req api.ChatEditRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.Text == "" {
		return writeError(c, http.StatusBadRequest, api.ErrChatTextRequired, "text required", nil)
	}
	if len(req.Text) > chat.MaxTextBytes {
		return writeError(c, http.StatusBadRequest, api.ErrChatTextTooLong, "text too long",
			map[string]any{"max_bytes": chat.MaxTextBytes, "got_bytes": len(req.Text)})
	}

	msg, err := chat.Edit(c.Request().Context(), sp, objectId, msgId, d.account, req.Text)
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, msg)
}

// chatDelete handles DELETE .../chat/messages/:msgId.
//
//	@Summary	Delete a chat message (author only)
//	@Tags		chat
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Chat object ID"
//	@Param		msgId		path	string	true	"Message ID"
//	@Success	204
//	@Failure	403	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/chat/messages/{msgId} [delete]
func (d *deps) chatDelete(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	msgId := c.Param("msgId")
	if objectId == "" || msgId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId and msgId required", nil)
	}

	if err := chat.Delete(c.Request().Context(), sp, objectId, msgId, d.account); err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.NoContent(http.StatusNoContent)
}

// chatReact handles POST .../chat/messages/:msgId/reactions/:emoji.
//
//	@Summary	Toggle a reaction on a message
//	@Tags		chat
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Chat object ID"
//	@Param		msgId		path		string	true	"Message ID"
//	@Param		emoji		path		string	true	"Emoji (≤64 bytes)"
//	@Success	200			{object}	api.ChatReactionsResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/chat/messages/{msgId}/reactions/{emoji} [post]
func (d *deps) chatReact(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	msgId := c.Param("msgId")
	emoji := c.Param("emoji")
	if objectId == "" || msgId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId and msgId required", nil)
	}
	if emoji == "" || len(emoji) > chat.MaxEmojiBytes {
		return writeError(c, http.StatusBadRequest, api.ErrChatEmojiInvalid,
			"emoji must be non-empty and ≤ 64 bytes", map[string]any{"len": len(emoji)})
	}

	msg, err := chat.ToggleReaction(c.Request().Context(), sp, objectId, msgId, d.account, emoji)
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, api.ChatReactionsResponse{Reactions: msg.Reactions})
}

// chatOpError maps chat-package errors to the canonical envelope.
// Conservative: known sentinels get specific 4xx codes; everything
// else falls through to the shared sdkOpError (5xx internal).
func chatOpError(c echo.Context, err error, spaceId, objectId string) error {
	switch {
	case errors.Is(err, chat.ErrNotFound):
		return writeError(c, http.StatusNotFound, api.ErrChatNotFound,
			"message not found",
			map[string]any{"spaceId": spaceId, "objectId": objectId})
	case errors.Is(err, chat.ErrNotAuthor):
		return writeError(c, http.StatusForbidden, api.ErrChatNotAuthor,
			"only the message author can perform this action",
			map[string]any{"spaceId": spaceId, "objectId": objectId})
	}
	return sdkOpError(c, err, map[string]any{"spaceId": spaceId, "objectId": objectId})
}
