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

// A removal and an upsert of the same doc can meet in one page when
// collectObject finds an object tombstoned after earlier chunkers already
// queued its entries (SYN-198). The removal wins — otherwise the upsert
// resurrects docs of an object nothing will ever re-stream.
func TestStore_RemovalWinsOverUpsertInSamePage(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0)
	const sp = "space1"

	// obj1 is evicted in the same page that upserts two of its datasets;
	// obj2's upsert is untouched by the unrelated prefix.
	err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("chat", "obj1", "chat_messages", "m1", "alpha bravo", 1)},
		{Entry: entry("basic", "obj1", "editor_blocks", "b1", "alpha echo", 2)},
		{Entry: entry("chat", "obj2", "chat_messages", "m3", "alpha delta", 3)},
	}, nil, []string{"obj1:"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchFTS(ctx, sp, "alpha", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ObjectId != "obj2" {
		t.Fatalf("hits = %+v, want only obj2 (obj1 upserts must not survive its eviction)", hits)
	}

	// A record-level delete covers the base doc and its chunk suffixes.
	err = s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("chat", "obj2", "chat_messages", "m3", "alpha golf", 4)},
		{Entry: entry("chat", "obj2", "chat_messages", "m3", "alpha hotel", 4), Chunk: 2},
	}, []string{"obj2:chat_messages:m3"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits, err = s.SearchFTS(ctx, sp, "alpha", nil, 10); err != nil || len(hits) != 0 {
		t.Fatalf("after record delete, hits = %+v, %v — want none", hits, err)
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

	if err := s.SetCursor(ctx, sp, 42, "gen-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DropSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	seq, _, err := s.Cursor(ctx, sp)
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
	seq, _, err := s.Cursor(ctx, "fresh")
	if err != nil || seq != 0 {
		t.Fatalf("fresh cursor = %d, %v; want 0, nil", seq, err)
	}
	if err := s.SetCursor(ctx, "fresh", 7, "gen-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCursor(ctx, "fresh", 19, "gen-1"); err != nil {
		t.Fatal(err)
	}
	seq, _, err = s.Cursor(ctx, "fresh")
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

func TestStore_ShortPropDocsSkipEmbedding(t *testing.T) {
	// Prop docs below minPropEmbedBytes stay FTS-only (never pending):
	// short names embed into a flat cosine band and only pollute the
	// vector leg. Long prop docs and short content-dataset docs still
	// queue for embedding.
	ctx := context.Background()
	s := mustStore(t, 4)
	const sp = "space1"

	longDesc := "a description long enough to carry actual semantics for the vector leg"
	if len(longDesc) < minPropEmbedBytes {
		t.Fatalf("test fixture too short: %d < %d", len(longDesc), minPropEmbedBytes)
	}
	err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("basic", "obj1", index.DatasetProp, "name", "Task", 1)},
		{Entry: entry("basic", "obj1", index.DatasetProp, "description", longDesc, 2)},
		{Entry: entry("chat", "obj2", "chat_messages", "m1", "ok", 3)},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	ids, _, err := s.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"obj1:prop:description": true, "obj2:chat_messages:m1": true}
	if len(ids) != len(want) {
		t.Fatalf("pending = %v, want %v", ids, want)
	}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("pending = %v, want %v (short prop doc must not queue)", ids, want)
		}
	}

	// The short name is still FTS-searchable.
	hits, err := s.SearchFTS(ctx, sp, "task", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].RecordId != "name" {
		t.Fatalf("fts hits = %+v, want the name doc", hits)
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

// FilterTerms enforces require / exclude on hits that did not come from
// the lexical leg (SYN-187): require keeps only docs containing every
// term (minus excluded ones), exclude-only drops docs containing any
// excluded term, and no terms is a pass-through.
func TestStore_FilterTerms(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 2)
	const sp = "filter"

	if err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("basic", "o1", "editor_blocks", "b1", "Anytype on Android is fast", 1)},
		{Entry: entry("basic", "o2", "editor_blocks", "b2", "Anytype on iOS is fast", 2)},
		{Entry: entry("basic", "o3", "editor_blocks", "b3", "Android beta builds", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	all := []Hit{
		{Scope: "basic", ObjectId: "o1", Dataset: "editor_blocks", RecordId: "b1", Score: 0.9},
		{Scope: "basic", ObjectId: "o2", Dataset: "editor_blocks", RecordId: "b2", Score: 0.8},
		{Scope: "basic", ObjectId: "o3", Dataset: "editor_blocks", RecordId: "b3", Score: 0.7},
	}
	ids := func(hs []Hit) []string {
		out := make([]string, 0, len(hs))
		for _, h := range hs {
			out = append(out, h.RecordId)
		}
		return out
	}
	check := func(name string, got []Hit, err error, want ...string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		g := ids(got)
		if len(g) != len(want) {
			t.Fatalf("%s = %v, want %v", name, g, want)
		}
		for i := range want {
			if g[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, g, want)
			}
		}
	}

	got, err := s.FilterTerms(ctx, sp, all, nil, nil)
	check("no terms", got, err, "b1", "b2", "b3")

	got, err = s.FilterTerms(ctx, sp, all, []string{"android"}, nil)
	check("require android", got, err, "b1", "b3")

	got, err = s.FilterTerms(ctx, sp, all, nil, []string{"android"})
	check("exclude android", got, err, "b2")

	got, err = s.FilterTerms(ctx, sp, all, []string{"anytype"}, []string{"ios"})
	check("require anytype exclude ios", got, err, "b1")

	got, err = s.FilterTerms(ctx, sp, all, []string{`"android beta"`}, nil)
	check("require phrase", got, err, "b3")

	got, err = s.FilterTerms(ctx, sp, all, []string{"andr*"}, nil)
	check("require prefix", got, err, "b1", "b3")

	got, err = s.FilterTerms(ctx, sp, all, []string{"windows"}, nil)
	check("require unmatched", got, err)

	// Chunk-aware: the term lives in chunk 1 only; the chunk-1 hit passes,
	// the chunk-0 hit of the same record does not.
	rec := entry("basic", "o4", "editor_blocks", "b4", "", 4)
	if err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: withData(rec, "opening words"), Chunk: 0},
		{Entry: withData(rec, "closing words about android"), Chunk: 1},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	chunks := []Hit{
		{Scope: "basic", ObjectId: "o4", Dataset: "editor_blocks", RecordId: "b4", Chunk: 0},
		{Scope: "basic", ObjectId: "o4", Dataset: "editor_blocks", RecordId: "b4", Chunk: 1},
	}
	got, err = s.FilterTerms(ctx, sp, chunks, []string{"android"}, nil)
	if err != nil || len(got) != 1 || got[0].Chunk != 1 {
		t.Fatalf("chunk-aware require = %+v (%v), want only chunk 1", got, err)
	}
	got, err = s.FilterTerms(ctx, sp, chunks, nil, []string{"android"})
	if err != nil || len(got) != 1 || got[0].Chunk != 0 {
		t.Fatalf("chunk-aware exclude = %+v (%v), want only chunk 0", got, err)
	}
}

