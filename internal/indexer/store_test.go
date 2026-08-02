//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any/internal/index"
)

func entry(scope, objectId, dataset, recordId, data string, seq uint64) index.IndexEntry {
	return index.IndexEntry{Scope: scope, ObjectId: objectId, Dataset: dataset, RecordId: recordId, Data: data, ApplySeq: seq}
}

func mustStore(t *testing.T, dim int) *Store {
	t.Helper()
	s, err := OpenStoreInMemory(context.Background(), dim, dim > 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_UpsertDeleteSearchFTS(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0)
	const sp = "space1"

	err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("chat", "obj1", "chat_messages", "m1", "the zeppelin disaster of 1937", 1)},
		{Entry: entry("chat", "obj1", "chat_messages", "m2", "lunch plans for tomorrow", 2)},
		{Entry: entry("basic", "obj2", "editor_blocks", "b1", "zeppelin engineering notes", 3)},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	hits, err := s.SearchFTS(ctx, sp, "zeppelin", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("fts hits = %d, want 2: %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.Score <= 0 {
			t.Errorf("hit %s score = %v, want > 0", h.RecordId, h.Score)
		}
	}

	// Scope residual filter.
	hits, err = s.SearchFTS(ctx, sp, "zeppelin", []string{"basic"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].RecordId != "b1" {
		t.Fatalf("scoped hits = %+v, want only b1", hits)
	}

	// Replace m1's text, delete m2; search reflects both.
	err = s.Apply(ctx, sp,
		[]DocUpsert{{Entry: entry("chat", "obj1", "chat_messages", "m1", "quiet afternoon", 4)}},
		[]string{"obj1:chat_messages:m2", "obj1:chat_messages:never-existed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hits, err = s.SearchFTS(ctx, sp, "zeppelin", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].RecordId != "b1" {
		t.Fatalf("after replace+delete, hits = %+v, want only b1", hits)
	}
}

func TestStore_PrefixDeleteAndDropSpace(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0)
	const sp = "space1"

	err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("chat", "obj1", "chat_messages", "m1", "alpha bravo", 1)},
		{Entry: entry("chat", "obj1", "chat_messages", "m2", "alpha charlie", 2)},
		{Entry: entry("basic", "obj1", "editor_blocks", "b1", "alpha echo", 3)},
		{Entry: entry("chat", "obj2", "chat_messages", "m3", "alpha delta", 4)},
		{Entry: entry("basic", "obj2", "editor_blocks", "b2", "alpha foxtrot", 5)},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Dataset-level prefix (type detach): obj2 loses its chat docs only.
	if err := s.Apply(ctx, sp, nil, nil, []string{"obj2:chat_messages:"}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchFTS(ctx, sp, "alpha", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 4 {
		t.Fatalf("after dataset prefix delete, hits = %+v, want 4", hits)
	}
	for _, h := range hits {
		if h.ObjectId == "obj2" && h.Dataset == "chat_messages" {
			t.Fatalf("obj2 chat doc survived the prefix delete: %+v", h)
		}
	}

	// Object-level prefix (object deletion): all of obj1 goes, across
	// datasets, and FTS no longer matches its text (locks in in-tx
	// index cleanup).
	if err := s.Apply(ctx, sp, nil, nil, []string{"obj1:"}); err != nil {
		t.Fatal(err)
	}
	hits, err = s.SearchFTS(ctx, sp, "alpha", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ObjectId != "obj2" || hits[0].RecordId != "b2" {
		t.Fatalf("after object prefix delete, hits = %+v, want only obj2/b2", hits)
	}
	if h, err := s.SearchFTS(ctx, sp, "bravo", nil, 10); err != nil || len(h) != 0 {
		t.Fatalf("deleted text still matches: %+v, %v", h, err)
	}

	if err := s.SetCursor(ctx, sp, 42); err != nil {
		t.Fatal(err)
	}
	if err := s.DropSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	seq, err := s.Cursor(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Errorf("cursor after DropSpace = %d, want 0", seq)
	}
	hits, err = s.SearchFTS(ctx, sp, "alpha", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("after DropSpace, hits = %+v, want none", hits)
	}
}

func TestStore_Cursor(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0)
	seq, err := s.Cursor(ctx, "fresh")
	if err != nil || seq != 0 {
		t.Fatalf("fresh cursor = %d, %v; want 0, nil", seq, err)
	}
	if err := s.SetCursor(ctx, "fresh", 7); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCursor(ctx, "fresh", 19); err != nil {
		t.Fatal(err)
	}
	seq, err = s.Cursor(ctx, "fresh")
	if err != nil || seq != 19 {
		t.Fatalf("cursor = %d, %v; want 19, nil", seq, err)
	}
}

func TestStore_BM25FTitleBoost(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0) // FTS-only
	s.SetFTSParams(0, 0, 8)
	const sp = "bm25f"

	if err := s.Apply(ctx, sp, []DocUpsert{
		// "alpha" in body only.
		{Entry: index.IndexEntry{Scope: "basic", ObjectId: "o", Dataset: "d", RecordId: "body", Data: "alpha beta gamma"}},
		// "alpha" in the boosted title only (different body so body doesn't match).
		{Entry: index.IndexEntry{Scope: "basic", ObjectId: "o", Dataset: "d", RecordId: "titled", Data: "delta epsilon", Title: "alpha"}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	hits, err := s.SearchFTS(ctx, sp, "alpha", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want both docs to match 'alpha' (title + body), got %d: %+v", len(hits), hits)
	}
	if hits[0].RecordId != "titled" {
		t.Fatalf("title boost (weight 8) should rank the title match first, got %s then %s",
			hits[0].RecordId, hits[1].RecordId)
	}
}

func TestStore_VectorMinSimFloor(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 2)
	const sp = "floor"

	// near ≈ query (cosine 1.0); far at cosine 0.6 to the query [1,0].
	if err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("basic", "o1", "editor_blocks", "near", "near", 1), Vector: []float32{1, 0}},
		{Entry: entry("basic", "o2", "editor_blocks", "far", "far", 2), Vector: []float32{0.6, 0.8}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	query := []float32{1, 0}

	// floor 0: legacy behavior, both positive-sim hits survive.
	hits, err := s.SearchVector(ctx, sp, query, nil, 10, 0)
	if err != nil || len(hits) != 2 {
		t.Fatalf("floor 0 hits = %d (%v), want both", len(hits), err)
	}

	// floor 0.7: the cosine-0.6 hit is dropped, the cosine-1.0 hit stays.
	hits, err = s.SearchVector(ctx, sp, query, nil, 10, 0.7)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].RecordId != "near" {
		t.Fatalf("floor 0.7 hits = %+v, want only 'near'", hits)
	}
}

func TestStore_VectorPendingLifecycle(t *testing.T) {
	ctx := context.Background()
	const dim = 4
	s := mustStore(t, dim)
	const sp = "space1"

	// Hand-made unit vectors: m1 ≈ query, b1 orthogonal.
	err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("chat", "obj1", "chat_messages", "m1", "close match", 1), Vector: []float32{1, 0, 0, 0}},
		{Entry: entry("basic", "obj2", "editor_blocks", "b1", "far away", 2), Vector: []float32{0, 1, 0, 0}},
		{Entry: entry("chat", "obj1", "chat_messages", "m2", "awaiting embedding", 3)}, // pending
		{Entry: entry("basic", "obj2", "editor_blocks", "b2", "", 4)},                  // empty text — never pending
		{Entry: entry("props", "obj3", "prop", "p1", "Author: Frank Herbert", 5)},      // props scope — FTS-only, never pending
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	ids, texts, err := s.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "obj1:chat_messages:m2" || texts[0] != "awaiting embedding" {
		t.Fatalf("pending = %v / %v, want only obj1:chat_messages:m2", ids, texts)
	}
	// The props doc is still FTS-searchable despite skipping the embed queue.
	ftsHits, err := s.SearchFTSQuery(ctx, sp, FTSQuery{Query: "Herbert"}, []string{"props"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ftsHits) != 1 || ftsHits[0].RecordId != "p1" {
		t.Fatalf("props fts hits = %+v, want p1", ftsHits)
	}

	query := []float32{0.9, 0.1, 0, 0}
	hits, err := s.SearchVector(ctx, sp, query, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].RecordId != "m1" {
		t.Fatalf("vector hits = %+v, want m1 nearest", hits)
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("similarity should decrease: %v", hits)
	}

	// Embed the pending doc near the query; it should now win searches.
	if err := s.SetVectors(ctx, sp, []string{"obj1:chat_messages:m2", "obj1:chat_messages:gone"}, [][]float32{{1, 0, 0, 0}, {0, 0, 1, 0}}); err != nil {
		t.Fatal(err)
	}
	ids, _, err = s.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("pending after SetVectors = %v, want none", ids)
	}
	hits, err = s.SearchVector(ctx, sp, query, []string{"chat"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("scoped vector hits = %+v, want m1+m2", hits)
	}
	for _, h := range hits {
		if h.Scope != "chat" {
			t.Errorf("scope filter leaked: %+v", h)
		}
	}

	// A re-upsert of an embedded doc goes back to pending (text changed).
	err = s.Apply(ctx, sp, []DocUpsert{{Entry: entry("chat", "obj1", "chat_messages", "m1", "rewritten", 5)}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids, _, err = s.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "obj1:chat_messages:m1" {
		t.Fatalf("pending after re-upsert = %v, want obj1:chat_messages:m1", ids)
	}
}

func TestStore_DimMismatch(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 4)
	if err := s.checkMeta(ctx); err != nil {
		t.Fatalf("same dim should pass: %v", err)
	}
	s.dim = 8
	if err := s.checkMeta(ctx); err == nil {
		t.Fatal("dim change should error")
	}
}

func TestStore_LazyDim(t *testing.T) {
	ctx := context.Background()
	// Unknown dim + embedder configured: pending is marked, vector
	// search degrades to empty instead of erroring.
	s, err := OpenStoreInMemory(ctx, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	const sp = "space1"
	if err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("chat", "obj1", "chat_messages", "m1", "waiting for the embedder", 1)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.Pending(ctx, sp, 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("pending with unknown dim = %v, %v; want the doc queued", ids, err)
	}
	hits, err := s.SearchVector(ctx, sp, []float32{1, 0}, nil, 10, 0)
	if err != nil || len(hits) != 0 {
		t.Fatalf("vector search with unknown dim = %v, %v; want empty, nil", hits, err)
	}

	// First successful embedding teaches the dimension; the pipeline
	// then works end to end.
	if err := s.EnsureDim(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if s.Dim() != 4 {
		t.Fatalf("Dim() = %d, want 4", s.Dim())
	}
	if err := s.SetVectors(ctx, sp, ids, [][]float32{{1, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	hits, err = s.SearchVector(ctx, sp, []float32{1, 0, 0, 0}, nil, 10, 0)
	if err != nil || len(hits) != 1 {
		t.Fatalf("vector search after EnsureDim = %v, %v; want the doc", hits, err)
	}

	// A contradicting dimension (model swapped) is an error.
	if err := s.EnsureDim(ctx, 8); err == nil {
		t.Fatal("EnsureDim with a different dim should error")
	}
}

func TestStore_SchemaVersionMismatch(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0)
	// Rewrite _meta as an older schema, then re-run the open check —
	// the store must refuse with an actionable message.
	if err := s.writeMetaDim(ctx, 0); err != nil {
		t.Fatal(err)
	}
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		t.Fatal(err)
	}
	arena := &anyenc.Arena{}
	meta := arena.NewObject()
	meta.Set("id", arena.NewString(metaDocId))
	meta.Set("schema", arena.NewNumberInt(1))
	meta.Set("dim", arena.NewNumberInt(0))
	if err := coll.UpsertOne(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if err := s.checkMeta(ctx); err == nil {
		t.Fatal("old schema version should refuse to open")
	}
}

func TestStore_FTSOperators(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0)
	const sp = "space1"

	if err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("basic", "o1", "editor_blocks", "b1", "the quick brown fox jumps", 1)},
		{Entry: entry("basic", "o2", "editor_blocks", "b2", "a brown bear sleeps", 2)},
		{Entry: entry("basic", "o3", "editor_blocks", "b3", "quick silver fox", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	ids := func(hits []Hit) map[string]bool {
		m := map[string]bool{}
		for _, h := range hits {
			m[h.RecordId] = true
		}
		return m
	}

	// OR (default): "quick fox" matches anything with quick OR fox.
	hits, err := s.SearchFTSQuery(ctx, sp, FTSQuery{Query: "quick fox"}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(hits); !got["b1"] || !got["b3"] {
		t.Fatalf("OR quick fox = %v, want b1+b3", got)
	}

	// AND (defaultOperator): "quick fox" requires both → b1, b3 (both have
	// quick+fox), not b2.
	hits, _ = s.SearchFTSQuery(ctx, sp, FTSQuery{Query: "quick bear", DefaultAnd: true}, nil, 10)
	if got := ids(hits); len(got) != 0 {
		t.Fatalf("AND quick bear = %v, want none (no doc has both)", got)
	}

	// Phrase: "brown fox" adjacent → only b1 ("brown bear", "silver fox" miss).
	hits, _ = s.SearchFTSQuery(ctx, sp, FTSQuery{Query: `"brown fox"`}, nil, 10)
	if got := ids(hits); len(got) != 1 || !got["b1"] {
		t.Fatalf("phrase \"brown fox\" = %v, want only b1", got)
	}

	// Require: must-have fox; the bare "brown" is a should (optional boost
	// once a must exists) → both fox docs match, b1 (also brown) ranks first.
	hits, _ = s.SearchFTSQuery(ctx, sp, FTSQuery{Query: "brown", Require: []string{"fox"}}, nil, 10)
	if got := ids(hits); !got["b1"] || !got["b3"] || got["b2"] {
		t.Fatalf("require fox = %v, want b1+b3", got)
	}
	if hits[0].RecordId != "b1" {
		t.Fatalf("require fox top = %s, want b1 (brown boost)", hits[0].RecordId)
	}

	// Exclude: fox but NOT quick → b1 has quick (out), b3 has quick (out) → none.
	hits, _ = s.SearchFTSQuery(ctx, sp, FTSQuery{Query: "fox", Exclude: []string{"quick"}}, nil, 10)
	if got := ids(hits); len(got) != 0 {
		t.Fatalf("exclude quick = %v, want none", got)
	}
	// Prefix: silv* → b3.
	hits, _ = s.SearchFTSQuery(ctx, sp, FTSQuery{Query: "silv*"}, nil, 10)
	if got := ids(hits); len(got) != 1 || !got["b3"] {
		t.Fatalf("prefix silv* = %v, want only b3", got)
	}
}
