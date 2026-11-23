//go:build !llamacpp || android || ios

package indexer

import (
	"context"
	"errors"
	"fmt"
)

// ErrNeuralEngineUnsupported is a machine or a build the Neural Engine
// path cannot run on.
var ErrNeuralEngineUnsupported = errors.New("unsupported")

// NeuralEngineReport is empty in a build without the local embedder.
type NeuralEngineReport struct{}

// DebugNeuralEngine: this build has no local embedder to measure.
func DebugNeuralEngine(context.Context, string, []string, func(string, ...any)) (*NeuralEngineReport, error) {
	return nil, fmt.Errorf("%w: this build has no local embedder (it is built with -tags llamacpp)", ErrNeuralEngineUnsupported)
}
