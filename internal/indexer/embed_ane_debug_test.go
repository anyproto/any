//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/indexer/coreml"
)

// debugPackage is a pinned two-function package on disk under models,
// and a probe that compiles it into a directory of its own.
func debugPackage(t *testing.T) (pkg *coreMLPackage, models string, probe neuralEngineProbe, compiled string) {
	t.Helper()
	models = t.TempDir()
	sum := sha256.Sum256([]byte("weights"))
	pkg = &coreMLPackage{name: "m", lengths: []int{128, 256}, files: []coreMLFile{{"weight.bin", hex.EncodeToString(sum[:])}}}
	if err := os.MkdirAll(pkg.dir(models), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg.dir(models), "weight.bin"), []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled = filepath.Join(t.TempDir(), "m.mlmodelc")
	probe = neuralEngineProbe{
		hasEngine: func() (bool, error) { return true, nil },
		compile: func(string) (string, error) {
			return compiled, os.MkdirAll(compiled, 0o755)
		},
		plan: func(_, function string) (coreml.Placement, error) {
			if function == "s256" {
				return coreml.Placement{CPU: 6014, CPUCore: []string{"ios18.matmul"}}, nil
			}
			return coreml.Placement{NeuralEngine: 6000, CPU: 14}, nil
		},
		measure: func(_, _ string, length, _, rounds int) (coreml.Timing, *coreml.Function, error) {
			return coreml.Timing{Load: time.Second, FirstPredict: 40 * time.Millisecond, Norm: 1,
				Tokens: length * 3 / 4, PerText: 20 * time.Millisecond}, nil, nil
		},
	}
	return pkg, models, probe, compiled
}

