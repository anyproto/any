//go:build llamacpp && !android && !ios

package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeANE stands in for Core ML: functions return a unit vector and
// record which length served each text.
type fakeANE struct {
	mu       sync.Mutex
	prepErrs []error // one per prepare call, the last repeating
	prepares int
	loadErr  map[int]error
	loaded   []int
	gate     chan struct{} // set: load waits for it
	started  chan int      // set: load announces itself first
	embedErr map[int]error // by function length; 0 = every function
	notUnit  bool
	// predicting, when set, receives the length at the start of each
	// prediction, which then waits for resume.
	predicting chan int
	resume     chan struct{}
	served     []int
	tokens     [][]int32 // what each served text was, in order
	closed     []int
}

func (f *fakeANE) prepare() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepares++
	if len(f.prepErrs) == 0 {
		return nil
	}
	return f.prepErrs[min(f.prepares, len(f.prepErrs))-1]
}

func (f *fakeANE) load(length int) (aneFunction, string, error) {
	f.mu.Lock()
	gate, started, err := f.gate, f.started, f.loadErr[length]
	f.mu.Unlock()
	if started != nil {
		started <- length
	}
	if gate != nil {
		<-gate
	}
	if err != nil {
		return nil, "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loaded = append(f.loaded, length)
	return &fakeFn{parent: f, length: length}, "fake", nil
}

func (f *fakeANE) snapshot() (loaded, served, closed []int, prepares int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.loaded...), append([]int(nil), f.served...), append([]int(nil), f.closed...), f.prepares
}

func (f *fakeANE) failOn(length int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.embedErr == nil {
		f.embedErr = map[int]error{}
	}
	if err == nil {
		delete(f.embedErr, length)
		return
	}
	f.embedErr[length] = err
}

type fakeFn struct {
	parent *fakeANE
	length int
	closed bool
}

func (fn *fakeFn) embed(tokens []int32, out []float32) error {
	f := fn.parent
	f.mu.Lock()
	predicting, resume := f.predicting, f.resume
	f.mu.Unlock()
	if predicting != nil {
		predicting <- fn.length
		<-resume
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if fn.closed {
		return errors.New("predicted on a closed function")
	}
	if err := f.embedErr[fn.length]; err != nil {
		return err
	}
	if err := f.embedErr[0]; err != nil {
		return err
	}
	f.served = append(f.served, fn.length)
	f.tokens = append(f.tokens, append([]int32(nil), tokens...))
	out[0] = 1
	if f.notUnit {
		out[0] = 3
	}
	return nil
}

func (fn *fakeFn) close() {
	fn.parent.mu.Lock()
	defer fn.parent.mu.Unlock()
	fn.closed = true
	fn.parent.closed = append(fn.parent.closed, fn.length)
}

// testClock is a clock tests move by hand.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestANE(t *testing.T, f *fakeANE) (*neuralEngine, *testClock) {
	t.Helper()
	clock := &testClock{t: time.Unix(1000, 0)}
	n := newNeuralEngine(f, []int{128, 256, 512}, 4, t.Logf)
	n.now = clock.now
	t.Cleanup(func() {
		n.close()
		n.loads.Wait()
	})
	return n, clock
}

func tokensOf(n int) []int32 { return make([]int32, n) }

// loadFor has the function for n-token texts loaded: the first text
// asks for it, the wait lets it land.
func loadFor(n *neuralEngine, tokens int) {
	n.embed(tokensOf(tokens))
	n.loads.Wait()
}

