//go:build llamacpp && !android && !ios

package indexer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
)

// planFn is a function whose vector depends on its text, as a model's
// does: an axis per first word for a text under six tokens, another for
// a longer one.
type planFn struct {
	err       error
	failFirst int           // predictions that fail before the rest succeed
	failAt    int           // the one prediction that fails, counted from 1; 0 = none
	slowFirst time.Duration // how long the first prediction takes
	constant  bool          // one vector whatever the text
	scale     float32       // the vectors' length; 0 = 1
	blind     bool          // the vector depends on the text's length alone
	collapse  bool          // one vector for every long text
	calls     int
	closed    bool
}

func (f *planFn) embed(tokens []int32, out []float32) error {
	f.calls++
	if f.calls == 1 {
		time.Sleep(f.slowFirst)
	}
	if f.err != nil {
		return f.err
	}
	if f.calls <= f.failFirst || f.calls == f.failAt {
		return errors.New("a passing failure")
	}
	clear(out)
	one := f.scale
	if one == 0 {
		one = 1
	}
	switch {
	case f.constant:
		out[0] = one
	case len(tokens) >= 6 && (f.collapse || tokens[len(tokens)-2] < 20000):
		out[2] = one
	case len(tokens) >= 6:
		out[3] = one
	case f.blind || tokens[1] == 1001:
		out[0] = one
	default:
		out[1] = one
	}
	return nil
}

func (f *planFn) close() { f.closed = true }

// testBackend is a Core ML backend over fake calls, whose package sits
// in dir. It lists the functions it opened.
type testBackend struct {
	*coreMLBackend
	opened []string
	fns    []*planFn
}

var testPackage = &coreMLPackage{name: "m", lengths: []int{128, 256, 512, 1024, 2048}}

func newTestBackend(t *testing.T, dir string) *testBackend {
	t.Helper()
	tb := &testBackend{}
	b := newCoreMLBackend(testPackage, filepath.Join(dir, "m.mlpackage"), 4, t.Logf)
	b.open = func(compiled, function string, length, dim int) (aneFunction, error) {
		tb.opened = append(tb.opened, function)
		fn := &planFn{}
		tb.fns = append(tb.fns, fn)
		return fn, nil
	}
	tb.coreMLBackend = b
	return tb
}

// A load opens the function and has it predict, and says how long the
// open, the first prediction and all four took.
func TestCoreMLBackend_Load(t *testing.T) {
	b := newTestBackend(t, t.TempDir())
	fn, info, err := b.load(128)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.fns[0].calls; got != 4 || fn == nil || !reflect.DeepEqual(b.opened, []string{"s128"}) {
		t.Fatalf("%d predictions on a load of %v, want 4 on s128", got, b.opened)
	}
	for _, part := range []string{"open ", ", first predict ", ", validate "} {
		if !strings.Contains(info, part) {
			t.Fatalf("load info %q, want the open, first predict and validate times", info)
		}
	}
}

// A function that opens but does not predict the model's vectors is not
// loaded, and is closed.
func TestCoreMLBackend_LoadValidates(t *testing.T) {
	for name, broken := range map[string]*planFn{
		"a failed prediction":                      {err: errors.New("predict failed")},
		"one vector for all":                       {constant: true},
		"one vector for all, short of unit length": {constant: true, scale: 0.995},
		"a vector by length alone":                 {blind: true},
		"one vector for every long text":           {collapse: true},
		"too many failures in a row":               {failFirst: 3 * aneMaxFailures},
	} {
		b := newTestBackend(t, t.TempDir())
		b.open = func(string, string, int, int) (aneFunction, error) { return broken, nil }
		if _, _, err := b.load(128); err == nil {
			t.Errorf("%s: loaded", name)
		}
		if !broken.closed {
			t.Errorf("%s: left open", name)
		}
	}
	if _, err := validateFunction(&fakeFn{parent: &fakeANE{notUnit: true}, length: 128}, 128, 4); err == nil {
		t.Error("a vector that is not unit length passed")
	}
	// One failed prediction is not the end of the function, as in serving.
	b := newTestBackend(t, t.TempDir())
	flaky := &planFn{failFirst: 1}
	b.open = func(string, string, int, int) (aneFunction, error) { return flaky, nil }
	if _, _, err := b.load(128); err != nil || flaky.closed {
		t.Errorf("a function whose first prediction failed: %v, closed %v", err, flaky.closed)
	}
}

