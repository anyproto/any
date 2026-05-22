// Multipeer subscription coverage. The single-process tests in
// internal/server/handlers_subscribe_test.go exercise the SSE wire
// format against locally-issued writes; what they cannot prove is
// that an event lands in the SSE stream when the originating change
// arrives via headsync from a remote peer rather than from a local
// HTTP write.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// sseRecorder accumulates frames off a peer's SSE stream onto an
// in-memory slice so tests can poll with `containsEvent` without
// blocking on a channel read. The mutex covers .frames; .err is set
// once when the stream loop exits and is never mutated concurrently
// with the loop itself.
type sseRecorder struct {
	mu     sync.Mutex
	frames []client.SSEFrame
	err    error
	done   chan struct{}
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{done: make(chan struct{})}
}

func (r *sseRecorder) push(f client.SSEFrame) error {
	r.mu.Lock()
	r.frames = append(r.frames, f)
	r.mu.Unlock()
	return nil
}

func (r *sseRecorder) snapshot() []client.SSEFrame {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]client.SSEFrame, len(r.frames))
	copy(out, r.frames)
	return out
}

// startSubscribeProperties opens the firehose against the peer and
// returns a recorder + cancel function. Returns once the stream goroutine
// is running but does NOT block on the `ready` frame — callers should
// poll for it with `waitFrameEvent`.
func startSubscribeProperties(t *testing.T, p *peer, spaceId string) (*sseRecorder, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rec := newSSERecorder()
	cl := client.New(p.addr, 0)
	go func() {
		err := cl.StreamSubscribeProperties(ctx, spaceId, rec.push)
		rec.mu.Lock()
		rec.err = err
		rec.mu.Unlock()
		close(rec.done)
	}()
	return rec, cancel
}

// startSubscribeObject is the per-object analog of startSubscribeProperties.
func startSubscribeObject(t *testing.T, p *peer, spaceId, objectId, dataset string) (*sseRecorder, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rec := newSSERecorder()
	cl := client.New(p.addr, 0)
	go func() {
		err := cl.StreamSubscribeObject(ctx, spaceId, objectId, dataset, rec.push)
		rec.mu.Lock()
		rec.err = err
		rec.mu.Unlock()
		close(rec.done)
	}()
	return rec, cancel
}

// waitFrameEvent polls until at least one frame with `event` has been
// recorded. Returns true on success; false on timeout.
func waitFrameEvent(rec *sseRecorder, event string, deadline time.Duration) bool {
	return pollUntil(deadline, func() bool {
		for _, f := range rec.snapshot() {
			if f.Event == event {
				return true
			}
		}
		return false
	})
}

// firstChangesContaining returns the first decoded `event: changes`
// batch where pred returns true for any contained event, or zero
// values if none match. Used to assert "the firehose eventually
// reported the remote write".
func firstChangesContaining(rec *sseRecorder, pred func(api.SubscribeEvent) bool) (api.SubscribeEvent, bool) {
	for _, f := range rec.snapshot() {
		if f.Event != "changes" {
			continue
		}
		var batch []api.SubscribeEvent
		if err := json.Unmarshal(f.Data, &batch); err != nil {
			continue
		}
		for _, ev := range batch {
			if pred(ev) {
				return ev, true
			}
		}
	}
	return api.SubscribeEvent{}, false
}

