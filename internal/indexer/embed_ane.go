//go:build llamacpp && !android && !ios

package indexer

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anyproto/any/internal/indexer/coreml"
)

// The Neural Engine path of the local embedder. A pinned model's Core ML
// package holds fixed-length functions; a text runs on the smallest
// loaded one that fits it. llama.cpp embeds everything else: a text
// longer than every function, and any text until its function is loaded.
// Both runtimes write one index (docs/13-index.md § Neural Engine).

const (
	// aneRetryInterval spaces the attempts to start on a package that is
	// still downloading.
	aneRetryInterval = 30 * time.Second
	// aneIdleRelease frees a function unused this long. The smallest one
	// stays: it serves queries.
	aneIdleRelease = time.Minute
	// aneMaxFailures consecutive failed predictions turn the Neural
	// Engine off for the rest of the process.
	aneMaxFailures = 3
	// aneLogPrefix marks the child's stderr lines the server logs at
	// info level.
	aneLogPrefix = "neural engine: "
)

// errANENotReady is a package that is not on disk yet: a wait, not a
// failure.
var errANENotReady = errors.New("core ml package not on disk yet")

// aneBackend is the Core ML side of the Neural Engine path.
type aneBackend interface {
	// prepare makes the compiled model available. errANENotReady asks
	// for a later attempt; any other error turns the path off.
	prepare() error
	// load readies the function for one length and describes it for the
	// log. It refuses a function whose core is planned off the Neural
	// Engine.
	load(length int) (aneFunction, string, error)
}

type aneFunction interface {
	embed(tokens []int32, out []float32) error
	close()
}

// neuralEngine routes tokenized texts to loaded functions. embed and
// close are called by one goroutine at a time (Local.mu); functions load
// on a goroutine of their own.
type neuralEngine struct {
	lengths []int // ascending
	dim     int
	backend aneBackend
	now     func() time.Time
	logf    func(format string, args ...any)

	mu       sync.Mutex
	off      bool
	prepared bool
	nextTry  time.Time // earliest next prepare
	fns      map[int]aneFunction
	used     map[int]time.Time
	failed   map[int]bool // lengths whose load failed
	queue    []int        // lengths waiting for the loader
	loading  bool
	failures int
	loads    sync.WaitGroup
}

func newNeuralEngine(backend aneBackend, lengths []int, dim int, logf func(string, ...any)) *neuralEngine {
	return &neuralEngine{
		lengths: lengths,
		dim:     dim,
		backend: backend,
		now:     time.Now,
		logf:    logf,
		fns:     map[int]aneFunction{},
		used:    map[int]time.Time{},
		failed:  map[int]bool{},
	}
}

// start loads the smallest function ahead of the first text.
func (n *neuralEngine) start() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.requestLocked(n.lengths[0])
}

// embed returns the model's vector for tokens. ok is false when
// llama.cpp has to embed the text.
func (n *neuralEngine) embed(tokens []int32) (vec []float32, ok bool) {
	n.mu.Lock()
	want := 0
	for _, l := range n.lengths {
		if len(tokens) <= l {
			want = l
			break
		}
	}
	if n.off || len(tokens) == 0 || want == 0 {
		n.mu.Unlock()
		return nil, false
	}
	now := n.now()
	n.releaseIdleLocked(now)
	var fn aneFunction
	use := 0
	for _, l := range n.lengths {
		if l >= want && n.fns[l] != nil {
			fn, use = n.fns[l], l
			break
		}
	}
	if use != want {
		n.requestLocked(want)
	}
	if fn == nil {
		n.mu.Unlock()
		return nil, false
	}
	n.used[use] = now
	n.mu.Unlock()

	out := make([]float32, n.dim)
	err := fn.embed(tokens, out)
	if err == nil && !unitVector(out) {
		err = errors.New("output is not a unit vector")
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		n.failures++
		n.logf("s%d failed: %v", use, err)
		if n.failures >= aneMaxFailures {
			n.turnOffLocked("repeated failures")
		}
		return nil, false
	}
	n.failures = 0
	return out, true
}

// unitVector reports whether vec is finite with length 1, which every
// function's output is.
func unitVector(vec []float32) bool {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	return !math.IsNaN(sum) && math.Abs(math.Sqrt(sum)-1) <= 1e-2
}

// requestLocked queues a function for loading and starts the loader.
func (n *neuralEngine) requestLocked(length int) {
	if n.off || n.failed[length] || n.fns[length] != nil {
		return
	}
	if !n.prepared && n.now().Before(n.nextTry) {
		return
	}
	for _, q := range n.queue {
		if q == length {
			return
		}
	}
	n.queue = append(n.queue, length)
	if !n.loading {
		n.loading = true
		n.loads.Add(1)
		go n.loader()
	}
}

// loader prepares the backend, then loads queued functions one at a
// time. Requests it drops while the package is not ready come back with
// the next texts.
func (n *neuralEngine) loader() {
	defer n.loads.Done()
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() { n.loading = false }()
	for !n.off && len(n.queue) > 0 {
		if !n.prepared {
			n.mu.Unlock()
			err := n.backend.prepare()
			n.mu.Lock()
			switch {
			case errors.Is(err, errANENotReady):
				n.nextTry = n.now().Add(aneRetryInterval)
				n.queue = nil
				return
			case err != nil:
				n.turnOffLocked(err.Error())
				return
			}
			n.prepared = true
			continue
		}
		length := n.queue[0]
		n.queue = n.queue[1:]
		n.mu.Unlock()
		fn, info, err := n.backend.load(length)
		n.mu.Lock()
		switch {
		case err != nil:
			n.failed[length] = true
			n.logf("s%d not used: %v", length, err)
		case n.off:
			fn.close()
		default:
			n.fns[length] = fn
			n.used[length] = n.now()
			n.logf("s%d ready (%s)", length, info)
		}
	}
}

