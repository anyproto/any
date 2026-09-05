//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/indexer"
)

// mustCreateSpace creates a space via the HTTP handler and returns its id.
func mustCreateSpace(t *testing.T, e http.Handler, name string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"`+name+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatal(err)
	}
	return sp.Id
}

// pollSearch polls the endpoint until check passes or the deadline hits.
func pollSearch(t *testing.T, e http.Handler, spaceId string, req api.SearchRequest, check func(api.SearchResponse) bool, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last api.SearchResponse
	for {
		last = doSearch(t, e, spaceId, req, http.StatusOK)
		if check(last) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: condition never met; last hits = %v", what, hitRecordIds(last))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestIndexer_ColdSync: all content exists BEFORE the indexer is built —
// the boot-time cold align (cursor 0) must index everything, FTS and
// vector both, across several objects.
func TestIndexer_ColdSync(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	spaceId := mustCreateSpace(t, e, "ColdSync")

	// Content written with NO indexer in the process.
	var msgIds []string
	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj
	for _, text := range []string{
		`{"text":"glacier formation processes"}`,
		`{"text":"tidal energy harvesting"}`,
		`{"text":"sourdough starter maintenance"}`,
	} {
		m := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", text, http.StatusCreated)
		msgIds = append(msgIds, m.RecordIds[0])
	}
	edObj := mustCreateModuleObject(t, e, spaceId, "editor")
	mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+edObj+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"glacier travel safety notes"}`, http.StatusCreated)

	// Indexer arrives late — Start must cold-align from cursor 0.
	ix := newTestIndexer(t, d, fakeEmbedder{dim: 16})
	defer func() { _ = ix.Close() }()
	ix.Start(ctx)

	pollSearch(t, e, spaceId, api.SearchRequest{Query: "glacier", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool { return len(r.Hits) == 2 }, "cold sync fts")
	// Vector pipeline drained too: the pending docs were embedded and
	// the lazily-created index answers.
	pollSearch(t, e, spaceId, api.SearchRequest{Query: "tidal energy harvesting", Mode: api.SearchModeVector, Limit: 1},
		func(r api.SearchResponse) bool { return len(r.Hits) == 1 && r.Hits[0].RecordId == msgIds[1] }, "cold sync vector")
}

// TestIndexer_TailCatchUp: the indexer indexed part of the stream, went
// away (close — cursor persisted in the file-backed store), more content
// arrived, and a fresh indexer over the same store must pick up ONLY the
// tail past the persisted cursor.
func TestIndexer_TailCatchUp(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")

	spaceId := mustCreateSpace(t, e, "TailCatchUp")
	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj
	head := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"head message before downtime"}`, http.StatusCreated)
	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}

	// First indexer life: index the head, then shut down.
	st1, err := indexer.OpenStore(ctx, dbPath, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	ix1 := indexer.New(d.sdk, d.eng.chunkers, st1, indexer.Options{})
	d.indexer = ix1
	if err := ix1.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	cursor1, _, err := st1.Cursor(ctx, spaceId)
	if err != nil || cursor1 == 0 {
		t.Fatalf("cursor after first sync = %d, %v; want > 0", cursor1, err)
	}
	// Simulate divergence below the cursor: drop the head doc from the
	// index. A correct tail catch-up must NOT resurrect it — its records
	// sit below the persisted cursor and never re-stream.
	if err := st1.Apply(ctx, spaceId, nil, []string{chatObj + ":chat_messages:" + head.RecordIds[0]}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ix1.Close(); err != nil { // closes st1, cursor persisted
		t.Fatal(err)
	}

	// Downtime: the tail arrives while no indexer is running.
	tail := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"tail message during downtime"}`, http.StatusCreated)

	// Second life over the same DB.
	st2, err := indexer.OpenStore(ctx, dbPath, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	ix2 := indexer.New(d.sdk, d.eng.chunkers, st2, indexer.Options{})
	d.indexer = ix2
	defer func() { _ = ix2.Close() }()

	resumed, _, err := st2.Cursor(ctx, spaceId)
	if err != nil || resumed != cursor1 {
		t.Fatalf("cursor after reopen = %d, %v; want persisted %d", resumed, err, cursor1)
	}
	if err := ix2.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	cursor2, _, err := st2.Cursor(ctx, spaceId)
	if err != nil || cursor2 <= cursor1 {
		t.Fatalf("cursor after catch-up = %d, %v; want > %d", cursor2, err, cursor1)
	}

	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "message", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 || res.Hits[0].RecordId != tail.RecordIds[0] {
		t.Fatalf("after catch-up want only the tail message (head was dropped below the cursor), got %v", hitRecordIds(res))
	}
}

// TestIndexer_TypeDetachEviction: detaching a chunker's gating type
// evicts the object's dataset docs via an id-prefix delete inside the
// same advance page — applySeq-consistent with the change feed. The
// detach itself goes through the SDK handle (the HTTP route is 501).
func TestIndexer_TypeDetachEviction(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough for eviction
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "DetachEviction")
	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj
	mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", `{"text":"detachable alpha"}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", `{"text":"detachable bravo"}`, http.StatusCreated)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "detachable", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 2 {
		t.Fatalf("pre-detach hits = %v, want 2", hitRecordIds(res))
	}

	// Detach the declaring type: the row re-streams with a bumped
	// _applySeq and the next advance prefix-evicts
	// objectId:chat_messages: — the object no longer holds the
	// collection (no owner of chat_messages among its types).
	chatType := installModuleType(t, e, spaceId, "chat")
	if _, err := sdkSpace.Properties().DetachType(ctx, chatObj, chatType); err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "detachable", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Fatalf("post-detach hits = %v, want none", hitRecordIds(res))
	}

	// Re-attach the declaring type (no write attaches one) — only the
	// new message indexes; rows below the cursor do not resurrect.
	if _, err := sdkSpace.Properties().AttachType(ctx, chatObj, chatType); err != nil {
		t.Fatal(err)
	}
	msg3 := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", `{"text":"detachable charlie"}`, http.StatusCreated)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "detachable", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 || res.Hits[0].RecordId != msg3.RecordIds[0] {
		t.Fatalf("post-reattach hits = %v, want only the new message", hitRecordIds(res))
	}
}

