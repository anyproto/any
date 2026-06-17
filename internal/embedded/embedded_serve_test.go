package embedded

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

// TestBootAndServe is the IOS-6169 boot-and-serve coverage that the
// rename/split off ios-embed-library dropped (audit GAP-1). The other
// embedded tests only prove the socket is dialable; this one proves the
// HTTP engine actually serves a /v1 route after Start binds, fully
// offline, and that the embedded path is genuinely FTS-only (no
// embedder).
//
// Two sub-checks:
//
//   - The "none" embedder path is true-nil (HasEmbedder()==false). This
//     constructs the indexer directly via the same exported factory Start
//     configures (Embedder="none") — a true-nil Embedder, not a typed-nil
//     boxed into the interface — so the compiled-out llama.cpp embedder is
//     never reached. Asserted by construction (the running engine's
//     indexer is not reachable from this package), mirroring
//     internal/server/embed_boot_test.go on ios-embed-library.
//
//   - Headless boot-and-serve: Start binds an ephemeral port, then a real
//     GET /v1/health over the bound socket returns 200. /v1/health is a
//     meta route served in UNAUTHORIZED mode (no account booted on a fresh
//     data dir), so it needs no staging peers and runs fully offline — the
//     point is that the HTTP engine serves after boot. Unauthorized health
//     reports an empty account and a non-zero startedAt; fields that need
//     network are not asserted.
func TestBootAndServe(t *testing.T) {
	resetState(t)
	defer resetState(t)

	// --- Sub-check A: the "none" embedder path is genuinely FTS-only. ---
	emb, err := indexer.NewEmbedder(config.Index{Embedder: "none"}, t.TempDir(), "")
	if err != nil {
		t.Fatalf(`NewEmbedder("none"): %v`, err)
	}
	if emb != nil {
		t.Fatalf(`NewEmbedder("none") must be true-nil (FTS-only), got %T`, emb)
	}
	ix := indexer.New(nil, nil, nil, indexer.Options{Embedder: emb})
	if ix.HasEmbedder() {
		t.Fatal("HasEmbedder() must be false for the none/FTS-only embedder")
	}

	// --- Sub-check B: headless boot-and-serve over a real socket. ---
	addr, err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if addr == "" {
		t.Fatal("Start returned empty addr")
	}
	defer Stop(true)

	// Short timeout: the route is local, so a slow response is a bug, not
	// a network hiccup to wait out.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health over socket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/health: status %d, want 200", resp.StatusCode)
	}

	var h api.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("decode HealthResponse: %v", err)
	}
	// Unauthorized boot: no account, but the engine is up and stamped a
	// start time. (Account / network-dependent fields are not asserted.)
	if h.Account != "" {
		t.Errorf("unauthorized health account = %q, want empty", h.Account)
	}
	if h.StartedAt.IsZero() {
		t.Error("health startedAt is zero, want a boot timestamp")
	}
	t.Logf("boot-and-serve OK: bound=%s status=%q account=%q (FTS-only, Embedder==nil)",
		addr, h.Status, h.Account)
}
