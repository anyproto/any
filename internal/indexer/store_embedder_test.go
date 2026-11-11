package indexer

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// The index records the embedder whose vectors it holds and refuses
// another one; a db from before the stamp is adopted only by an
// embedder that could have built it, or while it holds no vectors.
func TestStore_PinEmbedder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	open := func() *Store {
		t.Helper()
		s, err := OpenStore(ctx, path, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}

	s := open()
	// No vectors yet: any embedder adopts the db.
	if err := s.PinEmbedder(ctx, "local:b/1", false); err != nil {
		t.Fatalf("empty db: %v", err)
	}
	if err := s.EnsureDim(ctx, 4); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s = open()
	if err := s.PinEmbedder(ctx, "local:b/1", false); err != nil {
		t.Fatalf("same embedder after a restart: %v", err)
	}
	if s.Dim() != 4 {
		t.Fatalf("the stamp write lost the dimension: %d", s.Dim())
	}
	for _, other := range []string{"local:a/1", ""} {
		if err := s.PinEmbedder(ctx, other, true); !errors.Is(err, ErrIndexRebuildRequired) {
			t.Fatalf("embedder %q on a db stamped by another: %v", other, err)
		}
	}
	// A meta rewrite keeps the stamp.
	if err := s.PinChunkRunes(ctx, 0); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s = open()
	if err := s.PinEmbedder(ctx, "local:b/1", false); err != nil {
		t.Fatalf("stamp lost across a meta rewrite: %v", err)
	}
}

func TestStore_PinEmbedder_Unstamped(t *testing.T) {
	ctx := context.Background()
	withVectors := func() *Store {
		t.Helper()
		s := mustStore(t, 0)
		if err := s.EnsureDim(ctx, 4); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Vectors of unknown origin: only an embedder that predates the
	// stamp may own them.
	if err := withVectors().PinEmbedder(ctx, "local:new/1", false); !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("a new model on unstamped vectors: %v", err)
	}
	s := withVectors()
	if err := s.PinEmbedder(ctx, "local:old/1", true); err != nil {
		t.Fatalf("a legacy model on unstamped vectors: %v", err)
	}
	if err := s.PinEmbedder(ctx, "local:new/1", false); !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("after the adoption the db is stamped: %v", err)
	}
	// An unknown embedder leaves an unstamped db alone.
	if err := withVectors().PinEmbedder(ctx, "", true); err != nil {
		t.Fatalf("unknown embedder on an unstamped db: %v", err)
	}
}
