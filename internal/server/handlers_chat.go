package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/chat"
)

// chatSend handles POST /v1/spaces/:spaceId/objects/:objectId/messages.
// Server stamps creator (= account.Id), createdAt, modifiedAt; clients
// supply only text and (optionally) replyToMessageId.
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

	msg, err := chat.Send(c.Request().Context(), sp, objectId, chat.SendOpts{
		Text:             req.Text,
		ReplyToMessageId: req.ReplyToMessageId,
	})
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusCreated, msg)
}

// chatList handles GET /v1/spaces/:spaceId/objects/:objectId/messages.
// Pagination cursors `before` / `after` are message ids; the server
// resolves them to `_ver.id` boundaries.
func (d *deps) chatList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	opts := chat.ListOpts{
		Before: c.QueryParam("before"),
		After:  c.QueryParam("after"),
	}
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"limit must be a non-negative integer", map[string]any{"got": raw})
		}
		opts.Limit = n
	}

	msgs, err := chat.List(c.Request().Context(), sp, objectId, opts)
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	if msgs == nil {
		msgs = []api.ChatMessage{}
	}
	return c.JSON(http.StatusOK, api.ChatListResponse{Messages: msgs})
}

// chatEdit handles PATCH .../messages/:msgId. Only the original
// author can edit; the API-layer pre-check returns 403 cleanly. The
// handler enforces the same rule for peer-originated changes.
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

// chatDelete handles DELETE .../messages/:msgId. Author-only,
// enforced both here (clean 403) and in the handler (peer changes).
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

// chatReact handles POST .../messages/:msgId/reactions/:emoji. The
// emoji is read from the path; toggle semantics are implemented in
// chat.ToggleReaction (read-decide-write).
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
