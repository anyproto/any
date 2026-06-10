package indexer

import (
	"context"
	"testing"

	"github.com/anyproto/any/internal/index"
)

func entry(scope, objectId, dataset, recordId, data string, seq uint64) index.IndexEntry {
	return index.IndexEntry{Scope: scope, ObjectId: objectId, Dataset: dataset, RecordId: recordId, Data: data, AddSeq: seq}
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
	}, nil)
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
		[]string{"chat_messages/m2", "chat_messages/never-existed"})
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

func TestStore_PurgeObjectAndDropSpace(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 0)
	const sp = "space1"

	err := s.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("chat", "obj1", "chat_messages", "m1", "alpha bravo", 1)},
		{Entry: entry("chat", "obj1", "chat_messages", "m2", "alpha charlie", 2)},
		{Entry: entry("chat", "obj2", "chat_messages", "m3", "alpha delta", 3)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.PurgeObject(ctx, sp, "obj1"); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchFTS(ctx, sp, "alpha", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ObjectId != "obj2" {
		t.Fatalf("after purge, hits = %+v, want only obj2", hits)
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
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ids, texts, err := s.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "chat_messages/m2" || texts[0] != "awaiting embedding" {
		t.Fatalf("pending = %v / %v, want only chat_messages/m2", ids, texts)
	}

	query := []float32{0.9, 0.1, 0, 0}
	hits, err := s.SearchVector(ctx, sp, query, nil, 10)
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
	if err := s.SetVectors(ctx, sp, []string{"chat_messages/m2", "chat_messages/gone"}, [][]float32{{1, 0, 0, 0}, {0, 0, 1, 0}}); err != nil {
		t.Fatal(err)
	}
	ids, _, err = s.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("pending after SetVectors = %v, want none", ids)
	}
	hits, err = s.SearchVector(ctx, sp, query, []string{"chat"}, 10)
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
	err = s.Apply(ctx, sp, []DocUpsert{{Entry: entry("chat", "obj1", "chat_messages", "m1", "rewritten", 5)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids, _, err = s.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "chat_messages/m1" {
		t.Fatalf("pending after re-upsert = %v, want chat_messages/m1", ids)
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
	}, nil); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.Pending(ctx, sp, 10)
	if err != nil || len(ids) != 1 {
		t.Fatalf("pending with unknown dim = %v, %v; want the doc queued", ids, err)
	}
	hits, err := s.SearchVector(ctx, sp, []float32{1, 0}, nil, 10)
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
	hits, err = s.SearchVector(ctx, sp, []float32{1, 0, 0, 0}, nil, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("vector search after EnsureDim = %v, %v; want the doc", hits, err)
	}

	// A contradicting dimension (model swapped) is an error.
	if err := s.EnsureDim(ctx, 8); err == nil {
		t.Fatal("EnsureDim with a different dim should error")
	}
}
