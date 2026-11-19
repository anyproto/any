package indexer

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// deferEmbedder leaves texts that start with "long" without a vector
// until released, as an embedder whose runtime for them is not ready.
type deferEmbedder struct {
	axisEmbedder
	mu    sync.Mutex
	ready bool
	wake  chan struct{}
	calls int
	sent  []string
}

func (e *deferEmbedder) EmbedDocsRouted(_ context.Context, _, texts []string) ([][]float32, EmbedRoute, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.sent = append(e.sent, texts...)
	out := make([][]float32, len(texts))
	rt := EmbedRoute{Backends: []string{EmbedBackendANE}}
	for i, t := range texts {
		if strings.HasPrefix(t, "long") && !e.ready {
			rt.Deferred++
			continue
		}
		v := make([]float32, e.dim)
		v[0] = 1
		out[i] = v
	}
	return out, rt, nil
}

func (e *deferEmbedder) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ready = true
	if e.wake != nil {
		close(e.wake)
		e.wake = make(chan struct{})
	}
}

func (e *deferEmbedder) counts() (calls int, sent []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls, append([]string(nil), e.sent...)
}

// wakingEmbedder is a deferEmbedder that says when to send again.
type wakingEmbedder struct{ *deferEmbedder }

func (e wakingEmbedder) DeferredWake() <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.wake
}

func deferDrain(t *testing.T, emb Embedder, every time.Duration, texts []string) (*spaceWorker, *Store, func() []string) {
	t.Helper()
	ctx := context.Background()
	st := mustStore(t, 4)
	var mu sync.Mutex
	var phases []string
	ix := &Indexer{store: st, opts: Options{
		Embedder:      emb,
		EmbedBatch:    2,
		PendingEvery:  every,
		AnnounceAfter: -1,
		OnProcess: func(u ProcessUpdate) {
			mu.Lock()
			defer mu.Unlock()
			if u.Embedding != nil && (len(phases) == 0 || phases[len(phases)-1] != u.Embedding.Phase) {
				phases = append(phases, u.Embedding.Phase)
			}
		},
	}.withDefaults()}
	for i, text := range texts {
		up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", fmt.Sprintf("m%02d", i), text, uint64(i+1))}
		if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	return &spaceWorker{ix: ix, sp: staticIdSpace{}}, st, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), phases...)
	}
}

func waitPending(t *testing.T, st *Store, want int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		n, err := st.PendingCount(context.Background(), "sp1")
		if err != nil {
			t.Fatal(err)
		}
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d docs pending, want %d", n, want)
		}
	}
}

// Texts the embedder defers do not hold the queue: every other text
// lands, past them and across pages, then the drain waits for the
// embedder's word without sending anything, and lands the rest.
func TestEmbedDrainGoesPastDeferredTexts(t *testing.T) {
	texts := []string{"long 1", "long 2", "long 3", "a", "b", "long 4", "c", "d", "e", "long 5"}
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, phases := deferDrain(t, emb, time.Hour, texts)
	done := make(chan error, 1)
	go func() { done <- w.drainPending(context.Background()) }()

	waitPending(t, st, 5) // the five short texts landed
	time.Sleep(100 * time.Millisecond)
	calls, sent := emb.counts()
	select {
	case err := <-done:
		t.Fatalf("the drain ended with texts deferred: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if again, _ := emb.counts(); again != calls {
		t.Fatalf("%d calls while waiting, %d before: the drain is not waiting", again, calls)
	}
	if len(sent) != len(texts) {
		t.Fatalf("sent %v, want each text once before the wait", sent)
	}
	if got := phases(); !slices.Equal(got, []string{EmbedPhaseEmbedding, EmbedPhasePreparing}) {
		t.Fatalf("phases %v, want embedding then preparing", got)
	}

	emb.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain did not end after the embedder's word")
	}
	waitPending(t, st, 0)
	if _, sent = emb.counts(); len(sent) != len(texts)+5 {
		t.Fatalf("sent %d texts, want the five deferred ones once more", len(sent))
	}
	if got := phases(); !slices.Equal(got, []string{EmbedPhaseEmbedding, EmbedPhasePreparing, EmbedPhaseEmbedding}) {
		t.Fatalf("phases %v, want embedding again after the wait", got)
	}
}

// An embedder that defers and never says when is asked again on the
// loop's own period: the drain neither ends nor spins.
func TestEmbedDrainRetriesDeferredTextsOnThePeriod(t *testing.T) {
	const every = 150 * time.Millisecond
	emb := &deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	w, st, _ := deferDrain(t, emb, every, []string{"long 1", "a", "long 2"})
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- w.drainPending(context.Background()) }()
	waitPending(t, st, 2)
	time.Sleep(3 * every)
	if calls, _ := emb.counts(); calls > 2+2*int(time.Since(start)/every+1) {
		t.Fatalf("%d calls in %s with a %s period: the drain spins", calls, time.Since(start), every)
	}
	emb.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain did not pick the deferred texts up")
	}
	waitPending(t, st, 0)
}

// A drain that waits for deferred texts leaves with its context.
func TestEmbedDrainLeavesTheDeferredWait(t *testing.T) {
	emb := wakingEmbedder{&deferEmbedder{axisEmbedder: axisEmbedder{dim: 4}, wake: make(chan struct{})}}
	w, st, _ := deferDrain(t, emb, time.Hour, []string{"long 1", "a"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.drainPending(ctx) }()
	waitPending(t, st, 1)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the drain ended without an error and a text pending")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the drain outlived its context")
	}
}

func TestStore_PendingFrom(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	for i := range 5 {
		up := DocUpsert{Entry: entry("chat", "o1", "chat_messages", fmt.Sprintf("m%d", i), fmt.Sprintf("text %d", i), uint64(i+1))}
		if err := st.Apply(ctx, "sp1", []DocUpsert{up}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	all, _, err := st.Pending(ctx, "sp1", 10)
	if err != nil || len(all) != 5 {
		t.Fatalf("Pending: %v, %v", all, err)
	}
	ids, texts, err := st.PendingFrom(ctx, "sp1", 2, 2)
	if err != nil || !slices.Equal(ids, all[2:4]) || len(texts) != 2 {
		t.Fatalf("PendingFrom(2, 2) = %v, %v, %v; want %v", ids, texts, err, all[2:4])
	}
	if ids, _, err = st.PendingFrom(ctx, "sp1", 5, 2); err != nil || len(ids) != 0 {
		t.Fatalf("PendingFrom past the queue = %v, %v", ids, err)
	}
}
