// Package coreml runs the functions of a compiled Core ML embedding model
// on Apple Silicon. It reaches Core ML through the Objective-C runtime, so
// it needs no cgo and cross-compiles with the rest of the binary.
package coreml

import (
	"errors"
	"strings"
)

// ErrUnsupported is returned by every call on a platform without Core ML,
// and on a macOS whose Core ML lacks a call this package makes.
var ErrUnsupported = errors.New("coreml: not supported on this platform")

// minNeuralEngineOps is the fewest Neural Engine operations a transformer
// function has when its core runs there; a CPU-only plan has none.
const minNeuralEngineOps = 100

// Placement counts where Core ML prefers to run one function's
// operations. It is the planned placement, not a runtime trace.
type Placement struct {
	NeuralEngine int
	GPU          int
	CPU          int
	// CPUCore names the core operators (convolution, matmul, linear,
	// softmax) planned on the CPU.
	CPUCore []string
}

// OnNeuralEngine reports whether the function's core is planned on the
// Neural Engine. Input lookup and the final normalization may stay on the
// CPU.
func (p Placement) OnNeuralEngine() bool {
	return p.NeuralEngine > minNeuralEngineOps && len(p.CPUCore) == 0
}

// coreOperator reports whether an ML program operator belongs to the
// transformer core.
func coreOperator(name string) bool {
	for _, part := range []string{"conv", "matmul", "linear", "softmax"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}
