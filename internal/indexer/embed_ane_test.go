//go:build llamacpp && !android && !ios

package indexer

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	embedErr error
	notUnit  bool
	served   []int
	tokens   [][]int32 // what each served text was, in order
	closed   []int
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

type fakeFn struct {
	parent *fakeANE
	length int
}

func (fn *fakeFn) embed(tokens []int32, out []float32) error {
	f := fn.parent
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.embedErr != nil {
		return f.embedErr
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

// A text runs on a larger loaded function while its own is missing.
func TestNeuralEngine_UsesLargerLoadedFunction(t *testing.T) {
	f := &fakeANE{loadErr: map[int]error{128: errors.New("planned on the cpu")}}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(10)); ok {
		t.Fatal("served with nothing loaded")
	}
	n.embed(tokensOf(200))
	n.loads.Wait()
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
	n.embed(tokensOf(10))
	n.loads.Wait()
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

func TestNeuralEngine_FailuresTurnOff(t *testing.T) {
	for name, broken := range map[string]func(*fakeANE){
		"errors":   func(f *fakeANE) { f.embedErr = errors.New("predict failed") },
		"not unit": func(f *fakeANE) { f.notUnit = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeANE{}
			n, _ := newTestANE(t, f)
			n.start()
			n.loads.Wait()
			if _, ok := n.embed(tokensOf(10)); !ok {
				t.Fatal("not served while healthy")
			}
			f.mu.Lock()
			broken(f)
			f.mu.Unlock()
			for range aneMaxFailures {
				if _, ok := n.embed(tokensOf(10)); ok {
					t.Fatal("served a failed prediction")
				}
			}
			f.mu.Lock()
			f.embedErr, f.notUnit = nil, false
			f.mu.Unlock()
			if _, ok := n.embed(tokensOf(10)); ok {
				t.Fatal("served after repeated failures turned it off")
			}
			if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{128}) {
				t.Fatalf("closed %v, want the loaded function released", closed)
			}
		})
	}
}

// One failure between successes is not a broken backend.
func TestNeuralEngine_FailureCountResets(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	for range aneMaxFailures * 2 {
		f.mu.Lock()
		f.embedErr = errors.New("once")
		f.mu.Unlock()
		n.embed(tokensOf(10))
		f.mu.Lock()
		f.embedErr = nil
		f.mu.Unlock()
		if _, ok := n.embed(tokensOf(10)); !ok {
			t.Fatal("turned off by failures that were not consecutive")
		}
	}
}

func TestNeuralEngine_ReleasesIdle(t *testing.T) {
	f := &fakeANE{}
	n, clock := newTestANE(t, f)
	n.start()
	n.embed(tokensOf(200))
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(200)); !ok {
		t.Fatal("256 not loaded")
	}
	clock.add(aneIdleRelease + time.Second)
	// The smallest stays however long it sat unused.
	if _, ok := n.embed(tokensOf(10)); !ok {
		t.Fatal("the smallest function was released")
	}
	if _, _, closed, _ := f.snapshot(); !reflect.DeepEqual(closed, []int{256}) {
		t.Fatalf("closed %v, want the idle 256", closed)
	}
	// The next text that needs it reloads it.
	if _, ok := n.embed(tokensOf(200)); ok {
		t.Fatal("served by a released function")
	}
	n.loads.Wait()
	if _, ok := n.embed(tokensOf(200)); !ok {
		t.Fatal("not reloaded on demand")
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

func TestCoreMLBackend_PrepareWaitsForFiles(t *testing.T) {
	pkg := &coreMLPackage{name: "m", files: []coreMLFile{{"Manifest.json", ""}, {"Data/weights/weight.bin", ""}}}
	models := t.TempDir()
	dir := pkg.dir(models)
	b := newCoreMLBackend(pkg, dir, 4)
	if want := filepath.Join(models, "m.mlmodelc"); b.compiled != want {
		t.Fatalf("compiled model at %s, want %s", b.compiled, want)
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