func TestNeuralEngine_LoadsOnDemand(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	if loaded, _, _, _ := f.snapshot(); !reflect.DeepEqual(loaded, []int{128}) {
		t.Fatalf("start loaded %v, want the smallest function", loaded)
	}
	if vec, ok := n.embed(tokensOf(10)); !ok || len(vec) != 4 {
		t.Fatalf("short text: vec %v ok %v", vec, ok)
	}
	// The first text of a length is llama.cpp's; it starts the load.
	if _, ok := n.embed(tokensOf(200)); ok {
		t.Fatal("served by a function that was never loaded")
	}
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(200)); !ok {
		t.Fatal("not served after its function loaded")
	}
	if _, ok := n.embed(tokensOf(256)); !ok {
		t.Fatal("a text of exactly the function's length must fit it")
	}
	// Longer than every function: llama.cpp, and nothing to load.
	if _, ok := n.embed(tokensOf(513)); ok {
		t.Fatal("served a text no function fits")
	}
	if _, ok := n.embed(nil); ok {
		t.Fatal("served an empty text")
	}
	n.loads.Wait()
	loaded, served, _, _ := f.snapshot()
	if !reflect.DeepEqual(loaded, []int{128, 256}) {
		t.Fatalf("loaded %v, want 128 and 256", loaded)
	}
	if !reflect.DeepEqual(served, []int{128, 256, 256}) {
		t.Fatalf("served by %v", served)
	}
}

// Texts that arrive while their function loads do not load it again.
func TestNeuralEngine_OneLoadPerFunction(t *testing.T) {
	f := &fakeANE{gate: make(chan struct{}), started: make(chan int, 8)}
	n, _ := newTestANE(t, f)
	n.start()
	<-f.started
	for range 5 {
		if _, ok := n.embed(tokensOf(10)); ok {
			t.Fatal("served while the function was still loading")
		}
	}
	close(f.gate)
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(10)); !ok {
		t.Fatal("not served after the load")
	}
	n.loads.Wait()
	loaded, _, closed, _ := f.snapshot()
	if !reflect.DeepEqual(loaded, []int{128}) || len(closed) != 0 {
		t.Fatalf("loaded %v, closed %v: want one load and no close", loaded, closed)
	}
}

// A text runs on a larger loaded function while its own is missing.
func TestNeuralEngine_UsesLargerLoadedFunction(t *testing.T) {
	f := &fakeANE{loadErr: map[int]error{128: errors.New("planned on the cpu")}}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(10)); ok {
		t.Fatal("served with nothing loaded")
	}
	loadFor(n, 200)
	if _, ok := n.embed(tokensOf(10)); !ok {
		t.Fatal("short text not served by the larger function")
	}
	n.loads.Wait()
	loaded, served, _, _ := f.snapshot()
	if !reflect.DeepEqual(loaded, []int{256}) {
		t.Fatalf("loaded %v: a failed function must not be retried", loaded)
	}
	if !reflect.DeepEqual(served, []int{256}) {
		t.Fatalf("served by %v", served)
	}
}

func TestNeuralEngine_WaitsForPackage(t *testing.T) {
	f := &fakeANE{prepErrs: []error{errANENotReady, nil}}
	n, clock := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	for range 3 {
		if _, ok := n.embed(tokensOf(10)); ok {
			t.Fatal("served before the package was there")
		}
		n.loads.Wait()
	}
	if _, _, _, prepares := f.snapshot(); prepares != 1 {
		t.Fatalf("prepared %d times inside the retry interval, want 1", prepares)
	}
	clock.add(aneRetryInterval)
	// The first text after the package lands is a long one: the
	// smallest function still loads, and first.
	loadFor(n, 200)
	if loaded, _, _, _ := f.snapshot(); !reflect.DeepEqual(loaded, []int{128, 256}) {
		t.Fatalf("loaded %v, want the smallest first, then the one asked for", loaded)
	}
	if _, ok := n.embed(tokensOf(10)); !ok {
		t.Fatal("not served once the package arrived")
	}
}

func TestNeuralEngine_PrepareFailureTurnsOff(t *testing.T) {
	f := &fakeANE{prepErrs: []error{errors.New("no neural engine")}}
	n, clock := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	clock.add(time.Hour)
	if _, ok := n.embed(tokensOf(10)); ok {
		t.Fatal("served after the backend refused to start")
	}
	n.loads.Wait()
	if loaded, _, _, prepares := f.snapshot(); len(loaded) != 0 || prepares != 1 {
		t.Fatalf("loaded %v after %d prepares, want nothing after 1", loaded, prepares)
	}
}

