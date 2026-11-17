//go:build llamacpp && !android && !ios

package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	// aneMaxFailures consecutive failed predictions retire a function
	// for the rest of the process.
	aneMaxFailures = 3
	// aneStageMaxAge is how old a compile's staging directory is before
	// another process removes it as abandoned.
	aneStageMaxAge = time.Hour
	// aneLogPrefix marks the child's stderr lines about this path. The
	// server logs them at info level and reads from them whether Core
	// ML is in use in the child: any line says it is, one that starts
	// with aneOffStatus that it no longer is (embed_worker.go).
	aneLogPrefix = "neural engine: "
	aneOffStatus = "off, "
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

// neuralEngine routes tokenized texts to loaded functions. start, embed
// and close are called by one goroutine at a time (Local.mu). Functions
// load on a goroutine of their own and idle ones are released from a
// timer; neither touches a function that is predicting.
type neuralEngine struct {
	lengths []int // ascending
	dim     int
	backend aneBackend
	idle    time.Duration // aneIdleRelease
	now     func() time.Time
	logf    func(format string, args ...any)

	mu       sync.Mutex
	off      bool
	prepared bool
	nextTry  time.Time // earliest next prepare
	fns      map[int]aneFunction
	used     map[int]time.Time
	failed   map[int]bool // lengths that failed to load or kept failing
	failures map[int]int  // consecutive failed predictions
	queue    []int        // lengths waiting for the loader
	inflight int          // length the loader is loading; 0 = none
	inUse    int          // length predicting now; 0 = none
	loading  bool
	release  *time.Timer // runs releaseTick; nil until a function can go idle
	loads    sync.WaitGroup
}

