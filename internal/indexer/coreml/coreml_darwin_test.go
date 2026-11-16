//go:build darwin && arm64 && !ios

package coreml

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
		spec = "128,256,512,1024"
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

// filler is a token sequence of n ids: BOS, one repeated word, EOS.
func filler(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = 9259
	}
	out[0], out[n-1] = 2, 1
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
// (default 128,256,512,1024). ANY_TEST_COREML_REFERENCE, when set, is a
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
		if err := os.Rename(tmp, compiled); err != nil {
			t.Fatalf("move the compiled model from %s: %v", tmp, err)
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
		t.Logf("%s plan in %s: %d neural engine, %d gpu, %d cpu operations, cpu core %v",
			name, time.Since(start).Round(time.Millisecond), plan.NeuralEngine, plan.GPU, plan.CPU, plan.CPUCore)
		if !plan.OnNeuralEngine() {
			t.Errorf("%s: core planned off the neural engine", name)
		}
		start = time.Now()
		fn, err := Open(compiled, name, l, dim)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		defer fn.Close()
		loaded := time.Since(start)
		out := make([]float32, dim)
		start = time.Now()
		if err := fn.Embed([]int32{2, 9259, 1}, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s load %s, first predict %s, norm %.8f",
			name, loaded.Round(time.Millisecond), time.Since(start).Round(time.Microsecond), vectorNorm(out))
		if n := vectorNorm(out); math.Abs(n-1) > 1e-3 {
			t.Errorf("%s: norm %f, want 1", name, n)
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

	t.Run("speed", func(t *testing.T) {
		for _, l := range lengths {
			fn := fns[l]
			tokens := filler(l * 3 / 4)
			out := make([]float32, dim)
			const rounds = 50
			start := time.Now()
			for range rounds {
				if err := fn.Embed(tokens, out); err != nil {
					t.Fatalf("s%d: %v", l, err)
				}
			}
			per := time.Since(start) / rounds
			t.Logf("s%d: %s per text of %d tokens (%.1f texts/s)", l, per.Round(time.Microsecond), len(tokens), float64(time.Second)/float64(per))
		}
	})

	// The embed child predicts on one function while another loads.
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
		second, err := Open(compiled, "s"+strconv.Itoa(large), large, dim)
		close(stop)
		wg.Wait()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("second s%d loaded in %s next to %d predictions on s%d", large, time.Since(start).Round(time.Millisecond), predictions, small)
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
