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

// parseOldOwnerPerm reads the role the previous owner keeps after an
// ownership change: reader, writer or admin. done reports that it wrote
// the 400 for any other value.
func parseOldOwnerPerm(c echo.Context, raw string) (perm space.Permission, resp error, done bool) {
	if raw == "" {
		return perm, writeError(c, http.StatusBadRequest, "request.missing_field", "oldOwnerPerm required", nil), true
	}
	perm, ok := parsePermission(raw)
	if !ok {
		return perm, writeError(c, http.StatusBadRequest, "request.schema",
			"unknown permission", map[string]any{"got": raw}), true
	}
	switch perm {
	case space.PermissionReader, space.PermissionWriter, space.PermissionAdmin:
		return perm, nil, false
	}
	return perm, writeError(c, http.StatusBadRequest, "request.invalid_field",
		"oldOwnerPerm is reader, writer or admin", map[string]any{"field": "oldOwnerPerm", "got": raw}), true
}

// duplicateIdentityError answers a list that names an identity twice;
// field is the request field that carries the list. done is false for
// a list of distinct identities.
func duplicateIdentityError(c echo.Context, identities []string, field string) (resp error, done bool) {
	seen := make(map[string]struct{}, len(identities))
	for _, id := range identities {
		if _, twice := seen[id]; twice {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"an identity is listed more than once", map[string]any{"field": field}), true
		}
		seen[id] = struct{}{}
	}
	return nil, false
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
	if errors.Is(err, space.ErrAccountNotFound) {
		return writeError(c, http.StatusNotFound, "members.not_found",
			"the space's ACL holds no account of this identity", details)
	}
	// Before the text matches below: the parent's refusal is worded
	// "permission denied" too.
	if resp, done := childSpaceError(c, err, details); done {
		return resp
	}
	// The coordinator's own refusal: the owner's invite create, guest
	// key and direct add first make the space shareable, and it holds
	// another account as the owner.
	if errors.Is(err, space.ErrCoordinatorForbidden) {
		return writeError(c, http.StatusForbidden, "acl.forbidden",
			"the coordinator refuses this operation for this account", details)
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

// aclIdentityOpError is aclOpError for an op that takes account
// identities in field: a malformed one is the caller's 400. The message
// is static: the SDK error quotes the raw input.
func aclIdentityOpError(c echo.Context, err error, field string, details map[string]any) error {
	if errors.Is(err, space.ErrBadIdentity) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"invalid account identity", map[string]any{"field": field})
	}
	return aclOpError(c, err, details)
}

// shareOpError maps the errors of the calls that share a space: invite
// create, guest key and direct add, which need the space on the
// network. field is the request field that carries identities, "" for
// a call that takes none.
func shareOpError(c echo.Context, err error, field string, details map[string]any) error {
	// The wait for the space's first push ran out.
	if errors.Is(err, space.ErrSpaceUnknown) {
		return spaceNotPushedError(c, details)
	}
	if field != "" {
		return aclIdentityOpError(c, err, field, details)
	}
	return aclOpError(c, err, details)
}

// spaceNotPushedError answers for a space of the account's own that the
// coordinator does not know yet.
func spaceNotPushedError(c echo.Context, details map[string]any) error {
	return writeError(c, http.StatusConflict, "space.not_pushed",
		"the space has not reached the network yet; retry once its first push has landed", details)
}

// derivedUnshareable refuses to share a derived space. Such a space
// belongs to one account for good: the network admits no second
// member, and words that refusal as a plan limit.
func (d *deps) derivedUnshareable(c echo.Context, sp space.Space) (error, bool) {
	if !sp.Info().Derived && !d.isDerivedSpaceId(sp.Id()) {
		return nil, false
	}
	return writeError(c, http.StatusConflict, "space.derived_unshareable",
		"a derived space belongs to its account alone and cannot be shared",
		map[string]any{"spaceId": sp.Id()}), true
}

// inviteDecodeError reshapes an invalid-invite failure into a uniform
// 400 invite.invalid envelope. The message is static so it never leaks
// SDK internal types or paths.
func inviteDecodeError(c echo.Context) error {
	return writeError(c, http.StatusBadRequest, "invite.invalid",
		"invite token is malformed or not recognized", nil)
}
