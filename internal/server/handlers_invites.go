package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// inviteCreate handles POST /v1/spaces/:spaceId/invites. Mints a fresh
// RequestToJoin invite (replacing any prior invite, per SDK semantics)
// and returns the share-friendly base58 token in the response body.
//
// Token encoding lives entirely server-side — joiners just paste the
// returned `inviteToken` into POST /v1/spaces/join.
func (d *deps) inviteCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	inv, err := sp.ACL().CreateInvite(c.Request().Context())
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

// inviteList handles GET /v1/spaces/:spaceId/invites. Reads the
// current ACL state via Members.Invites() — RecordId is what the
// DELETE path expects.
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

// inviteRevokeAll handles DELETE /v1/spaces/:spaceId/invites. Tears
// down every active invite in one batch.
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

// spaceJoin handles POST /v1/spaces/join. The body carries the
// share-friendly invite token; the server passes it through to
// Service.Join (the SDK parses it internally).
//
// The SDK signals "request posted, awaiting owner approval" by
// returning (nil, spaceimpl.ErrJoinPending) — that's a success in
// the RequestToJoin flow. We surface it as 202 Accepted with the
// joining-state SpaceInfo recovered from Service.List, so callers
// can poll members/me until status flips to active.
func (d *deps) spaceJoin(c echo.Context) error {
	var req api.SpaceJoinRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}
	if req.InviteToken == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "inviteToken required", nil)
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
	// error string. Decode the invite for the spaceId, then look up the
	// freshly-stamped joining-state index entry via List.
	if strings.Contains(err.Error(), "join pending owner approval") {
		inv, dErr := space.DecodeInvite(req.InviteToken)
		if dErr != nil {
			return inviteDecodeError(c, dErr)
		}
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
	return aclOpError(c, err, nil)
}
