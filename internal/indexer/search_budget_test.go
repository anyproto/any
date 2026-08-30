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

// cancellingQueryEmbedder cancels the request itself while embedding —
// a client that disconnects after the lexical leg already ran.
type cancellingQueryEmbedder struct {
	axisEmbedder
	cancel context.CancelFunc
}

func (c cancellingQueryEmbedder) EmbedQuery(ctx context.Context, _ string) ([]float32, error) {
	c.cancel()
	<-ctx.Done()
	return nil, ctx.Err()
}

type searchOut struct {
	res api.SearchResponse
	err error
}

// searchBounded runs Search off the test goroutine so a Search that
// does not bound the embedding fails the test instead of hanging it.
func searchBounded(t *testing.T, ix *Indexer, ctx context.Context, sp string, req api.SearchRequest) searchOut {
	t.Helper()
	ch := make(chan searchOut, 1)
	go func() {
		res, err := ix.Search(ctx, sp, req)
		ch <- searchOut{res, err}
	}()
	select {
	case out := <-ch:
		return out
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not bound the query embedding")
		return searchOut{}
	}
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

	out := searchBounded(t, ix, ctx, sp, api.SearchRequest{Query: "anytype", Limit: 10})
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.res.Mode != api.SearchModeFTS || out.res.VectorStatus != api.VectorStatusUnavailable {
		t.Fatalf("mode %q vectorStatus %q, want fts/unavailable", out.res.Mode, out.res.VectorStatus)
	}
	if len(out.res.Hits) != 1 {
		t.Fatalf("lexical leg lost: %d hits", len(out.res.Hits))
	}

	out = searchBounded(t, ix, ctx, sp, api.SearchRequest{Query: "anytype", Mode: api.SearchModeVector, Limit: 10})
	if !errors.Is(out.err, ErrEmbedderUnavailable) {
		t.Fatalf("vector mode err = %v, want ErrEmbedderUnavailable", out.err)
	}

	// The request itself ending mid-embed is the caller's cancellation,
	// not a degraded reply.
	gone, cancel := context.WithCancel(ctx)
	defer cancel()
	ix.opts.Embedder = cancellingQueryEmbedder{axisEmbedder{dim: 4}, cancel}
	ix.opts.QueryEmbedTimeout = time.Minute
	out = searchBounded(t, ix, gone, sp, api.SearchRequest{Query: "anytype", Limit: 10})
	if !errors.Is(out.err, context.Canceled) {
		t.Fatalf("cancelled caller err = %v, want context.Canceled", out.err)
	}
}

func TestOptions_QueryEmbedTimeoutDefault(t *testing.T) {
	if d := (Options{}).withDefaults().QueryEmbedTimeout; d != 5*time.Second {
		t.Fatalf("default QueryEmbedTimeout = %s, want 5s", d)
	}
}
