package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1:2046":          true,
		"127.0.0.1":               true,
		"127.0.0.2:2046":          true,
		"localhost:5173":          true,
		"LOCALHOST:2046":          true,
		"[::1]:2046":              true,
		"[::ffff:127.0.0.1]:2046": true,
		"":                        true,
		"evil.example:2046":       false,
		"127.0.0.1.nip.io:2046":   false,
		"localhost.:2046":         false,
		"api.localhost:2046":      false,
		"192.168.1.10:2046":       false,
		"0.0.0.0:2046":            false,
		"[::]:2046":               false,
	} {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// A browser reaches the loopback listener under any name that resolves
// to 127.0.0.1, and a page under that name is same-origin with the API.
func TestLoopbackHostGuard(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	for _, host := range []string{"127.0.0.1:2046", "localhost:5173", "[::1]:2046"} {
		req := newTestRequest(http.MethodGet, "/v1/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q: status %d, want 200", host, rec.Code)
		}
	}
	for _, host := range []string{"evil.example:2046", "127.0.0.1.nip.io:2046"} {
		req := newTestRequest(http.MethodGet, "/v1/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assertRefused(t, rec, "request.host_not_allowed")
	}
}

// CORS hides only the response: a text/plain POST from any website is a
// simple request, needs no preflight, and reached the handler.
func TestCrossSiteWriteGuard(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	join := func(origin string) *httptest.ResponseRecorder {
		req := newTestRequest(http.MethodPost, "/v1/spaces/join", strings.NewReader(`{"inviteToken":"x"}`))
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	for _, origin := range []string{"https://evil.example", "null", "http://127.0.0.1.nip.io:2046", "file://"} {
		assertRefused(t, join(origin), "request.origin_not_allowed")
	}
	// Past the guard the handler answers for itself: the token is garbage.
	for _, origin := range []string{"", desktopOrigin, "http://tauri.localhost", "https://127.0.0.1:5173", "http://localhost:2046"} {
		if rec := join(origin); rec.Code == http.StatusForbidden {
			t.Errorf("Origin %q: refused (%s), want the handler's answer", origin, rec.Body.String())
		}
	}

	req := newTestRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cross-site GET: status %d, want 200", rec.Code)
	}
}

func assertRefused(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, code)
}
