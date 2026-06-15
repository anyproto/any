//go:build !vector || gomobile

package indexer

import "github.com/anyproto/any/internal/config"

// NewEmbedder is a no-op without the `vector` build tag, and on every
// gomobile build regardless of tags: the embedding / ANN pipeline is
// compiled out, so no embedder is ever constructed and the indexer runs
// FTS-only regardless of index.embedder. The embedder implementations
// (ollama / openai / local-llama) are not linked into the binary at all
// — this keeps the llama.cpp purego/ffi bindings out of builds that
// don't want them (notably gomobile/Android, whose missing libffi
// otherwise panics at startup). See docs/13-index.md § build tags and
// the `vector && !gomobile` variant in embed_factory_vector.go.
func NewEmbedder(cfg config.Index, dataDir string) (Embedder, error) {
	return nil, nil
}
