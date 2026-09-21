package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

func TestLocalDiscoverySwitchRecords(t *testing.T) {
	var s localDiscoverySwitch

	if _, stated := s.get(); stated {
		t.Fatal("fresh switch reports a statement")
	}
	s.set(false)
	if enabled, stated := s.get(); !stated || enabled {
		t.Errorf("get = (%v, %v), want (false, true)", enabled, stated)
	}
	s.set(true)
	if enabled, stated := s.get(); !stated || !enabled {
		t.Errorf("get = (%v, %v), want (true, true)", enabled, stated)
	}
}

// The whole point of the endpoint is that it works with no engine and
// before auth: the host states its answer first, and the next boot
// starts discovery in that state.
func TestLocalDiscoveryRoutesWorkUnauthorized(t *testing.T) {
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodGet, "/v1/local-discovery", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var res api.LocalDiscoveryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Enabled != d.cfg.LocalDiscoveryEnabled() {
		t.Errorf("GET before any statement = %v, want the config default %v", res.Enabled, d.cfg.LocalDiscoveryEnabled())
	}

	rec = doJSON(t, e, http.MethodPut, "/v1/local-discovery", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Enabled {
		t.Error("PUT response says enabled after disabling")
	}
	if enabled, stated := d.localDiscovery.get(); !stated || enabled {
		t.Errorf("switch after PUT = (%v, %v), want (false, true)", enabled, stated)
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/local-discovery", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Enabled {
		t.Error("GET after disabling still reports enabled")
	}

	// An absent value is a 400, never a silent off.
	for _, body := range []string{`{"enabled":"yes"}`, `{}`, ``} {
		rec = doJSON(t, e, http.MethodPut, "/v1/local-discovery", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: want 400, got %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if enabled, _ := d.localDiscovery.get(); enabled {
		t.Error("a rejected body changed the switch")
	}
}

// A managed server treats the switch as host-owned: PUT needs the
// control token, GET does not. And the state is what p2p.enabled
// allows, not what was asked.
func TestLocalDiscoveryManagedAndP2PDisabled(t *testing.T) {
	d := newUnauthorizedDeps(t)
	d.cfg.Mode = config.ModeManaged
	d.controlToken = "tok"
	off := false
	d.cfg.P2P.Enabled = &off
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPut, "/v1/local-discovery", `{"enabled":true}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PUT without the control token: want 403, got %d %s", rec.Code, rec.Body.String())
	}
	if _, stated := d.localDiscovery.get(); stated {
		t.Error("a refused PUT changed the switch")
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/local-discovery", ""); rec.Code != http.StatusOK {
		t.Errorf("GET without the control token: want 200, got %d %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPut, "/v1/local-discovery", strings.NewReader(`{"enabled":true}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(api.ControlTokenHeader, "tok")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT with the control token: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var res api.LocalDiscoveryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Enabled {
		t.Error("PUT true reports on while p2p.enabled is off")
	}
	if enabled, stated := d.localDiscovery.get(); !stated || !enabled {
		t.Error("the statement itself must still be recorded for a later boot")
	}
}

// With an engine live the switch reaches the SDK at once, and a
// statement made while an engine boots reaches that engine as soon as
// its SDK is open.
func TestLocalDiscoveryAppliesToLiveEngine(t *testing.T) {
	d, cleanup := newTestDeps(t)
	defer cleanup()
	e := buildEcho(d)

	if !d.sdk.LocalDiscoveryEnabled() {
		t.Fatal("test engine must start with discovery on")
	}
	rec := doJSON(t, e, http.MethodPut, "/v1/local-discovery", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if d.sdk.LocalDiscoveryEnabled() {
		t.Error("PUT false did not reach the live engine")
	}
	rec = doJSON(t, e, http.MethodGet, "/v1/local-discovery", "")
	var res api.LocalDiscoveryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Enabled {
		t.Error("GET reports the engine as on after PUT false")
	}

	// During a boot: not ready, but the SDK is open. The statement goes
	// to it directly rather than waiting for the rest of the boot.
	d.ready.Store(false)
	d.bootingSDK.Store(d.sdk)
	rec = doJSON(t, e, http.MethodPut, "/v1/local-discovery", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT during boot: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if !d.sdk.LocalDiscoveryEnabled() {
		t.Error("statement during boot did not reach the booting SDK")
	}
	// Publish: the hook re-applies whatever is recorded.
	d.bootingSDK.Store(nil)
	d.localDiscovery.set(false)
	d.ready.Store(true)
	d.applyLocalDiscovery()
	if d.sdk.LocalDiscoveryEnabled() {
		t.Error("statement recorded before publish was not applied")
	}
}

// The host's statement outranks the config default in the SDK config of
// the next boot, and the config default resolves per platform and mode.
func TestLocalDiscoveryBootConfig(t *testing.T) {
	cfg := config.Defaults()
	if !cfg.LocalDiscoveryEnabled() {
		t.Error("standalone default must be on")
	}
	cfg.Mode = config.ModeManaged
	if got, want := cfg.LocalDiscoveryEnabled(), runtime.GOOS != "darwin"; got != want {
		t.Errorf("managed default = %v on %s, want %v", got, runtime.GOOS, want)
	}
	on := true
	cfg.P2P.LocalDiscovery = &on
	if !cfg.LocalDiscoveryEnabled() {
		t.Error("explicit true must win over the platform default")
	}

	d := newUnauthorizedDeps(t)
	if got := d.bootConfig().P2P.LocalDiscovery; got != d.cfg.P2P.LocalDiscovery {
		t.Errorf("boot config with no statement = %v, want the process config's %v", got, d.cfg.P2P.LocalDiscovery)
	}
	d.localDiscovery.set(false)
	if got := d.bootConfig(); got.P2P.LocalDiscovery == nil || got.LocalDiscoveryEnabled() {
		t.Error("host statement false must reach the boot config as off")
	}
	d.localDiscovery.set(true)
	if !d.bootConfig().LocalDiscoveryEnabled() {
		t.Error("host statement true must reach the boot config as on")
	}
}
