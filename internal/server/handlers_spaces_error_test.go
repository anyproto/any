package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
)

// spaceError backs resolveSpace for every /v1/spaces/:id/** handler —
// pin the sentinel mappings so a not-yet-accepted space answers a typed
// 409 instead of degrading to 500 internal.
func TestSpaceError_Mapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantHTTP int
		wantCode string
	}{
		{"not accepted", fmt.Errorf("spaceimpl: space %q join is pending owner approval: %w",
			"s1", space.ErrSpaceNotAccepted), http.StatusConflict, "space.not_accepted"},
		{"deleted", fmt.Errorf("spaceimpl: space %q: %w",
			"s1", space.ErrSpaceDeleted), http.StatusConflict, "space.deleted"},
		{"fallback", errors.New("boom"), http.StatusInternalServerError, "internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)
			if err := spaceError(c, tc.err, "s1"); err != nil {
				t.Fatalf("spaceError returned %v", err)
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
