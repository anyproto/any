package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
)

// badSpaceIdError answers an id that is not a space id; field names the
// path parameter or the body field that carried it.
func badSpaceIdError(c echo.Context, field string) error {
	return writeError(c, http.StatusBadRequest, "request.invalid_field",
		"invalid space id", map[string]any{"field": field})
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
