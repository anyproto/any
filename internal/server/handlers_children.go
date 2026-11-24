package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// registerChildSpaceRoutes registers the routes of child spaces: a
// space created under a parent space, an organization.
func registerChildSpaceRoutes(g *echo.Group, d *deps) {
	g.POST("/spaces/:spaceId/retry-push", d.spaceRetryPush)
	g.GET("/spaces/:spaceId/children", d.spaceChildren)

	// A child reached from outside it, by its id alone: the account
	// need not be a member of the child, and nothing of it is loaded.
	g.GET("/children/:childId/members", d.childMembers)
	g.POST("/children/:childId/acl/remove", d.childRemoveMembers)
	g.POST("/children/:childId/acl/ownership", d.childChangeOwner)
	g.DELETE("/children/:childId", d.childDelete)
}

// spaceChildren handles GET /v1/spaces/:spaceId/children —
// Service.Children. The coordinator answers on every call; nothing is
// stored, and no row is created for a listed child.
//
//	@Summary		List the child spaces of a parent space
//	@Description	Every child of the space as the coordinator holds it, the ones this account is no member of included. For an admin or the owner of the parent; a member finds the children it belongs to in the space list, by SpaceInfo.parentSpaceId. The type of the space is not checked: an admin or the owner of a space that is not an organization gets an empty list, so an empty list does not say the space can have children; SpaceInfo.type does. Needs the network.
//	@Tags			spaces
//	@Produce		json
//	@Param			spaceId	path		string	true	"Space ID of the parent"
//	@Success		200		{object}	api.SpaceChildrenResponse
//	@Failure		400		{object}	api.ErrorEnvelope	"request.invalid_field — spaceId is not a space id"
//	@Failure		403		{object}	api.ErrorEnvelope	"space.parent_permission_denied — the account is not an admin or the owner of the space, or the space is a child the coordinator refused for the role in its parent"
//	@Failure		404		{object}	api.ErrorEnvelope	"space.not_found — the coordinator does not know the space and the account does not hold it as an active space"
//	@Failure		409		{object}	api.ErrorEnvelope	"space.deleted — the coordinator holds the space as deleted or in deletion; space.not_pushed — the account's own active space has not reached the network yet and holds no refusal; space.parent_not_available — the parent's ACL has not reached the network yet, or the space is a child refused for it; space.parent_invalid, space.child_limit_reached — the space is a child the coordinator refused"
//	@Failure		503		{object}	api.ErrorEnvelope	"network.unreachable — the coordinator could not be reached"
//	@Router			/spaces/{spaceId}/children [get]
func (d *deps) spaceChildren(c echo.Context) error {
	id := c.Param("spaceId")
	children, err := d.sdk.Spaces().Children(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, space.ErrBadSpaceId) {
			return badSpaceIdError(c, "spaceId")
		}
		return d.childrenError(c, err, id)
	}
	out := make([]api.ChildSpace, 0, len(children))
	for _, child := range children {
		out = append(out, api.ChildSpace{Id: child.Id, Status: childStatusString(child.Status)})
	}
	return c.JSON(http.StatusOK, api.SpaceChildrenResponse{Children: out})
}

// childrenError maps the errors of the children list. A space the
// coordinator does not know and the account holds as active is on no
// node.
func (d *deps) childrenError(c echo.Context, err error, spaceId string) error {
	if errors.Is(err, space.ErrSpaceUnknown) {
		if info, ok := d.activeSpace(c.Request().Context(), spaceId); ok {
			return unpushedSpaceError(c, info)
		}
	}
	return spaceError(c, err, spaceId)
}

// unpushedSpaceError answers for an active space of the account that is
// on no node: the code of the refusal its row holds, space.not_pushed
// for a row with none.
func unpushedSpaceError(c echo.Context, info space.SpaceInfo) error {
	if refused := signRefusalError(info.Refusal); refused != nil {
		return spaceError(c, refused, info.Id)
	}
	return spaceNotPushedError(c, map[string]any{"spaceId": info.Id})
}