// TestIndexer_EditorCoalescing: a multi-block editor doc indexes as ONE
// coalesced window (not one doc per block), so a query spanning terms from
// different blocks returns a single window hit; deleting the anchor block
// re-anchors the window without leaving an orphan.
func TestIndexer_EditorCoalescing(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "EditorCoalescing")
	edObj := mustCreateModuleObject(t, e, spaceId, "editor")
	base := "/v1/spaces/" + spaceId + "/objects/" + edObj + "/editor/editor_blocks/blocks"

	head := mustModify(t, e, http.MethodPost, base,
		`{"type":"heading","text":"sourdough guide","style":{"level":1}}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, base, `{"type":"paragraph","text":"feed the starter daily"}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, base, `{"type":"paragraph","text":"it should double in size"}`, http.StatusCreated)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	// Terms from the heading AND both paragraphs resolve to ONE window —
	// per-block indexing would have returned up to three separate docs.
	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "sourdough starter double", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 {
		t.Fatalf("coalesced window: hits = %v, want exactly 1", hitRecordIds(res))
	}
	h := res.Hits[0]
	if h.ObjectId != edObj || h.Scope != "basic" || h.RecordId != "win_"+head.RecordIds[0] {
		t.Fatalf("window hit shape wrong: %+v", h)
	}
	if h.Data == "" || !contains(h.Data, "feed the starter") || !contains(h.Data, "double in size") {
		t.Fatalf("window data should concatenate member blocks: %q", h.Data)
	}

	// Delete the anchor (heading) block: the window re-anchors on the next
	// block and the old win_<heading> doc is gone (no orphan).
	doJSONExpect(t, e, http.MethodDelete,
		"/v1/spaces/"+spaceId+"/objects/"+edObj+"/editor/editor_blocks/blocks/"+head.RecordIds[0], http.StatusOK)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "starter double", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 {
		t.Fatalf("after anchor delete: hits = %v, want 1 re-anchored window", hitRecordIds(res))
	}
	if res.Hits[0].RecordId == "win_"+head.RecordIds[0] {
		t.Errorf("stale anchor window survived: %+v", res.Hits[0])
	}
	// The deleted heading text no longer matches.
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "guide", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Fatalf("deleted heading text still matches: %v", hitRecordIds(res))
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestIndexer_EditorReconcileEmbedReuse: appending a new section to an
// editor doc re-embeds only the NEW window — the reconcile diff keeps the
// unchanged windows (and their vectors) instead of re-embedding the whole
// document (the embed-preserving reconcile, chunker-hybrid-search-report
// § 9.5 follow-up).
func TestIndexer_EditorReconcileEmbedReuse(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	emb := &countingEmbedder{fakeEmbedder: fakeEmbedder{dim: 16}}
	st, err := indexer.OpenStoreInMemory(ctx, emb.dim, true)
	if err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(d.sdk, d.eng.chunkers, st, indexer.Options{Embedder: emb})
	d.indexer = ix
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "EditorEmbedReuse")
	edObj := mustCreateModuleObject(t, e, spaceId, "editor")
	base := "/v1/spaces/" + spaceId + "/objects/" + edObj + "/editor/editor_blocks/blocks"

	// Two heading sections → two windows.
	mustModify(t, e, http.MethodPost, base, `{"type":"heading","text":"alpha","style":{"level":1}}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, base, `{"type":"paragraph","text":"first section body"}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, base, `{"type":"heading","text":"beta","style":{"level":1}}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, base, `{"type":"paragraph","text":"second section body"}`, http.StatusCreated)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	afterTwo := emb.docs.Load()
	if afterTwo != 2 {
		t.Fatalf("expected 2 windows embedded, got %d", afterTwo)
	}

	// Append a third section → exactly ONE new window embedded; the two
	// existing windows keep their vectors (not re-embedded).
	mustModify(t, e, http.MethodPost, base, `{"type":"heading","text":"gamma","style":{"level":1}}`, http.StatusCreated)
	mustModify(t, e, http.MethodPost, base, `{"type":"paragraph","text":"third section body"}`, http.StatusCreated)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := emb.docs.Load(); got != afterTwo+1 {
		t.Errorf("append re-embedded too much: %d → %d (want +1 for the new window only)", afterTwo, got)
	}
	// All three sections are searchable (unique per-section words; "section"
	// is shared so it would match all — that's BM25 OR, not a bug).
	for _, q := range []string{"first", "second", "third"} {
		if res := doSearch(t, e, spaceId, api.SearchRequest{Query: q, Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK); len(res.Hits) != 1 {
			t.Errorf("query %q: hits = %v, want 1", q, hitRecordIds(res))
		}
	}
}

// flakyEmbedder is a fakeEmbedder with a kill switch — models an
// embedding service that is down for a while and then recovers.
type flakyEmbedder struct {
	fakeEmbedder
	down atomic.Bool
}

func (f *flakyEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	if f.down.Load() {
		return nil, errors.New("embedder down")
	}
	return f.fakeEmbedder.EmbedDocs(ctx, texts)
}

func (f *flakyEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if f.down.Load() {
		return nil, errors.New("embedder down")
	}
	return f.fakeEmbedder.EmbedQuery(ctx, text)
}

// TestIndexer_EmbedderOutage: an unavailable embedder must never break
// the pipeline — FTS keeps indexing and answering, the vector side
// freezes (docs queue as pending), and once the embedder recovers,
// embedding resumes without a restart. The store starts with dim 0:
// there is no boot probe, the dimension is learned from the first
// successful batch.
func TestIndexer_EmbedderOutage(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	emb := &flakyEmbedder{fakeEmbedder: fakeEmbedder{dim: 16}}
	emb.down.Store(true) // down from the very start — boot must not care

	st, err := indexer.OpenStoreInMemory(ctx, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(d.sdk, d.eng.chunkers, st, indexer.Options{Embedder: emb})
	d.indexer = ix
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "EmbedderOutage")
	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	msg := mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+chatObj+"/chat/messages",
		`{"text":"resilience probe message"}`, http.StatusCreated)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	// Sync surfaces the embed failure (the async worker only logs it),
	// but the advance — the FTS half — must have landed regardless.
	if err := ix.SyncSpace(ctx, sdkSpace); err == nil {
		t.Fatal("SyncSpace should surface the embed failure while the embedder is down")
	}

	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "resilience", Mode: api.SearchModeFTS}, http.StatusOK)
	if len(res.Hits) != 1 {
		t.Fatalf("fts must work during the outage, hits = %v", hitRecordIds(res))
	}
	// Hybrid degrades to fts instead of failing, and the response tells
	// the consumer agent the vector leg is temporarily unavailable (as
	// opposed to disabled by config).
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "resilience"}, http.StatusOK)
	if res.Mode != api.SearchModeFTS || len(res.Hits) != 1 {
		t.Fatalf("hybrid should degrade to fts during the outage: mode=%s hits=%v", res.Mode, hitRecordIds(res))
	}
	if res.VectorStatus != api.VectorStatusUnavailable {
		t.Errorf("outage: vectorStatus = %s, want unavailable", res.VectorStatus)
	}
	// Explicit vector mode reports the outage as retryable.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/search",
		`{"query":"resilience","mode":"vector"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("vector mode during outage: status %d body=%s", rec.Code, rec.Body.String())
	}

	// Recovery: no restart, the same Sync drains the queued pending
	// docs, learns the dimension, builds the vector index.
	emb.down.Store(false)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatalf("sync after recovery: %v", err)
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "resilience probe", Mode: api.SearchModeVector}, http.StatusOK)
	if len(res.Hits) != 1 || res.Hits[0].RecordId != msg.RecordIds[0] {
		t.Fatalf("vector search after recovery: hits = %v", hitRecordIds(res))
	}
	if res.VectorStatus != api.VectorStatusUsed {
		t.Errorf("after recovery: vectorStatus = %s, want used", res.VectorStatus)
	}
}

