//go:build llamacpp && !android && !ios

package indexer

import (
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

// The report has every function's plan with its verdict and its
// timings, and the model compiled for it is gone afterwards.
func TestNeuralEngineReport(t *testing.T) {
	pkg, models, probe, compiled := debugPackage(t)
	var said []string
	r, err := probe.report(pkg, models, 4, nil, func(format string, _ ...any) { said = append(said, format) })
	if err != nil {
		t.Fatal(err)
	}
	if r.Package != pkg.id() || len(r.Functions) != 2 || len(said) == 0 {
		t.Fatalf("report %+v, progress %v", r, said)
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
// fails is that function's error and the others are still measured.
func TestNeuralEngineReport_FilterAndFailures(t *testing.T) {
	pkg, models, probe, _ := debugPackage(t)
	r, err := probe.report(pkg, models, 4, []string{"s256"}, nil)
	if err != nil || len(r.Functions) != 1 || r.Functions[0].Function != "s256" {
		t.Fatalf("filtered: %+v, %v", r, err)
	}
	if _, err := probe.report(pkg, models, 4, []string{"s512"}, nil); err == nil {
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
	r, err = probe.report(pkg, models, 4, nil, nil)
	if err != nil || len(r.Functions) != 2 {
		t.Fatalf("with failures: %+v, %v", r, err)
	}
	if f := r.Functions[0]; f.PlanError == "" || f.Placement != "" || f.Error == "" || f.LoadMs != 0 {
		t.Fatalf("s128: %+v", f)
	}
	if f := r.Functions[1]; f.PlanError == "" || f.Error != "" || f.LoadMs != 1000 {
		t.Fatalf("s256: %+v", f)
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
	if _, err := probe.report(pkg, t.TempDir(), 4, nil, nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("a missing package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkg.dir(models), "weight.bin"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.report(pkg, models, 4, nil, nil); err == nil || !strings.Contains(err.Error(), "not the pinned one") {
		t.Fatalf("a package off its pin: %v", err)
	}
	probe.hasEngine = func() (bool, error) { return false, nil }
	if _, err := probe.report(pkg, models, 4, nil, nil); !errors.Is(err, ErrNeuralEngineUnsupported) {
		t.Fatalf("no neural engine: %v", err)
	}
	probe.hasEngine = func() (bool, error) { return false, coreml.ErrUnsupported }
	if _, err := probe.report(pkg, models, 4, nil, nil); !errors.Is(err, ErrNeuralEngineUnsupported) {
		t.Fatalf("no Core ML: %v", err)
	}
}

// Off an Apple Silicon Mac the command is unsupported before it looks
// for anything.
func TestDebugNeuralEngine_UnsupportedHost(t *testing.T) {
	host := aneHost
	aneHost = func() bool { return false }
	defer func() { aneHost = host }()
	if _, err := DebugNeuralEngine(t.TempDir(), nil, nil); !errors.Is(err, ErrNeuralEngineUnsupported) {
		t.Fatalf("DebugNeuralEngine = %v, want unsupported", err)
	}
}
