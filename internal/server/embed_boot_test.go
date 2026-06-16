package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

// TestEmbed_BootAndServe is the IOS-6169 de-risking spike (0.1 + 0.4): it
// proves the server is callable HEADLESS — no cobra — via RunWithListener
// with an ephemeral port (127.0.0.1:0) and Index.Embedder="none", that the
// onListen callback hands back the resolved port WITHOUT parsing stdout,
// that a real /v1 read route serves over the bound socket after an account
// boots via POST /v1/auth, and that the no-embedder path is genuinely
// FTS-only (NewEmbedder returns a true-nil Embedder, HasEmbedder()==false).
//
// Needs the staging nodeconf like every other SDK-booting test here; the
// staging *network* being unreachable is fine — boot completes offline,
// only peer sync is degraded (exactly the cold-launch path on a device).
func TestEmbed_BootAndServe(t *testing.T) {
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}

	// --- Sub-check A: the "none" embedder path is genuinely FTS-only. ---
	// This is the typed-nil trap guard: NewEmbedder must return a true-nil
	// Embedder (not a typed-nil boxed into the interface) so HasEmbedder()
	// reports false. Asserted directly because the running engine's indexer
	// is not reachable from outside server.Run.
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
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Listen.Addr = "127.0.0.1:0" // ask the kernel for an ephemeral port
	cfg.Index.Enabled = true        // indexer runs, but...
	cfg.Index.Embedder = "none"     // ...FTS-only (no llama, no model download)
	cfg.Network.NodeconfPath = stagingPath

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- RunWithListener(ctx, cfg, func(addr string) { addrCh <- addr })
	}()

	var boundAddr string
	select {
	case boundAddr = <-addrCh:
	case err := <-runErr:
		t.Fatalf("Run returned before listening: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for onListen (LISTENING)")
	}
	if boundAddr == "" || strings.HasSuffix(boundAddr, ":0") {
		t.Fatalf("onListen did not resolve an ephemeral port: %q", boundAddr)
	}
	base := "http://" + boundAddr

	// Before auth the engine is unauthorized: /v1/spaces returns 401.
	if code, _ := httpGet(t, base+"/v1/spaces"); code != http.StatusUnauthorized {
		t.Fatalf("pre-auth GET /v1/spaces: want 401, got %d", code)
	}

	// Boot a fresh account over the real socket.
	code, body := httpPost(t, base+"/v1/auth", `{}`)
	if code != http.StatusOK {
		t.Fatalf("POST /v1/auth over socket: %d %s", code, body)
	}
	var authResp api.AuthResponse
	if err := json.Unmarshal([]byte(body), &authResp); err != nil {
		t.Fatal(err)
	}
	if authResp.AccountId == "" || !authResp.Created || authResp.Mnemonic == "" {
		t.Fatalf("generate reply: %+v", authResp)
	}

	// The verified real read route (probe-any-api: GET /v1/spaces ->
	// api.SpaceListResponse) serves over the bound socket. A fresh account
	// has no spaces yet: a 200 with an empty list is the real authorized
	// flow end to end.
	code, body = httpGet(t, base+"/v1/spaces")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/spaces after auth: %d %s", code, body)
	}
	var spaces api.SpaceListResponse
	if err := json.Unmarshal([]byte(body), &spaces); err != nil {
		t.Fatalf("decode SpaceListResponse: %v (body=%s)", err, body)
	}
	t.Logf("boot-and-serve OK: bound=%s account=%s spaces=%d (FTS-only, Embedder==nil)",
		boundAddr, authResp.AccountId, len(spaces.Spaces))

	// Graceful shutdown: cancel ctx, Run must return nil.
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func httpGet(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func httpPost(t *testing.T, url, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
