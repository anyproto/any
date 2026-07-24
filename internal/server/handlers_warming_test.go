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
