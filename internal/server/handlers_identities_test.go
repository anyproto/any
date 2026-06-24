package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// TestServer_SpacesQuery_DatasetAllowlist guards the symkey leak: the
// tech-space index object hosts the `identities` directory whose rows
// carry a synced symKey (the profile-decryption secret). The generic
// space-list query/subscribe must refuse any dataset outside the closed
// {spaces, profile} allowlist. The guard runs before the SDK is touched,
// so a bare deps (no staging) exercises it.
func TestServer_SpacesQuery_DatasetAllowlist(t *testing.T) {
	d := &deps{}
	d.ready.Store(true)
	e := buildEcho(d)

	for _, path := range []string{"/v1/spaces/query", "/v1/spaces/query/subscribe"} {
		for _, ds := range []string{"identities", "bogus"} {
			rec := doJSON(t, e, http.MethodPost, path, `{"dataset":"`+ds+`"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s dataset=%s: status=%d, want 400; body=%s", path, ds, rec.Code, rec.Body.String())
			}
			if got := errEnvCode(t, rec.Body.Bytes()); got != "request.invalid_field" {
				t.Errorf("%s dataset=%s: code=%q, want request.invalid_field", path, ds, got)
			}
		}
	}
}

// TestServer_Identities_Reads exercises the directory GET endpoints
// against a live SDK. A fresh account has encountered no identities, so
// List is empty and Get on any id is a 404.
func TestServer_Identities_Reads(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodGet, "/v1/identities", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/identities status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.IdentitiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	rec = doJSON(t, e, http.MethodGet, "/v1/identities/never-encountered", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown identity status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "identity.not_found" {
		t.Errorf("code=%q, want identity.not_found", got)
	}
}

// TestServer_Identities_StreamLifecycle drives the directory subscribe
// SSE endpoint: observe `ready`, trip shutdown, observe `closed`. We
// don't force a directory change — those are network side effects.
func TestServer_Identities_StreamLifecycle(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	frames := make(chan client.SSEFrame, 16)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- cl.StreamIdentities(streamCtx, func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-streamCtx.Done():
			}
			return nil
		})
	}()

	if got := waitFrame(t, frames, 5*time.Second); got.Event != "ready" {
		t.Fatalf("first frame = %q, want ready (data=%s)", got.Event, got.Data)
	}

	d.cancelShutdown()

	got := waitFrame(t, frames, 5*time.Second)
	if got.Event != "closed" {
		t.Fatalf("post-shutdown frame = %q, want closed (data=%s)", got.Event, got.Data)
	}
	var closed api.SubscribeClosed
	if err := json.Unmarshal(got.Data, &closed); err != nil {
		t.Fatalf("decode closed: %v", err)
	}
	if closed.Reason != api.SubscribeClosedServerShutdown {
		t.Errorf("reason = %q, want %q", closed.Reason, api.SubscribeClosedServerShutdown)
	}

	select {
	case err := <-streamErr:
		if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("stream err = %v, want nil or context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit after server shutdown")
	}
}
