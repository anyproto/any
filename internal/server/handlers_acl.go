package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// aclAccept handles POST /v1/spaces/:spaceId/acl/accept.
//
//	@Summary	Accept a join request
//	@Tags		acl
//	@Accept		json
//	@Param		spaceId	path	string				true	"Space ID"
//	@Param		body	body	api.ACLAcceptRequest	true	"Request record + permission"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/accept [post]
func (d *deps) aclAccept(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLAcceptRequest](c, "")
	if !ok {
		return nil
	}
	if req.RequestRecordId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "requestRecordId required", nil)
	}
	perm, ok := parsePermission(req.Permission)
	if !ok {
		return writeError(c, http.StatusBadRequest, "request.schema",
			"unknown permission", map[string]any{"got": req.Permission})
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().AcceptRequest(c.Request().Context(), req.RequestRecordId, perm); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id(), "recordId": req.RequestRecordId})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclDecline handles POST /v1/spaces/:spaceId/acl/decline.
//
//	@Summary	Decline a join request
//	@Tags		acl
//	@Accept		json
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		body	body	api.ACLDeclineRequest	true	"Identity to decline"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/decline [post]
func (d *deps) aclDecline(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLDeclineRequest](c, "")
	if !ok {
		return nil
	}
	if req.Identity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "identity required", nil)
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().DeclineRequest(c.Request().Context(), req.Identity); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id(), "identity": req.Identity})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclChangePermissions handles POST /v1/spaces/:spaceId/acl/permissions.
//
//	@Summary	Change member permissions (batch)
//	@Tags		acl
//	@Accept		json
//	@Param		spaceId	path	string								true	"Space ID"
//	@Param		body	body	api.ACLChangePermissionsRequest	true	"Permission changes"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/permissions [post]
func (d *deps) aclChangePermissions(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLChangePermissionsRequest](c, "")
	if !ok {
		return nil
	}
	if len(req.Changes) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "changes must be non-empty", nil)
	}
	out := make([]space.PermissionChange, 0, len(req.Changes))
	for i, ch := range req.Changes {
		if ch.Identity == "" {
			return writeError(c, http.StatusBadRequest, "request.missing_field",
				"identity required", map[string]any{"index": i})
		}
		perm, ok := parsePermission(ch.Permission)
		if !ok {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"unknown permission", map[string]any{"index": i, "got": ch.Permission})
		}
		out = append(out, space.PermissionChange{Identity: ch.Identity, Permission: perm})
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().ChangePermissions(c.Request().Context(), out); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclRemove handles POST /v1/spaces/:spaceId/acl/remove.
//
//	@Summary	Remove members (rotates read key)
//	@Tags		acl
//	@Accept		json
//	@Param		spaceId	path	string				true	"Space ID"
//	@Param		body	body	api.ACLRemoveRequest	true	"Identities to remove"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/remove [post]
func (d *deps) aclRemove(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLRemoveRequest](c, "")
	if !ok {
		return nil
	}
	if len(req.Identities) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "identities must be non-empty", nil)
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().RemoveAccounts(c.Request().Context(), req.Identities); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclAdd handles POST /v1/spaces/:spaceId/acl/add.
//
//	@Summary	Add members directly (bypasses invite flow)
//	@Tags		acl
//	@Accept		json
//	@Param		spaceId	path	string			true	"Space ID"
//	@Param		body	body	api.ACLAddRequest	true	"Accounts to add"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/add [post]
func (d *deps) aclAdd(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLAddRequest](c, "")
	if !ok {
		return nil
	}
	if len(req.Accounts) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "accounts must be non-empty", nil)
	}
	out := make([]space.MemberAdd, 0, len(req.Accounts))
	for i, a := range req.Accounts {
		if a.Identity == "" {
			return writeError(c, http.StatusBadRequest, "request.missing_field",
				"identity required", map[string]any{"index": i})
		}
		perm, ok := parsePermission(a.Permission)
		if !ok {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"unknown permission", map[string]any{"index": i, "got": a.Permission})
		}
		out = append(out, space.MemberAdd{
			Identity:   a.Identity,
			Permission: perm,
			Metadata: space.AccountMetadata{
				Name:        a.Metadata.Name,
				Description: a.Metadata.Description,
				IconCID:     a.Metadata.IconCID,
			},
		})
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().AddAccounts(c.Request().Context(), out); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclOwnership handles POST /v1/spaces/:spaceId/acl/ownership.
//
//	@Summary	Transfer ownership
//	@Tags		acl
//	@Accept		json
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		body	body	api.ACLOwnershipRequest	true	"New owner + old owner permission"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/ownership [post]
func (d *deps) aclOwnership(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLOwnershipRequest](c, "")
	if !ok {
		return nil
	}
	if req.NewOwner == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "newOwner required", nil)
	}
	perm, ok := parsePermission(req.OldOwnerPerm)
	if !ok {
		return writeError(c, http.StatusBadRequest, "request.schema",
			"unknown permission", map[string]any{"got": req.OldOwnerPerm})
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().OwnershipChange(c.Request().Context(), req.NewOwner, perm); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id(), "newOwner": req.NewOwner})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclSelfRemove handles POST /v1/spaces/:spaceId/acl/self-remove.
//
//	@Summary	Remove yourself from a space
//	@Tags		acl
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/self-remove [post]
func (d *deps) aclSelfRemove(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().RequestSelfRemove(c.Request().Context()); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclCancelJoin handles POST /v1/spaces/:spaceId/acl/cancel-join.
//
// Account-level, not space-level: the only state a cancel applies to is
// a pending join, and a pending join is exactly what resolveSpace
// refuses (the SDK never materializes a space before acceptance), so
// the route goes through Service.CancelJoin — the joining client posts
// the withdrawal to the ACL chain the nodes serve, no local space
// involved. On success the joiner's row reads `deleted` and a fresh
// POST /v1/spaces/join with a valid invite revives it.
//
//	@Summary		Cancel your pending join request
//	@Description	Withdraws this account's pending join request. The space row flips to status "deleted" on this device; POST /v1/spaces/join with a valid invite re-requests. 409 space.join_not_pending when the row is not joining, or when the owner accepted or declined first — the row then settles to active or deleted on its own.
//	@Tags			acl
//	@Param			spaceId	path	string	true	"Space ID"
//	@Success		204
//	@Failure		404	{object}	api.ErrorEnvelope	"space.not_found"
//	@Failure		409	{object}	api.ErrorEnvelope	"space.join_not_pending"
//	@Failure		500	{object}	api.ErrorEnvelope
//	@Router			/spaces/{spaceId}/acl/cancel-join [post]
func (d *deps) aclCancelJoin(c echo.Context) error {
	id := c.Param("spaceId")
	if id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "spaceId required", nil)
	}
	details := map[string]any{"spaceId": id}
	if err := d.sdk.Spaces().CancelJoin(c.Request().Context(), id); err != nil {
		if errors.Is(err, space.ErrJoinNotPending) {
			return writeError(c, http.StatusConflict, "space.join_not_pending",
				"no pending join request to cancel: the space is not in the joining state, or the owner already resolved the request", details)
		}
		if errors.Is(err, space.ErrSpaceUnknown) {
			return spaceError(c, err, id)
		}
		return aclOpError(c, err, details)
	}
	return c.NoContent(http.StatusNoContent)
}

// aclStopSharing handles POST /v1/spaces/:spaceId/acl/stop-sharing.
//
//	@Summary	Stop sharing (removes all non-owners)
//	@Tags		acl
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/acl/stop-sharing [post]
func (d *deps) aclStopSharing(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().StopSharing(c.Request().Context()); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}
