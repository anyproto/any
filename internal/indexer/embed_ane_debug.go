//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer/coreml"
)

// ErrNeuralEngineUnsupported is a machine or a build the Neural Engine
// path cannot run on.
var ErrNeuralEngineUnsupported = errors.New("unsupported")

// debugSpeedRounds is how many texts a function's speed run predicts.
const debugSpeedRounds = 50

// NeuralEngineReport is what `any debug neural-engine` measured: the
// pinned Core ML package of the default model, compiled apart from the
// model the embedder uses, one entry per function.
type NeuralEngineReport struct {
	Model     string                 `json:"model"`
	Package   string                 `json:"package"`        // its name with the digest of the pin
	Host      string                 `json:"host,omitempty"` // chip and OS build
	CompileMs float64                `json:"compileMs"`
	Functions []NeuralEngineFunction `json:"functions"`
}

// NeuralEngineFunction is one function's timings and compute plan, in the
// order they are taken. A failed load or prediction leaves Error and the
// timings it did not reach empty; a failed plan leaves PlanError.
type NeuralEngineFunction struct {
	Function string `json:"function"`
	// LoadMs is the first load from the model this command compiled.
	LoadMs         float64 `json:"loadMs"`
	FirstPredictMs float64 `json:"firstPredictMs"`
	// The speed run: texts of SpeedTokens tokens, one after another.
	SpeedTokens    int     `json:"speedTokens"`
	MsPerText      float64 `json:"msPerText"`
	TextsPerSecond float64 `json:"textsPerSecond"`
	Error          string  `json:"error,omitempty"` // the load or a prediction failed
	// PlanMs is the plan read after that load.
	PlanMs       float64  `json:"planMs"`
	NeuralEngine int      `json:"neuralEngine"`
	GPU          int      `json:"gpu"`
	CPU          int      `json:"cpu"`
	Unknown      int      `json:"unknown"`               // operations the plan names no device for
	CPUCore      []string `json:"cpuCore,omitempty"`     // core operators planned on the CPU
	UnknownCore  []string `json:"unknownCore,omitempty"` // core operators without a device
	// Placement is the verdict on the plan (coreml.Verdict*); "" = no plan.
	Placement string `json:"placement,omitempty"`
	PlanError string `json:"planError,omitempty"`
}

// neuralEngineProbe is Core ML as the report uses it.
type neuralEngineProbe struct {
	hasEngine func() (bool, error)
	compile   func(pkg string) (string, error)
	plan      func(compiled, function string) (coreml.Placement, error)
	measure   func(compiled, function string, length, dim, rounds int) (coreml.Timing, *coreml.Function, error)
}

// DebugNeuralEngine measures the default model's Core ML package in
// modelsDir, the functions named ("s128") or all; progress, when set, is
// told what is under way. An ended ctx returns at once, mid-call too.
func DebugNeuralEngine(ctx context.Context, modelsDir string, functions []string, progress func(format string, args ...any)) (*NeuralEngineReport, error) {
	if !aneHost() {
		return nil, fmt.Errorf("%w: it needs an Apple Silicon Mac, not a virtual machine, on macOS 15 or later", ErrNeuralEngineUnsupported)
	}
	model, err := lookupLocalModel(config.LocalModelEmbeddingGemma2)
	if err != nil {
		return nil, err
	}
	if model.coreml == nil {
		return nil, fmt.Errorf("%w: %s has no Core ML package", ErrNeuralEngineUnsupported, model.id)
	}
	probe := neuralEngineProbe{hasEngine: coreml.HasNeuralEngine, compile: coreml.Compile, plan: coreml.Plan, measure: coreml.Measure}
	r, err := probe.report(ctx, model.coreml, modelsDir, model.dim, functions, progress)
	if r != nil {
		r.Model = model.id
	}
	return r, err
}

