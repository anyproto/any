//go:build !llamacpp || android || ios

package indexer

import "github.com/anyproto/any/internal/config"

// localEmbedderBuilt: this build has no local embedder.
const localEmbedderBuilt = false

// newLocalEmbedder is unavailable without the `llamacpp` tag and on every
// mobile target: the llama.cpp bindings load libffi at process start, so
// linking them would crash any process on a machine without it.
func newLocalEmbedder(config.IndexLocal, string, string, func(ProcessUpdate)) (Embedder, error) {
	return nil, errLocalNotBuilt
}

// localPinnedDim: no local model, no width to contradict.
func localPinnedDim(config.IndexLocal) (model string, dim int) { return "", 0 }
