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

// The CRDT version refusal maps to one conflict code on both the
// write path and the boot path, carrying the two versions.
func TestSdkOpError_CRDTVersionNewer(t *testing.T) {
	e := echo.New()
	newer := &space.CRDTVersionNewerError{Stored: 3, Supported: 1}
	for name, fn := range map[string]func(c echo.Context) error{
		"write": func(c echo.Context) error {
			return sdkOpError(c, fmt.Errorf("modify: %w", newer), map[string]any{"spaceId": "s"})
		},
		"boot": func(c echo.Context) error { return (&deps{}).authBootError(c, fmt.Errorf("open sdk: %w", newer)) },
	} {
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
		if err := fn(c); err != nil {
			t.Fatalf("%s: handler returned %v", name, err)
		}
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409: %s", name, rec.Code, rec.Body.String())
		}
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Code != "sdk.crdt_version_newer" {
			t.Errorf("%s: code = %q", name, env.Error.Code)
		}
		if env.Error.Details["stored"] != float64(3) || env.Error.Details["supported"] != float64(1) {
			t.Errorf("%s: details = %v", name, env.Error.Details)
		}
	}
}
