//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"errors"
	"testing"

	"github.com/anyproto/any-sync/app/logger"
)

// prefixEmbedder embeds the first n texts of every call and fails on
// the rest — a child dying mid-batch, as the embed loop sees it.
type prefixEmbedder struct {
	axisEmbedder
	n int
}

func (p *prefixEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, _ := p.axisEmbedder.EmbedDocs(ctx, texts[:min(p.n, len(texts))])
	return vecs, errors.New("child died")
}

// A batch that fails part-way still lands the vectors it produced;
// only the rest stays pending, and the round reports the failure.
func TestEmbedDrain_LandsPrefixOfFailedBatch(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	ix := &Indexer{store: st, lg: logger.NewNamed("indexer.test"), opts: Options{
		Embedder:      &prefixEmbedder{axisEmbedder: axisEmbedder{dim: 4}, n: 2},
		AnnounceAfter: -1,
	}.withDefaults()}
	w := &spaceWorker{ix: ix, sp: staticIdSpace{}}
	const sp = "sp1"
	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: entry("email", "m1", "email_messages", "r", "first", 1)},
		{Entry: entry("email", "m2", "email_messages", "r", "second", 2)},
		{Entry: entry("email", "m3", "email_messages", "r", "third", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.drainPending(ctx); err == nil {
		t.Fatal("a failed batch must surface its error")
	}
	remaining, err := st.PendingCount(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("%d docs still pending, want 1 (the prefix landed)", remaining)
	}
}
