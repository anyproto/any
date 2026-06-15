// Package indexer is the phase-2 consumer of the chunker contract in
// internal/index (docs/13-index.md): a background service that keeps a
// local any-store database with a BM25 full-text index and an IVF-SQ
// vector index per space, fed from the SDK's per-space change feed
// (Space.Changes()), and a hybrid (RRF) search over both.
package indexer

import (
	"context"
	"errors"
)

// ErrEmbedderUnavailable wraps query-time embedding failures: the
// embedder is configured but not currently reachable. Vector-mode
// searches surface it as a retryable condition; hybrid degrades to FTS
// instead.
var ErrEmbedderUnavailable = errors.New("indexer: embedder unavailable")

// Embedder turns text into vectors. A nil Embedder on the Indexer means
// FTS-only operation: documents are stored without vectors and the
// vector search leg is unavailable.
//
// Documents and queries embed through separate methods because retrieval
// models distinguish the two roles (task prompts / instructions); both
// must produce vectors of the same dimension.
type Embedder interface {
	// EmbedDocs embeds passages for storage, one vector per text.
	EmbedDocs(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQuery embeds a single search query.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	// Dim reports the embedding dimension (probing the backend if needed).
	Dim(ctx context.Context) (int, error)
}

// NewEmbedder constructs the configured embedding client. Its two
// build-tagged variants live in embed_factory_vector.go (the real
// switch) and embed_factory_novector.go (a no-op returning nil, so the
// indexer runs FTS-only when the `vector` build tag is absent). See
// docs/13-index.md § build tags.
