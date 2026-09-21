package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	sdkp2p "github.com/anyproto/any-sync-sdk/p2p"

	"github.com/anyproto/any/internal/api"
)

// Unknown, not Possible: the gate has an opinion about the permission
// and none about interfaces, and answering Possible would override the
// SDK's own check on a machine with no usable interface.
func TestLocalNetworkGateProbe(t *testing.T) {
	var g localNetworkGate

	if got := g.probe(context.Background(), 0); got != sdkp2p.PossibilityUnknown {
		t.Errorf("open gate probes %v, want unknown", got)
	}
	g.set(false)
	if got := g.probe(context.Background(), 0); got != sdkp2p.PossibilityRestricted {
		t.Errorf("closed gate probes %v, want restricted", got)
	}
	g.set(true)
	if got := g.probe(context.Background(), 0); got != sdkp2p.PossibilityUnknown {
		t.Errorf("reopened gate probes %v, want unknown", got)
	}
}

// set reports a CHANGE, not the new value — it is what decides whether
// the SDK is nudged, and nudging on every restatement would tear down a
// healthy discovery session each time the host repeats itself.
func TestLocalNetworkGateSetReportsChange(t *testing.T) {
	var g localNetworkGate

	if g.set(true) {
		t.Error("enabling an already-open gate reported a change")
	}
	if !g.set(false) {
		t.Error("closing an open gate reported no change")
	}
	if g.set(false) {
		t.Error("re-closing a closed gate reported a change")
	}
	if !g.set(true) {
		t.Error("reopening a closed gate reported no change")
	}
}

func putLocalNetwork(t *testing.T, d *deps, body string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/p2p/local-network", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err := d.localNetworkSet(e.NewContext(req, rec)); err != nil {
		t.Fatalf("localNetworkSet: %v", err)
	}
	return rec
}

// The whole point of the endpoint is that it works with no engine: the
// host states its answer around auth, and the next SDK boot reads the
// gate on its first discovery cycle.
func TestLocalNetworkSetWithoutEngine(t *testing.T) {
	d := &deps{}

	rec := putLocalNetwork(t, d, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var res api.LocalNetworkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Enabled {
		t.Error("response says enabled after disabling")
	}
	if d.localNetwork().enabled() {
		t.Error("gate still open after disabling")
	}
	if got := d.localNetwork().probe(context.Background(), 0); got != sdkp2p.PossibilityRestricted {
		t.Errorf("probe = %v after disabling, want restricted", got)
	}

	if rec := putLocalNetwork(t, d, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("re-enable status = %d: %s", rec.Code, rec.Body.String())
	}
	if !d.localNetwork().enabled() {
		t.Error("gate still closed after re-enabling")
	}
}

// installLocalNetworkProbe runs before every engine boot, so it must
// point at the SAME gate each time — an answer given before logout has
// to survive the next boot.
func TestInstallLocalNetworkProbeKeepsTheAnswer(t *testing.T) {
	t.Cleanup(func() { sdkp2p.SetPossibilityProbe(nil) })
	d := &deps{}

	putLocalNetwork(t, d, `{"enabled":false}`)
	d.installLocalNetworkProbe()

	probe := sdkp2p.PossibilityProbe()
	if probe == nil {
		t.Fatal("no probe installed")
	}
	if got := probe(context.Background(), 0); got != sdkp2p.PossibilityRestricted {
		t.Errorf("installed probe = %v, want restricted", got)
	}

	// A second boot must not hand the SDK a fresh, open gate.
	d.installLocalNetworkProbe()
	if got := sdkp2p.PossibilityProbe()(context.Background(), 0); got != sdkp2p.PossibilityRestricted {
		t.Errorf("probe after a second install = %v, want restricted", got)
	}
}

func TestLocalNetworkSetRejectsBadBody(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/p2p/local-network", strings.NewReader(`{"enabled":"yes"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err := (&deps{}).localNetworkSet(e.NewContext(req, rec)); err != nil {
		t.Fatalf("localNetworkSet: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
