package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
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
	chatObj := mustCreateObject(t, e, spaceId, `{}`)
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj
	for _, text := range []string{
		`{"text":"glacier formation processes"}`,
		`{"text":"tidal energy harvesting"}`,
		`{"text":"sourdough starter maintenance"}`,
	} {
		m := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages", text, http.StatusCreated)
		msgIds = append(msgIds, m.RecordIds[0])
	}
	edObj := mustCreateObject(t, e, spaceId, `{}`)
	mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+edObj+"/editor/blocks",
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
	chatObj := mustCreateObject(t, e, spaceId, `{}`)
	chatBase := "/v1/spaces/" + spaceId + "/objects/" + chatObj
	head := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"head message before downtime"}`, http.StatusCreated)
	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}

	// First indexer life: index the head, then shut down.
	st1, err := indexer.OpenStore(ctx, dbPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	ix1 := indexer.New(d.sdk, d.chunkers, st1, indexer.Options{})
	d.indexer = ix1
	if err := ix1.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	cursor1, err := st1.Cursor(ctx, spaceId)
	if err != nil || cursor1 == 0 {
		t.Fatalf("cursor after first sync = %d, %v; want > 0", cursor1, err)
	}
	// Simulate divergence below the cursor: drop the head doc from the
	// index. A correct tail catch-up must NOT resurrect it — its records
	// sit below the persisted cursor and never re-stream.
	if err := st1.Apply(ctx, spaceId, nil, []string{"chat_messages/" + head.RecordIds[0]}); err != nil {
		t.Fatal(err)
	}
	if err := ix1.Close(); err != nil { // closes st1, cursor persisted
		t.Fatal(err)
	}

	// Downtime: the tail arrives while no indexer is running.
	tail := mustModify(t, e, http.MethodPost, chatBase+"/chat/messages",
		`{"text":"tail message during downtime"}`, http.StatusCreated)

	// Second life over the same DB.
	st2, err := indexer.OpenStore(ctx, dbPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	ix2 := indexer.New(d.sdk, d.chunkers, st2, indexer.Options{})
	d.indexer = ix2
	defer func() { _ = ix2.Close() }()

	resumed, err := st2.Cursor(ctx, spaceId)
	if err != nil || resumed != cursor1 {
		t.Fatalf("cursor after reopen = %d, %v; want persisted %d", resumed, err, cursor1)
	}
	if err := ix2.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	cursor2, err := st2.Cursor(ctx, spaceId)
	if err != nil || cursor2 <= cursor1 {
		t.Fatalf("cursor after catch-up = %d, %v; want > %d", cursor2, err, cursor1)
	}

	res := doSearch(t, e, spaceId, api.SearchRequest{Query: "message", Mode: api.SearchModeFTS, Limit: 10}, http.StatusOK)
	if len(res.Hits) != 1 || res.Hits[0].RecordId != tail.RecordIds[0] {
		t.Fatalf("after catch-up want only the tail message (head was dropped below the cursor), got %v", hitRecordIds(res))
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
	chatObj := mustCreateObject(t, e, spaceId, `{}`)
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
	edObj := mustCreateObject(t, e, spaceId, `{}`)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+edObj+"/editor/markdown/append",
		`{"content":"appended delta paragraph"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("markdown append: %d %s", rec.Code, rec.Body.String())
	}
	pollSearch(t, e, spaceId, api.SearchRequest{Query: "delta", Mode: api.SearchModeFTS, Limit: 10},
		func(r api.SearchResponse) bool {
			return len(r.Hits) == 1 && r.Hits[0].Scope == "basic" && r.Hits[0].ObjectId == edObj
		}, "appended block indexed")
}
