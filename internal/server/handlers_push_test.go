package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/push"
)

// decodeErr unwraps the canonical error envelope.
func decodeErr(t *testing.T, body []byte) api.APIError {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, body)
	}
	return env.Error
}

// TestPush_Disabled: with no push service (deps.push == nil) every
// /v1/push endpoint answers 409 push.disabled. No SDK needed — the
// handlers gate before touching it.
func TestPush_Disabled(t *testing.T) {
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1)}
	d.ready.Store(true)
	e := buildEcho(d)

	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/v1/push/token", `{"platform":"ios","token":"tok"}`},
		{http.MethodGet, "/v1/push/token", ""},
		{http.MethodDelete, "/v1/push/token", ""},
		{http.MethodGet, "/v1/push/subscriptions", ""},
	}
	for _, tc := range cases {
		rec := doJSON(t, e, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s %s: status = %d, want 409; body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
			continue
		}
		if apiErr := decodeErr(t, rec.Body.Bytes()); apiErr.Code != "push.disabled" {
			t.Errorf("%s %s: code = %q, want push.disabled", tc.method, tc.path, apiErr.Code)
		}
	}
}

// TestPushError_GenericInternalMessage: the 500 fallback must never
// echo the underlying error — token-file failures are os.PathErrors
// carrying the absolute push-token.json path, and the envelope must
// not leak filesystem paths (the real error goes to the log).
func TestPushError_GenericInternalMessage(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/v1/push/token", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	leaky := &os.PathError{
		Op:   "open",
		Path: "/home/someone/.any/acc1/push-token.json",
		Err:  errors.New("permission denied"),
	}
	if err := pushError(c, leaky); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	apiErr := decodeErr(t, rec.Body.Bytes())
	if apiErr.Code != "internal" {
		t.Errorf("code = %q, want internal", apiErr.Code)
	}
	if apiErr.Message != "push operation failed" {
		t.Errorf("message = %q, want the generic \"push operation failed\"", apiErr.Message)
	}
	if body := rec.Body.String(); strings.Contains(body, "push-token.json") || strings.Contains(body, "/home/") {
		t.Errorf("envelope leaks the filesystem path: %s", body)
	}
}

// TestPushToken_Validation: body validation runs before the
// availability gate, so 400s surface even on a push-less server.
func TestPushToken_Validation(t *testing.T) {
	d := &deps{startedAt: time.Now().UTC(), shutdown: make(chan struct{}, 1)}
	d.ready.Store(true)
	e := buildEcho(d)

	cases := []struct {
		name, body, code string
	}{
		{"bad json", "{not json", "request.bad_json"},
		{"missing platform", `{"token":"tok"}`, "request.missing_field"},
		{"missing token", `{"platform":"ios"}`, "request.missing_field"},
		{"bad platform", `{"platform":"desktop","token":"tok"}`, "request.invalid_field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, "/v1/push/token", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if apiErr := decodeErr(t, rec.Body.Bytes()); apiErr.Code != tc.code {
				t.Errorf("code = %q, want %q", apiErr.Code, tc.code)
			}
		})
	}
}

// TestPushToken_Lifecycle exercises the token endpoints against a
// server whose config names a push node that isn't reachable (a
// loopback port nothing listens on). That is the designed degraded
// mode: the persist is durable and the forward retries in the
// background, so set/status/revoke all succeed locally.
func TestPushToken_Lifecycle(t *testing.T) {
	d, teardown := newTestDepsCfg(t, func(cfg *config.Config) {
		cfg.Push = config.Push{PeerId: "12D3KooWTestPushNode", Addrs: []string{"127.0.0.1:1"}}
	})
	defer teardown()
	e := buildEcho(d)

	// No token yet.
	rec := doJSON(t, e, http.MethodGet, "/v1/push/token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET token: %d %s", rec.Code, rec.Body.String())
	}
	var st api.PushTokenStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Registered || st.Platform != "" {
		t.Errorf("fresh status = %+v, want unregistered", st)
	}

	// Register.
	rec = doJSON(t, e, http.MethodPost, "/v1/push/token", `{"platform":"android","token":"fcm-abc"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST token: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, "/v1/push/token", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Registered || st.Platform != "android" {
		t.Errorf("status after set = %+v", st)
	}
	// The token file landed in the account dir.
	tokenPath := filepath.Join(d.root, "push-token.json")
	if _, err := os.Stat(tokenPath); err != nil {
		t.Errorf("token file missing: %v", err)
	}

	// A restarted service reloads the persisted token.
	p2 := push.New(d.sdk, d.root)
	p2.Start(d.shutdownCtx)
	if reg, platform := p2.TokenStatus(); !reg || platform != "android" {
		t.Errorf("reloaded status = (%v, %q), want (true, android)", reg, platform)
	}
	if err := p2.Close(); err != nil {
		t.Fatalf("close reloaded service: %v", err)
	}

	// Revoke.
	rec = doJSON(t, e, http.MethodDelete, "/v1/push/token", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE token: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, "/v1/push/token", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Registered {
		t.Errorf("status after revoke = %+v, want unregistered", st)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Errorf("token file should be gone, stat err = %v", err)
	}
}

// TestPush_SDKNotConfigured: a push service handle over an SDK opened
// WITHOUT a push node (a config mismatch bootEngine normally
// prevents) — every SDK-touching endpoint maps ErrPushNotConfigured
// onto the same 409 push.disabled.
func TestPush_SDKNotConfigured(t *testing.T) {
	d, teardown := newTestDeps(t) // SDK without config.Push
	defer teardown()
	d.push = push.New(d.sdk, d.root)
	defer func() { _ = d.push.Close() }()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodGet, "/v1/push/subscriptions", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("GET subscriptions: %d %s", rec.Code, rec.Body.String())
	}
	if apiErr := decodeErr(t, rec.Body.Bytes()); apiErr.Code != "push.disabled" {
		t.Errorf("code = %q, want push.disabled", apiErr.Code)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/push/token", `{"platform":"ios","token":"tok"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST token: %d %s", rec.Code, rec.Body.String())
	}
	if apiErr := decodeErr(t, rec.Body.Bytes()); apiErr.Code != "push.disabled" {
		t.Errorf("code = %q, want push.disabled", apiErr.Code)
	}
}
