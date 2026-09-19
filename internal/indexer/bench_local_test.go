//go:build llamacpp && !android && !ios

package indexer

import (
	"io"

	"github.com/anyproto/any/internal/config"
)

// benchLocalEmbedder opens the in-process llama.cpp embedder for benches
// that embed with the real model (bench_local_off_test.go is the build
// without it).
func benchLocalEmbedder(cfg config.IndexLocal) (interface {
	Embedder
	io.Closer
}, error) {
	l, err := NewLocal(cfg, "", "", nil)
	if err != nil {
		return nil, err
	}
	return l, nil
}