func (p neuralEngineProbe) report(ctx context.Context, pkg *coreMLPackage, modelsDir string, dim int, functions []string, progress func(string, ...any)) (*NeuralEngineReport, error) {
	if progress == nil {
		progress = func(string, ...any) {}
	}
	lengths, err := pickFunctions(pkg, functions)
	if err != nil {
		return nil, err
	}
	switch ok, err := p.hasEngine(); {
	case errors.Is(err, coreml.ErrUnsupported):
		return nil, fmt.Errorf("%w: %v", ErrNeuralEngineUnsupported, err)
	case err != nil:
		return nil, err
	case !ok:
		return nil, fmt.Errorf("%w: this machine has no neural engine", ErrNeuralEngineUnsupported)
	}
	dir := pkg.dir(modelsDir)
	if !pkg.complete(dir) {
		return nil, fmt.Errorf("the Core ML package is not installed: %s is missing or incomplete (the server downloads it for the local embedder)", dir)
	}
	if !debugStep(ctx, progress, "checking %s against its pin", pkg.id()) {
		return nil, ctx.Err()
	}
	if err := pkg.verify(dir); err != nil {
		return nil, fmt.Errorf("the Core ML package is not the pinned one: %w", err)
	}
	// Core ML compiles into a temporary directory of its own: the model
	// the embedder compiled beside the package is not read or written.
	if !debugStep(ctx, progress, "compiling into a temporary directory") {
		return nil, ctx.Err()
	}
	start := time.Now()
	compiled, err := p.compile(dir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(compiled)
	r := &NeuralEngineReport{Package: pkg.id(), Host: aneHostSetup(), CompileMs: millis(time.Since(start))}
	// On a goroutine of its own: a Core ML call cannot be interrupted, and
	// an interrupted command still removes what it compiled.
	done := make(chan []NeuralEngineFunction, 1)
	go func() { done <- p.functions(ctx, compiled, lengths, dim, progress) }()
	select {
	case r.Functions = <-done:
	case <-ctx.Done():
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

// functions measures the functions of the given lengths, one after
// another, until ctx ends. Each is loaded first, as the embedder does
// after a compile, and its plan is read after that load.
func (p neuralEngineProbe) functions(ctx context.Context, compiled string, lengths []int, dim int, progress func(string, ...any)) (out []NeuralEngineFunction) {
	for _, length := range lengths {
		f := NeuralEngineFunction{Function: "s" + strconv.Itoa(length)}
		if !debugStep(ctx, progress, "%s: loading and predicting", f.Function) {
			return out
		}
		t, fn, err := p.measure(compiled, f.Function, length, dim, debugSpeedRounds)
		// A step that failed leaves those before it measured.
		f.LoadMs, f.FirstPredictMs = millis(t.Load), millis(t.FirstPredict)
		if err != nil {
			f.Error = err.Error()
		} else {
			fn.Close()
			f.SpeedTokens, f.MsPerText = t.Tokens, millis(t.PerText)
			if t.PerText > 0 {
				f.TextsPerSecond = float64(int(10*float64(time.Second)/float64(t.PerText))) / 10
			}
		}
		if !debugStep(ctx, progress, "%s: reading its compute plan", f.Function) {
			return out
		}
		start := time.Now()
		plan, err := p.plan(compiled, f.Function)
		f.PlanMs = millis(time.Since(start))
		if err != nil {
			f.PlanError = err.Error()
		} else {
			f.NeuralEngine, f.GPU, f.CPU, f.Unknown = plan.NeuralEngine, plan.GPU, plan.CPU, plan.Unknown
			f.CPUCore, f.UnknownCore = plan.CPUCore, plan.UnknownCore
			f.Placement = plan.Verdict()
		}
		out = append(out, f)
	}
	return out
}

// debugStep tells progress about a step and reports whether to take
// it: not after an interrupt, one that came while the line was written
// included, which a full stderr can block.
func debugStep(ctx context.Context, progress func(string, ...any), format string, args ...any) bool {
	if ctx.Err() != nil {
		return false
	}
	progress(format, args...)
	return ctx.Err() == nil
}

// pickFunctions resolves the functions asked for ("s128"), in the
// package's order; none asked for is all of them.
func pickFunctions(pkg *coreMLPackage, functions []string) ([]int, error) {
	if len(functions) == 0 {
		return pkg.lengths, nil
	}
	asked := map[int]bool{}
	for _, name := range functions {
		length, err := strconv.Atoi(strings.TrimPrefix(name, "s"))
		known := false
		for _, l := range pkg.lengths {
			known = known || l == length
		}
		if err != nil || !strings.HasPrefix(name, "s") || !known {
			return nil, fmt.Errorf("the package has no function %q", name)
		}
		asked[length] = true
	}
	var out []int
	for _, l := range pkg.lengths {
		if asked[l] {
			out = append(out, l)
		}
	}
	return out, nil
}

// millis is d in milliseconds, to the microsecond.
func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
