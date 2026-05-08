// Single-binary SSE coverage. internal/server/handlers_subscribe_test.go
// drives the same code paths inside the test process via httptest;
// these tests boot the actual `any` binary and stream over a real TCP
// socket. Catches transport-level regressions: chunked encoding, the
// flush hook on echo's response writer, X-Accel-Buffering, and the
// graceful shutdown handshake (the SDK's Subscription.Close lifecycle
// crossing a real listener instead of an in-memory one).
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// TestE2E_SubscribeBinary opens an SSE stream against the running
// binary, drives a CRDT change, and asserts that the change shows up
// as a `changes` frame. Then cancels the client and confirms the
// stream goroutine exits.
func TestE2E_SubscribeBinary(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr
	cl := client.New(addr, 0)

	// Setup space + object so subscribe has something to attach to.
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"sub-binary"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	frames := make(chan client.SSEFrame, 16)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- cl.StreamSubscribeObject(streamCtx, sp.Id, obj.ObjectId, "objects",
			func(f client.SSEFrame) error {
				select {
				case frames <- f:
				case <-streamCtx.Done():
				}
				return nil
			})
	}()

	first := waitFrameWithTimeout(t, frames, 10*time.Second)
	if first.Event != "ready" {
		t.Fatalf("first frame = %q, want ready (data=%s)", first.Event, first.Data)
	}

	// Drive a $set on the object's `objects` row. The firehose dataset
	// is `objects`, and an upsert against the same id with new fields
	// is enough to fire the per-object subscription too.
	body := fmt.Sprintf(
		`{"objectId":%q,"dataset":"objects","records":[{"id":%q,"upsert":true,"ops":[{"type":"$set","path":"","value":{"x":1}}]}]}`,
		obj.ObjectId, obj.ObjectId,
	)
	mustStatus(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/modify",
		body, http.StatusOK)

	got := waitFrameWithTimeout(t, frames, 10*time.Second)
	if got.Event != "changes" {
		t.Fatalf("second frame = %q, want changes (data=%s)", got.Event, got.Data)
	}
	var batch []api.SubscribeEvent
	if err := json.Unmarshal(got.Data, &batch); err != nil {
		t.Fatalf("decode changes: %v body=%s", err, got.Data)
	}
	if len(batch) == 0 {
		t.Fatal("changes batch empty")
	}
	if batch[0].SpaceId != sp.Id || batch[0].ObjectId != obj.ObjectId || batch[0].Dataset != "objects" {
		t.Errorf("event tuple = %+v, want (%s,%s,objects)", batch[0], sp.Id, obj.ObjectId)
	}

	streamCancel()
	select {
	case err := <-streamErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			// http.Client cancellations sometimes surface as the
			// transport's "context canceled" string rather than the
			// sentinel; either is fine for a clean exit.
			if !isCanceled(err) {
				t.Errorf("stream err = %v, want clean cancel", err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not exit after cancel")
	}
}

// TestE2E_SubscribeBinaryShutdown opens an SSE stream and then triggers
// /v1/shutdown. The handler should emit a terminal `closed` frame
// with reason=server_shutdown so a thin client can distinguish a
// reconnectable close from a hard transport error.
func TestE2E_SubscribeBinaryShutdown(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	// Don't `defer srv.stop` — this test drives shutdown explicitly,
	// and a stale stop() call after waitExit would race.
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr
	cl := client.New(addr, 0)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"sub-shutdown"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	frames := make(chan client.SSEFrame, 16)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- cl.StreamSubscribeObject(streamCtx, sp.Id, obj.ObjectId, "objects",
			func(f client.SSEFrame) error {
				select {
				case frames <- f:
				case <-streamCtx.Done():
				}
				return nil
			})
	}()

	if first := waitFrameWithTimeout(t, frames, 10*time.Second); first.Event != "ready" {
		t.Fatalf("first frame = %q, want ready", first.Event)
	}

	// Trigger shutdown. The handler is supposed to flush a `closed`
	// frame before the goroutine releases streamsWG and the listener
	// tears down. A 10s timeout matches the server's drain budget.
	mustStatus(t, http.MethodPost, base+"/v1/shutdown", "", http.StatusNoContent)

	got := waitFrameWithTimeout(t, frames, 12*time.Second)
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

	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit cleanly: %v\n%s", err, srv.output())
	}
	select {
	case <-streamErr:
	case <-time.After(5 * time.Second):
		t.Errorf("stream goroutine did not exit after server stop")
	}
}

// waitFrameWithTimeout pulls one frame from ch with a deadline. The
// channel-based read keeps tests responsive and turns missed events
// into immediate, labeled failures rather than silent hangs.
func waitFrameWithTimeout(t *testing.T, ch <-chan client.SSEFrame, d time.Duration) client.SSEFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(d):
		t.Fatalf("timed out waiting %s for SSE frame", d)
		return client.SSEFrame{}
	}
}

// isCanceled is a string-based fallback for http.Client cancellations
// that don't unwrap to context.Canceled. The transport sometimes
// surfaces its own "request canceled" message; treating those as
// clean exits keeps the test from flapping on mid-stream cancels.
func isCanceled(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "canceled") ||
		strings.Contains(msg, "EOF")
}
