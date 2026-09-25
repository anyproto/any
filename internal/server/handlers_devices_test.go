package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// activeDevicesMap defers the per-slug winner to the SDK's canonical
// space.ActiveDevice; these tests pin the map assembly around it —
// slug collection, dangling claims yielding no entry, independent
// slugs — not the tiebreak matrix (that lives in the SDK's tests).
func TestActiveDevicesMap(t *testing.T) {
	installed := map[string]any{}
	devices := []space.Device{
		{
			PeerId: "peerA",
			Apps:   map[string]map[string]any{"bao": installed, "other": installed},
			ActiveClaims: map[string]space.DeviceClaim{
				"bao":   {Seq: 1, At: 10},
				"other": {Seq: 5, At: 10},
			},
		},
		{
			PeerId: "peerB",
			Apps:   map[string]map[string]any{"bao": installed},
			ActiveClaims: map[string]space.DeviceClaim{
				"bao": {Seq: 2, At: 10},
				// Dangling: claim without the app installed anywhere else.
				"ghost": {Seq: 9, At: 99},
			},
		},
	}
	active := activeDevicesMap(devices)
	want := map[string]string{"bao": "peerB", "other": "peerA"}
	if len(active) != len(want) {
		t.Fatalf("active = %v, want %v", active, want)
	}
	for slug, peer := range want {
		if active[slug] != peer {
			t.Errorf("active[%q] = %q, want %q", slug, active[slug], peer)
		}
	}
	if _, ok := active["ghost"]; ok {
		t.Error("dangling claim produced a winner")
	}
}

func TestActiveDevicesMapEmpty(t *testing.T) {
	if got := activeDevicesMap(nil); got != nil {
		t.Errorf("activeDevicesMap(nil) = %v, want nil", got)
	}
	noClaims := []space.Device{{PeerId: "peerA", Apps: map[string]map[string]any{"bao": {}}}}
	if got := activeDevicesMap(noClaims); got != nil {
		t.Errorf("activeDevicesMap(no claims) = %v, want nil", got)
	}
}

// A claim with a target elects the target even when the claimer lacks
// the app, and the wire row carries the target.
func TestActiveDevicesMapTargetedClaim(t *testing.T) {
	devices := []space.Device{
		{PeerId: "phone", ActiveClaims: map[string]space.DeviceClaim{"bao": {Seq: 3, At: 10, Target: "box"}}},
		{PeerId: "box", Apps: map[string]map[string]any{"bao": {}}},
	}
	if got := activeDevicesMap(devices)["bao"]; got != "box" {
		t.Errorf("active[bao] = %q, want box", got)
	}
	if got := deviceToAPI(devices[0]).ActiveClaims["bao"]; got != (api.DeviceActiveClaim{Seq: 3, At: 10, Target: "box"}) {
		t.Errorf("wire claim = %+v, want target box", got)
	}
}

// An explicitly empty peerId is refused before the SDK is reached,
// never read as a self claim. A null one is an absent one.
func TestDeviceActivateEmptyPeerId(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/activate", strings.NewReader(`{"app":"bao","peerId":""}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	if err := (&deps{}).deviceActivate(e.NewContext(req, rec)); err != nil {
		t.Fatalf("deviceActivate: %v", err)
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if rec.Code != http.StatusBadRequest || env.Error.Code != "request.invalid_field" {
		t.Errorf("empty peerId = %d %s, want 400 request.invalid_field", rec.Code, env.Error.Code)
	}
}
