package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// inviteCreate handles POST /v1/spaces/:spaceId/invites.
//
//	@Summary	Create an invite
//	@Tags		invites
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	201		{object}	api.InviteCreateResponse
//	@Failure	409		{object}	api.ErrorEnvelope	"Duplicate invite"
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/invites [post]
func (d *deps) inviteCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	inv, err := sp.ACL().CreateInvite(c.Request().Context())
	if err != nil {
		if strings.Contains(err.Error(), "duplicate invites") {
			return writeError(c, http.StatusConflict, "invite.duplicate",
				"an invite already exists for this space",
				map[string]any{"spaceId": sp.Id()})
		}
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	token, err := space.EncodeInvite(inv)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.InviteCreateResponse{
		SpaceId:     inv.SpaceId,
		InviteToken: token,
	})
}

// inviteGet handles GET /v1/spaces/:spaceId/invites/:recordId.
//
//	@Summary	Get an invite by record ID
//	@Tags		invites
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		recordId	path		string	true	"Invite record ID"
//	@Success	200			{object}	api.InviteInfo
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/invites/{recordId} [get]
func (d *deps) inviteGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	recordId := c.Param("recordId")
	if recordId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "recordId required", nil)
	}
	invs, err := sp.Members().Invites(c.Request().Context())
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	for _, inv := range invs {
		if inv.RecordId == recordId {
			return c.JSON(http.StatusOK, api.InviteInfo{
				RecordId:   inv.RecordId,
				Permission: spacePermissionString(inv.Permission),
			})
		}
	}
	return writeError(c, http.StatusNotFound, "invite.not_found",
		"invite not found", map[string]any{"recordId": recordId})
}

// inviteList handles GET /v1/spaces/:spaceId/invites.
//
//	@Summary	List invites for a space
//	@Tags		invites
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.InvitesListResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/invites [get]
func (d *deps) inviteList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	invs, err := sp.Members().Invites(c.Request().Context())
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	out := make([]api.InviteInfo, 0, len(invs))
	for _, inv := range invs {
		out = append(out, api.InviteInfo{
			RecordId:   inv.RecordId,
			Permission: spacePermissionString(inv.Permission),
		})
	}
	return c.JSON(http.StatusOK, api.InvitesListResponse{Invites: out})
}

// inviteRevoke handles DELETE /v1/spaces/:spaceId/invites/:recordId.
//
//	@Summary	Revoke a single invite
//	@Tags		invites
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		recordId	path	string	true	"Invite record ID"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/invites/{recordId} [delete]
func (d *deps) inviteRevoke(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	recordId := c.Param("recordId")
	if recordId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "recordId required", nil)
	}
	if err := sp.ACL().RevokeInvite(c.Request().Context(), recordId); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id(), "recordId": recordId})
	}
	return c.NoContent(http.StatusNoContent)
}

// inviteRevokeAll handles DELETE /v1/spaces/:spaceId/invites.
//
//	@Summary	Revoke all invites
//	@Tags		invites
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/invites [delete]
func (d *deps) inviteRevokeAll(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().RevokeAllInvites(c.Request().Context()); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// guestKeyCreate handles POST /v1/spaces/:spaceId/guest-key.
//
//	@Summary	Create (or return) the space's guest key — public read-only access
//	@Description	Mints a shared read-only guest identity (ACL guest account) and returns it as an invite token for POST /v1/spaces/join. Idempotent: one active guest key per space; repeated calls return the same token. Owner only.
//	@Tags		invites
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	201		{object}	api.InviteCreateResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/guest-key [post]
func (d *deps) guestKeyCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	inv, err := sp.ACL().CreateGuestKey(c.Request().Context())
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	token, err := space.EncodeInvite(inv)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.InviteCreateResponse{
		SpaceId:     inv.SpaceId,
		InviteToken: token,
	})
}

