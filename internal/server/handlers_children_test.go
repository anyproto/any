package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// childSpaceErrors is every parent and child space sentinel of the SDK
// with the status and code it answers.
var childSpaceErrors = []struct {
	err      error
	wantHTTP int
	wantCode string
}{
	{space.ErrOffline, http.StatusServiceUnavailable, "network.unreachable"},
	{space.ErrParentSpaceNotAvailable, http.StatusConflict, "space.parent_not_available"},
	{space.ErrParentSpaceInvalid, http.StatusConflict, "space.parent_invalid"},
	{space.ErrParentPermissionDenied, http.StatusForbidden, "space.parent_permission_denied"},
	{space.ErrChildSpacesLimitReached, http.StatusConflict, "space.child_limit_reached"},
	{space.ErrNotParentMember, http.StatusConflict, "acl.not_parent_member"},
	{space.ErrSpaceHasChildren, http.StatusConflict, "space.has_children"},
	{space.ErrNotChildSpace, http.StatusConflict, "space.not_child"},
	{space.ErrSpaceNotStored, http.StatusConflict, "space.not_stored"},
}

// childSpaceMappers is every error mapper a child-space sentinel can
// reach, each called with the space id s1.
var childSpaceMappers = map[string]func(echo.Context, error) error{
	"spaceError": func(c echo.Context, err error) error { return spaceError(c, err, "s1") },
	"sdkOpError": func(c echo.Context, err error) error { return sdkOpError(c, err, map[string]any{"spaceId": "s1"}) },
	"aclOpError": func(c echo.Context, err error) error { return aclOpError(c, err, map[string]any{"spaceId": "s1"}) },
	"aclIdentityOpError": func(c echo.Context, err error) error {
		return aclIdentityOpError(c, err, "identities", map[string]any{"spaceId": "s1"})
	},
}

// mapError runs one error mapper and returns the status and the
// envelope it wrote.
func mapError(t *testing.T, mapper func(echo.Context) error) (int, api.ErrorEnvelope) {
	t.Helper()
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
	if err := mapper(c); err != nil {
		t.Fatalf("mapper returned %v", err)
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v: %s", err, rec.Body.String())
	}
	return rec.Code, env
}

// Every mapper answers a child-space sentinel with the same code: the
// space mapper, the generic one and the ACL ones. The ACL mappers must
// match the typed error before their "permission denied" text match.
func TestChildSpaceError_Mapping(t *testing.T) {
	for _, tc := range childSpaceErrors {
		for name, mapper := range childSpaceMappers {
			wrapped := fmt.Errorf("spaceimpl: Op: space %q: %w", "s1", tc.err)
			code, env := mapError(t, func(c echo.Context) error { return mapper(c, wrapped) })
			if code != tc.wantHTTP || env.Error.Code != tc.wantCode {
				t.Errorf("%s(%v): %d %s, want %d %s", name, tc.err, code, env.Error.Code, tc.wantHTTP, tc.wantCode)
			}
			if got := env.Error.Details["spaceId"]; got != "s1" {
				t.Errorf("%s(%v): details %v, want the spaceId kept", name, tc.err, env.Error.Details)
			}
		}
	}
}

// An identity the ACL holds no account of is the 404 of the members
// routes, on the ACL mappers.
func TestAclOpError_AccountNotFound(t *testing.T) {
	err := fmt.Errorf("spaceimpl: RemoveAccounts: %w", space.ErrAccountNotFound)
	for name, mapper := range map[string]func(echo.Context) error{
		"aclOpError": func(c echo.Context) error { return aclOpError(c, err, map[string]any{"spaceId": "s1"}) },
		"aclIdentityOpError": func(c echo.Context) error {
			return aclIdentityOpError(c, err, "identities", map[string]any{"spaceId": "s1"})
		},
	} {
		code, env := mapError(t, mapper)
		if code != http.StatusNotFound || env.Error.Code != "members.not_found" {
			t.Errorf("%s: %d %s, want 404 members.not_found", name, code, env.Error.Code)
		}
	}
}
