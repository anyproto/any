package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
)

// registerChildSpaceRoutes registers the routes of child spaces: a
// space created under a parent space.
func registerChildSpaceRoutes(g *echo.Group, d *deps) {
	g.POST("/spaces/:spaceId/retry-push", d.spaceRetryPush)
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

// childSpaceError maps the SDK's parent and child space sentinels.
// Shared by the space, op and ACL mappers, so a refusal reads the same
// on every route.
func childSpaceError(c echo.Context, err error, details map[string]any) (error, bool) {
	switch {
	case errors.Is(err, space.ErrOffline):
		return writeError(c, http.StatusServiceUnavailable, "network.unreachable",
			"the network could not be reached; nothing was changed", details), true
	case errors.Is(err, space.ErrParentSpaceNotAvailable):
		return writeError(c, http.StatusConflict, "space.parent_not_available",
			"the parent space does not exist on the network or is deleted", details), true
	case errors.Is(err, space.ErrParentSpaceInvalid):
		return writeError(c, http.StatusConflict, "space.parent_invalid",
			"the parent is not a regular top-level space: a child, derived, one-to-one or tech space has no children", details), true
	case errors.Is(err, space.ErrParentPermissionDenied):
		return writeError(c, http.StatusForbidden, "space.parent_permission_denied",
			"this account's role in the parent space does not allow the operation", details), true
	case errors.Is(err, space.ErrChildSpacesLimitReached):
		return writeError(c, http.StatusConflict, "space.child_limit_reached",
			"the owner of the parent space is at its limit of child spaces", details), true
	case errors.Is(err, space.ErrNotParentMember):
		return writeError(c, http.StatusConflict, "acl.not_parent_member",
			"only a member of the parent space becomes a member of a child space", details), true
	case errors.Is(err, space.ErrSpaceHasChildren):
		return writeError(c, http.StatusConflict, "space.has_children",
			"the space still has child spaces: delete them, then delete the space once its children list is empty", details), true
	case errors.Is(err, space.ErrNotChildSpace):
		return writeError(c, http.StatusConflict, "space.not_child",
			"the account's space list shows the space has no parent", details), true
	case errors.Is(err, space.ErrSpaceNotStored):
		return writeError(c, http.StatusConflict, "space.not_stored",
			"this device holds no copy of the space; a device that holds it pushes it", details), true
	}
	return nil, false
}
