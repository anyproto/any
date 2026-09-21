package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// set reports a CHANGE, not the new value: it decides whether the SDK is
// nudged, and nudging on every restatement would tear down a healthy
// discovery session each time the host repeats itself. The first
// statement always counts, whatever the config default was.
func TestLocalDiscoverySwitchSetReportsChange(t *testing.T) {
	var s localDiscoverySwitch

	if _, stated := s.get(); stated {
		t.Fatal("fresh switch reports a statement")
	}
	if !s.set(true) {
		t.Error("first statement reported no change")
	}
	if s.set(true) {
		t.Error("restating on reported a change")
	}
	if !s.set(false) {
		t.Error("switching off reported no change")
	}
	if s.set(false) {
		t.Error("restating off reported a change")
	}
	if enabled, stated := s.get(); !stated || enabled {
		t.Errorf("get = (%v, %v), want (false, true)", enabled, stated)
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

	rec = doJSON(t, e, http.MethodPut, "/v1/local-discovery", `{"enabled":"yes"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad body: want 400, got %d %s", rec.Code, rec.Body.String())
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
