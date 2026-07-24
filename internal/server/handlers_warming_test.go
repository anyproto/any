package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// TestDeferWarmup_ServesDuringWarmup boots deps with deferWarmup on
// and pins the contract: SDK-backed local reads serve as soon as boot
// returns (warmup may still be running), health reports the warming
// flag consistently, and the flag settles to false once WarmupDone
// fires. Warming==true right after boot is deliberately not asserted —
// a fresh account has nothing to eager-load and can finish instantly;
// the held-open case is pinned SDK-side.
func TestDeferWarmup_ServesDuringWarmup(t *testing.T) {
	d, teardown := newTestDepsCfg(t, func(cfg *config.Config) {
		cfg.DeferWarmup = true
	})
	defer teardown()
	e := buildEcho(d)

	// Local reads serve immediately — no waiting on the sync warmup.
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/spaces during warmup: %d %s", rec.Code, rec.Body.String())
	}

	select {
	case <-d.sdk.WarmupDone():
	case <-time.After(2 * time.Minute):
		t.Fatal("warmup did not complete")
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/health: %d %s", rec.Code, rec.Body.String())
	}
	var h api.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.Warming {
		t.Error("warming must be false after WarmupDone")
	}
}

// TestDeferWarmup_CloseWithoutWaiting tears the engine down right
// after a deferred boot, without awaiting WarmupDone — the SDK's
// Close must cancel and join any in-flight warmup (best-effort here;
// the deterministic mid-warmup case is pinned by the SDK's own tests).
func TestDeferWarmup_CloseWithoutWaiting(t *testing.T) {
	_, teardown := newTestDepsCfg(t, func(cfg *config.Config) {
		cfg.DeferWarmup = true
	})
	teardown()
}

// TestWarming_WireShape pins the warming=true observable on /v1/health
// and GET /v1/auth — the live-boot test above can't (a fresh account
// has nothing to eager-load and may finish warmup before the first
// request lands), so the sdkWarming seam stands in for a held-open
// warmup. No SDK, no staging gate. Also pins the unauthorized side:
// ready=false must force warming=false on the wire even when the
// probe would report true, so an inverted ready gate fails here.
func TestWarming_WireShape(t *testing.T) {
	getWarming := func(d *deps) (health api.HealthResponse, auth api.AuthStatusResponse) {
		t.Helper()
		e := buildEcho(d)
		rec := doJSON(t, e, http.MethodGet, "/v1/health", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/health: %d %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		rec = doJSON(t, e, http.MethodGet, "/v1/auth", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/auth: %d %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &auth); err != nil {
			t.Fatal(err)
		}
		return health, auth
	}

	// Authorized, warmup in flight.
	d := &deps{
		account:    "acc-warming",
		startedAt:  time.Now().UTC(),
		root:       t.TempDir(),
		sdkWarming: func() bool { return true },
	}
	d.ready.Store(true)
	h, a := getWarming(d)
	if !h.Warming || h.Account != "acc-warming" {
		t.Errorf("health during warmup: warming=%v account=%q, want true/acc-warming", h.Warming, h.Account)
	}
	if !a.Authorized || !a.Warming {
		t.Errorf("auth during warmup: authorized=%v warming=%v, want true/true", a.Authorized, a.Warming)
	}

	// Unauthorized: warming must be false regardless of the probe.
	d = &deps{
		startedAt:  time.Now().UTC(),
		root:       t.TempDir(),
		sdkWarming: func() bool { return true },
	}
	h, a = getWarming(d)
	if h.Warming || h.Account != "" {
		t.Errorf("unauthorized health: warming=%v account=%q, want false/empty", h.Warming, h.Account)
	}
	if a.Authorized || a.Warming {
		t.Errorf("unauthorized auth: authorized=%v warming=%v, want false/false", a.Authorized, a.Warming)
	}
}
