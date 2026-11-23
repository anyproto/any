// Package coreml runs the functions of a compiled Core ML embedding model
// on Apple Silicon. It reaches Core ML through the Objective-C runtime, so
// it needs no cgo and cross-compiles with the rest of the binary.
package coreml

import (
	"errors"
	"fmt"
	"math"
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

// What a plan says of where a function's core runs.
const (
	VerdictOn      = "on"      // on the Neural Engine
	VerdictOff     = "off"     // a core operator on the CPU, or too few operations on the Neural Engine
	VerdictUnknown = "unknown" // the plan names no device for operations the answer hangs on
)

// Verdict is the judgement on a plan: whether Core ML puts the function's
// core on the Neural Engine, or that the plan does not say.
func (p Placement) Verdict() string {
	switch {
	case !p.Conclusive():
		return VerdictUnknown
	case p.OnNeuralEngine():
		return VerdictOn
	}
	return VerdictOff
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

// planned is what a plan's completion handler gave: the plan's handle,
// or why there is none.
type planned struct {
	plan uintptr
	err  string
}

// awaitPlan waits for a function's plan until timeout. Past it the wait
// is a *LatePlanError whose Done closes once the handler has run; a plan
// that still comes then goes to release.
func awaitPlan(done <-chan planned, function string, timeout time.Duration, release func(plan uintptr)) (uintptr, error) {
	select {
	case r := <-done:
		if r.plan == 0 {
			return 0, fmt.Errorf("coreml: compute plan for %s: %s", function, r.err)
		}
		return r.plan, nil
	case <-time.After(timeout):
	}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		if r := <-done; r.plan != 0 {
			release(r.plan)
		}
	}()
	return 0, &LatePlanError{Function: function, Waited: timeout, Done: returned}
}

// Timing is how long the calls of one function take on this machine.
type Timing struct {
	Load         time.Duration // opening the function
	FirstPredict time.Duration
	Norm         float64       // length of the first prediction's vector
	Tokens       int           // of each text of the speed run
	PerText      time.Duration // mean of the speed run; 0 = none was run
}

// Measure opens one function of the compiled model and times its first
// prediction, then rounds predictions of a text three quarters of its
// length. The caller closes the function it returns.
func Measure(compiled, function string, length, dim, rounds int) (Timing, *Function, error) {
	var t Timing
	start := time.Now()
	fn, err := Open(compiled, function, length, dim)
	if err != nil {
		return t, nil, err
	}
	t.Load = time.Since(start)
	out := make([]float32, dim)
	start = time.Now()
	if err := fn.Embed(filler(3), out); err != nil {
		fn.Close()
		return t, nil, err
	}
	t.FirstPredict = time.Since(start)
	var sum float64
	for _, x := range out {
		sum += float64(x) * float64(x)
	}
	t.Norm = math.Sqrt(sum)
	if rounds <= 0 {
		return t, fn, nil
	}
	tokens := filler(max(length*3/4, 3))
	t.Tokens = len(tokens)
	start = time.Now()
	for range rounds {
		if err := fn.Embed(tokens, out); err != nil {
			fn.Close()
			return t, nil, err
		}
	}
	t.PerText = time.Since(start) / time.Duration(rounds)
	return t, fn, nil
}

// filler is a token sequence of n ids, at least two: BOS, one repeated
// word, EOS.
func filler(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = 9259
	}
	out[0], out[n-1] = 2, 1
	return out
}
