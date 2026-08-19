package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// The direct-add invite statuses ride the same List/Get surface as every
// other status — pin the enum → wire-string mapping.
func TestSpaceStatusString_InviteStatuses(t *testing.T) {
	if got := spaceStatusString(space.StatusInvitePending); got != api.SpaceStatusInvitePending {
		t.Errorf("StatusInvitePending = %q, want %q", got, api.SpaceStatusInvitePending)
	}
	if got := spaceStatusString(space.StatusInviteDeclined); got != api.SpaceStatusInviteDeclined {
		t.Errorf("StatusInviteDeclined = %q, want %q", got, api.SpaceStatusInviteDeclined)
	}
}

// inviteStateError string-matches the SDK's documented messages (no
// exported sentinels for this family yet) — pin the mapping so an SDK
// wording change fails loudly here instead of silently degrading every
// error to 500. Unknown space rides the errors.Is sentinel through the
// spaceError fallback.
func TestInviteStateError_Mapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantHTTP int
		wantCode string
	}{
		{"unknown space", fmt.Errorf("spaceimpl: AcceptInvite: %w %q", space.ErrSpaceUnknown, "s1"), http.StatusNotFound, "space.not_found"},
		{"one-to-one", errors.New(`spaceimpl: AcceptInvite: "s1" is a 1-1 space — use AcceptOneToOne`), http.StatusBadRequest, "request.invalid_field"},
		{"not pending", errors.New(`spaceimpl: DeclineInvite: space "s1" is not invite-pending`), http.StatusConflict, "space.not_invite_pending"},
		{"deleted", errors.New(`spaceimpl: AcceptInvite: space "s1" is deleted`), http.StatusConflict, "space.deleted"},
		{"fallback", errors.New("boom"), http.StatusInternalServerError, "internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
			if err := inviteStateError(c, tc.err, "s1"); err != nil {
				t.Fatalf("inviteStateError returned %v", err)
			}
			if rec.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantHTTP, rec.Body.String())
			}
			if got := errEnvCode(t, rec.Body.Bytes()); got != tc.wantCode {
				t.Errorf("code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}
