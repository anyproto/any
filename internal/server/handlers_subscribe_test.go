package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// TestServer_SubscribeObject_Lifecycle drives the full SSE lifecycle
// against a real echo server: open the stream, observe the `ready`
// frame, trigger a CRDT change, observe the `changes` frame, then
// cancel and confirm the stream cleans up.
//
// Uses httptest.NewServer (not the buffered ResponseRecorder helper
// other tests use) because SSE relies on flushed mid-response writes
// — the recorder buffers everything until WriteHeader, which kills
// streaming.
func TestServer_SubscribeObject_Lifecycle(t *testing.T) {
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
		streamErr <- cl.StreamSubscribeObject(streamCtx, spaceId, objectId, "objects", func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-streamCtx.Done():
			}
			return nil
		})
	}()

	if got := waitFrame(t, frames, 5*time.Second); got.Event != "ready" {
		t.Fatalf("first frame event = %q, want ready (data=%s)", got.Event, got.Data)
	}

	// Drive a CRDT change and expect a `changes` frame to land.
	body := fmt.Sprintf(
		`{"objectId":%q,"dataset":"objects","records":[{"id":%q,"upsert":true,"ops":[{"type":"$set","path":"","value":{"x":1}}]}]}`,
		objectId, objectId,
	)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/modify", body)
	if rec.Code/100 != 2 {
		t.Fatalf("modify: %d %s", rec.Code, rec.Body.String())
	}

	got := waitFrame(t, frames, 5*time.Second)
	if got.Event != "changes" {
		t.Fatalf("second frame event = %q, want changes (data=%s)", got.Event, got.Data)
	}
	var batch []api.SubscribeEvent
	if err := json.Unmarshal(got.Data, &batch); err != nil {
		t.Fatalf("decode changes: %v body=%s", err, got.Data)
	}
	if len(batch) == 0 {
		t.Fatalf("changes batch empty, want >=1")
	}
	if batch[0].SpaceId != spaceId || batch[0].ObjectId != objectId || batch[0].Dataset != "objects" {
		t.Errorf("event tuple = %+v, want (%s,%s,objects)", batch[0], spaceId, objectId)
	}

	// Root-level $set must render as `"path": []` on the wire, not
	// `"path": null` — clients dedup on op.path and an array is the
	// only documented wire shape.
	var rawEvents []struct {
		Records []struct {
			Ops []struct {
				Path json.RawMessage `json:"path"`
			} `json:"ops"`
		} `json:"records"`
	}
	if err := json.Unmarshal(got.Data, &rawEvents); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, ev := range rawEvents {
		for _, rec := range ev.Records {
			for _, op := range rec.Ops {
				if string(op.Path) == "null" {
					t.Errorf("op path emitted as JSON null; want [] (got=%s)", op.Path)
				}
			}
		}
	}

	streamCancel()
	select {
	case err := <-streamErr:
		if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("stream err = %v, want nil or context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit after cancel")
	}
}

// TestServer_SubscribeProperties_Firehose checks that the firehose
// fires for writes to the per-space `objects` dataset regardless of
// which object changed.
func TestServer_SubscribeProperties_Firehose(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spaceId, typeId, _ := setupSubscribeFixture(t, e)

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	frames := make(chan client.SSEFrame, 16)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- cl.StreamSubscribeProperties(streamCtx, spaceId, func(f client.SSEFrame) error {
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

	// Creating a fresh object lands a row in the per-space `objects`
	// collection — exactly what the firehose subscribes to.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects",
		fmt.Sprintf(`{"types":[%q]}`, typeId))
	if rec.Code/100 != 2 {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}

	got := waitFrame(t, frames, 5*time.Second)
	if got.Event != "changes" {
		t.Fatalf("second frame event = %q, want changes (data=%s)", got.Event, got.Data)
	}
	var batch []api.SubscribeEvent
	if err := json.Unmarshal(got.Data, &batch); err != nil {
		t.Fatalf("decode changes: %v", err)
	}
	if len(batch) == 0 {
		t.Fatal("changes batch empty")
	}
	for _, ev := range batch {
		if ev.Dataset != "objects" {
			t.Errorf("firehose delivered non-objects dataset %q", ev.Dataset)
		}
	}

	// A fresh object materialises a new `objects` row — the event
	// record carries created:true (the SDK's _ver.id creation marker,
	// surfaced as a flag, never as a wire op).
	sawCreated := false
	for _, ev := range batch {
		for _, r := range ev.Records {
			if r.Created {
				sawCreated = true
			}
			if r.Created && r.Deleted {
				t.Errorf("record %s has both created and deleted set", r.Id)
			}
			for _, op := range r.Ops {
				if len(op.Path) > 0 && strings.HasPrefix(op.Path[0], "_") {
					t.Errorf("record %s ships protocol op path %v; _-prefixed paths must not be on the wire", r.Id, op.Path)
				}
			}
		}
	}
	if !sawCreated {
		t.Errorf("object-create firehose event missing created:true record")
	}

	streamCancel()
	<-streamErr
}

// TestServer_Subscribe_ShutdownEmitsClosed verifies that tripping
// the server-wide shutdownCtx makes the handler emit a terminal
// `closed` frame with reason=server_shutdown before exiting.
func TestServer_Subscribe_ShutdownEmitsClosed(t *testing.T) {
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
		streamErr <- cl.StreamSubscribeObject(streamCtx, spaceId, objectId, "objects", func(f client.SSEFrame) error {
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

	// Trip the shutdown signal. The handler should emit `closed` and
	// drop streamsWG so the teardown path returns promptly.
	d.cancelShutdown()

	got := waitFrame(t, frames, 5*time.Second)
	if got.Event != "closed" {
		t.Fatalf("post-shutdown frame = %q, want closed", got.Event)
	}
	var closed api.SubscribeClosed
	if err := json.Unmarshal(got.Data, &closed); err != nil {
		t.Fatalf("decode closed: %v", err)
	}
	if closed.Reason != api.SubscribeClosedServerShutdown {
		t.Errorf("reason = %q, want %q", closed.Reason, api.SubscribeClosedServerShutdown)
	}

	select {
	case <-streamErr:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit after server shutdown signal")
	}
}

// waitFrame pulls one frame from ch with a deadline. Tests that
// silently hang on a missed event are painful; a labeled timeout
// turns those into immediate failures.
func waitFrame(t *testing.T, ch <-chan client.SSEFrame, d time.Duration) client.SSEFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(d):
		t.Fatal("timed out waiting for SSE frame")
		return client.SSEFrame{}
	}
}

// setupSubscribeFixture creates a space + type + object so the
// streaming tests have something to subscribe to. Drives every step
// through HTTP to keep parity with the rest of the test suite.
func setupSubscribeFixture(t *testing.T, e http.Handler) (spaceId, typeId, objectId string) {
	t.Helper()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SubTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	spaceId = sp.Id

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"Movie","description":"film"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	typeId = typeResp.TypeId

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects",
		fmt.Sprintf(`{"types":[%q]}`, typeId))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var objResp api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &objResp); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	objectId = objResp.ObjectId
	return spaceId, typeId, objectId
}
