package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/config"
)

// TestServer_UIIndex sanity-checks the embedded HTML harness under the
// default config: served as HTML at GET /ui (and /ui/) without the SDK
// booted — debug surface, no API calls. Doubles as the regression guard
// that a standalone `any run` (default config) still serves /ui after
// IOS-116 gated the mount behind cfg.WebUI.Enabled (acceptance #3).
func TestServer_UIIndex(t *testing.T) {
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1), cfg: config.Defaults()}
	e := buildEcho(d)

	for _, path := range []string{"/ui", "/ui/"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q, want text/html…", path, ct)
		}
		body := rec.Body.String()
		// The page is the embedded app shell — assert on a few stable
		// landmarks (title, sidebar/netlog roots, an API path the JS calls).
		for _, want := range []string{"<title>any</title>", `id="tree"`, `id="space-select"`, `id="netlog"`, "/v1/spaces"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: body missing %q", path, want)
			}
		}
	}
}

// TestServer_UIIndex_Disabled proves the headless gate: with
// cfg.WebUI.Enabled=false (what embedded.Start sets for every
// app-embedded boot), the /ui routes are never mounted, so both /ui and
// /ui/ 404 — IOS-116, acceptance #1's app-boot side at the route layer.
func TestServer_UIIndex_Disabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.WebUI.Enabled = false
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1), cfg: cfg}
	e := buildEcho(d)

	for _, path := range []string{"/ui", "/ui/"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, r)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (web ui disabled)", path, rec.Code)
		}
	}
}