// The load's line has the time of the very first prediction: a retry
// after a failed validation does not put a warm one in its place.
func TestCoreMLBackend_LoadKeepsTheFirstPredictionAcrossRetries(t *testing.T) {
	const slow = 30 * time.Millisecond
	b := newTestBackend(t, t.TempDir())
	fn := &planFn{slowFirst: slow, failAt: 2}
	b.open = func(string, string, int, int) (aneFunction, error) { return fn, nil }
	_, info, err := b.load(128)
	if err != nil || fn.calls != 6 {
		t.Fatalf("load after a failed second prediction: %v, %d predictions, want 2 then 4", err, fn.calls)
	}
	_, rest, _ := strings.Cut(info, "first predict ")
	first, err := time.ParseDuration(strings.SplitN(rest, ",", 2)[0])
	if err != nil || first < slow {
		t.Fatalf("load info %q: first predict %v, want the first prediction's %v or more", info, first, slow)
	}
}

// The server follows a child's Core ML calls by its lines: its
// preparation, its loads and its idle releases, each from the line that
// opens it to the one that ends it.
func TestANECalls(t *testing.T) {
	open := map[string]bool{}
	for _, step := range []struct {
		status string
		want   []string
	}{
		{"starting", []string{"prepare"}},
		{"s256 still loading after 10m0s: llama.cpp embeds its texts until it is ready", []string{"prepare"}},
		{"package compiled in 40s", nil},
		{"starting", []string{"prepare"}},
		{"s128 loading", []string{"s128 load"}},
		{"s128 ready (open 1s)", nil},
		{"s256 loading", []string{"s256 load"}},
		{"s128 failed: predict", []string{"s256 load"}},
		{"s256 not used: it does not predict", nil},
		{"s512 loading", []string{"s512 load"}},
		{"s128 not used any more: 3 failures in a row", []string{"s512 load"}},
		{"s512 loaded and discarded", nil},
		{"starting", []string{"prepare"}},
		{"off, llama.cpp embeds everything: this machine has no neural engine", nil},
		// An idle release is a Core ML call between its two lines.
		{"s256 releasing", []string{"s256 release"}},
		{"s256 released after 5m0s unused", nil},
	} {
		aneCalls(open, step.status)
		var got []string
		for call := range open {
			got = append(got, call)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("after %q: open %v, want %v", step.status, got, step.want)
		}
	}
}

// Only a line that changes what a deferred text can do wakes the drain.
func TestANEWakes(t *testing.T) {
	for status, want := range map[string]bool{
		"s256 ready (open 1s)":                                                         true,
		"s256 not used: coreml: load s256: test error 1":                               true,
		"s256 not used any more: 3 failures in a row":                                  true,
		"s256 still loading after 10m0s: llama.cpp embeds its texts until it is ready": true,
		"off, llama.cpp embeds everything: no engine":                                  true,
		"starting":                        false,
		"package compiled in 40s":         false,
		"s256 loading":                    false,
		"s256 loaded and discarded":       false,
		"s256 failed: predict":            false,
		"s256 released after 5m0s unused": false,
		"s256 releasing":                  false,
	} {
		if got := aneWakes(status); got != want {
			t.Errorf("aneWakes(%q) = %v, want %v", status, got, want)
		}
	}
}

