//go:build gomobile

package indexer

import (
	"fmt"

	"github.com/anyproto/any/internal/config"
)

// NewLocal is stubbed out on gomobile/Android builds. The real local embedder
// (embed_local.go) links llama.cpp through the yzma / jupiterrider-ffi bindings,
// whose libffi CIF descriptors initialize at package load and resolve
// ffi_prep_cif — a symbol Android does not provide, which panics the Go runtime
// at startup (before any work runs). The mobile runtime has no indexer, so the
// whole local-embedder path is excluded from the gomobile bind via the
// !gomobile build tag; this stub keeps NewEmbedder's "local" case compiling and
// turns an explicit local-embedder request into an ordinary error.
func NewLocal(cfg config.IndexLocal, dataDir string) (Embedder, error) {
	return nil, fmt.Errorf("indexer: local embedder unavailable on mobile builds")
}
