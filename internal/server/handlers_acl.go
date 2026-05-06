package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// aclAccept handles POST /v1/spaces/:spaceId/acl/accept. Approves a
// pending join request and grants `permission`.
//
// Body validation runs before resolveSpace so a malformed payload
// returns 400 without paying for a space lookup.
func (d *deps) aclAccept(c echo.Context) error {
	var req api.ACLAcceptRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
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

// aclDecline handles POST /v1/spaces/:spaceId/acl/decline. Identity is
// the joiner's account id (not the record id).
func (d *deps) aclDecline(c echo.Context) error {
	var req api.ACLDeclineRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
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
// Batched: the SDK rejects on first invalid identity, no partial
// commit beyond what any-sync's ACL semantics allow.
func (d *deps) aclChangePermissions(c echo.Context) error {
	var req api.ACLChangePermissionsRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
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

// aclRemove handles POST /v1/spaces/:spaceId/acl/remove. Rotates the
// read key as part of the SDK call so removed members can't decrypt
// new content.
func (d *deps) aclRemove(c echo.Context) error {
	var req api.ACLRemoveRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
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

// aclAdd handles POST /v1/spaces/:spaceId/acl/add. Server-side flow
// where the owner already holds joiner identities — bypasses the
// invite/request round-trip.
func (d *deps) aclAdd(c echo.Context) error {
	var req api.ACLAddRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
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
func (d *deps) aclOwnership(c echo.Context) error {
	var req api.ACLOwnershipRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
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
// Withdraws the caller's pending join request.
func (d *deps) aclCancelJoin(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	if err := sp.ACL().CancelJoinRequest(c.Request().Context()); err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.NoContent(http.StatusNoContent)
}

// aclStopSharing handles POST /v1/spaces/:spaceId/acl/stop-sharing.
// Drops every non-owner member, revokes every invite, rotates read key.
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