// With several documents to a decode, llama.cpp takes only the ones the
// Neural Engine path leaves to it: a text behind a miss is routed on its
// own, and each miss is counted under its own reason.
func TestLocal_LlamaGroupClassifiesEachText(t *testing.T) {
	f := &fakeANE{loadErr: map[int]error{512: errors.New("broken")}}
	n, _ := newTestANE(t, f)
	loadFor(n, 10)
	loadFor(n, 400) // s512, the last that holds it, fails to load
	f.mu.Lock()
	f.gate, f.started = make(chan struct{}), make(chan int, 1)
	f.mu.Unlock()
	l := &Local{ane: n, nCtx: 2048, batchDocs: 4}
	toks := func(n int) []llama.Token { return make([]llama.Token, n) }

	// s512 failed, s256 is loading: the 200-token text waits.
	tokenized := [][]llama.Token{toks(400), toks(200)}
	lens := []int{400, 200}
	if _, ok, _ := l.embedANE(tokenized[0], true); ok {
		t.Fatal("served by a failed function")
	}
	end, misses := l.llamaGroup(tokenized, lens, 0, true)
	if end != 1 || !reflect.DeepEqual(misses, []string{EmbedReasonANEFailed}) {
		t.Fatalf("group ends at %d with %v, want the failed text alone", end, misses)
	}
	<-f.started
	// Texts that all fall back share the decode, each with its reason; a
	// text a loaded function serves ends the group.
	tokenized = [][]llama.Token{toks(400), toks(100000), toks(10), toks(400)}
	lens = []int{400, 600, 10, 400}
	_, _, _ = l.embedANE(tokenized[0], true)
	end, misses = l.llamaGroup(tokenized, lens, 0, true)
	if end != 2 || !reflect.DeepEqual(misses, []string{EmbedReasonANEFailed, EmbedReasonTooLong}) {
		t.Fatalf("group ends at %d with %v, want the two fallback texts", end, misses)
	}
	close(f.gate)
	n.loads.Wait()
	// With no Neural Engine path the group is llama.cpp's own.
	if end, misses = (&Local{nCtx: 2048, batchDocs: 4}).llamaGroup(tokenized, lens, 0, true); end != 4 || misses != nil {
		t.Fatalf("without the path: group ends at %d with %v", end, misses)
	}
}

// What a backend's errors say names no path under the models directory,
// and neither does a status line made of any os error.
func TestStatusLinesNameNoPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secret-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	clean := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: no error", what)
		}
		for _, part := range []string{"secret-root", "m.mlmodelc", "m.mlpackage"} {
			if strings.Contains(err.Error(), part) {
				t.Errorf("%s: %q names %s", what, err, part)
			}
		}
	}
	b := newTestBackend(t, root)
	b.open = func(compiled, _ string, _, _ int) (aneFunction, error) {
		return nil, errors.New("coreml: load s128: could not read " + compiled + "/model.mil")
	}
	_, _, err := b.load(128)
	clean("a load error", err)
	// A prediction of a function that loaded fine.
	failing := &planFn{}
	b.open = func(string, string, int, int) (aneFunction, error) { return failing, nil }
	fn, _, err := b.load(128)
	if err != nil {
		t.Fatal(err)
	}
	failing.err = errors.New("coreml: s128 predict: file://" + b.compiled + "/weights/weight.bin is gone, and so is " + b.dir)
	clean("a serving error", fn.embed(tokensOf(10), make([]float32, 4)))

	// The engine's own lines, whatever backend the error comes from.
	_, pathErr := os.ReadFile(filepath.Join(root, "model.bin"))
	linkErr := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "b"))
	for name, f := range map[string]*fakeANE{
		"preparation fails": {prepErrs: []error{fmt.Errorf("install the compiled model: %w", linkErr)}},
		"a load fails":      {loadErr: map[int]error{128: pathErr}},
		// A copy between volumes: the destination, and inside it the source.
		"a copy fails": {prepErrs: []error{fmt.Errorf("install the compiled model: %w", &fs.PathError{Op: "CopyFS",
			Path: filepath.Join(root, "m.mlmodelc.1.tmp"),
			Err:  &fs.PathError{Op: "open", Path: "/var/folders/secret-tmp/model.mil", Err: fs.ErrPermission}})}},
		"a prediction fails": {embedErr: map[int]error{0: pathErr}},
	} {
		log := newLogLines()
		n, _ := newTestANE(t, f)
		n.logf = log.logf
		n.start()
		n.loads.Wait()
		n.embed(tokensOf(10))
		n.loads.Wait()
		if log.has("secret-root") || log.has("secret-tmp") || len(log.lines) == 0 {
			t.Errorf("%s: lines %v name the path", name, log.lines)
		}
	}
}
