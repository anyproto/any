package server

import (
	"errors"
	"fmt"
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
//	@Success	201			{object}	api.ModifyResult
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
	if err := validateAttachmentsRequest(req.Attachments); err != nil {
		return writeError(c, http.StatusBadRequest, api.ErrChatAttachmentsInvalid,
			err.Error(), nil)
	}

	res, err := chat.Send(c.Request().Context(), sp, objectId, chat.SendOpts{
		Text:             req.Text,
		ReplyToMessageId: req.ReplyToMessageId,
		FromAgent:        req.FromAgent,
		Attachments:      req.Attachments,
	})
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusCreated, modifyResultToAPI(res))
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
//	@Success	200			{object}	api.ModifyResult
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

	res, err := chat.Edit(c.Request().Context(), sp, objectId, msgId, d.account, req.Text)
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// chatDelete handles DELETE .../chat/messages/:msgId.
//
//	@Summary	Delete a chat message (author only)
//	@Tags		chat
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Chat object ID"
//	@Param		msgId		path	string	true	"Message ID"
//	@Success	200	{object}	api.ModifyResult
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

	res, err := chat.Delete(c.Request().Context(), sp, objectId, msgId, d.account)
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
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
//	@Success	200			{object}	api.ModifyResult
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

	res, err := chat.ToggleReaction(c.Request().Context(), sp, objectId, msgId, d.account, emoji)
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// validateAttachmentsRequest runs the HTTP-layer shape checks on the
// attachments map (id alphabet, count cap, per-entry type / link
// presence + length). Deeper validation also runs handler-side; this
// version exists so the server returns a clean 400 with a specific
// error code instead of letting a malformed body burn an SDK
// round-trip and surface as the generic SDK rejection.
func validateAttachmentsRequest(atts map[string]api.ChatAttachment) error {
	if len(atts) == 0 {
		return nil
	}
	if len(atts) > chat.MaxAttachments {
		return fmt.Errorf("attachments: too many entries (max %d, got %d)", chat.MaxAttachments, len(atts))
	}
	for id, a := range atts {
		if !isValidAttachmentId(id) {
			return fmt.Errorf("attachments: invalid id %q (must match [A-Za-z0-9_-]+, ≤ %d bytes)", id, chat.MaxAttachmentIdBytes)
		}
		if a.Type == "" {
			return fmt.Errorf("attachments[%s].type required", id)
		}
		if len(a.Type) > chat.MaxAttachmentTypeBytes {
			return fmt.Errorf("attachments[%s].type too long (%d > %d bytes)", id, len(a.Type), chat.MaxAttachmentTypeBytes)
		}
		if a.Link == "" {
			return fmt.Errorf("attachments[%s].link required", id)
		}
		if len(a.Link) > chat.MaxAttachmentLinkBytes {
			return fmt.Errorf("attachments[%s].link too long (%d > %d bytes)", id, len(a.Link), chat.MaxAttachmentLinkBytes)
		}
	}
	return nil
}

func isValidAttachmentId(id string) bool {
	if len(id) == 0 || len(id) > chat.MaxAttachmentIdBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
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