// signRefusalError is the SDK error of the coordinator's refusal to
// accept a child space; nil for any other value.
func signRefusalError(r space.Refusal) error {
	switch r {
	case space.RefusalParentNotAvailable:
		return space.ErrParentSpaceNotAvailable
	case space.RefusalParentInvalid:
		return space.ErrParentSpaceInvalid
	case space.RefusalParentPermissionDenied:
		return space.ErrParentPermissionDenied
	case space.RefusalChildLimitReached:
		return space.ErrChildSpacesLimitReached
	}
	return nil
}

// activeSpace returns the row of spaceId when the account's space list
// holds it as active.
func (d *deps) activeSpace(ctx context.Context, spaceId string) (space.SpaceInfo, bool) {
	infos, err := d.sdk.Spaces().List(ctx)
	if err != nil {
		return space.SpaceInfo{}, false
	}
	for _, info := range infos {
		if info.Id == spaceId {
			return info, info.Status == space.StatusActive
		}
	}
	return space.SpaceInfo{}, false
}

func childStatusString(s space.ChildStatus) string {
	switch s {
	case space.ChildStatusActive:
		return api.ChildStatusActive
	case space.ChildStatusDeletionPending:
		return api.ChildStatusDeletionPending
	case space.ChildStatusDeleted:
		return api.ChildStatusDeleted
	default:
		return api.ChildStatusUnknown
	}
}

// childMembers handles GET /v1/children/:childId/members —
// Service.ChildMembers with Service.ChildRotationPending, both read
// from the ACL the nodes serve.
//
//	@Summary		List the accounts of a child space from the parent side
//	@Description	The accounts of the child with role and status, read from the ACL the nodes serve: the identities to remove, and the active members one of whom can become the owner. No names: the caller holds no key of the child. rotationPending is true while members removed from the parent side still hold the child's read key. The nodes serve a space's ACL to any account, so no role is checked. Needs the network.
//	@Tags			children
//	@Produce		json
//	@Param			childId	path		string	true	"Space ID of the child"
//	@Success		200		{object}	api.ChildMembersResponse
//	@Failure		400		{object}	api.ErrorEnvelope	"request.invalid_field — childId is not a space id"
//	@Failure		404		{object}	api.ErrorEnvelope	"space.not_found — the coordinator does not know the space"
//	@Failure		409		{object}	api.ErrorEnvelope	"space.not_child — the account's space list shows the space has no parent; space.deleted — the coordinator holds the space as deleted or in deletion"
//	@Failure		503		{object}	api.ErrorEnvelope	"network.unreachable — no node could be reached"
//	@Router			/children/{childId}/members [get]
func (d *deps) childMembers(c echo.Context) error {
	id := c.Param("childId")
	members, err := d.sdk.Spaces().ChildMembers(c.Request().Context(), id)
	if err != nil {
		return childOpError(c, err, id, "")
	}
	pending, err := d.sdk.Spaces().ChildRotationPending(c.Request().Context(), id)
	if err != nil {
		return childOpError(c, err, id, "")
	}
	return c.JSON(http.StatusOK, childMembersToAPI(members, pending))
}

// childMembersToAPI shapes the parent-side read of a child's accounts.
func childMembersToAPI(members []space.ChildMember, rotationPending bool) api.ChildMembersResponse {
	out := make([]api.ChildMember, 0, len(members))
	for _, m := range members {
		out = append(out, api.ChildMember{
			Identity:   m.Identity,
			Permission: spacePermissionString(m.Permission),
			Status:     memberStatusString(m.Status),
		})
	}
	return api.ChildMembersResponse{Members: out, RotationPending: rotationPending}
}

