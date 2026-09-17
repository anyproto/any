//go:build !llamacpp || android || ios

package indexer

import (
	"io"

	"github.com/anyproto/any/internal/config"
)

// benchLocalEmbedder has no local embedder to open in this build.
func benchLocalEmbedder(config.IndexLocal) (interface {
	Embedder
	io.Closer
}, error) {
	return nil, errLocalNotBuilt
}
