package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
)

// Read-state marks racing a space delete return ErrSpaceNotTracked —
// pin the 404 mapping so it never regresses to 500.
func TestSdkOpError_SpaceNotTracked(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
	if err := sdkOpError(c, fmt.Errorf("mark read: %w", space.ErrSpaceNotTracked), nil); err != nil {
		t.Fatalf("sdkOpError returned %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "space.not_found" {
		t.Errorf("code = %q, want space.not_found", got)
	}
}
