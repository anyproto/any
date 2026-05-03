package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestServer_LegacyUIIndex sanity-checks the legacy embedded HTML
// harness, now mounted at /ui-legacy (the new SPA owns /ui — see
// docs/08-app-architecture.md and webapp.go).
func TestServer_LegacyUIIndex(t *testing.T) {
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1)}
	e := buildEcho(d)

	r := httptest.NewRequest(http.MethodGet, "/ui-legacy", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html…", ct)
	}
	body := rec.Body.String()
	// The page is the legacy harness — assert on stable landmarks.
	for _, want := range []string{"<title>any</title>", `id="tree"`, `id="space-select"`, `id="netlog"`, "/v1/spaces"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestServer_WebappIndex confirms /ui serves the SPA index. With no
// `make web` having run, that's the placeholder; with one having run,
// the real Vite-built index. Either way: 200 + text/html.
func TestServer_WebappIndex(t *testing.T) {
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1)}
	e := buildEcho(d)

	for _, path := range []string{"/ui", "/ui/", "/ui/some-deep-route"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
			continue
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q, want text/html…", path, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Errorf("%s: Cache-Control = %q, want to contain no-cache", path, cc)
		}
	}
}
