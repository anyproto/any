package indexer

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
)

// A restart over an embedded space with nothing pending: the stored
// vectors serve vector and hybrid search before the embed loop runs, and
// the embed loop keeps landing into the index on disk.
func TestIndexer_VectorSearchAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	const sp = "sp1"
	emb := axisEmbedder{dim: 4}
	opts := Options{Embedder: emb, AnnounceAfter: -1}.withDefaults()
	open := func(dim int) *Store {
		t.Helper()
		st, err := OpenStore(ctx, path, dim, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() }) // a second Close is harmless
		return st
	}
	drain := func(st *Store) {
		t.Helper()
		w := &spaceWorker{ix: &Indexer{store: st, opts: opts}, sp: staticIdSpace{}}
		if err := w.drainPending(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// No lexical match: every hit comes from the vector leg.
	searchAll := func(st *Store, want int) {
		t.Helper()
		ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: opts}
		for _, mode := range []string{api.SearchModeVector, api.SearchModeHybrid} {
			res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "quasar", Mode: mode, Limit: 10}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.VectorStatus != api.VectorStatusUsed || len(res.Hits) != want {
				t.Fatalf("%s: vectorStatus=%s hits=%+v, want used and %d hits", mode, res.VectorStatus, res.Hits, want)
			}
		}
	}

	st := open(emb.dim)
	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("email", "m1", "email_messages", "r", "Anytype for Android release notes", 1)},
		{Entry: entry("email", "m2", "email_messages", "r", "Anytype for iOS release notes", 2)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// No vector index yet: no hits, no error.
	hits, err := st.SearchVector(ctx, sp, []float32{1, 0, 0, 0}, nil, 10, 0)
	if err != nil || len(hits) != 0 {
		t.Fatalf("vector search before the index = %v, %v; want empty, nil", hits, err)
	}
	drain(st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopened as the server does by default: the dimension comes from
	// the db, not the config.
	st = open(0)
	if st.Dim() != emb.dim {
		t.Fatalf("Dim() after restart = %d, want %d", st.Dim(), emb.dim)
	}
	if ids, _, err := st.Pending(ctx, sp, 10); err != nil || len(ids) != 0 {
		t.Fatalf("pending after restart = %v, %v; want none", ids, err)
	}
	searchAll(st, 2)

	// The first batch after the restart lands into the index on disk.
	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("email", "m3", "email_messages", "r", "Anytype desktop and Android sync", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	drain(st)
	searchAll(st, 3)
}

// A query embedding at another dimension than the index's (the model
// changed under a learned dimension) freezes the vector side only.
func TestIndexer_SearchQueryDimMismatch(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	const sp = "sp1"
	opts := Options{Embedder: axisEmbedder{dim: 4}, AnnounceAfter: -1}.withDefaults()
	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("email", "m1", "email_messages", "r", "Anytype for Android release notes", 1)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	w := &spaceWorker{ix: &Indexer{store: st, opts: opts}, sp: staticIdSpace{}}
	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}

	ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: Options{Embedder: axisEmbedder{dim: 8}, AnnounceAfter: -1}.withDefaults()}
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "android", Limit: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != api.SearchModeFTS || res.VectorStatus != api.VectorStatusUnavailable || len(res.Hits) != 1 {
		t.Fatalf("hybrid: mode=%s vectorStatus=%s hits=%+v, want fts/unavailable and m1", res.Mode, res.VectorStatus, res.Hits)
	}
	_, err = ix.Search(ctx, sp, api.SearchRequest{Query: "android", Mode: api.SearchModeVector, Limit: 10}, nil)
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Fatalf("vector: err = %v, want ErrEmbedderUnavailable", err)
	}
}
