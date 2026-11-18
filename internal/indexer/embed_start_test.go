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
	during func()    // runs inside the next PrepareBulk
	at     time.Time // the first EmbedDocs
}

func (e *prepEmbedder) PrepareBulk(context.Context) {
	e.mu.Lock()
	e.events = append(e.events, "prepare")
	during := e.during
	e.during = nil
	e.mu.Unlock()
	if during != nil {
		during()
	}
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

// With no delay the loop still drains what was pending before it
// started. The embedder is prepared before every round that has work,
// never for an empty one, and what arrives during the first wait is in
// the total.
func TestSpaceWorker_EmbedLoopDrainsAtStartAndAsksAgain(t *testing.T) {
	const sp = "sp"
	ctx, cancel := context.WithCancel(context.Background())
	st := mustStore(t, 4)
	for i, id := range []string{"r1", "r2", "r3"} {
		doc := DocUpsert{Entry: index.IndexEntry{Scope: index.ScopeBasic, ObjectId: "O", Dataset: "editor_blocks", RecordId: id, Data: "a text to embed " + id, ApplySeq: uint64(i + 1)}}
		if _, err := st.ApplyPage(ctx, sp, []DocUpsert{doc}, nil, nil, nil, &LinkOps{}); err != nil {
			t.Fatal(err)
		}
	}
	emb := &prepEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	emb.during = func() { // a text arrives while the drain waits
		doc := DocUpsert{Entry: index.IndexEntry{Scope: index.ScopeBasic, ObjectId: "O", Dataset: "editor_blocks", RecordId: "r4", Data: "a text that arrived late", ApplySeq: 4}}
		if _, err := st.ApplyPage(ctx, sp, []DocUpsert{doc}, nil, nil, nil, &LinkOps{}); err != nil {
			t.Error(err)
		}
	}
	var mu sync.Mutex
	var updates []ProcessUpdate
	ix := New(nil, nil, st, Options{Embedder: emb, EmbedBatch: 1, PendingEvery: time.Minute, AnnounceAfter: -1,
		OnProcess: func(u ProcessUpdate) { mu.Lock(); updates = append(updates, u); mu.Unlock() }})
	ix.ctx = ctx
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
			t.Fatal("the backlog was never embedded: no nudge, and the tick is a minute away")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Wait for the drain's terminal frame.
	for {
		mu.Lock()
		n := len(updates)
		var lastU ProcessUpdate
		if n > 0 {
			lastU = updates[n-1]
		}
		mu.Unlock()
		if n > 0 && lastU.Phase == ProcessDone {
			if lastU.Done != 4 || lastU.Total != 4 {
				t.Fatalf("done frame = %d of %d, want 4 of 4: the late text is in the total", lastU.Done, lastU.Total)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the drain never reported done")
		}
		time.Sleep(10 * time.Millisecond)
	}
	events, _ := emb.snapshot()
	want := []string{"prepare", "embed", "prepare", "embed", "prepare", "embed", "prepare", "embed"}
	if len(events) != len(want) {
		t.Fatalf("calls = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("calls = %v, want %v", events, want)
		}
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