// TestIndexer_RealtimeUpdates: with workers running, live edits /
// deletes / appends flow through the dirty-signal path into the index.
func TestIndexer_RealtimeUpdates(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, fakeEmbedder{dim: 16})
	defer func() { _ = ix.Close() }()
	ix.Start(ctx)

	spaceId := mustCreateSpace(t, e, "Realtime")
	chatObj := mustCreateModuleObject(t, e, spaceId, "chat")
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj

	keep := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"persistent topic alpha"}`, http.StatusCreated)
	doomed := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"ephemeral topic bravo"}`, http.StatusCreated)

	pollSearch(t, e, spaceId, api.SearchRequest{Query: "topic", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool { return len(r.Hits) == 2 }, "initial realtime index")

	// Live edit: old text must stop matching, new text must match.
	rec := doJSON(t, e, http.MethodPatch, chatBase+"/chat/messages/"+keep.RecordIds[0],
		`{"text":"persistent topic charlie"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat edit: %d %s", rec.Code, rec.Body.String())
	}
	pollSearch(t, e, spaceId, api.SearchRequest{Query: "charlie", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool { return len(r.Hits) == 1 && r.Hits[0].RecordId == keep.RecordIds[0] }, "edit indexed")
	pollSearch(t, e, spaceId, api.SearchRequest{Query: "alpha", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool { return len(r.Hits) == 0 }, "old text evicted")

	// Live delete: tombstone entry evicts the doc.
	doJSONExpect(t, e, http.MethodDelete, chatBase+"/chat/messages/"+doomed.RecordIds[0], http.StatusOK)
	pollSearch(t, e, spaceId, api.SearchRequest{Query: "bravo", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool { return len(r.Hits) == 0 }, "deleted message evicted")

	// Live append on a fresh editor object (covers the markdown append
	// fast path + a second dataset through the same worker).
	edObj := mustCreateModuleObject(t, e, spaceId, "editor")
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+edObj+"/editor/editor_blocks/markdown/append",
		`{"content":"appended delta paragraph"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("markdown append: %d %s", rec.Code, rec.Body.String())
	}
	pollSearch(t, e, spaceId, api.SearchRequest{Query: "delta", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool {
			return len(r.Hits) == 1 && r.Hits[0].Scope == "basic" && r.Hits[0].ObjectId == edObj
		}, "appended block indexed")
}

// TestIndexer_ObjectDeleteEviction: an object delete surfaces only as
// ObjectChange{Deleted:true} — the advance must prefix-evict
// `objectId:`, including the ungated prop chunker's docs (name /
// description), which have no other removal path. Also pins the read
// side: a per-object query on the dead id answers 404
// object.not_found, not 500.
func TestIndexer_ObjectDeleteEviction(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough for eviction
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "DeleteEviction")
	chatType := installModuleType(t, e, spaceId, "chat")
	obj := mustCreateObject(t, e, spaceId,
		`{"types":["`+chatType+`"],"initialProperties":{"any":{"name":"ephemeral quokka dossier"}}}`)
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + obj
	mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"ephemeral quokka message"}`, http.StatusCreated)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "quokka", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 2 { // prop name doc + chat message
		t.Fatalf("pre-delete hits = %v, want 2", hitRecordIds(res))
	}

	rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+obj, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete object: %d %s", rec.Code, rec.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	res = doSearch(t, e, spaceId, api.SearchRequest{Query: "quokka", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Fatalf("post-delete hits = %v, want none", hitRecordIds(res))
	}

	// A client following a stale hit into the per-object read path gets
	// a typed 404, not a 500 (any-sync's deleted-tree sentinel mapped).
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query",
		`{"objectId":"`+obj+`","dataset":"chat_messages"}`)
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
	}
	if rec.Code != http.StatusNotFound || env.Error.Code != "object.not_found" {
		t.Fatalf("dead-id query = %d %s, want 404 object.not_found", rec.Code, env.Error.Code)
	}
}

// TestIndexer_TypeDefinitionsExcluded pins the prop-chunker exclusion of
// type-definition objects (`any.types = ["__type__"]`): a created type's
// name must never surface as a search hit — its one-word name otherwise
// wins BM25 on field-length normalization and shadows real content. The
// control object proves the exclusion is selective (same token, indexed).
func TestIndexer_TypeDefinitionsExcluded(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, nil) // FTS-only is enough
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "TypeDefScope")

	// A custom type named with a unique token, and a control object
	// whose name carries the same token.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"Zebrafinch","xKey":"zebrafinch"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("type create: %d %s", rec.Code, rec.Body.String())
	}
	ctrlObj := mustCreateObject(t, e, spaceId,
		`{"initialProperties":{"any":{"name":"zebrafinch sightings journal"}}}`)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "zebrafinch", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %v, want only the control object", hitRecordIds(res))
	}
	if h := res.Hits[0]; h.ObjectId != ctrlObj || h.Dataset != "prop" {
		t.Fatalf("hit = {dataset:%s obj:%s}, want the control's prop/name (obj %s)", h.Dataset, h.ObjectId, ctrlObj)
	}
}

// TestIndexer_PropsDefaultOn: a user property with no meta.index is
// searchable under the dedicated scope "props" end-to-end — catalog
// resolution, advance loop, self-describing "<name>: <value>" entry
// text — and a search that excludes the scope never sees it.
func TestIndexer_PropsDefaultOn(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()
	ix := newTestIndexer(t, d, fakeEmbedder{dim: 16})
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "PropsDefaultOn")

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types", `{"name":"Book","xKey":"book"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+tr.TypeId+"/properties",
		`{"name":"Author","kind":"string","xKey":"author"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create prop: %d %s", rec.Code, rec.Body.String())
	}
	var pr api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
		t.Fatal(err)
	}
	obj := mustCreateObject(t, e, spaceId, `{"types":["`+tr.TypeId+`"]}`)
	mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/properties/"+obj+"/set/"+tr.TypeId,
		`{"patch":{"`+pr.PropId+`":"Dan Simmons"}}`, http.StatusOK)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	res := doSearch(t, e, spaceId,
		api.SearchRequest{Query: "Simmons", Mode: api.SearchModeFTS, Scopes: []string{"props"}, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 || res.Hits[0].RecordId != pr.PropId || res.Hits[0].Data != "Author: Dan Simmons" {
		t.Fatalf("props hits = %+v, want one %q", res.Hits, "Author: Dan Simmons")
	}
	// A content-only search (scopes without props) never sees property
	// noise.
	res = doSearch(t, e, spaceId,
		api.SearchRequest{Query: "Simmons", Mode: api.SearchModeFTS, Scopes: []string{"basic", "chat"}, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 0 {
		t.Fatalf("content-only hits = %v, want none", hitRecordIds(res))
	}
}
