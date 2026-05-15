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

// TestServer_SyncStatusReads exercises the two GET endpoints. The SDK
// returns a sensible Unknown for both unknown spaces (via SyncStatus
// rollup) and unknown objects, so we assert shape, not error codes.
func TestServer_SyncStatusReads(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// Bring up one space so /sync-status has somewhere to report on.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SyncStatusTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	t.Run("space", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/sync-status", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var got api.SpaceSyncStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SpaceId != sp.Id {
			t.Errorf("spaceId = %q, want %q", got.SpaceId, sp.Id)
		}
		switch got.State {
		case "unknown", "offline", "syncing", "synced", "error":
		default:
			t.Errorf("state = %q, want one of the five enum values", got.State)
		}
	})

	t.Run("object unknown id returns state=unknown", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodGet,
			"/v1/spaces/"+sp.Id+"/sync-status/objects/never-existed", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var got api.ObjectSyncStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ObjectId != "never-existed" {
			t.Errorf("objectId echoed = %q, want never-existed", got.ObjectId)
		}
		if got.State != "unknown" {
			t.Errorf("state = %q, want unknown", got.State)
		}
	})

	t.Run("peers still 501", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/sync-status/peers", "")
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d, want 501; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// TestServer_SyncStatusAccount_StreamLifecycle drives the account-wide
// /v1/sync-status/subscribe SSE endpoint: observe the `ready` frame,
// trip server shutdown, observe `closed`. We don't try to force a
// state transition mid-test — the SDK is the source of those, and
// they're indirect side effects of network activity we don't control
// in a local test.
func TestServer_SyncStatusAccount_StreamLifecycle(t *testing.T) {
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
		streamErr <- cl.StreamSyncStatusAccount(streamCtx, func(f client.SSEFrame) error {
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

// TestServer_SyncStatusObject_StreamLifecycle: same `ready` → shutdown
// → `closed` round-trip on the per-object subscribe endpoint.
func TestServer_SyncStatusObject_StreamLifecycle(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spaceId, _, objectId := setupSubscribeFixture(t, e)

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	frames := make(chan client.SSEFrame, 16)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- cl.StreamSyncStatusObject(streamCtx, spaceId, objectId, func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-streamCtx.Done():
			}
			return nil
		})
	}()

	if got := waitFrame(t, frames, 5*time.Second); got.Event != "ready" {
		t.Fatalf("first frame = %q, want ready", got.Event)
	}

	d.cancelShutdown()

	got := waitFrame(t, frames, 5*time.Second)
	if got.Event != "closed" {
		t.Fatalf("post-shutdown frame = %q, want closed", got.Event)
	}

	select {
	case <-streamErr:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit after server shutdown")
	}
}
