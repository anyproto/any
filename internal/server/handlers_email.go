package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/email"
)

// emailMailboxGet handles GET /v1/spaces/:spaceId/email/mailbox.
// Derive-on-read (the agent-brain pattern): resolves — creating on
// first use — the deterministic mailbox object for the address, with
// the email type attached so ingest writes are admitted immediately.
// Reads go through /query on the returned object with
// dataset=email_messages.
//
//	@Summary	Resolve the per-address mailbox object id
//	@Tags		email
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		address	query		string	true	"Mailbox address"
//	@Success	200		{object}	api.EmailMailboxResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/email/mailbox [get]
func (d *deps) emailMailboxGet(c echo.Context) error {
	address := email.NormalizeAddress(c.QueryParam("address"))
	if address == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "address query parameter required", nil)
	}
	if len(address) > email.MaxAddrBytes {
		return writeError(c, http.StatusBadRequest, api.ErrEmailAddrInvalid, "address too long",
			map[string]any{"max_bytes": email.MaxAddrBytes})
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId, err := email.DeriveMailboxObjectId(c.Request().Context(), sp, address)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusOK, api.EmailMailboxResponse{ObjectId: objectId})
}

// emailIngest handles POST /v1/spaces/:spaceId/objects/:objectId/email/messages.
// One sync page in, ONE ModifyBatch out: new ids are created, existing
// ids get their mutable fields (labelIds / historyId) patched when
// changed, identical ids are skipped. Message content is validated
// here for the clean 400 (id shape, required fields, batch size); the
// dataset handler re-validates at apply time.
//
//	@Summary	Ingest a batch of email messages (upsert by provider id)
//	@Tags		email
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Mailbox object ID"
//	@Param		body		body		api.EmailIngestRequest	true	"Messages"
//	@Success	200			{object}	api.EmailIngestResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/email/messages [post]
func (d *deps) emailIngest(c echo.Context) error {
	req, ok := bindBodyStrict[api.EmailIngestRequest](c, "")
	if !ok {
		return nil
	}
	if len(req.Messages) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "messages required", nil)
	}
	if len(req.Messages) > email.MaxIngestBatch {
		return writeError(c, http.StatusBadRequest, api.ErrEmailBatchTooLarge, "too many messages in one batch",
			map[string]any{"max": email.MaxIngestBatch, "got": len(req.Messages)})
	}
	seen := make(map[string]struct{}, len(req.Messages))
	for i := range req.Messages {
		msg := &req.Messages[i]
		if !email.ValidMessageId(msg.Id) {
			return writeError(c, http.StatusBadRequest, api.ErrEmailInvalid,
				"invalid message id (must match [A-Za-z0-9_-], ≤ 64 bytes)",
				map[string]any{"index": i})
		}
		if _, dup := seen[msg.Id]; dup {
			return writeError(c, http.StatusBadRequest, api.ErrEmailInvalid,
				"duplicate message id in batch", map[string]any{"id": msg.Id})
		}
		seen[msg.Id] = struct{}{}
		if msg.ThreadId == "" {
			return writeError(c, http.StatusBadRequest, api.ErrEmailInvalid,
				"threadId required", map[string]any{"id": msg.Id})
		}
		if msg.InternalDate <= 0 {
			return writeError(c, http.StatusBadRequest, api.ErrEmailInvalid,
				"internalDate required (unix ms, > 0)", map[string]any{"id": msg.Id})
		}
	}

	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	res, err := email.Ingest(c.Request().Context(), sp, objectId, req.Messages)
	if err != nil {
		return emailOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, res)
}

// emailPatch handles PATCH /v1/spaces/:spaceId/objects/:objectId/email/messages/:msgId
// — the incremental label-sync path. Only the mutable allow-list
// (labelIds, historyId) is patchable.
//
//	@Summary	Patch a message's mutable fields (labelIds, historyId)
//	@Tags		email
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Mailbox object ID"
//	@Param		msgId		path		string					true	"Message ID"
//	@Param		body		body		api.EmailPatchRequest	true	"Fields to set"
//	@Success	200			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	403			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/email/messages/{msgId} [patch]
func (d *deps) emailPatch(c echo.Context) error {
	req, ok := bindBodyStrict[api.EmailPatchRequest](c, "")
	if !ok {
		return nil
	}
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	msgId := c.Param("msgId")
	res, err := email.Patch(c.Request().Context(), sp, objectId, msgId, d.account, *req)
	if err != nil {
		return emailOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// emailDelete handles DELETE /v1/spaces/:spaceId/objects/:objectId/email/messages/:msgId
// — provider-side delete/expunge mirrored into the dataset.
//
//	@Summary	Delete a message (ingesting account only)
//	@Tags		email
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Mailbox object ID"
//	@Param		msgId		path		string	true	"Message ID"
//	@Success	200			{object}	api.ModifyResult
//	@Failure	403			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/email/messages/{msgId} [delete]
func (d *deps) emailDelete(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	msgId := c.Param("msgId")
	res, err := email.Delete(c.Request().Context(), sp, objectId, msgId, d.account)
	if err != nil {
		return emailOpError(c, err, sp.Id(), objectId)
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// emailOpError maps email-package errors to the canonical envelope;
// unknown errors fall through to the shared sdkOpError.
func emailOpError(c echo.Context, err error, spaceId, objectId string) error {
	details := map[string]any{"spaceId": spaceId, "objectId": objectId}
	switch {
	case errors.Is(err, email.ErrNotFound):
		return writeError(c, http.StatusNotFound, api.ErrEmailNotFound, "message not found", details)
	case errors.Is(err, email.ErrNotAuthor):
		return writeError(c, http.StatusForbidden, api.ErrEmailNotAuthor,
			"only the ingesting account can perform this action", details)
	case errors.Is(err, email.ErrNoFields):
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one mutable field required (labelIds, historyId)", nil)
	}
	return sdkOpError(c, err, details)
}
