//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
)

// blockingEmbedder never answers a query until the caller gives up —
// a cold model load or a wedged child, as Search sees them.
type blockingEmbedder struct{ axisEmbedder }

func (b blockingEmbedder) EmbedQuery(ctx context.Context, _ string) ([]float32, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Search bounds the query embedding: past the budget hybrid answers
// lexical-only and says so, vector fails as an embedder outage, and a
// caller that left gets its own cancellation rather than a degraded
// reply.
func TestIndexer_SearchQueryEmbedBudget(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: Options{
		Embedder:          blockingEmbedder{axisEmbedder{dim: 4}},
		AnnounceAfter:     -1,
		QueryEmbedTimeout: 50 * time.Millisecond,
	}.withDefaults()}
	const sp = "sp1"
	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("email", "m1", "email_messages", "r", "Anytype for Android release notes", 1)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "anytype", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("hybrid search waited %s for the query embedding", elapsed)
	}
	if res.Mode != api.SearchModeFTS || res.VectorStatus != api.VectorStatusUnavailable {
		t.Fatalf("mode %q vectorStatus %q, want fts/unavailable", res.Mode, res.VectorStatus)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("lexical leg lost: %d hits", len(res.Hits))
	}

	_, err = ix.Search(ctx, sp, api.SearchRequest{Query: "anytype", Mode: api.SearchModeVector, Limit: 10})
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Fatalf("vector mode err = %v, want ErrEmbedderUnavailable", err)
	}

	gone, cancel := context.WithCancel(ctx)
	cancel()
	_, err = ix.Search(gone, sp, api.SearchRequest{Query: "anytype", Limit: 10})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller err = %v, want context.Canceled", err)
	}
}

func TestOptions_QueryEmbedTimeoutDefault(t *testing.T) {
	if d := (Options{}).withDefaults().QueryEmbedTimeout; d != 5*time.Second {
		t.Fatalf("default QueryEmbedTimeout = %s, want 5s", d)
	}
}
