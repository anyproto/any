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
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}

	req, ok := bindBody[api.ChatSendRequest](c)
	if !ok {
		return nil
	}
	// Text carries the message unless attachments do — a photo sent with
	// no caption is an ordinary message. Only a payload with neither is
	// empty, and stays rejected. (Edit below is deliberately stricter:
	// it replaces text on an existing record and can't clear it.)
	if req.Text == "" && len(req.Attachments) == 0 {
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
	if err := validateAgentRequest(req.Agent); err != nil {
		return writeError(c, http.StatusBadRequest, api.ErrChatAgentInvalid,
			err.Error(), nil)
	}
	if err := validateAttachmentsRequest(req.Attachments); err != nil {
		return writeError(c, http.StatusBadRequest, api.ErrChatAttachmentsInvalid,
			err.Error(), nil)
	}

	res, err := chat.Send(c.Request().Context(), sp, objectId, chat.SendOpts{
		Text:             req.Text,
		ReplyToMessageId: req.ReplyToMessageId,
		Agent:            req.Agent,
		Attachments:      req.Attachments,
	})
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	// Push hook (sender-scoped by construction — only this server's own
	// writes land here). Asynchronous: the read-back for the derived
	// `mentions` runs on a push-service goroutine, so the response never
	// waits on it and read failures log-and-skip inside the service.
	if d.push != nil && len(res.RecordIds) > 0 {
		d.push.NotifyChatMessage(sp, objectId, res.RecordIds[0])
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
	sp, objectId, msgId, errResp, done := d.resolveSpaceObjectMsg(c)
	if done {
		return errResp
	}

	req, ok := bindBody[api.ChatEditRequest](c)
	if !ok {
		return nil
	}
	if req.Text == "" {
		return writeError(c, http.StatusBadRequest, api.ErrChatTextRequired, "text required", nil)
	}
	if len(req.Text) > chat.MaxTextBytes {
		return writeError(c, http.StatusBadRequest, api.ErrChatTextTooLong, "text too long",
			map[string]any{"max_bytes": chat.MaxTextBytes, "got_bytes": len(req.Text)})
	}

	// Pre-edit mention snapshot for the push diff — must happen BEFORE
	// the edit lands (one cheap local read; skipped when push is off).
	// A failed snapshot skips the edit push rather than over-notifying.
	var pushBefore []string
	pushOk := false
	if d.push != nil {
		pushBefore, pushOk = d.push.ChatMentionsBefore(c.Request().Context(), sp, objectId, msgId)
	}

	res, err := chat.Edit(c.Request().Context(), sp, objectId, msgId, d.account, req.Text)
	if err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	// Push hook: only NEWLY-ADDED mentions are notified (async diff
	// against the snapshot inside the push service; never blocks).
	if pushOk {
		d.push.NotifyChatEdit(sp, objectId, msgId, pushBefore)
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
	sp, objectId, msgId, errResp, done := d.resolveSpaceObjectMsg(c)
	if done {
		return errResp
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
	sp, objectId, msgId, errResp, done := d.resolveSpaceObjectMsg(c)
	if done {
		return errResp
	}
	emoji := c.Param("emoji")
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

// validateAgentRequest runs the HTTP-layer shape checks on the agent
// group (name presence + lengths). The request struct is closed so
// unknown sub-fields can't arrive here; `done` is a plain bool with no
// shape to check. Deeper validation also runs handler-side; this
// version exists so the server returns a clean 400 with a specific
// error code instead of burning an SDK round-trip.
func validateAgentRequest(a *api.ChatAgentMeta) error {
	if a == nil {
		return nil
	}
	if a.Name == "" {
		return fmt.Errorf("agent.name required")
	}
	if len(a.Name) > chat.MaxAgentNameBytes {
		return fmt.Errorf("agent.name too long (%d > %d bytes)", len(a.Name), chat.MaxAgentNameBytes)
	}
	if len(a.DebugLink) > chat.MaxDebugLinkBytes {
		return fmt.Errorf("agent.debugLink too long (%d > %d bytes)", len(a.DebugLink), chat.MaxDebugLinkBytes)
	}
	return nil
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
		if !chat.ValidAttachmentId(id) {
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

// chatReadAll handles POST .../chat/read-all.
//
//	@Summary	Mark every message, mention, and reaction in the chat read
//	@Tags		chat
//	@Produce	json
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Chat object ID"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/chat/read-all [post]
func (d *deps) chatReadAll(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	if err := chat.ReadAll(c.Request().Context(), sp, objectId); err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	// Silent push so the account's OTHER devices refresh their badges
	// (heart hooks its read RPC the same way). Non-blocking.
	if d.push != nil {
		d.push.NotifyChatRead(sp.Id(), objectId)
	}
	return c.NoContent(http.StatusNoContent)
}

// chatRead handles POST .../chat/messages/:msgId/read — mark this
// message and everything ordered before it read.
//
//	@Summary	Mark a message and everything before it read
//	@Tags		chat
//	@Produce	json
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Chat object ID"
//	@Param		msgId		path	string	true	"Message ID (read boundary, inclusive)"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/chat/messages/{msgId}/read [post]
func (d *deps) chatRead(c echo.Context) error {
	sp, objectId, msgId, errResp, done := d.resolveSpaceObjectMsg(c)
	if done {
		return errResp
	}
	if err := chat.Read(c.Request().Context(), sp, objectId, msgId); err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	// Silent push — same own-devices badge refresh as read-all.
	if d.push != nil {
		d.push.NotifyChatRead(sp.Id(), objectId)
	}
	return c.NoContent(http.StatusNoContent)
}

// chatReadReactions handles POST .../chat/messages/:msgId/reactions-read —
// mark this message's unread reactions read. A reaction is a change
// ordered after its target message, so chatRead (which cuts at the
// message's own version) can never cover it; this route clears it. Note
// it also advances message read state: marking a change read covers its
// causal ancestry, so unread messages the reactor had already seen clear
// too — see chat.ReadReactions. Idempotent: a message with no unread
// reactions is a no-op (204).
//
//	@Summary	Mark a message's unread reactions read
//	@Tags		chat
//	@Produce	json
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Chat object ID"
//	@Param		msgId		path	string	true	"Message ID"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/chat/messages/{msgId}/reactions-read [post]
func (d *deps) chatReadReactions(c echo.Context) error {
	sp, objectId, msgId, errResp, done := d.resolveSpaceObjectMsg(c)
	if done {
		return errResp
	}
	if err := chat.ReadReactions(c.Request().Context(), sp, objectId, msgId); err != nil {
		return chatOpError(c, err, sp.Id(), objectId)
	}
	return c.NoContent(http.StatusNoContent)
}
