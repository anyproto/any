package indexer

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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
	// A db an earlier run filled: reopened, with a learned dimension.
	withVectors := func() *Store {
		t.Helper()
		path := filepath.Join(t.TempDir(), "index.db")
		s, err := OpenStore(ctx, path, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.EnsureDim(ctx, 4); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		if s, err = OpenStore(ctx, path, 0, true); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
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

// A network embedder names its model: it adopts an unstamped db once,
// and a later switch to another model or embedder is refused.
func TestStore_PinEmbedder_Network(t *testing.T) {
	ctx := context.Background()
	if id, legacy := EmbedderStamp(NewOpenAI("http://127.0.0.1:1", "bge-m3", "")); id != "openai:bge-m3" || !legacy {
		t.Fatalf("openai stamp = %q, %v", id, legacy)
	}
	if id, legacy := EmbedderStamp(NewOllama("", "mxbai-embed-large")); id != "ollama:mxbai-embed-large" || !legacy {
		t.Fatalf("ollama stamp = %q, %v", id, legacy)
	}

	path := filepath.Join(t.TempDir(), "index.db")
	s, err := OpenStore(ctx, path, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureDim(ctx, 4); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if s, err = OpenStore(ctx, path, 0, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.PinEmbedder(ctx, "ollama:mxbai-embed-large", true); err != nil {
		t.Fatalf("a network embedder on unstamped vectors: %v", err)
	}
	if err := s.PinEmbedder(ctx, "ollama:mxbai-embed-large", true); err != nil {
		t.Fatalf("the same embedder again: %v", err)
	}
	for _, other := range []string{"ollama:nomic-embed-text", "openai:bge-m3", "local:qwen3-embedding-0.6b/1/0"} {
		if err := s.PinEmbedder(ctx, other, true); !errors.Is(err, ErrIndexRebuildRequired) {
			t.Errorf("%s on a db %s filled: %v", other, "ollama:mxbai-embed-large", err)
		}
	}

	// The online/local pair names its primary as a twin: an index the
	// hosted model built alone is the pair's, and keeps its stamp.
	pair := newFallbackEmbedder(NewOllama("", "mxbai-embed-large"), &stubEmbedder{dim: 4})
	if twins := EmbedderStampTwins(pair); !slices.Equal(twins, []string{"ollama:mxbai-embed-large"}) {
		t.Fatalf("the pair's twins = %q", twins)
	}
	if err := s.PinEmbedder(ctx, "local:qwen3-embedding-0.6b/1/0", true, EmbedderStampTwins(pair)...); err != nil {
		t.Fatalf("the pair on its primary's index: %v", err)
	}
	if err := s.PinEmbedder(ctx, "ollama:mxbai-embed-large", true); err != nil {
		t.Fatalf("the primary alone after the pair: %v", err)
	}
	if err := s.PinEmbedder(ctx, "local:qwen3-embedding-0.6b/1/0", true, "openai:bge-m3"); !errors.Is(err, ErrIndexRebuildRequired) {
		t.Errorf("a pair with another primary: %v", err)
	}
}

// A configured dimension is written into a new db before any vector
// exists: the db this open created is adopted by any embedder.
func TestStore_PinEmbedder_FreshWithConfiguredDim(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := OpenStore(ctx, path, 768, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PinEmbedder(ctx, "local:new/1", false); err != nil {
		t.Fatalf("a new db with index.vector.dim set: %v", err)
	}
	_ = s.Close()
	// Reopened, it is stamped: the same embedder passes, another does not.
	s, err = OpenStore(ctx, path, 768, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PinEmbedder(ctx, "local:new/1", false); err != nil {
		t.Fatal(err)
	}
	if err := s.PinEmbedder(ctx, "local:other/1", false); !errors.Is(err, ErrIndexRebuildRequired) {
		t.Fatalf("another embedder: %v", err)
	}
}
