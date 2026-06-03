package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// parsePermission turns a wire permission string into the SDK enum.
// Returns ok=false on unknown values so callers can emit a precise
// 400 invalid_request rather than a fall-through 500.
func parsePermission(s string) (space.Permission, bool) {
	switch s {
	case api.SpacePermissionNone, "":
		return space.PermissionNone, true
	case api.SpacePermissionReader:
		return space.PermissionReader, true
	case api.SpacePermissionGuest:
		return space.PermissionGuest, true
	case api.SpacePermissionWriter:
		return space.PermissionWriter, true
	case api.SpacePermissionAdmin:
		return space.PermissionAdmin, true
	case api.SpacePermissionOwner:
		return space.PermissionOwner, true
	default:
		return space.PermissionNone, false
	}
}

// memberStatusString mirrors the SDK enum onto the wire constants.
func memberStatusString(s space.MemberStatus) string {
	switch s {
	case space.MemberStatusJoining:
		return api.MemberStatusJoining
	case space.MemberStatusActive:
		return api.MemberStatusActive
	case space.MemberStatusRemoved:
		return api.MemberStatusRemoved
	case space.MemberStatusDeclined:
		return api.MemberStatusDeclined
	case space.MemberStatusRemoving:
		return api.MemberStatusRemoving
	case space.MemberStatusCanceled:
		return api.MemberStatusCanceled
	default:
		return api.MemberStatusUnknown
	}
}

// memberToAPI shapes one space.Member onto the wire.
func memberToAPI(m space.Member) api.Member {
	return api.Member{
		Identity:        m.Identity,
		Permission:      spacePermissionString(m.Permission),
		Status:          memberStatusString(m.Status),
		Name:            m.Name,
		Description:     m.Description,
		IconCID:         m.IconCID,
		RequestRecordId: m.RequestRecordId,
	}
}

// aclOpError maps ACL-side errors onto the canonical envelope. The
// SDK doesn't yet expose typed sentinels for most of these — substring
// matches are the pragmatic route until it does. Conservative: known
// patterns get specific 4xx codes, everything else falls through to
// sdkOpError (500 internal).
func aclOpError(c echo.Context, err error, details map[string]any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
	}
	if errors.Is(err, space.ErrNotFound) {
		return writeError(c, http.StatusNotFound, "members.not_found", err.Error(), details)
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no permissions") || strings.Contains(msg, "permission denied"):
		return writeError(c, http.StatusForbidden, "acl.forbidden", msg, details)
	case strings.Contains(msg, "insufficient permissions"):
		return writeError(c, http.StatusForbidden, "acl.forbidden", msg, details)
	case strings.Contains(msg, "no record"):
		return writeError(c, http.StatusNotFound, "acl.record_not_found", msg, details)
	}
	return sdkOpError(c, err, details)
}

// inviteDecodeError reshapes an invalid-invite failure into a uniform
// 400 invite.invalid envelope. The message is static so it never leaks
// SDK internal types or paths.
func inviteDecodeError(c echo.Context) error {
	return writeError(c, http.StatusBadRequest, "invite.invalid",
		"invite token is malformed or not recognized", nil)
}
