package indexer

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubEmbedder counts calls and can be toggled to fail.
type stubEmbedder struct {
	dim     int
	fail    bool
	docs    int
	queries int
}

func (s *stubEmbedder) EmbedDocs(_ context.Context, texts []string) ([][]float32, error) {
	s.docs++
	if s.fail {
		return nil, errors.New("embedder down")
	}
	return make([][]float32, len(texts)), nil
}

func (s *stubEmbedder) EmbedQuery(_ context.Context, _ string) ([]float32, error) {
	s.queries++
	if s.fail {
		return nil, errors.New("embedder down")
	}
	return make([]float32, s.dim), nil
}

func (s *stubEmbedder) Dim(context.Context) (int, error) { return s.dim, nil }

func TestFallbackEmbedder(t *testing.T) {
	ctx := context.Background()
	primary := &stubEmbedder{dim: 8}
	fallback := &stubEmbedder{dim: 8}
	f := newFallbackEmbedder(primary, fallback)
	clock := time.Unix(1000, 0)
	f.now = func() time.Time { return clock }

	// Healthy: primary serves, fallback untouched.
	if _, err := f.EmbedDocs(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if primary.docs != 1 || fallback.docs != 0 {
		t.Fatalf("healthy: primary=%d fallback=%d, want 1/0", primary.docs, fallback.docs)
	}

	// Primary goes down: each call tries primary then falls back, until the
	// breaker opens at failThreshold (3).
	primary.fail = true
	for i := 0; i < f.failThreshold; i++ {
		if _, err := f.EmbedDocs(ctx, []string{"x"}); err != nil {
			t.Fatalf("fallback should mask primary failure: %v", err)
		}
	}
	if primary.docs != 1+f.failThreshold {
		t.Fatalf("primary attempts = %d, want %d (tried each call until open)", primary.docs, 1+f.failThreshold)
	}
	if fallback.docs != f.failThreshold {
		t.Fatalf("fallback docs = %d, want %d", fallback.docs, f.failThreshold)
	}

	// Breaker open: primary is skipped entirely (no wasted timeout).
	before := primary.docs
	if _, err := f.EmbedDocs(ctx, []string{"y"}); err != nil {
		t.Fatal(err)
	}
	if primary.docs != before {
		t.Fatalf("breaker open: primary called %d times, want 0 more", primary.docs-before)
	}
	if fallback.docs != f.failThreshold+1 {
		t.Fatalf("fallback docs = %d, want %d", fallback.docs, f.failThreshold+1)
	}

	// After cooldown the breaker half-opens and probes the primary once;
	// primary is healthy again → it closes and serves directly.
	clock = clock.Add(f.cooldown + time.Second)
	primary.fail = false
	probe := primary.docs
	if _, err := f.EmbedDocs(ctx, []string{"z"}); err != nil {
		t.Fatal(err)
	}
	if primary.docs != probe+1 {
		t.Fatalf("half-open should probe primary once: +%d", primary.docs-probe)
	}
	// Next call goes straight to primary (breaker closed), fallback idle.
	fbBefore := fallback.docs
	if _, err := f.EmbedDocs(ctx, []string{"z2"}); err != nil {
		t.Fatal(err)
	}
	if fallback.docs != fbBefore {
		t.Fatalf("after recovery fallback should be idle, got +%d", fallback.docs-fbBefore)
	}

	// EmbedQuery follows the same path (primary healthy now).
	if _, err := f.EmbedQuery(ctx, "q"); err != nil {
		t.Fatal(err)
	}
	if primary.queries != 1 {
		t.Fatalf("query primary calls = %d, want 1", primary.queries)
	}

	if d, _ := f.Dim(ctx); d != 8 {
		t.Fatalf("Dim = %d, want 8 (from fallback)", d)
	}
}

// slowEmbedder never answers before its ctx ends — a hung primary.
type slowEmbedder struct{ stubEmbedder }

func (s *slowEmbedder) EmbedQuery(ctx context.Context, _ string) ([]float32, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A budgeted query (Indexer.Search) must not spend its whole budget on
// a hung primary: the primary gets half, the fallback the rest.
func TestFallbackEmbedder_QueryBudgetSplit(t *testing.T) {
	primary := &slowEmbedder{stubEmbedder{dim: 8}}
	fallback := &stubEmbedder{dim: 8}
	f := newFallbackEmbedder(primary, fallback)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	v, err := f.EmbedQuery(ctx, "q")
	if err != nil {
		t.Fatalf("fallback should answer inside the budget: %v", err)
	}
	if len(v) != 8 || fallback.queries != 1 {
		t.Fatalf("fallback not used: v=%d queries=%d", len(v), fallback.queries)
	}
	if elapsed := time.Since(start); elapsed >= 400*time.Millisecond {
		t.Fatalf("query took %s, the whole budget", elapsed)
	}
	if left := time.Until(mustDeadline(t, ctx)); left <= 0 {
		t.Fatal("no budget left for the fallback")
	}

	// Without a deadline the primary is simply tried, as before.
	free, cancelFree := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancelFree()
	}()
	if _, err := f.EmbedQuery(free, "q"); err != nil {
		t.Fatalf("fallback after a cancelled primary: %v", err)
	}
}

func mustDeadline(t *testing.T, ctx context.Context) time.Time {
	t.Helper()
	d, ok := ctx.Deadline()
	if !ok {
		t.Fatal("ctx has no deadline")
	}
	return d
}
