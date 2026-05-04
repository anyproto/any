package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestServer_UIIndex sanity-checks the embedded HTML harness: served as
// HTML at GET /ui (and /ui/) without the SDK booted — debug surface,
// no API calls.
func TestServer_UIIndex(t *testing.T) {
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1)}
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