// A function that keeps failing is retired; the others keep serving.
func TestNeuralEngine_FailuresRetireFunction(t *testing.T) {
	for name, broken := range map[string]func(*fakeANE, bool){
		"errors": func(f *fakeANE, on bool) {
			if on {
				f.failOn(128, errors.New("predict failed"))
			} else {
				f.failOn(128, nil)
			}
		},
		"not unit": func(f *fakeANE, on bool) {
			f.mu.Lock()
			f.notUnit = on
			f.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeANE{}
			n, _ := newTestANE(t, f)
			n.start()
			loadFor(n, 200)
			if _, ok := n.embed(tokensOf(10)); !ok {
				t.Fatal("not served while healthy")
			}
			broken(f, true)
			for range aneMaxFailures {
				if _, ok := n.embed(tokensOf(10)); ok {
					t.Fatal("served a failed prediction")
				}
			}
			broken(f, false)
			if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{128}) {
				t.Fatalf("closed %v, want the failing function released", closed)
			}
			// Short texts now run on the next function up.
			if _, ok := n.embed(tokensOf(10)); !ok {
				t.Fatal("the healthy function stopped serving")
			}
			n.loads.Wait()
			loaded, served, _, _ := f.snapshot()
			if !reflect.DeepEqual(loaded, []int{128, 256}) {
				t.Fatalf("loaded %v: a retired function must not be reloaded", loaded)
			}
			if served[len(served)-1] != 256 {
				t.Fatalf("served by %v, want the last one on 256", served)
			}
		})
	}
}

// Failures count per function: successes on one do not hide another
// that fails every time.
func TestNeuralEngine_FailuresCountPerFunction(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	n.start()
	loadFor(n, 200)
	f.failOn(256, errors.New("predict failed"))
	for range aneMaxFailures {
		if _, ok := n.embed(tokensOf(200)); ok {
			t.Fatal("served a failed prediction")
		}
		if _, ok := n.embed(tokensOf(10)); !ok {
			t.Fatal("the healthy function stopped serving")
		}
	}
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{256}) {
		t.Fatalf("closed %v, want 256 retired despite the successes on 128", closed)
	}
}

// One failure between successes is not a broken function.
func TestNeuralEngine_FailureCountResets(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	for range aneMaxFailures * 2 {
		f.failOn(128, errors.New("once"))
		n.embed(tokensOf(10))
		f.failOn(128, nil)
		if _, ok := n.embed(tokensOf(10)); !ok {
			t.Fatal("retired by failures that were not consecutive")
		}
	}
}

func TestNeuralEngine_ReleasesIdle(t *testing.T) {
	f := &fakeANE{}
	n, clock := newTestANE(t, f)
	n.start()
	loadFor(n, 200)
	if _, ok := n.embed(tokensOf(200)); !ok {
		t.Fatal("256 not loaded")
	}
	clock.add(aneIdleRelease - time.Second)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); len(closed) != 0 {
		t.Fatalf("closed %v before the idle time passed", closed)
	}
	clock.add(2 * time.Second)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{256}) {
		t.Fatalf("closed %v, want the idle 256", closed)
	}
	// The smallest stays however long it sat unused.
	clock.add(time.Hour)
	n.releaseTick()
	if _, ok := n.embed(tokensOf(10)); !ok {
		t.Fatal("the smallest function was released")
	}
	// The next text that needs the released one reloads it.
	if _, ok := n.embed(tokensOf(200)); ok {
		t.Fatal("served by a released function")
	}
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(200)); !ok {
		t.Fatal("not reloaded on demand")
	}
}

