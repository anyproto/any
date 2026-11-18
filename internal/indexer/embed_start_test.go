package indexer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/index"
)

// prepEmbedder records the order of its calls: the embed loop prepares
// it before the first batch.
type prepEmbedder struct {
	axisEmbedder
	mu     sync.Mutex
	events []string
	at     time.Time // the first EmbedDocs
}

func (e *prepEmbedder) PrepareBulk(context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, "prepare")
}

func (e *prepEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	if e.at.IsZero() {
		e.at = time.Now()
	}
	e.events = append(e.events, "embed")
	e.mu.Unlock()
	return e.axisEmbedder.EmbedDocs(ctx, texts)
}

func (e *prepEmbedder) snapshot() ([]string, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...), e.at
}

// The embed loop leaves the start of a run to the boot: nothing embeds
// before EmbedStartDelay has passed, a backlog from the previous run is
// drained right after it, and the embedder is prepared before its first
// batch.
func TestSpaceWorker_EmbedLoopStartsAfterTheDelay(t *testing.T) {
	const sp = "sp"
	const delay = 400 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	st := mustStore(t, 4)
	doc := DocUpsert{Entry: index.IndexEntry{Scope: index.ScopeBasic, ObjectId: "O", Dataset: "editor_blocks", RecordId: "r1", Data: "a text to embed", ApplySeq: 1}}
	if _, err := st.ApplyPage(ctx, sp, []DocUpsert{doc}, nil, nil, nil, &LinkOps{}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PendingCount(ctx, sp); err != nil || n != 1 {
		t.Fatalf("pending = %d, %v; want 1", n, err)
	}

	emb := &prepEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	// The doc was pending before this run: nothing nudges the loop for
	// it, and the tick is far away.
	ix := New(nil, nil, st, Options{Embedder: emb, EmbedStartDelay: delay, PendingEvery: time.Minute, AnnounceAfter: -1})
	ix.ctx = ctx
	started := time.Now()
	ix.embedFrom = started.Add(ix.opts.EmbedStartDelay)
	w := newSpaceWorker(ix, idSpace{id: sp})
	w.ctx = ctx
	ix.wg.Add(1)
	go w.embedLoop()
	defer func() {
		cancel()
		ix.wg.Wait()
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if n, err := st.PendingCount(ctx, sp); err == nil && n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the doc was never embedded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	events, at := emb.snapshot()
	if len(events) < 2 || events[0] != "prepare" || events[1] != "embed" {
		t.Fatalf("calls = %v, want prepare before the first embed", events)
	}
	if waited := at.Sub(started); waited < delay {
		t.Fatalf("embedded %s after the start, before the %s delay ended", waited, delay)
	}
}

// Start sets the moment the embed loops may begin.
func TestIndexer_StartSetsTheEmbedDelay(t *testing.T) {
	const delay = 90 * time.Second
	st := mustStore(t, 0)
	ix := New(nil, index.NewRegistry(), st, Options{EmbedStartDelay: delay})
	ix.spacesAPI = &fakeSpaces{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := time.Now()
	ix.Start(ctx)
	after := time.Now()
	defer func() { _ = ix.Close() }()
	if ix.embedFrom.Before(before.Add(delay)) || ix.embedFrom.After(after.Add(delay)) {
		t.Fatalf("embedFrom = %s after Start, want %s", ix.embedFrom.Sub(before), delay)
	}
}
