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