// The release needs no further text: the timer frees an idle function
// on its own, and stops with the engine.
func TestNeuralEngine_ReleaseTimer(t *testing.T) {
	f := &fakeANE{}
	n := newNeuralEngine(f, []int{128, 256, 512}, 4, t.Logf)
	n.idle = 100 * time.Millisecond
	t.Cleanup(func() {
		n.close()
		n.loads.Wait()
	})
	n.start()
	loadFor(n, 200)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, _, closed, _ := f.snapshot(); len(closed) > 0 {
			if !reflect.DeepEqual(closed, []int{256}) {
				t.Fatalf("closed %v, want only the idle 256", closed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the idle function was never released without a further text")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := n.embed(tokensOf(10)); !ok {
		t.Fatal("the smallest function was released")
	}

	// Shutdown with a function waiting for its release, far enough off
	// that only the shutdown can close it: the timer is stopped and a
	// late tick does nothing.
	n.mu.Lock()
	n.idle = time.Hour
	n.mu.Unlock()
	loadFor(n, 200)
	n.close()
	n.mu.Lock()
	armed := n.release.Stop()
	n.mu.Unlock()
	if armed {
		t.Fatal("shutdown left the release timer armed")
	}
	n.releaseTick()
	_, _, closed, _ := f.snapshot()
	if !reflect.DeepEqual(closed, []int{256, 128, 256}) && !reflect.DeepEqual(closed, []int{256, 256, 128}) {
		t.Fatalf("closed %v, want 256 by the timer, then 128 and 256 by the shutdown", closed)
	}
}

// A failure count belongs to the loaded instance: a function released
// and loaded again starts from zero.
func TestNeuralEngine_FailureCountEndsWithInstance(t *testing.T) {
	f := &fakeANE{}
	n, clock := newTestANE(t, f)
	n.start()
	loadFor(n, 200)
	f.failOn(256, errors.New("predict failed"))
	for range aneMaxFailures - 1 {
		n.embed(tokensOf(200))
	}
	f.failOn(256, nil)
	clock.add(aneIdleRelease + time.Second)
	n.releaseTick()
	loadFor(n, 200)
	f.failOn(256, errors.New("once more"))
	n.embed(tokensOf(200))
	f.failOn(256, nil)
	if _, ok := n.embed(tokensOf(200)); !ok {
		t.Fatal("a fresh instance was retired on failures counted before it loaded")
	}
}

// With every function refused there is nothing left to try.
func TestNeuralEngine_OffWhenNoFunctionLeft(t *testing.T) {
	refused := errors.New("planned on the cpu")
	f := &fakeANE{loadErr: map[int]error{128: refused, 256: refused, 512: refused}}
	var mu sync.Mutex
	var lines []string
	n := newNeuralEngine(f, []int{128, 256, 512}, 4, func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	t.Cleanup(func() {
		n.close()
		n.loads.Wait()
	})
	n.start()
	for _, tokens := range []int{10, 200, 400} {
		loadFor(n, tokens)
	}
	n.mu.Lock()
	off := n.off
	n.mu.Unlock()
	if !off {
		t.Fatal("still on with every function refused")
	}
	mu.Lock()
	defer mu.Unlock()
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, aneOffStatus) {
		t.Fatalf("last line %q, want the off status the server reads", last)
	}
}

// A function is never released under a prediction.
func TestNeuralEngine_ReleaseSkipsFunctionInUse(t *testing.T) {
	f := &fakeANE{}
	n, clock := newTestANE(t, f)
	n.start()
	loadFor(n, 200)
	f.mu.Lock()
	f.predicting, f.resume = make(chan int), make(chan struct{})
	f.mu.Unlock()
	served := make(chan bool)
	go func() {
		_, ok := n.embed(tokensOf(200))
		served <- ok
	}()
	<-f.predicting
	clock.add(aneIdleRelease + time.Second)
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); len(closed) != 0 {
		t.Fatalf("closed %v under a prediction", closed)
	}
	f.resume <- struct{}{}
	if !<-served {
		t.Fatal("the prediction in flight failed")
	}
	f.mu.Lock()
	f.predicting, f.resume = nil, nil
	f.mu.Unlock()
	n.releaseTick()
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{256}) {
		t.Fatalf("closed %v, want 256 once it was free", closed)
	}
}