// TestE2E_MultipeerSubscribeFirehose verifies that the per-space
// `objects` firehose on the joiner fires when the owner creates an
// object remotely. Exercises the full path: owner.Modify → consensus
// push → joiner headsync → joiner SDK applies → joiner Subscription
// mailbox → SSE batch.
func TestE2E_MultipeerSubscribeFirehose(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer SSE firehose takes ~90s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"sub-firehose"}`, http.StatusCreated, &sp)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	rec, cancel := startSubscribeProperties(t, joiner, sp.Id)
	defer cancel()

	if !waitFrameEvent(rec, "ready", 30*time.Second) {
		t.Fatalf("joiner never received `ready` frame; err=%v", rec.err)
	}

	// Now drive a remote change. The new object's id is what the
	// firehose should mention in a subsequent `changes` batch.
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	if obj.ObjectId == "" {
		t.Fatalf("owner: empty objectId after create")
	}

	if !pollUntil(3*time.Minute, func() bool {
		_, ok := firstChangesContaining(rec, func(ev api.SubscribeEvent) bool {
			if ev.Dataset != "objects" {
				return false
			}
			for _, r := range ev.Records {
				if r.Id == obj.ObjectId {
					return true
				}
			}
			return false
		})
		return ok
	}) {
		dump := summarizeFrames(rec)
		t.Fatalf("joiner SSE never observed remote object %s; frames seen:\n%s",
			obj.ObjectId, dump)
	}

	cancel()
	select {
	case <-rec.done:
	case <-time.After(5 * time.Second):
		t.Errorf("SSE goroutine did not exit within 5s of cancel")
	}
}

// TestE2E_MultipeerSubscribeChatMessages exercises the chat liveness
// path cross-peer: the joiner subscribes to chat_messages on a shared
// object and then the owner sends a message. Demonstrates that an
// SSE-driven chat client built against this server gets push delivery
// of remote messages without polling /messages.
func TestE2E_MultipeerSubscribeChatMessages(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer chat-SSE test takes ~120s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"chat-sse"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Wait for the chat object itself to converge to the joiner — the
	// SSE Subscribe call resolves the object via the SDK's per-object
	// Controller, and that requires the object to exist locally. Poll
	// against /v1/spaces/:id/objects/query rather than open a stream
	// that 500s.
	if !pollUntil(3*time.Minute, func() bool {
		var resp map[string]any
		mustJSON(t, http.MethodPost,
			joiner.base+"/v1/spaces/"+sp.Id+"/objects/query", "{}",
			http.StatusOK, &resp)
		records, _ := resp["records"].([]any)
		for _, r := range records {
			m, _ := r.(map[string]any)
			if id, _ := m["id"].(string); id == obj.ObjectId {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("chat object never converged on joiner")
	}

	rec, cancel := startSubscribeObject(t, joiner, sp.Id, obj.ObjectId, "chat_messages")
	defer cancel()

	if !waitFrameEvent(rec, "ready", 30*time.Second) {
		t.Fatalf("joiner never received `ready` frame; err=%v", rec.err)
	}

	// Owner sends a message. The chat handler stamps creator/createdAt
	// and writes one record to the chat_messages dataset, which is
	// exactly what the joiner's stream subscribes to.
	var sent api.ChatMessage
	mustJSON(t, http.MethodPost,
		owner.base+"/v1/spaces/"+sp.Id+"/objects/"+obj.ObjectId+"/chat/messages",
		`{"text":"live!"}`, http.StatusCreated, &sent)
	if sent.Id == "" {
		t.Fatalf("owner-side send returned no id: %+v", sent)
	}

	var matched api.SubscribeEvent
	if !pollUntil(3*time.Minute, func() bool {
		ev, ok := firstChangesContaining(rec, func(ev api.SubscribeEvent) bool {
			if ev.Dataset != "chat_messages" || ev.ObjectId != obj.ObjectId {
				return false
			}
			for _, r := range ev.Records {
				if r.Id == sent.Id {
					return true
				}
			}
			return false
		})
		if ok {
			matched = ev
		}
		return ok
	}) {
		dump := summarizeFrames(rec)
		t.Fatalf("joiner SSE never observed remote chat message %s; frames seen:\n%s",
			sent.Id, dump)
	}

	// Auto-stamped fields must reach the joiner via the live event.
	// SDK ApplyResult.DerivedOps surfaces `creator` / `createdAt` /
	// `modifiedAt` (chat.stampCreate) and the modifier's own
	// `_ver.id` creation marker alongside the caller's $set{text}.
	// Pre-fix, only `text` reached subscribers — a remote-driven
	// chat client would render messages with no author.
	pathsByRecord := collectEventSetPaths(matched)
	got := pathsByRecord[sent.Id]
	for _, want := range []string{"creator", "createdAt", "modifiedAt", "_ver.id", "text"} {
		if _, ok := got[want]; !ok {
			t.Errorf("chat create event missing %q in $set ops; got paths=%v", want, got)
		}
	}

	cancel()
	select {
	case <-rec.done:
	case <-time.After(5 * time.Second):
		t.Errorf("SSE goroutine did not exit within 5s of cancel")
	}
}

// collectEventSetPaths flattens every $set in ev into a per-record
// set of dotted paths. Handles both wire shapes: single-field
// (Path=[…], Payload=value) and multi-field (Path=[], Payload=object
// keyed by dotted paths). Used to assert auto-stamped fields land in
// the live event without rebuilding a CRDT view in the test.
func collectEventSetPaths(ev api.SubscribeEvent) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{})
	for _, rec := range ev.Records {
		paths := make(map[string]struct{})
		for _, op := range rec.Ops {
			if op.Type != "$set" {
				continue
			}
			if len(op.Path) > 0 {
				paths[strings.Join(op.Path, ".")] = struct{}{}
				continue
			}
			// Multi-field $set: payload is an object whose keys are
			// dotted paths.
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(op.Payload, &obj); err != nil {
				continue
			}
			for k := range obj {
				paths[k] = struct{}{}
			}
		}
		out[rec.Id] = paths
	}
	return out
}

// summarizeFrames renders a recorder's accumulated frames as one line
// per frame so timeout failure messages stay diagnosable. Truncates
// per-line data to keep the test log readable on long batches.
func summarizeFrames(rec *sseRecorder) string {
	var sb strings.Builder
	for i, f := range rec.snapshot() {
		data := string(f.Data)
		if len(data) > 200 {
			data = data[:200] + "...(truncated)"
		}
		fmt.Fprintf(&sb, "  [%d] event=%q data=%s\n", i, f.Event, data)
	}
	if sb.Len() == 0 {
		return "  (no frames)"
	}
	return sb.String()
}
