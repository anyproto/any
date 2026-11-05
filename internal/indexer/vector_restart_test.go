package indexer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// A restart over an embedded space with nothing pending: the stored
// vectors serve vector and hybrid search before the embed loop runs.
func TestIndexer_VectorSearchAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	const sp = "sp1"
	emb := axisEmbedder{dim: 4}
	opts := Options{Embedder: emb, AnnounceAfter: -1}.withDefaults()

	st, err := OpenStore(ctx, path, emb.dim, true)
	if err != nil {
		t.Fatal(err)
	}
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
	w := &spaceWorker{ix: &Indexer{store: st, opts: opts}, sp: staticIdSpace{}}
	if err := w.drainPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = OpenStore(ctx, path, emb.dim, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if ids, _, err := st.Pending(ctx, sp, 10); err != nil || len(ids) != 0 {
		t.Fatalf("pending after restart = %v, %v; want none", ids, err)
	}

	ix := &Indexer{store: st, opts: opts}
	for _, mode := range []string{api.SearchModeVector, api.SearchModeHybrid} {
		// No lexical match: every hit comes from the vector leg.
		res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "quasar", Mode: mode, Limit: 10}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.VectorStatus != api.VectorStatusUsed || len(res.Hits) != 2 {
			t.Fatalf("%s after restart: vectorStatus=%s hits=%+v, want used and both docs", mode, res.VectorStatus, res.Hits)
		}
	}
}