func TestNeuralEngine_CloseDuringLoad(t *testing.T) {
	f := &fakeANE{gate: make(chan struct{}), started: make(chan int, 1)}
	n, _ := newTestANE(t, f)
	n.start()
	<-f.started
	n.close()
	close(f.gate)
	n.loads.Wait()
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{128}) {
		t.Fatalf("closed %v: a function that lands after close must be released", closed)
	}
	if _, ok := n.embed(tokensOf(10)); ok {
		t.Fatal("served after close")
	}
}

func TestInstallDir(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "compiled")
	if err := os.MkdirAll(filepath.Join(src, "weights"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "weights", "weight.bin"), []byte("w"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "model.mlmodelc")
	if err := installDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "weights", "weight.bin")); err != nil || string(b) != "w" {
		t.Fatalf("installed file: %q, %v", b, err)
	}

	// Another process installed first: its model stays, ours goes.
	again := filepath.Join(root, "again")
	if err := os.MkdirAll(again, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(again, "other"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installDir(again, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "other")); err == nil {
		t.Fatal("replaced an installed model")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("left %d entries behind, want only the installed model", len(entries))
	}
}

// A staging directory a dead process left is removed once it is old; a
// fresh one belongs to a compile in flight.
func TestRemoveStaleStages(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "model.mlmodelc")
	old, fresh := stagePath(dst, 111), stagePath(dst, 222)
	for _, d := range []string{old, fresh, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * aneStageMaxAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	removeStaleStages(dst)
	if _, err := os.Stat(old); err == nil {
		t.Error("an abandoned staging directory was kept")
	}
	for _, d := range []string{fresh, dst} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed", filepath.Base(d))
		}
	}
}

func TestCoreMLPackage_Verify(t *testing.T) {
	dir := t.TempDir()
	sum := func(s string) string {
		h := sha256.Sum256([]byte(s))
		return hex.EncodeToString(h[:])
	}
	pkg := &coreMLPackage{name: "m", files: []coreMLFile{
		{"Manifest.json", sum("manifest")},
		{"Data/weights/weight.bin", sum("weights")},
		{"unpinned", ""},
	}}
	write := func(path, body string) {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Manifest.json", "manifest")
	write("Data/weights/weight.bin", "weights")
	write("unpinned", "anything")
	if err := pkg.verify(dir); err != nil {
		t.Fatalf("the pinned files: %v", err)
	}
	// A file still being copied in is shorter than the pinned one.
	write("Data/weights/weight.bin", "weig")
	if err := pkg.verify(dir); err == nil || !strings.Contains(err.Error(), "weight.bin") {
		t.Fatalf("a partial file: err = %v, want one naming it", err)
	}
}

func TestCoreMLBackend_PrepareWaitsForFiles(t *testing.T) {
	pkg := &coreMLPackage{name: "m", files: []coreMLFile{{"Manifest.json", ""}, {"Data/weights/weight.bin", ""}}}
	models := t.TempDir()
	dir := pkg.dir(models)
	b := newCoreMLBackend(pkg, dir, 4, t.Logf)
	if want := filepath.Join(models, pkg.id()+".mlmodelc"); b.compiled != want {
		t.Fatalf("compiled model at %s, want %s", b.compiled, want)
	}
	// Another pin is another directory, with a compiled model of its own.
	repinned := *pkg
	repinned.files = []coreMLFile{{"Manifest.json", "00"}, {"Data/weights/weight.bin", ""}}
	if repinned.dir(models) == dir {
		t.Fatal("a changed checksum kept the package directory")
	}
	if err := b.prepare(); !errors.Is(err, errANENotReady) {
		t.Fatalf("prepare with no package = %v, want errANENotReady", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.prepare(); !errors.Is(err, errANENotReady) {
		t.Fatalf("prepare with half a package = %v, want errANENotReady", err)
	}
}