// childRemoveMembers handles POST /v1/children/:childId/acl/remove —
// Service.RemoveChildMembers.
//
//	@Summary		Remove members of a child space as an admin or the owner of its parent
//	@Description	For an admin or the owner of the parent, and nobody else: no role in the child allows it. The members lose their permissions at once. The read key is not changed, since the caller does not hold it: the removed members read what is written until a device of an admin or the owner of the child changes the key, which it does by itself (rotationPending on GET /children/{childId}/members, and on GET /spaces/{spaceId}/members for the child's members). The child's owner cannot be removed. Needs the network.
//	@Tags			children
//	@Accept			json
//	@Param			childId	path	string					true	"Space ID of the child"
//	@Param			body	body	api.ACLRemoveRequest	true	"Identities to remove"
//	@Success		204
//	@Failure		400	{object}	api.ErrorEnvelope	"request.missing_field — identities is empty; request.invalid_field — an identity is listed more than once or is malformed, or childId is not a space id"
//	@Failure		403	{object}	api.ErrorEnvelope	"space.parent_permission_denied — the account is not an admin or the owner of the parent, or an admin removes an account that is an admin or the owner of the parent; acl.forbidden — the child's owner or the caller is among the identities, or the space is a top-level space of another account"
//	@Failure		404	{object}	api.ErrorEnvelope	"members.not_found — an identity is no member of the child; space.not_found — the coordinator does not know the space"
//	@Failure		409	{object}	api.ErrorEnvelope	"space.not_child — the account's space list shows the space has no parent; space.deleted — the coordinator holds the space as deleted or in deletion; space.parent_not_available — the coordinator cannot read the ACL of the child's parent"
//	@Failure		503	{object}	api.ErrorEnvelope	"network.unreachable — the network could not be reached; nothing changed"
//	@Router			/children/{childId}/acl/remove [post]
func (d *deps) childRemoveMembers(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLRemoveRequest](c, "")
	if !ok {
		return nil
	}
	if len(req.Identities) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "identities must be non-empty", nil)
	}
	if resp, done := duplicateIdentityError(c, req.Identities, "identities"); done {
		return resp
	}
	id := c.Param("childId")
	if err := d.sdk.Spaces().RemoveChildMembers(c.Request().Context(), id, req.Identities); err != nil {
		return childOpError(c, err, id, "identities")
	}
	return c.NoContent(http.StatusNoContent)
}

// childChangeOwner handles POST /v1/children/:childId/acl/ownership —
// Service.ChangeChildOwner.
//
//	@Summary		Hand a child space to another of its members, as the owner of its parent or its own owner
//	@Description	newOwner, an active member of the child and a member of the parent, becomes its owner. The previous owner stays a member with oldOwnerPerm; removing them is a separate call. The network accepts the call from the owner of the parent and from the child's own owner, whatever its role in the parent. Needs the network.
//	@Tags			children
//	@Accept			json
//	@Param			childId	path	string					true	"Space ID of the child"
//	@Param			body	body	api.ACLOwnershipRequest	true	"New owner + the role the previous owner keeps: reader, writer or admin"
//	@Success		204
//	@Failure		400	{object}	api.ErrorEnvelope	"request.missing_field — newOwner or oldOwnerPerm is missing; request.invalid_field — oldOwnerPerm is none, guest or owner, or childId is not a space id; request.schema — an unknown permission"
//	@Failure		403	{object}	api.ErrorEnvelope	"space.parent_permission_denied — the account is neither the owner of the parent nor the child's own owner; acl.forbidden — the new owner is the owner already, or the space is a top-level space of another account"
//	@Failure		404	{object}	api.ErrorEnvelope	"members.not_found — the new owner is no member of the child; space.not_found — the coordinator does not know the space"
//	@Failure		409	{object}	api.ErrorEnvelope	"space.not_child — the account's space list shows the space has no parent; space.deleted — the coordinator holds the space as deleted or in deletion; acl.not_parent_member — the new owner is no member of the parent; space.parent_not_available — the coordinator cannot read the ACL of the child's parent"
//	@Failure		503	{object}	api.ErrorEnvelope	"network.unreachable — the network could not be reached; nothing changed"
//	@Router			/children/{childId}/acl/ownership [post]
func (d *deps) childChangeOwner(c echo.Context) error {
	req, ok := bindBodyStrict[api.ACLOwnershipRequest](c, "")
	if !ok {
		return nil
	}
	if req.NewOwner == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "newOwner required", nil)
	}
	perm, errResp, done := parseOldOwnerPerm(c, req.OldOwnerPerm)
	if done {
		return errResp
	}
	id := c.Param("childId")
	if err := d.sdk.Spaces().ChangeChildOwner(c.Request().Context(), id, req.NewOwner, perm); err != nil {
		return childOpError(c, err, id, "newOwner")
	}
	return c.NoContent(http.StatusNoContent)
}