// The report has every function's timings and its plan with its
// verdict, the plan read after the load, and the model compiled for it
// is gone afterwards.
func TestNeuralEngineReport(t *testing.T) {
	pkg, models, probe, compiled := debugPackage(t)
	var calls []string
	plan, measure := probe.plan, probe.measure
	probe.plan = func(compiled, function string) (coreml.Placement, error) {
		calls = append(calls, "plan "+function)
		return plan(compiled, function)
	}
	probe.measure = func(compiled, function string, length, dim, rounds int) (coreml.Timing, *coreml.Function, error) {
		calls = append(calls, "load "+function)
		return measure(compiled, function, length, dim, rounds)
	}
	var said []string
	r, err := probe.report(context.Background(), pkg, models, 4, nil, func(format string, _ ...any) { said = append(said, format) })
	if err != nil {
		t.Fatal(err)
	}
	if r.Package != pkg.id() || len(r.Functions) != 2 || len(said) == 0 {
		t.Fatalf("report %+v, progress %v", r, said)
	}
	if want := []string{"load s128", "plan s128", "load s256", "plan s256"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	on, off := r.Functions[0], r.Functions[1]
	if on.Function != "s128" || on.Placement != coreml.VerdictOn || on.NeuralEngine != 6000 || on.CPU != 14 ||
		on.LoadMs != 1000 || on.FirstPredictMs != 40 || on.MsPerText != 20 || on.TextsPerSecond != 50 || on.SpeedTokens != 96 {
		t.Fatalf("s128: %+v", on)
	}
	if off.Function != "s256" || off.Placement != coreml.VerdictOff || !reflect.DeepEqual(off.CPUCore, []string{"ios18.matmul"}) {
		t.Fatalf("s256: %+v", off)
	}
	if _, err := os.Stat(compiled); !os.IsNotExist(err) {
		t.Fatalf("the temporary compiled model is still there: %v", err)
	}
	if entries, _ := os.ReadDir(models); len(entries) != 1 {
		t.Fatalf("the models directory has %d entries, want the package alone", len(entries))
	}
}

// A function filter keeps the package's order; a plan or a load that
// fails is that function's error and the others are still measured. A
// speed run that fails keeps the load and first prediction it followed.
func TestNeuralEngineReport_FilterAndFailures(t *testing.T) {
	pkg, models, probe, _ := debugPackage(t)
	r, err := probe.report(context.Background(), pkg, models, 4, []string{"s256"}, nil)
	if err != nil || len(r.Functions) != 1 || r.Functions[0].Function != "s256" {
		t.Fatalf("filtered: %+v, %v", r, err)
	}
	if _, err := probe.report(context.Background(), pkg, models, 4, []string{"s512"}, nil); err == nil {
		t.Fatal("a function the package does not have was accepted")
	}
	probe.plan = func(_, function string) (coreml.Placement, error) {
		return coreml.Placement{}, errors.New("coreml: compute plan for " + function + ": no answer")
	}
	measure := probe.measure
	probe.measure = func(compiled, function string, length, dim, rounds int) (coreml.Timing, *coreml.Function, error) {
		if function == "s128" {
			return coreml.Timing{}, nil, errors.New("coreml: load s128: test error 1")
		}
		return measure(compiled, function, length, dim, rounds)
	}
	r, err = probe.report(context.Background(), pkg, models, 4, nil, nil)
	if err != nil || len(r.Functions) != 2 {
		t.Fatalf("with failures: %+v, %v", r, err)
	}
	if f := r.Functions[0]; f.PlanError == "" || f.Placement != "" || f.Error == "" || f.LoadMs != 0 {
		t.Fatalf("s128: %+v", f)
	}
	if f := r.Functions[1]; f.PlanError == "" || f.Error != "" || f.LoadMs != 1000 {
		t.Fatalf("s256: %+v", f)
	}
	probe.measure = func(_, _ string, length, _, _ int) (coreml.Timing, *coreml.Function, error) {
		return coreml.Timing{Load: time.Second, FirstPredict: 40 * time.Millisecond, Norm: 1, Tokens: length * 3 / 4},
			nil, errors.New("coreml: s128 predict: test error 2")
	}
	r, err = probe.report(context.Background(), pkg, models, 4, []string{"s128"}, nil)
	if err != nil || len(r.Functions) != 1 {
		t.Fatalf("a failed speed run: %+v, %v", r, err)
	}
	if f := r.Functions[0]; f.Error == "" || f.LoadMs != 1000 || f.FirstPredictMs != 40 || f.SpeedTokens != 0 || f.MsPerText != 0 || f.TextsPerSecond != 0 {
		t.Fatalf("a failed speed run: %+v", f)
	}
}

// Nothing is compiled from a package that is missing or is not the
// pinned one, or on a machine without a Neural Engine.
func TestNeuralEngineReport_Refusals(t *testing.T) {
	pkg, models, probe, _ := debugPackage(t)
	probe.compile = func(string) (string, error) {
		t.Fatal("compiled")
		return "", nil
	}
	if _, err := probe.report(context.Background(), pkg, t.TempDir(), 4, nil, nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("a missing package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkg.dir(models), "weight.bin"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.report(context.Background(), pkg, models, 4, nil, nil); err == nil || !strings.Contains(err.Error(), "not the pinned one") {
		t.Fatalf("a package off its pin: %v", err)
	}
	probe.hasEngine = func() (bool, error) { return false, nil }
	if _, err := probe.report(context.Background(), pkg, models, 4, nil, nil); !errors.Is(err, ErrNeuralEngineUnsupported) {
		t.Fatalf("no neural engine: %v", err)
	}
	probe.hasEngine = func() (bool, error) { return false, coreml.ErrUnsupported }
	if _, err := probe.report(context.Background(), pkg, models, 4, nil, nil); !errors.Is(err, ErrNeuralEngineUnsupported) {
		t.Fatalf("no Core ML: %v", err)
	}
}

// Off an Apple Silicon Mac the command is unsupported before it looks
// for anything.
func TestDebugNeuralEngine_UnsupportedHost(t *testing.T) {
	host := aneHost
	aneHost = func() bool { return false }
	defer func() { aneHost = host }()
	if _, err := DebugNeuralEngine(context.Background(), t.TempDir(), nil, nil); !errors.Is(err, ErrNeuralEngineUnsupported) {
		t.Fatalf("DebugNeuralEngine = %v, want unsupported", err)
	}
}

// An interrupted run ends at once, inside a Core ML call too, and still
// removes the model it compiled.
func TestNeuralEngineReport_Interrupted(t *testing.T) {
	pkg, models, probe, compiled := debugPackage(t)
	ctx, cancel := context.WithCancel(context.Background())
	inPlan, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	plan := probe.plan
	probe.plan = func(compiled, function string) (coreml.Placement, error) {
		close(inPlan)
		<-release
		return plan(compiled, function)
	}
	go func() {
		<-inPlan
		cancel()
	}()
	if r, err := probe.report(ctx, pkg, models, 4, nil, nil); !errors.Is(err, context.Canceled) || r != nil {
		t.Fatalf("an interrupted run: %+v, %v", r, err)
	}
	if _, err := os.Stat(compiled); !os.IsNotExist(err) {
		t.Fatalf("the temporary compiled model is still there: %v", err)
	}
}

// An interrupt that comes while a step's progress line is written, which
// a full stderr can hold up, starts none of the Core ML calls after it.
func TestNeuralEngineReport_InterruptedBeforeACall(t *testing.T) {
	for _, line := range []string{"checking ", "compiling ", "%s: loading", "%s: reading"} {
		pkg, models, probe, compiled := debugPackage(t)
		var calls []string
		compile, measure, plan := probe.compile, probe.measure, probe.plan
		probe.compile = func(pkg string) (string, error) {
			calls = append(calls, "compile")
			return compile(pkg)
		}
		probe.measure = func(compiled, function string, length, dim, rounds int) (coreml.Timing, *coreml.Function, error) {
			calls = append(calls, "load")
			return measure(compiled, function, length, dim, rounds)
		}
		probe.plan = func(compiled, function string) (coreml.Placement, error) {
			calls = append(calls, "plan")
			return plan(compiled, function)
		}
		ctx, cancel := context.WithCancel(context.Background())
		progress := func(format string, _ ...any) {
			if strings.HasPrefix(format, line) {
				cancel()
			}
		}
		// The functions' steps run on a goroutine of report's: they are
		// taken here, on the test's.
		if strings.HasPrefix(line, "%s") {
			probe.functions(ctx, compiled, []int{128}, 4, progress)
		} else if r, err := probe.report(ctx, pkg, models, 4, nil, progress); !errors.Is(err, context.Canceled) || r != nil {
			t.Fatalf("interrupted at %q: %+v, %v", line, r, err)
		}
		if want := map[string][]string{"%s: reading": {"load"}}[line]; !reflect.DeepEqual(calls, want) {
			t.Errorf("interrupted at %q: calls %v, want %v", line, calls, want)
		}
		cancel()
	}
}