// releaseIdleLocked closes every function but the smallest that went
// unused for aneIdleRelease. The next text that needs one reloads it.
func (n *neuralEngine) releaseIdleLocked(now time.Time) {
	for _, l := range n.lengths[1:] {
		if fn := n.fns[l]; fn != nil && now.Sub(n.used[l]) > aneIdleRelease {
			fn.close()
			delete(n.fns, l)
			n.logf("s%d released after %s unused", l, aneIdleRelease)
		}
	}
}

func (n *neuralEngine) turnOffLocked(reason string) {
	if n.off {
		return
	}
	n.off = true
	n.queue = nil
	for l, fn := range n.fns {
		fn.close()
		delete(n.fns, l)
	}
	if reason != "" {
		n.logf("off, llama.cpp embeds everything: %s", reason)
	}
}

// close releases every function. A load in flight is not waited for; its
// function is closed when it lands.
func (n *neuralEngine) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.turnOffLocked("")
}

// coreMLPackage is a pinned model as a Core ML package: one function
// "s<length>" per fixed token length, each taking token ids and
// returning the model's unit vector.
type coreMLPackage struct {
	name    string // package directory in the models dir, less ".mlpackage"
	url     string // directory the files download from; "" = no source
	files   []coreMLFile
	lengths []int // ascending
}

type coreMLFile struct {
	path   string // slash-separated, inside the package
	sha256 string
}

func (p *coreMLPackage) dir(modelsDir string) string {
	return filepath.Join(modelsDir, p.name+".mlpackage")
}

// complete reports whether every file of the package is in dir.
func (p *coreMLPackage) complete(dir string) bool {
	for _, f := range p.files {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.path))); err != nil {
			return false
		}
	}
	return true
}

// coreMLBackend runs a package through Core ML. The compiled model sits
// next to the package and is shared by every process under the root.
type coreMLBackend struct {
	pkg      *coreMLPackage
	dir      string // the .mlpackage
	compiled string // the .mlmodelc
	dim      int
}

func newCoreMLBackend(pkg *coreMLPackage, dir string, dim int) *coreMLBackend {
	return &coreMLBackend{
		pkg:      pkg,
		dir:      dir,
		compiled: strings.TrimSuffix(dir, ".mlpackage") + ".mlmodelc",
		dim:      dim,
	}
}

func (b *coreMLBackend) prepare() error {
	if !b.pkg.complete(b.dir) {
		return errANENotReady
	}
	ok, err := coreml.HasNeuralEngine()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("this machine has no neural engine")
	}
	if _, err := os.Stat(b.compiled); err == nil {
		return nil
	}
	start := time.Now()
	tmp, err := coreml.Compile(b.dir)
	if err != nil {
		return err
	}
	if err := installDir(tmp, b.compiled); err != nil {
		return fmt.Errorf("install the compiled model: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%spackage compiled in %s\n", aneLogPrefix, time.Since(start).Round(time.Millisecond))
	return nil
}

// installDir moves directory src to dst, which appears whole or not at
// all. A dst another process installed meanwhile is kept.
func installDir(src, dst string) error {
	stage := dst + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	_ = os.RemoveAll(stage)
	if err := os.Rename(src, stage); err != nil {
		// src can sit on another volume.
		if err := os.CopyFS(stage, os.DirFS(src)); err != nil {
			_ = os.RemoveAll(stage)
			return err
		}
		_ = os.RemoveAll(src)
	}
	if err := os.Rename(stage, dst); err != nil {
		_ = os.RemoveAll(stage)
		if _, statErr := os.Stat(dst); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func (b *coreMLBackend) load(length int) (aneFunction, string, error) {
	name := "s" + strconv.Itoa(length)
	start := time.Now()
	plan, err := coreml.Plan(b.compiled, name)
	if err != nil {
		return nil, "", err
	}
	if !plan.OnNeuralEngine() {
		return nil, "", fmt.Errorf("core ml plans it off the neural engine: %d operations there, %d on the gpu, %d on the cpu, cpu core %v",
			plan.NeuralEngine, plan.GPU, plan.CPU, plan.CPUCore)
	}
	planned := time.Since(start)
	fn, err := coreml.Open(b.compiled, name, length, b.dim)
	if err != nil {
		return nil, "", err
	}
	info := fmt.Sprintf("plan %s, load %s, %d operations on the neural engine, %d on the cpu",
		planned.Round(time.Millisecond), (time.Since(start) - planned).Round(time.Millisecond), plan.NeuralEngine, plan.CPU)
	return coreMLFunction{fn}, info, nil
}

type coreMLFunction struct{ fn *coreml.Function }

func (f coreMLFunction) embed(tokens []int32, out []float32) error { return f.fn.Embed(tokens, out) }
func (f coreMLFunction) close()                                    { f.fn.Close() }

// aneStderrLog is the child's log: a prefixed stderr line, which the
// server logs.
func aneStderrLog(format string, args ...any) {
	fmt.Fprintf(os.Stderr, aneLogPrefix+format+"\n", args...)
}
