//go:build !llamacpp || android || ios

package indexer

import "github.com/anyproto/any/internal/config"

// newLocalEmbedder is unavailable without the `llamacpp` tag and on every
// mobile target: the llama.cpp bindings load libffi at process start, so
// linking them would crash any process on a machine without it.
func newLocalEmbedder(config.IndexLocal, string, string, func(ProcessUpdate)) (Embedder, error) {
	return nil, errLocalNotBuilt
}
