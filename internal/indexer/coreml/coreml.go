// Package coreml runs the functions of a compiled Core ML embedding model
// on Apple Silicon. It reaches Core ML through the Objective-C runtime, so
// it needs no cgo and cross-compiles with the rest of the binary.
package coreml

import (
	"errors"
	"fmt"
	"strings"
	"time"
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
	// Unknown counts the operations the plan names no device for, and
	// UnknownCore names the core operators among them.
	Unknown     int
	UnknownCore []string
}

// Conclusive reports whether OnNeuralEngine would hold wherever the
// operations of unknown placement run. A plan with no operation is not.
func (p Placement) Conclusive() bool {
	switch {
	case p.NeuralEngine+p.GPU+p.CPU+p.Unknown == 0:
		return false
	case len(p.CPUCore) > 0, p.NeuralEngine+p.Unknown <= minNeuralEngineOps:
		return true // off the Neural Engine whatever the unknown ones do
	case len(p.UnknownCore) > 0:
		return false
	}
	return p.NeuralEngine > minNeuralEngineOps
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

// LatePlanError is a Plan that stopped waiting for Core ML, which is
// still loading the plan. Done closes when that call has returned.
type LatePlanError struct {
	Function string
	Waited   time.Duration
	Done     <-chan struct{}
}

func (e *LatePlanError) Error() string {
	return fmt.Sprintf("coreml: compute plan for %s: no answer in %s", e.Function, e.Waited)
}

// Thermal states of NSProcessInfo, in its order.
const (
	ThermalNominal = iota
	ThermalFair
	ThermalSerious
	ThermalCritical
)

// Power is what the system says about its power and heat.
type Power struct {
	LowPower bool // Low Power Mode is on
	Thermal  int  // Thermal*
}

// Strain names what makes this a bad moment for minutes of extra work;
// "" = nothing does.
func (p Power) Strain() string {
	switch {
	case p.LowPower:
		return "low power mode"
	case p.Thermal >= ThermalSerious:
		return "thermal pressure"
	}
	return ""
}
