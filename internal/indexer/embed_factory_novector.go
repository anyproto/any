//go:build !vector || gomobile || mobile

package indexer

import "github.com/anyproto/any/internal/config"

// NewEmbedder is a no-op without the `vector` build tag, and on every
// mobile build (`gomobile` or `mobile`) regardless of tags: the
// embedding / ANN pipeline is compiled out, so no embedder is ever
// constructed and the indexer runs FTS-only regardless of
// index.embedder. The embedder implementations (ollama / openai /
// local-llama) are not linked into the binary at all — this keeps the
// llama.cpp purego/ffi bindings out of builds that can't tolerate them,
// where merely linking the edge panics the process at startup. See
// docs/13-index.md § build tags and the `vector && !gomobile && !mobile`
// variant in embed_factory_vector.go.
func NewEmbedder(cfg config.Index, modelsDir, legacyModelsDir string) (Embedder, error) {
	return nil, nil
}
