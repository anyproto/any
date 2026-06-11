package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Desktop-shell CORS contract (docs/plans/20260611-desktop-shell-server-contract.md):
// the webview origins must pass browser CORS on REST and SSE responses;
// everything without an Origin header stays untouched.

const desktopOrigin = "tauri://localhost"

func TestCORS_PreflightEchoesDesktopOrigin(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	req := httptest.NewRequest(http.MethodOptions, "/v1/health", nil)
	req.Header.Set("Origin", desktopOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != desktopOrigin {
		t.Fatalf("preflight ACAO = %q, want %q (status %d)", got, desktopOrigin, rec.Code)
	}
}

func TestCORS_GetCarriesACAOForDesktopOrigin(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "http://tauri.localhost")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://tauri.localhost" {
		t.Fatalf("ACAO = %q, want the Windows webview origin", got)
	}
}

func TestCORS_NoOriginGetsNoACAO(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO unexpectedly set for origin-less request: %q", got)
	}
}

func TestCORS_UnlistedOriginGetsNoACAO(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO leaked to an unlisted origin: %q", got)
	}
}