// Chunk docs share the record's id range: a record-id delete removes
// every chunk, a chunk-id delete removes only that chunk, and sibling
// records whose ids extend the record's are untouched.
func TestStore_ChunkRangeDelete(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 2)
	const sp = "chunks"
	rec := entry("basic", "o1", "email_messages", "m1", "", 1)
	if err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: withData(rec, "chunk zero"), Chunk: 0},
		{Entry: withData(rec, "chunk one"), Chunk: 1},
		{Entry: withData(rec, "chunk two"), Chunk: 2},
		{Entry: entry("basic", "o1", "email_messages", "m1:x", "sibling colon", 2)},
		{Entry: entry("basic", "o1", "email_messages", "m10", "sibling digit", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	hashes, err := s.DocHashesByRecords(ctx, sp, []string{"o1:email_messages:m1"})
	if err != nil || len(hashes) != 3 {
		t.Fatalf("record hashes = %v (%v), want the 3 chunk docs", hashes, err)
	}
	hits, _ := s.SearchFTS(ctx, sp, "chunk", nil, 10)
	if len(hits) != 3 {
		t.Fatalf("chunk hits = %d, want 3", len(hits))
	}
	chunks := map[int]bool{}
	for _, h := range hits {
		if h.RecordId != "m1" {
			t.Fatalf("chunk hit recordId = %s, want m1", h.RecordId)
		}
		chunks[h.Chunk] = true
	}
	if !chunks[0] || !chunks[1] || !chunks[2] {
		t.Fatalf("chunk numbers = %v", chunks)
	}

	// Delete one chunk by its id: the others and the siblings stay.
	if err := s.Apply(ctx, sp, nil, []string{chunkDocId("o1:email_messages:m1", 2)}, nil); err != nil {
		t.Fatal(err)
	}
	if hits, _ = s.SearchFTS(ctx, sp, "chunk", nil, 10); len(hits) != 2 {
		t.Fatalf("after chunk delete = %d, want 2", len(hits))
	}
	// Delete the record: every chunk goes, siblings stay.
	if err := s.Apply(ctx, sp, nil, []string{"o1:email_messages:m1"}, nil); err != nil {
		t.Fatal(err)
	}
	if hits, _ = s.SearchFTS(ctx, sp, "chunk", nil, 10); len(hits) != 0 {
		t.Fatalf("after record delete = %d, want 0", len(hits))
	}
	if hits, _ = s.SearchFTS(ctx, sp, "sibling", nil, 10); len(hits) != 2 {
		t.Fatalf("siblings = %d, want 2", len(hits))
	}
}

func withData(e index.IndexEntry, data string) index.IndexEntry {
	e.Data = data
	return e
}