// childDelete handles DELETE /v1/children/:childId —
// Service.DeleteChild. The coordinator answers before the reply.
//
//	@Summary		Delete a child space, as the owner of its parent or its own owner
//	@Description	Deletes the child on the network, for everyone, without access to its data. The network accepts the call from the owner of the parent and from the child's own owner, whatever its role in the parent. When the account also holds the child in its own list, the row then reads deleted and the local copy is removed. DELETE /spaces/{spaceId} stays the call for a space of the account's own list: on a child it deletes on the network only for the child's owner. Needs the network.
//	@Tags			children
//	@Param			childId	path	string	true	"Space ID of the child"
//	@Success		204
//	@Failure		400	{object}	api.ErrorEnvelope	"request.invalid_field — childId is not a space id"
//	@Failure		403	{object}	api.ErrorEnvelope	"space.parent_permission_denied — the account is neither the owner of the parent nor the child's own owner"
//	@Failure		404	{object}	api.ErrorEnvelope	"space.not_found — the coordinator does not know the space"
//	@Failure		409	{object}	api.ErrorEnvelope	"space.not_child — the account's space list shows the space has no parent, or the coordinator shows an active top-level space of another account; space.parent_not_available — the coordinator cannot read the ACL of the child's parent"
//	@Failure		503	{object}	api.ErrorEnvelope	"network.unreachable — the coordinator could not be reached; nothing changed"
//	@Router			/children/{childId} [delete]
func (d *deps) childDelete(c echo.Context) error {
	id := c.Param("childId")
	if err := d.sdk.Spaces().DeleteChild(c.Request().Context(), id); err != nil {
		return childOpError(c, err, id, "")
	}
	return c.NoContent(http.StatusNoContent)
}

// childOpError maps the errors of the calls made on a child from the
// parent side. field is the request field that carries identities.
func childOpError(c echo.Context, err error, childId, field string) error {
	if errors.Is(err, space.ErrBadSpaceId) {
		return badSpaceIdError(c, "childId")
	}
	// The coordinator does not know the space, or holds it as deleted.
	if errors.Is(err, space.ErrSpaceUnknown) || errors.Is(err, space.ErrSpaceDeleted) {
		return spaceError(c, err, childId)
	}
	details := map[string]any{"spaceId": childId}
	if errors.Is(err, space.ErrInsufficientPermissions) {
		return writeError(c, http.StatusForbidden, "acl.forbidden",
			"the child's ACL forbids this record", details)
	}
	return aclIdentityOpError(c, err, field, details)
}

// badSpaceIdError answers an id that is not a space id; field names the
// path parameter or the body field that carried it.
func badSpaceIdError(c echo.Context, field string) error {
	return writeError(c, http.StatusBadRequest, "request.invalid_field",
		"invalid space id", map[string]any{"field": field})
}