func newNeuralEngine(backend aneBackend, lengths []int, dim int, logf func(string, ...any)) *neuralEngine {
	return &neuralEngine{
		lengths:  lengths,
		dim:      dim,
		backend:  backend,
		idle:     aneIdleRelease,
		now:      time.Now,
		logf:     logf,
		fns:      map[int]aneFunction{},
		used:     map[int]time.Time{},
		failed:   map[int]bool{},
		failures: map[int]int{},
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
	n.used[use] = n.now()
	n.inUse = use
	n.armReleaseLocked()
	n.mu.Unlock()

	out := make([]float32, n.dim)
	err := fn.embed(tokens, out)
	if err == nil && !unitVector(out) {
		err = errors.New("output is not a unit vector")
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	n.inUse = 0
	if err != nil {
		n.failures[use]++
		n.logf("s%d failed: %v", use, err)
		if n.failures[use] >= aneMaxFailures && n.fns[use] == fn {
			fn.close()
			delete(n.fns, use)
			n.failed[use] = true
			n.logf("s%d not used any more: %d failures in a row", use, aneMaxFailures)
			n.offIfUselessLocked()
		}
		return nil, false
	}
	n.failures[use] = 0
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

// requestLocked queues a function for loading and starts the loader. A
// length that is loaded, loading, queued or failed is left alone.
func (n *neuralEngine) requestLocked(length int) {
	if n.off || n.failed[length] || n.fns[length] != nil || n.inflight == length {
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
// time, the smallest first. Requests it drops while the package is not
// ready come back with the next texts.
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
			// The smallest function serves queries: it loads first,
			// whichever text got here first.
			queue := []int{n.lengths[0]}
			for _, q := range n.queue {
				if q != n.lengths[0] {
					queue = append(queue, q)
				}
			}
			n.queue = queue
			continue
		}
		length := n.queue[0]
		n.queue = n.queue[1:]
		if n.fns[length] != nil || n.failed[length] {
			continue
		}
		n.inflight = length
		n.mu.Unlock()
		fn, info, err := n.backend.load(length)
		n.mu.Lock()
		n.inflight = 0
		switch {
		case err != nil:
			n.failed[length] = true
			n.logf("s%d not used: %v", length, err)
			n.offIfUselessLocked()
		case n.off || n.fns[length] != nil:
			fn.close()
		default:
			n.fns[length] = fn
			n.used[length] = n.now()
			delete(n.failures, length) // counted on an earlier instance
			n.logf("s%d ready (%s)", length, info)
			n.armReleaseLocked()
		}
	}
}

// armReleaseLocked schedules releaseTick for the moment the next loaded
// function, the smallest aside, has gone unused for the idle time.
func (n *neuralEngine) armReleaseLocked() {
	now := n.now()
	wait := time.Duration(-1)
	for _, l := range n.lengths[1:] {
		if n.fns[l] == nil {
			continue
		}
		if d := n.idle - now.Sub(n.used[l]); wait < 0 || d < wait {
			wait = max(d, 50*time.Millisecond)
		}
	}
	switch {
	case wait < 0:
	case n.release == nil:
		n.release = time.AfterFunc(wait, n.releaseTick)
	default:
		n.release.Reset(wait)
	}
}

// releaseTick closes every function but the smallest that went unused
// for the idle time. The next text that needs one reloads it.
func (n *neuralEngine) releaseTick() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.off {
		return
	}
	now := n.now()
	for _, l := range n.lengths[1:] {
		fn := n.fns[l]
		if fn == nil || l == n.inUse || now.Sub(n.used[l]) < n.idle {
			continue
		}
		fn.close()
		delete(n.fns, l)
		delete(n.failures, l)
		n.logf("s%d released after %s unused", l, n.idle)
	}
	n.armReleaseLocked()
}

// offIfUselessLocked turns the path off once every function has failed.
func (n *neuralEngine) offIfUselessLocked() {
	for _, l := range n.lengths {
		if !n.failed[l] {
			return
		}
	}
	n.turnOffLocked("no function can be used")
}

func (n *neuralEngine) turnOffLocked(reason string) {
	if n.off {
		return
	}
	n.off = true
	n.queue = nil
	if n.release != nil {
		n.release.Stop()
	}
	for l, fn := range n.fns {
		fn.close()
		delete(n.fns, l)
	}
	if reason != "" {
		n.logf(aneOffStatus+"llama.cpp embeds everything: %s", reason)
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
	name    string // what its files on disk are named after
	url     string // directory the files download from; "" = no source
	files   []coreMLFile
	lengths []int // ascending
	// notices are the licence texts that go with the package. They are
	// fetched beside it, from a revision of their own.
	noticesURL string
	notices    []coreMLFile
}

type coreMLFile struct {
	path   string // slash-separated, inside the package
	sha256 string // "" = not checked
}

// id names the package on disk: its name and a digest of the pinned
// files. A changed pin is then another directory with a compiled model
// of its own.
func (p *coreMLPackage) id() string {
	h := sha256.New()
	for _, f := range p.files {
		_, _ = io.WriteString(h, f.path+"\x00"+f.sha256+"\x00")
	}
	return p.name + "-" + hex.EncodeToString(h.Sum(nil))[:8]
}

func (p *coreMLPackage) dir(modelsDir string) string {
	return filepath.Join(modelsDir, p.id()+".mlpackage")
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

// verify checks the package files in dir against their pinned checksums.
func (p *coreMLPackage) verify(dir string) error {
	for _, f := range p.files {
		if err := f.verify(dir); err != nil {
			return err
		}
	}
	return nil
}

// noticesDir is where the package's licence texts sit, beside it.
func (p *coreMLPackage) noticesDir(modelsDir string) string {
	return filepath.Join(modelsDir, p.id()+".licenses")
}

// noticesComplete reports whether every licence text is in place with
// its pinned checksum.
func (p *coreMLPackage) noticesComplete(modelsDir string) bool {
	dir := p.noticesDir(modelsDir)
	for _, f := range p.notices {
		if f.verify(dir) != nil {
			return false
		}
	}
	return true
}

// verify checks the file in dir against its pinned checksum; with none
// pinned the file only has to be there.
func (f coreMLFile) verify(dir string) error {
	file, err := os.Open(filepath.Join(dir, filepath.FromSlash(f.path)))
	if err != nil {
		return err
	}
	defer file.Close()
	if f.sha256 == "" {
		return nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.sha256 {
		return fmt.Errorf("%s is not the pinned file: sha256 %s, want %s", f.path, got, f.sha256)
	}
	return nil
}

// coreMLBackend runs a package through Core ML. The compiled model sits
// next to the package and is shared by every process under the root.
type coreMLBackend struct {
	pkg      *coreMLPackage
	dir      string // the .mlpackage
	compiled string // the .mlmodelc
	dim      int
	logf     func(format string, args ...any)
	// plans holds the placement of every function that passed its
	// check: a reload after an idle release does not plan again. Used
	// by one loader at a time.
	plans map[int]coreml.Placement
	plan  func(compiled, function string) (coreml.Placement, error)
	open  func(compiled, function string, length, dim int) (aneFunction, error)
}

func newCoreMLBackend(pkg *coreMLPackage, dir string, dim int, logf func(string, ...any)) *coreMLBackend {
	return &coreMLBackend{
		pkg:      pkg,
		dir:      dir,
		compiled: strings.TrimSuffix(dir, ".mlpackage") + ".mlmodelc",
		dim:      dim,
		logf:     logf,
		plans:    map[int]coreml.Placement{},
		plan:     coreml.Plan,
		open: func(compiled, function string, length, dim int) (aneFunction, error) {
			fn, err := coreml.Open(compiled, function, length, dim)
			if err != nil {
				return nil, err
			}
			return coreMLFunction{fn}, nil
		},
	}
}

func (b *coreMLBackend) prepare() error {
	if !b.pkg.complete(b.dir) {
		return errANENotReady
	}
	// Written before the first Core ML call: a crash inside one is then
	// a death with the Neural Engine in use.
	b.logf("starting")
	ok, err := coreml.HasNeuralEngine()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("this machine has no neural engine")
	}
	removeStaleStages(b.compiled)
	if _, err := os.Stat(b.compiled); err == nil {
		return nil
	}
	// The compiled model outlives this process, so it is built from the
	// pinned bytes only. A package still being copied in fails here.
	if err := b.pkg.verify(b.dir); err != nil {
		return err
	}
	start := time.Now()
	tmp, err := coreml.Compile(b.dir)
	if err != nil {
		return err
	}
	if err := installDir(tmp, b.compiled); err != nil {
		return fmt.Errorf("install the compiled model: %w", err)
	}
	b.logf("package compiled in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

func stagePath(dst string, pid int) string {
	return dst + "." + strconv.Itoa(pid) + ".tmp"
}

// removeStaleStages deletes the staging directories of installs toward
// dst that a dead process left behind.
func removeStaleStages(dst string) {
	stages, _ := filepath.Glob(dst + ".*.tmp")
	for _, s := range stages {
		if st, err := os.Stat(s); err == nil && time.Since(st.ModTime()) > aneStageMaxAge {
			_ = os.RemoveAll(s)
		}
	}
}

// installDir moves directory src to dst, which appears whole or not at
// all. A dst another process installed meanwhile is kept.
func installDir(src, dst string) error {
	stage := stagePath(dst, os.Getpid())
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
	plan, known := b.plans[length]
	if !known {
		var err error
		if plan, err = b.plan(b.compiled, name); err != nil {
			return nil, "", err
		}
		if !plan.OnNeuralEngine() {
			return nil, "", fmt.Errorf("core ml plans it off the neural engine: %d operations there, %d on the gpu, %d on the cpu, cpu core %v",
				plan.NeuralEngine, plan.GPU, plan.CPU, plan.CPUCore)
		}
		b.plans[length] = plan
	}
	planned := time.Since(start)
	fn, err := b.open(b.compiled, name, length, b.dim)
	if err != nil {
		return nil, "", err
	}
	info := fmt.Sprintf("plan %s, load %s, %d operations on the neural engine, %d on the cpu",
		planned.Round(time.Millisecond), (time.Since(start) - planned).Round(time.Millisecond), plan.NeuralEngine, plan.CPU)
	return fn, info, nil
}

type coreMLFunction struct{ fn *coreml.Function }

func (f coreMLFunction) embed(tokens []int32, out []float32) error { return f.fn.Embed(tokens, out) }
func (f coreMLFunction) close()                                    { f.fn.Close() }

// aneStderrLog is the child's log: a prefixed stderr line, which the
// server logs.
func aneStderrLog(format string, args ...any) {
	fmt.Fprintf(os.Stderr, aneLogPrefix+format+"\n", args...)
}
