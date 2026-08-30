package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// TestSdkOpErrorObjectNotFound pins the SDK sentinel → wire code map in
// sdkOpError: a per-object read/write naming a deleted or unknown
// object is 404 object.not_found, never 500. The sentinel is matched
// with errors.Is, so the wrapped form (how the SDK's BuildTree path
// actually returns it) must map identically.
func TestSdkOpErrorObjectNotFound(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{fmt.Errorf("query: spaceobjects: BuildTree x: %w", space.ErrObjectNotFound), http.StatusNotFound, "object.not_found"},
		{fmt.Errorf("blocks: List: query: spaceobjects: BuildTree x: %w", space.ErrObjectNotFound), http.StatusNotFound, "object.not_found"},
		{space.ErrObjectNotFound, http.StatusNotFound, "object.not_found"},
		{errors.New("something else entirely"), http.StatusInternalServerError, "internal"},
	}
	e := echo.New()
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := sdkOpError(c, tc.err, nil); err != nil {
			t.Fatalf("sdkOpError returned %v", err)
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
		}
		if rec.Code != tc.wantStatus || env.Error.Code != tc.wantCode {
			t.Errorf("sdkOpError(%v) = %d %s, want %d %s", tc.err, rec.Code, env.Error.Code, tc.wantStatus, tc.wantCode)
		}
	}
}