// spaceRetryPush handles POST /v1/spaces/:spaceId/retry-push —
// Service.RetryPush. The coordinator's answer is on the row before the
// reply: 204 clears SpaceInfo.refusal, a refusal sets it.
//
//	@Summary		Ask the coordinator now to accept a refused child space
//	@Description	For a child whose SpaceInfo.refusal is set, once the cause is gone; left alone, the server asks again by itself, less and less often. 204: the coordinator accepted the space, the refusal is cleared and the space is pushed to the nodes in the background. A refusal answers its own code and is recorded on the row.
//	@Tags			spaces
//	@Param			spaceId	path	string	true	"Space ID of the child"
//	@Success		204
//	@Failure		400	{object}	api.ErrorEnvelope	"request.invalid_field — spaceId is not a space id"
//	@Failure		403	{object}	api.ErrorEnvelope	"space.parent_permission_denied — refused again: the account is below writer in the parent"
//	@Failure		404	{object}	api.ErrorEnvelope	"space.not_found"
//	@Failure		409	{object}	api.ErrorEnvelope	"space.parent_not_available, space.parent_invalid, space.child_limit_reached — refused again; space.not_child — the account's space list shows the space has no parent; space.not_stored — this device holds no copy of the space; space.deleted; space.not_accepted"
//	@Failure		503	{object}	api.ErrorEnvelope	"network.unreachable — the coordinator could not be reached; nothing changed"
//	@Router			/spaces/{spaceId}/retry-push [post]
func (d *deps) spaceRetryPush(c echo.Context) error {
	id := c.Param("spaceId")
	if err := d.sdk.Spaces().RetryPush(c.Request().Context(), id); err != nil {
		if errors.Is(err, space.ErrBadSpaceId) {
			return badSpaceIdError(c, "spaceId")
		}
		return spaceError(c, err, id)
	}
	return c.NoContent(http.StatusNoContent)
}

// childSpaceError maps the SDK's parent and child space sentinels, and
// the one of a space type with no guest role. Shared by the space, op
// and ACL mappers, so a refusal reads the same on every route.
func childSpaceError(c echo.Context, err error, details map[string]any) (error, bool) {
	switch {
	case errors.Is(err, space.ErrOffline):
		return writeError(c, http.StatusServiceUnavailable, "network.unreachable",
			"the network could not be reached; nothing was changed", details), true
	case errors.Is(err, space.ErrParentSpaceNotAvailable):
		return writeError(c, http.StatusConflict, "space.parent_not_available",
			"the parent space does not exist on the network or is deleted, or the coordinator cannot read its ACL", details), true
	case errors.Is(err, space.ErrParentSpaceInvalid):
		return writeError(c, http.StatusConflict, "space.parent_invalid",
			"the parent is not an organization: only a space of type any.organization has child spaces", details), true
	case errors.Is(err, space.ErrParentPermissionDenied):
		return writeError(c, http.StatusForbidden, "space.parent_permission_denied",
			"this account's role in the parent space does not allow the operation", details), true
	case errors.Is(err, space.ErrChildSpacesLimitReached):
		return writeError(c, http.StatusConflict, "space.child_limit_reached",
			"the owner of the parent space is at its limit of child spaces", details), true
	case errors.Is(err, space.ErrNotParentMember):
		return writeError(c, http.StatusConflict, "acl.not_parent_member",
			"only a member of the parent space becomes a member of a child space", details), true
	case errors.Is(err, space.ErrNoGuestRole):
		return writeError(c, http.StatusConflict, "acl.no_guest_role",
			"an organization and its child spaces have no guest role and no guest key: a read-only member is a reader", details), true
	case errors.Is(err, space.ErrSpaceHasChildren):
		return writeError(c, http.StatusConflict, "space.has_children",
			"the space still has child spaces: delete them, then delete the space once its children list is empty", details), true
	case errors.Is(err, space.ErrNotChildSpace):
		return writeError(c, http.StatusConflict, "space.not_child",
			"the space has no parent: the account's space list or the coordinator shows a top-level space", details), true
	case errors.Is(err, space.ErrSpaceNotStored):
		return writeError(c, http.StatusConflict, "space.not_stored",
			"this device holds no copy of the space; a device that holds it pushes it", details), true
	}
	return nil, false
}
