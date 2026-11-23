//go:build llamacpp && !android && !ios

package indexer

import (
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

// NeuralEngineFunction is one function's compute plan and timings. A
// step that failed leaves its error and the fields after it empty.
type NeuralEngineFunction struct {
	Function     string   `json:"function"`
	PlanMs       float64  `json:"planMs"`
	NeuralEngine int      `json:"neuralEngine"`
	GPU          int      `json:"gpu"`
	CPU          int      `json:"cpu"`
	Unknown      int      `json:"unknown"`               // operations the plan names no device for
	CPUCore      []string `json:"cpuCore,omitempty"`     // core operators planned on the CPU
	UnknownCore  []string `json:"unknownCore,omitempty"` // core operators without a device
	// Placement is the verdict on the plan (coreml.Verdict*); "" = no plan.
	Placement      string  `json:"placement,omitempty"`
	PlanError      string  `json:"planError,omitempty"`
	LoadMs         float64 `json:"loadMs"`
	FirstPredictMs float64 `json:"firstPredictMs"`
	// The speed run: texts of SpeedTokens tokens, one after another.
	SpeedTokens    int     `json:"speedTokens"`
	MsPerText      float64 `json:"msPerText"`
	TextsPerSecond float64 `json:"textsPerSecond"`
	Error          string  `json:"error,omitempty"` // the load or a prediction failed
}

// neuralEngineProbe is Core ML as the report uses it.
type neuralEngineProbe struct {
	hasEngine func() (bool, error)
	compile   func(pkg string) (string, error)
	plan      func(compiled, function string) (coreml.Placement, error)
	measure   func(compiled, function string, length, dim, rounds int) (coreml.Timing, *coreml.Function, error)
}

// DebugNeuralEngine measures the default model's Core ML package in
// modelsDir on this machine. functions, when set, names the ones to
// measure ("s128"). progress, when set, is told what is under way.
func DebugNeuralEngine(modelsDir string, functions []string, progress func(format string, args ...any)) (*NeuralEngineReport, error) {
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
	r, err := probe.report(model.coreml, modelsDir, model.dim, functions, progress)
	if r != nil {
		r.Model = model.id
	}
	return r, err
}

func (p neuralEngineProbe) report(pkg *coreMLPackage, modelsDir string, dim int, functions []string, progress func(string, ...any)) (*NeuralEngineReport, error) {
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
	progress("checking %s against its pin", pkg.id())
	if err := pkg.verify(dir); err != nil {
		return nil, fmt.Errorf("the Core ML package is not the pinned one: %w", err)
	}
	// Core ML compiles into a temporary directory of its own: the model
	// the embedder compiled beside the package is not read or written.
	progress("compiling into a temporary directory")
	start := time.Now()
	compiled, err := p.compile(dir)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(compiled)
	r := &NeuralEngineReport{Package: pkg.id(), Host: aneHostSetup(), CompileMs: millis(time.Since(start))}
	for _, length := range lengths {
		f := NeuralEngineFunction{Function: "s" + strconv.Itoa(length)}
		progress("%s: reading its compute plan", f.Function)
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
		progress("%s: loading and predicting", f.Function)
		t, fn, err := p.measure(compiled, f.Function, length, dim, debugSpeedRounds)
		if err != nil {
			f.Error = err.Error()
		} else {
			fn.Close()
			f.LoadMs, f.FirstPredictMs = millis(t.Load), millis(t.FirstPredict)
			f.SpeedTokens, f.MsPerText = t.Tokens, millis(t.PerText)
			if t.PerText > 0 {
				f.TextsPerSecond = float64(int(10*float64(time.Second)/float64(t.PerText))) / 10
			}
		}
		r.Functions = append(r.Functions, f)
	}
	return r, nil
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
