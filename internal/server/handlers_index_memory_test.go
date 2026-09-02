//go:build fts && vector && !gomobile

package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
)

// countingEmbedder wraps fakeEmbedder and counts how many doc texts were
// embedded — lets a test prove that a re-index pass does NOT trigger
// re-embedding (the content-hash skip).
type countingEmbedder struct {
	fakeEmbedder
	docs atomic.Int64
}

func (c *countingEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	c.docs.Add(int64(len(texts)))
	return c.fakeEmbedder.EmbedDocs(ctx, texts)
}

// concEmbedder records the peak number of concurrent EmbedDocs calls, to
// verify the embed loop parallelizes when EmbedConcurrency > 1.
type concEmbedder struct {
	fakeEmbedder
	mu       sync.Mutex
	cur, max int
}

func (c *concEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	c.mu.Lock()
	c.cur++
	if c.cur > c.max {
		c.max = c.cur
	}
	c.mu.Unlock()
	time.Sleep(20 * time.Millisecond) // hold so overlaps are observable
	c.mu.Lock()
	c.cur--
	c.mu.Unlock()
	return c.fakeEmbedder.EmbedDocs(ctx, texts)
}

func (c *concEmbedder) peak() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}

// TestIndexer_ParallelEmbed: with EmbedConcurrency > 1 the embed loop
// embeds batches in parallel (the online throughput path) and still lands
// every pending doc. Seeds pending docs into the store directly, then
// drains via SyncSpace.
func TestIndexer_ParallelEmbed(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	emb := &concEmbedder{fakeEmbedder: fakeEmbedder{dim: 16}}
	st, err := indexer.OpenStoreInMemory(ctx, emb.dim, true)
	if err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(d.sdk, d.eng.chunkers, st, indexer.Options{Embedder: emb, EmbedBatch: 8, EmbedConcurrency: 4})
	d.indexer = ix
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "ParallelEmbed")
	sp, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}

	// Seed 80 pending docs (Data, no vector ⇒ pending) directly.
	const n = 80
	ups := make([]indexer.DocUpsert, n)
	for i := range ups {
		ups[i] = indexer.DocUpsert{Entry: index.IndexEntry{
			Scope: "basic", ObjectId: "o", Dataset: "d",
			RecordId: fmt.Sprintf("r%d", i), Data: fmt.Sprintf("doc number %d", i),
		}}
	}
	if err := st.Apply(ctx, spaceId, ups, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Drain (SyncSpace = advance over the empty SDK space + drainPending).
	if err := ix.SyncSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}

	// Every doc embedded (pending drained).
	if ids, _, err := st.Pending(ctx, spaceId, n+1); err != nil || len(ids) != 0 {
		t.Fatalf("pending after drain = %d (%v), want 0", len(ids), err)
	}
	// Parallelism actually happened (batch 8 × 80 docs, concurrency 4).
	if got := emb.peak(); got < 2 {
		t.Errorf("peak concurrent EmbedDocs = %d, want > 1 (parallel embed)", got)
	}
}