// guestKeyRevoke handles DELETE /v1/spaces/:spaceId/guest-key.
//
//	@Summary	Revoke the space's guest key
//	@Description	Removes the shared guest identity from the ACL and rotates the read key: every guest copy stops receiving new content. A later create mints a fresh key, so old tokens die permanently. Owner only.
//	@Tags		invites
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope	"No active guest key"
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/guest-key [delete]
func (d *deps) guestKeyRevoke(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().RevokeGuestKey(c.Request().Context()); err != nil {
		if strings.Contains(err.Error(), "no active guest key") {
			return writeError(c, http.StatusNotFound, "guest_key.not_found",
				"no active guest key for this space", map[string]any{"spaceId": sp.Id()})
		}
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// spaceJoin handles POST /v1/spaces/join.
//
//	@Summary	Join a space via invite token
//	@Tags		spaces
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.SpaceJoinRequest	true	"Invite token + optional metadata"
//	@Success	201		{object}	api.SpaceInfo			"Joined immediately"
//	@Success	202		{object}	api.SpaceInfo			"Join pending owner approval (member) or space load pending (guest)"
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope	"Guest token for a space this account already tracks / deleted"
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/join [post]
func (d *deps) spaceJoin(c echo.Context) error {
	req, ok := bindBody[api.SpaceJoinRequest](c)
	if !ok {
		return nil
	}
	if req.InviteToken == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "inviteToken required", nil)
	}
	inv, dErr := space.DecodeInvite(req.InviteToken)
	if dErr != nil {
		return inviteDecodeError(c)
	}
	// Guest (public-access) invite: no join request, no approval — the
	// space is pulled read-only signing as the shared guest identity.
	// 201 when the bounded synchronous load succeeded, 202 while the
	// background load finishes (poll the space list for active).
	if inv.Kind == space.InviteKindGuest {
		sp, err := d.sdk.Spaces().JoinGuest(c.Request().Context(), req.InviteToken)
		if err == nil {
			return c.JSON(http.StatusCreated, spaceInfoToAPI(sp.Info()))
		}
		if errors.Is(err, space.ErrGuestJoinPending) {
			infos, lErr := d.sdk.Spaces().List(c.Request().Context())
			if lErr == nil {
				for _, info := range infos {
					if info.Id == inv.SpaceId {
						return c.JSON(http.StatusAccepted, spaceInfoToAPI(info))
					}
				}
			}
			return c.JSON(http.StatusAccepted, api.SpaceInfo{
				Id:     inv.SpaceId,
				Status: api.SpaceStatusActive,
			})
		}
		// Client conditions the SDK reports as plain errors — match the
		// documented strings (same pragmatic pattern as join-pending).
		if strings.Contains(err.Error(), "already tracked by this account") {
			return writeError(c, http.StatusConflict, "space.already_member",
				"this account already tracks the space as a member", map[string]any{"spaceId": inv.SpaceId})
		}
		if strings.Contains(err.Error(), "tombstones are sticky") {
			return writeError(c, http.StatusConflict, "space.deleted",
				"the space was deleted on this account", map[string]any{"spaceId": inv.SpaceId})
		}
		return sdkOpError(c, err, map[string]any{"spaceId": inv.SpaceId})
	}
	sp, err := d.sdk.Spaces().Join(c.Request().Context(), space.JoinRequest{
		Invite: req.InviteToken,
		Metadata: space.AccountMetadata{
			Name:        req.Metadata.Name,
			Description: req.Metadata.Description,
			IconCID:     req.Metadata.IconCID,
		},
	})
	if err == nil {
		return c.JSON(http.StatusCreated, spaceInfoToAPI(sp.Info()))
	}
	// spaceimpl.ErrJoinPending is internal-only; match the documented
	// error string, then look up the freshly-stamped joining-state index
	// entry via List.
	if strings.Contains(err.Error(), "join pending owner approval") {
		infos, lErr := d.sdk.Spaces().List(c.Request().Context())
		if lErr != nil {
			return sdkOpError(c, lErr, map[string]any{"spaceId": inv.SpaceId})
		}
		for _, info := range infos {
			if info.Id == inv.SpaceId {
				return c.JSON(http.StatusAccepted, spaceInfoToAPI(info))
			}
		}
		// Entry should exist after a successful Join; fall back to a
		// minimal SpaceInfo so callers still get the spaceId.
		return c.JSON(http.StatusAccepted, api.SpaceInfo{
			Id:     inv.SpaceId,
			Status: api.SpaceStatusJoining,
		})
	}
	if errors.Is(err, space.ErrInvalidInvite) {
		return inviteDecodeError(c)
	}
	return aclOpError(c, err, nil)
}
