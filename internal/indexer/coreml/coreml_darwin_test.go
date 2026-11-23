//go:build darwin && arm64 && !ios

package coreml

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego/objc"
)

// referenceRow is one row of ANY_TEST_COREML_REFERENCE: a token sequence,
// the function length it runs at and the vector the stock model gives.
type referenceRow struct {
	Case   int       `json:"case"`
	Tokens []int32   `json:"tokens"`
	Length int       `json:"length"`
	Vector []float64 `json:"vector"`
}

func testLengths(t *testing.T) []int {
	t.Helper()
	spec := os.Getenv("ANY_TEST_COREML_LENGTHS")
	if spec == "" {
		spec = "128,256,512,1024,2048"
	}
	var out []int
	for _, s := range strings.Split(spec, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			t.Fatalf("ANY_TEST_COREML_LENGTHS: %v", err)
		}
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func vectorNorm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// TestPackage runs a real model package on this Mac.
// ANY_TEST_COREML_PACKAGE is the .mlpackage; the compiled model is kept
// next to it. ANY_TEST_COREML_LENGTHS lists the function lengths
// (default 128,256,512,1024,2048). ANY_TEST_COREML_REFERENCE, when set, is a
// JSON list of {case, tokens, length, vector} rows the functions must
// reproduce.
func TestPackage(t *testing.T) {
	pkg := os.Getenv("ANY_TEST_COREML_PACKAGE")
	if pkg == "" {
		t.Skip("set ANY_TEST_COREML_PACKAGE to a model's .mlpackage")
	}
	const dim = 768
	lengths := testLengths(t)

	ok, err := HasNeuralEngine()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("neural engine among the compute devices: %v", ok)
	if !ok {
		t.Fatal("this machine lists no neural engine")
	}

	compiled := strings.TrimSuffix(pkg, ".mlpackage") + ".mlmodelc"
	if _, err := os.Stat(compiled); err != nil {
		start := time.Now()
		tmp, err := Compile(pkg)
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmp) // left by a failed copy; gone after a rename
		if err := os.Rename(tmp, compiled); err != nil {
			// Core ML's temporary directory can sit on another volume: copy
			// into a stage of this test's own beside the destination, then
			// move into place whole. A failed copy or rename leaves no stage.
			stage, err := os.MkdirTemp(filepath.Dir(compiled), filepath.Base(compiled)+".copying-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(stage)
			if err := os.CopyFS(stage, os.DirFS(tmp)); err != nil {
				t.Fatalf("copy the compiled model from %s: %v", tmp, err)
			}
			if err := os.Rename(stage, compiled); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("compiled in %s", time.Since(start).Round(time.Millisecond))
	} else {
		t.Logf("using the compiled model at %s", compiled)
	}

	fns := map[int]*Function{}
	for _, l := range lengths {
		name := "s" + strconv.Itoa(l)
		start := time.Now()
		plan, err := Plan(compiled, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s plan in %s: %d neural engine, %d gpu, %d cpu operations, cpu core %v: %s",
			name, time.Since(start).Round(time.Millisecond), plan.NeuralEngine, plan.GPU, plan.CPU, plan.CPUCore, plan.Verdict())
		if plan.Verdict() != VerdictOn {
			t.Errorf("%s: core not planned on the neural engine", name)
		}
		m, fn, err := Measure(t.Context(), compiled, name, l, dim, 50)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		defer fn.Close()
		t.Logf("%s load %s, first predict %s, norm %.8f, %s per text of %d tokens (%.1f texts/s)",
			name, m.Load.Round(time.Millisecond), m.FirstPredict.Round(time.Microsecond), m.Norm,
			m.PerText.Round(time.Microsecond), m.Tokens, float64(time.Second)/float64(m.PerText))
		if math.Abs(m.Norm-1) > 1e-3 {
			t.Errorf("%s: norm %f, want 1", name, m.Norm)
		}
		fns[l] = fn
	}

	t.Run("reference", func(t *testing.T) {
		path := os.Getenv("ANY_TEST_COREML_REFERENCE")
		if path == "" {
			t.Skip("set ANY_TEST_COREML_REFERENCE to the reference rows")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var rows []referenceRow
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, row := range rows {
			fn := fns[row.Length]
			if fn == nil {
				continue
			}
			if len(row.Vector) != dim {
				t.Fatalf("case %d: reference vector has %d values, want %d", row.Case, len(row.Vector), dim)
			}
			out := make([]float32, dim)
			if err := fn.Embed(row.Tokens, out); err != nil {
				t.Fatalf("case %d: %v", row.Case, err)
			}
			var dot, na, nb, maxAbs float64
			for i, x := range out {
				a, b := float64(x), row.Vector[i]
				dot, na, nb = dot+a*b, na+a*a, nb+b*b
				maxAbs = math.Max(maxAbs, math.Abs(a-b))
			}
			cos := dot / math.Sqrt(na*nb)
			t.Logf("case %d: s%d, %d tokens, cosine %.8f, max abs %.6f", row.Case, row.Length, len(row.Tokens), cos, maxAbs)
			if cos < 0.999 || maxAbs > 0.01 || math.Abs(math.Sqrt(na)-1) > 1e-3 {
				t.Errorf("case %d: off the reference", row.Case)
			}
			checked++
		}
		if checked == 0 {
			t.Fatal("no reference row matches a tested length")
		}
	})

	// Padding is masked out, so one text gives one vector on every
	// function; two different texts do not, or the inputs never reach
	// the model.
	t.Run("same text on every function", func(t *testing.T) {
		words := func(n, step int) []int32 {
			out := filler(n)
			for i := 1; i < n-1; i++ {
				out[i] = int32(1000 + i*step)
			}
			return out
		}
		cosine := func(a, b []float32) float64 {
			var dot float64
			for i := range a {
				dot += float64(a[i]) * float64(b[i])
			}
			return dot / (vectorNorm(a) * vectorNorm(b))
		}
		text := words(lengths[0]/2, 7)
		first := make([]float32, dim)
		if err := fns[lengths[0]].Embed(text, first); err != nil {
			t.Fatal(err)
		}
		for _, l := range lengths[1:] {
			out := make([]float32, dim)
			if err := fns[l].Embed(text, out); err != nil {
				t.Fatalf("s%d: %v", l, err)
			}
			c := cosine(first, out)
			t.Logf("s%d against s%d: cosine %.8f", l, lengths[0], c)
			if c < 0.999 {
				t.Errorf("s%d: cosine %v to the same text on s%d", l, c, lengths[0])
			}
		}
		other := make([]float32, dim)
		if err := fns[lengths[0]].Embed(words(lengths[0]/2, 131), other); err != nil {
			t.Fatal(err)
		}
		c := cosine(first, other)
		t.Logf("two different texts on s%d: cosine %.6f", lengths[0], c)
		if c > 0.999 {
			t.Errorf("two different texts give one vector (cosine %v)", c)
		}
	})

	// The embed child predicts on one function while another is
	// planned and loaded.
	t.Run("load while predicting", func(t *testing.T) {
		small, large := lengths[0], lengths[len(lengths)-1]
		stop := make(chan struct{})
		var wg sync.WaitGroup
		var predictErr error
		predictions := 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := make([]float32, dim)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := fns[small].Embed(filler(small/2), out); err != nil {
					predictErr = err
					return
				}
				predictions++
			}
		}()
		start := time.Now()
		plan, planErr := Plan(compiled, "s"+strconv.Itoa(large))
		second, err := Open(compiled, "s"+strconv.Itoa(large), large, dim)
		close(stop)
		wg.Wait()
		if planErr != nil {
			t.Fatal(planErr)
		}
		if !plan.OnNeuralEngine() {
			t.Errorf("s%d: core planned off the neural engine next to predictions", large)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("second s%d planned and loaded in %s next to %d predictions on s%d", large, time.Since(start).Round(time.Millisecond), predictions, small)
		if predictErr != nil {
			t.Fatal(predictErr)
		}
		out := make([]float32, dim)
		if err := second.Embed(filler(large/2), out); err != nil {
			t.Fatal(err)
		}
		second.Close()
		if err := second.Embed(filler(large/2), out); err == nil {
			t.Error("a closed function still predicts")
		}
	})

	t.Run("refusals", func(t *testing.T) {
		fn := fns[lengths[0]]
		out := make([]float32, dim)
		if err := fn.Embed(filler(lengths[0]+1), out); err == nil {
			t.Error("accepted more tokens than the function holds")
		}
		if err := fn.Embed(nil, out); err == nil {
			t.Error("accepted no tokens")
		}
		if err := fn.Embed(filler(8), make([]float32, dim-1)); err == nil {
			t.Error("accepted an output of the wrong width")
		}
		if _, err := Open(compiled, "no-such-function", 128, dim); err == nil {
			t.Error("opened a function the model does not have")
		} else {
			t.Logf("unknown function: %v", err)
		}
		if _, err := Plan(compiled, "no-such-function"); err == nil {
			t.Error("planned a function the model does not have")
		}
		if _, err := Open(compiled+".missing", "s128", 128, dim); err == nil {
			t.Error("opened a model that is not there")
		}
	})
}

// An NSError is named by its domain and code alone: its description can
// carry a file URL, and the name is logged.
func TestDescribeNamesNoPath(t *testing.T) {
	if err := load(); err != nil {
		t.Skip(err)
	}
	const path = "/private/var/secret-root/model.mlmodelc/model.mil"
	var got string
	_ = scope(func() error {
		info := classSend(clsMutableDictionary, "dictionary")
		send(info, "setObject:forKey:", nsString("could not read "+path), nsString("NSLocalizedDescription"))
		nserr := classSend(objc.GetClass("NSError"), "errorWithDomain:code:userInfo:", nsString("com.example.test"), 42, info)
		got = describe(nserr)
		return nil
	})
	if got != "com.example.test error 42" {
		t.Fatalf("describe = %q", got)
	}
	if describe(0) != "no error reported" {
		t.Fatalf("describe(nil) = %q", describe(0))
	}
}
