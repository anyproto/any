//go:build llamacpp && !android && !ios

package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anyproto/any/internal/indexer/coreml"
)

// The Neural Engine path of the local embedder. A pinned model's Core ML
// package holds fixed-length functions; a text runs on the smallest
// loaded one that fits it. llama.cpp embeds everything else: a text
// longer than every function, and a query until its function is loaded;
// a document waits for it. Both runtimes write one index
// (docs/13-index.md § Neural Engine).

const (
	// aneRetryInterval spaces the attempts to start on a package that is
	// still downloading.
	aneRetryInterval = 30 * time.Second
	// aneIdleRelease frees a function unused this long. The smallest
	// usable one stays: it serves queries. Long enough that a backlog with few long
	// texts does not reload the large functions over and over.
	aneIdleRelease = 5 * time.Minute
	// aneBulkWait bounds how long bulk work waits for a function: for the
	// path's first one (workerEmbedder.PrepareBulk), and for the one a
	// document needs, from when it was asked for (neuralEngine.embedBulk).
	aneBulkWait = 10 * time.Minute
	// anePackageWait bounds how long bulk work waits for the package's
	// download, from its first waiter.
	anePackageWait = 30 * time.Minute
	// aneMaxFailures consecutive failed predictions retire a function
	// for the rest of the process.
	aneMaxFailures = 3
	// aneStageMaxAge is how old a compile's staging directory is before
	// another process removes it as abandoned.
	aneStageMaxAge = time.Hour
	// aneLogPrefix marks the child's stderr lines about this path. The
	// server logs them and reads from them whether Core ML is in use in
	// the child: any line says it is, until one starts with aneOffStatus.
	aneLogPrefix = "neural engine: "
	aneOffStatus = "off, "
	// aneStarting comes before the path's first Core ML call, aneLoading
	// after "s<length> " before a function's load; aneLoadDiscarded ends a
	// load whose function was closed unused.
	aneStarting      = "starting"
	aneLoading       = "loading"
	aneLoadDiscarded = "loaded and discarded"
	// aneNotUsed opens what follows "s<length> " for a function that did
	// not load.
	aneNotUsed  = "not used: "
	aneCompiled = "package compiled"
	// aneReleasing and aneReleased follow "s<length> " around the close of
	// a function that went unused.
	aneReleasing = "releasing"
	aneReleased  = "released"
	anePreparing = "prepare" // names the call aneStarting opens
)

// aneCalls keeps, in open, the Core ML calls a child's status lines say
// are in flight: its preparation until a function's line or off, its
// loads and its idle releases. A child that dies with one open died in it.
func aneCalls(open map[string]bool, status string) {
	if status == aneStarting {
		open[anePreparing] = true
		return
	}
	if strings.HasPrefix(status, aneOffStatus) {
		delete(open, anePreparing)
		return
	}
	name, rest, ok := strings.Cut(status, " ")
	if !ok || !strings.HasPrefix(name, "s") {
		return
	}
	switch {
	case rest == aneLoading:
		delete(open, anePreparing)
		open[name+" load"] = true
	case strings.HasPrefix(rest, "ready"), strings.HasPrefix(rest, aneNotUsed):
		// An earlier release's child prints no "loading" line: its
		// preparation runs through its first load.
		delete(open, anePreparing)
		delete(open, name+" load")
	case rest == aneLoadDiscarded:
		delete(open, name+" load")
	case rest == aneReleasing:
		open[name+" release"] = true
	case strings.HasPrefix(rest, aneReleased):
		delete(open, name+" release")
	}
}

// aneWakes reports whether a status line changes what a deferred text
// can do: its function is loaded or not used, llama.cpp takes its texts
// past the bound, or the path is off.
func aneWakes(status string) bool {
	if strings.HasPrefix(status, aneOffStatus) {
		return true
	}
	name, rest, ok := strings.Cut(status, " ")
	if !ok || !strings.HasPrefix(name, "s") {
		return false
	}
	return strings.HasPrefix(rest, "ready") || strings.HasPrefix(rest, "not used") ||
		strings.HasPrefix(rest, "still loading")
}

// aneSettles reports whether a status line ends the wait for the path's
// first function, named first ("" = whichever): it is loaded ("s128
// ready (…)"), it is not used ("s128 not used: …"), or the path is off.
func aneSettles(status, first string) bool {
	if strings.HasPrefix(status, aneOffStatus) {
		return true
	}
	name, rest, ok := strings.Cut(status, " ")
	if !ok || !strings.HasPrefix(name, "s") || (first != "" && name != first) {
		return false
	}
	return strings.HasPrefix(rest, "ready") || strings.HasPrefix(rest, "not used")
}

// aneSettledAs names how a status line that settles the path did it.
func aneSettledAs(status string) string {
	_, rest, _ := strings.Cut(status, " ")
	switch {
	case strings.HasPrefix(status, aneOffStatus):
		return BulkOff
	case strings.HasPrefix(rest, "ready"):
		return BulkReady
	}
	return BulkFailed
}

// errANENotReady is a package that is not on disk yet: a wait, not a
// failure.
var errANENotReady = errors.New("core ml package not on disk yet")

// aneBackend is the Core ML side of the Neural Engine path.
type aneBackend interface {
	// present reports, cheaply, whether the package is on disk: prepare
	// then has something to work on.
	present() bool
	// prepare makes the compiled model available. errANENotReady asks
	// for a later attempt; any other error turns the path off.
	prepare() error
	// load readies the function for one length, checks that it predicts
	// and describes it for the log.
	load(length int) (aneFunction, string, error)
}

type aneFunction interface {
	embed(tokens []int32, out []float32) error
	close()
}

// neuralEngine routes tokenized texts to loaded functions. embed and
// close are called by one goroutine at a time (Local.mu); start also
// runs from the retry timer. Loads and idle releases run on goroutines
// of their own and never touch a predicting function.
type neuralEngine struct {
	lengths []int // ascending
	dim     int
	backend aneBackend
	idle    time.Duration // aneIdleRelease
	retryIn time.Duration // aneRetryInterval
	wait    time.Duration // aneBulkWait
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
	asked    map[int]time.Time
	// owed is when the wait of bulk documents for a function ends: set
	// with the first one deferred, kept until one embeds on the function
	// or the wait runs out with it loaded. late runs lateTick then.
	owed     map[int]time.Time
	late     map[int]*time.Timer
	inflight int         // length the loader is loading; 0 = none
	inUse    int         // length predicting now; 0 = none
	loading  bool        // the loader runs
	miss     string      // why embed last left a text to llama.cpp (EmbedReason*)
	release  *time.Timer // runs releaseTick; nil until a function can go idle
	retry    *time.Timer // runs start again after a package that was not on disk
	loads    sync.WaitGroup
}

func newNeuralEngine(backend aneBackend, lengths []int, dim int, logf func(string, ...any)) *neuralEngine {
	return &neuralEngine{
		lengths:  lengths,
		dim:      dim,
		backend:  backend,
		idle:     aneIdleRelease,
		retryIn:  aneRetryInterval,
		wait:     aneBulkWait,
		asked:    map[int]time.Time{},
		owed:     map[int]time.Time{},
		late:     map[int]*time.Timer{},
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
	vec, ok, _ = n.embedFor(tokens, false)
	return vec, ok
}

// embedBulk is embed for a document of bulk work. deferred: no vector,
// and none from llama.cpp either: the function is on its way and the
// document's wait for it has not run out. The caller sends it again.
func (n *neuralEngine) embedBulk(tokens []int32) (vec []float32, ok, deferred bool) {
	return n.embedFor(tokens, true)
}

// fallsBack reports whether embed would leave the text to llama.cpp
// right now, and why (EmbedReason*), without embedding it.
func (n *neuralEngine) fallsBack(tokens []int32, bulk bool) (miss string, falls bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	fn, _, _, deferred := n.routeLocked(tokens, bulk)
	return n.miss, fn == nil && !deferred
}

func (n *neuralEngine) embedFor(tokens []int32, bulk bool) (vec []float32, ok, deferred bool) {
	n.mu.Lock()
	fn, use, _, deferred := n.routeLocked(tokens, bulk)
	if fn == nil {
		n.mu.Unlock()
		return nil, false, deferred
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
	// The path went off while it predicted.
	if n.off && n.fns[use] == fn {
		fn.close()
		delete(n.fns, use)
	}
	if err != nil {
		n.failures[use]++
		n.logf("s%d failed: %v", use, statusErr(err))
		if n.failures[use] >= aneMaxFailures && n.fns[use] == fn {
			fn.close()
			delete(n.fns, use)
			n.failed[use] = true
			n.settleOwedLocked(use)
			n.logf("s%d not used any more: %d failures in a row", use, aneMaxFailures)
			n.offIfUselessLocked()
		}
		n.miss = EmbedReasonANEFailed
		return nil, false, false
	}
	n.failures[use] = 0
	// A bulk document embedded on this function: the wait for it is over.
	if bulk {
		n.settleOwedLocked(use)
		n.armReleaseLocked()
	}
	return out, true, false
}

// routeLocked picks the function for a text and asks for the one it
// belongs on (want): the smallest that holds it and has not failed. A nil
// fn is a miss, with n.miss the reason, unless deferred.
func (n *neuralEngine) routeLocked(tokens []int32, bulk bool) (fn aneFunction, use, want int, deferred bool) {
	fits := false
	for _, l := range n.lengths {
		if len(tokens) > l {
			continue
		}
		fits = true
		if !n.failed[l] {
			want = l
			break
		}
	}
	switch {
	case n.off:
		n.miss = EmbedReasonANEOff
	case len(tokens) == 0 || !fits:
		n.miss = EmbedReasonTooLong
	case want == 0:
		n.miss = EmbedReasonANEFailed
	}
	if n.off || len(tokens) == 0 || want == 0 {
		return nil, 0, want, false
	}
	for _, l := range n.lengths {
		if l >= want && n.fns[l] != nil && !n.failed[l] {
			fn, use = n.fns[l], l
			break
		}
	}
	if use != want {
		// A package that landed is not left to the retry timer.
		if !n.prepared && bulk && n.backend.present() {
			n.nextTry = time.Time{}
		}
		n.requestLocked(want)
	}
	if fn != nil {
		return fn, use, want, false
	}
	switch {
	case !n.prepared && (!bulk || !n.backend.present()):
		// A package that is there and being prepared is waited for.
		n.miss = EmbedReasonPackageNotReady
	case !bulk:
		n.miss = EmbedReasonFunctionLoading
	default:
		if n.owed[want].IsZero() {
			n.owed[want] = n.now().Add(n.wait)
			n.late[want] = time.AfterFunc(n.wait, func() { n.lateTick(want) })
		}
		if n.now().Before(n.owed[want]) {
			return nil, 0, want, true
		}
		n.miss = EmbedReasonReadinessTimeout
	}
	return nil, 0, want, false
}

// lastMiss is why embed last answered ok = false.
func (n *neuralEngine) lastMiss() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.miss
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

// requestLocked queues a function for loading, the queue ascending, and
// starts the loader. A length that is loaded, loading, queued or failed
// is left alone.
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
	slices.Sort(n.queue)
	if n.asked[length].IsZero() {
		n.asked[length] = n.now()
	}
	if !n.loading {
		n.loading = true
		n.loads.Add(1)
		go n.loader()
	}
}

// loader prepares the backend, then loads queued functions one at a
// time, the smallest queued first; a load in flight is not interrupted
// for a smaller one. Requests it drops while the package is not ready
// come back with the next texts.
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
				n.nextTry = n.now().Add(n.retryIn)
				n.queue = nil
				clear(n.asked)
				n.settleOwedLocked()
				// Again without a text asking: bulk work sends none
				// until the first function is ready.
				if n.retry != nil {
					n.retry.Stop()
				}
				if !n.off {
					n.retry = time.AfterFunc(n.retryIn, n.start)
				}
				return
			case err != nil:
				n.turnOffLocked(statusErr(err))
				return
			}
			// Closed while it prepared: the queue went with it.
			if n.off {
				return
			}
			n.prepared = true
			// The smallest function serves queries: it loads whichever
			// text got here first.
			if n.queue[0] != n.lengths[0] {
				n.queue = append([]int{n.lengths[0]}, n.queue...)
			}
			continue
		}
		length := n.queue[0]
		n.queue = n.queue[1:]
		if n.fns[length] != nil || n.failed[length] {
			delete(n.asked, length)
			continue
		}
		n.inflight = length
		n.logf("s%d %s", length, aneLoading)
		n.mu.Unlock()
		fn, info, err := n.backend.load(length)
		n.mu.Lock()
		var asked time.Duration
		if t := n.asked[length]; !t.IsZero() {
			asked = n.now().Sub(t).Round(time.Millisecond)
		}
		delete(n.asked, length)
		switch {
		case err != nil:
			n.failed[length] = true
			n.settleOwedLocked(length)
			n.logf("s%d %s%v", length, aneNotUsed, statusErr(err))
			n.offIfUselessLocked()
		case n.off:
			// The path went off while it loaded.
			fn.close()
			n.logf("s%d %s", length, aneLoadDiscarded)
		default:
			n.fns[length] = fn
			n.used[length] = n.now()
			// Nobody is left waiting for a function that loaded late.
			if t := n.owed[length]; !t.IsZero() && !n.now().Before(t) {
				n.settleOwedLocked(length)
			}
			delete(n.failures, length) // counted on an earlier instance
			n.logf("s%d ready (%s; asked for %s ago)", length, info, asked)
			n.armReleaseLocked()
		}
		n.inflight = 0
	}
}

// settleOwedLocked ends the bulk wait for the given functions, or for
// all of them.
func (n *neuralEngine) settleOwedLocked(lengths ...int) {
	if len(lengths) == 0 {
		for l := range n.owed {
			lengths = append(lengths, l)
		}
	}
	for _, l := range lengths {
		delete(n.owed, l)
		if t := n.late[l]; t != nil {
			t.Stop()
			delete(n.late, l)
		}
	}
}

// lateTick runs when the bulk wait for a function is over. Still not
// loaded: llama.cpp embeds its documents from here, and the line has the
// server send them. Loaded and unused: it may be released again.
func (n *neuralEngine) lateTick(length int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.off || n.owed[length].IsZero() {
		return
	}
	if n.fns[length] != nil {
		n.settleOwedLocked(length)
		n.armReleaseLocked()
		return
	}
	n.logf("s%d still loading after %s: llama.cpp embeds its texts until it is ready", length, n.wait)
}

// keptLocked is the function that is not released as idle: the smallest
// that has not failed, which serves queries. 0 = none.
func (n *neuralEngine) keptLocked() int {
	for _, l := range n.lengths {
		if !n.failed[l] {
			return l
		}
	}
	return 0
}

// armReleaseLocked schedules releaseTick for the moment the next loaded
// function, the kept one and those bulk documents wait for aside, has
// gone unused for the idle time.
func (n *neuralEngine) armReleaseLocked() {
	now := n.now()
	wait := time.Duration(-1)
	kept := n.keptLocked()
	for _, l := range n.lengths {
		if l == kept || n.fns[l] == nil || !n.owed[l].IsZero() {
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

// releaseTick closes every function but the kept one that went unused
// for the idle time. The next text that needs one reloads it. A function
// a deferred document waits for stays until that wait is over.
func (n *neuralEngine) releaseTick() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.off {
		return
	}
	now := n.now()
	kept := n.keptLocked()
	for _, l := range n.lengths {
		fn := n.fns[l]
		if fn == nil || l == kept || l == n.inUse || !n.owed[l].IsZero() || now.Sub(n.used[l]) < n.idle {
			continue
		}
		// A child that dies in the close died in Core ML.
		n.logf("s%d %s", l, aneReleasing)
		fn.close()
		delete(n.fns, l)
		delete(n.failures, l)
		n.logf("s%d %s after %s unused", l, aneReleased, n.idle)
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
	clear(n.asked)
	n.settleOwedLocked()
	if n.release != nil {
		n.release.Stop()
	}
	if n.retry != nil {
		n.retry.Stop()
	}
	// The one that is predicting is closed when it returns (embedFor).
	for l, fn := range n.fns {
		if l == n.inUse {
			continue
		}
		fn.close()
		delete(n.fns, l)
	}
	// After the closes: a child that dies in one died with Core ML on.
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

// statusErr is err's text for a status line, without the file names the
// os package puts in its errors: the line is logged.
func statusErr(err error) string {
	msg := err.Error()
	// Every one in the chain: a copy's error names its source inside the
	// error that names its destination.
	for e := err; e != nil; e = errors.Unwrap(e) {
		var paths []string
		switch fe := e.(type) {
		case *fs.PathError:
			paths = []string{fe.Path}
		case *os.LinkError:
			paths = []string{fe.Old, fe.New}
		}
		for _, p := range paths {
			if p != "" {
				msg = strings.ReplaceAll(msg, p, "a file")
			}
		}
	}
	return msg
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
	tempDir  string // where a compile bound for another volume waits for its copy
	dim      int
	logf     func(format string, args ...any)
	open     func(compiled, function string, length, dim int) (aneFunction, error)
}

func newCoreMLBackend(pkg *coreMLPackage, dir string, dim int, logf func(string, ...any)) *coreMLBackend {
	return &coreMLBackend{
		pkg:      pkg,
		dir:      dir,
		compiled: strings.TrimSuffix(dir, ".mlpackage") + ".mlmodelc",
		tempDir:  os.TempDir(),
		dim:      dim,
		logf:     logf,
		open: func(compiled, function string, length, dim int) (aneFunction, error) {
			fn, err := coreml.Open(compiled, function, length, dim)
			if err != nil {
				return nil, err
			}
			return coreMLFunction{fn}, nil
		},
	}
}

func (b *coreMLBackend) present() bool { return b.pkg.complete(b.dir) }

func (b *coreMLBackend) prepare() (err error) {
	defer func() { err = b.scrub(err) }()
	if !b.present() {
		return errANENotReady
	}
	// Written before the first Core ML call: a crash inside one is then
	// a death with the Neural Engine in use.
	b.logf(aneStarting)
	ok, err := coreml.HasNeuralEngine()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("this machine has no neural engine")
	}
	removeStaleStages(b.compiled, b.tempDir, processAlive)
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
	if err := installDir(tmp, b.compiled, b.tempDir); err != nil {
		return fmt.Errorf("install the compiled model: %w", err)
	}
	b.logf(aneCompiled+" in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

// stageSuffix ends a stage's name. Not ".tmp": an earlier release's child
// removes an hour-old <dst>.*.tmp whoever owns it, a live copy that slept
// included.
const stageSuffix = ".stage"

func stagePath(dst string, pid int) string {
	return dst + "." + strconv.Itoa(pid) + stageSuffix
}

// removeStaleStages deletes the staging directories of installs toward
// dst that a dead process left behind, beside dst and in tempDir: named
// as stagePath names them or with the earlier ".tmp" suffix, old, and of
// a pid that alive reports gone.
func removeStaleStages(dst, tempDir string, alive func(pid int) bool) {
	base := filepath.Base(dst)
	for _, dir := range []string{filepath.Dir(dst), tempDir} {
		if dir == "" {
			continue
		}
		// By name, not a pattern: the directory's own path can hold a bracket.
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			pid, ok := stagePID(e.Name(), base)
			if !ok {
				continue
			}
			// Only a directory, which is all an install makes: a file or a
			// link with a stage's name is not one. A live owner's stage is
			// in use however old: its copy can have slept through the bound.
			s := filepath.Join(dir, e.Name())
			if st, err := os.Lstat(s); err == nil && st.IsDir() && time.Since(st.ModTime()) > aneStageMaxAge && !alive(pid) {
				_ = os.RemoveAll(s)
			}
		}
	}
}

// stagePID is the pid in name, a stage that stagePath names for a
// destination named base or one with the earlier ".tmp" suffix; false for
// any other name.
func stagePID(name, base string) (int, bool) {
	digits, ok := strings.CutPrefix(name, base+".")
	if !ok {
		return 0, false
	}
	if d, isStage := strings.CutSuffix(digits, stageSuffix); isStage {
		digits = d
	} else if digits, ok = strings.CutSuffix(digits, ".tmp"); !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 32)
	return int(n), err == nil && n > 0 && strconv.FormatInt(n, 10) == digits
}

// renameDir is os.Rename; tests make it fail as across volumes.
var renameDir = os.Rename

// installDir moves directory src to dst, which appears whole or not at
// all; a dst another process installed meanwhile is kept. A src on another
// volume is copied from a stage in tempDir that removeStaleStages sweeps.
func installDir(src, dst, tempDir string) error {
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	stage := stagePath(dst, os.Getpid())
	_ = os.RemoveAll(stage)
	if err := renameDir(src, stage); err != nil {
		// src can sit on another volume: Core ML compiles into the system
		// temporary directory. It is copied only from a name there that the
		// sweep knows, so a stop mid-copy strands nothing.
		if held := filepath.Join(tempDir, filepath.Base(stage)); held != src {
			_ = os.RemoveAll(held)
			if err := renameDir(src, held); err != nil {
				_ = os.RemoveAll(src)
				return installedMeanwhile(dst, err)
			}
			src = held
			_ = os.Chtimes(held, time.Time{}, time.Now()) // its age counts from here
		}
		err := os.CopyFS(stage, os.DirFS(src))
		_ = os.RemoveAll(src)
		if err != nil {
			_ = os.RemoveAll(stage)
			return installedMeanwhile(dst, err)
		}
	}
	if err := os.Rename(stage, dst); err != nil {
		_ = os.RemoveAll(stage)
		return installedMeanwhile(dst, err)
	}
	return nil
}

// installedMeanwhile is err of an install that failed, nil when another
// process installed dst meanwhile.
func installedMeanwhile(dst string, err error) error {
	if _, statErr := os.Stat(dst); statErr == nil {
		return nil
	}
	return err
}

// load opens the function and has it predict.
func (b *coreMLBackend) load(length int) (_ aneFunction, _ string, err error) {
	defer func() { err = b.scrub(err) }()
	start := time.Now()
	fn, err := b.open(b.compiled, "s"+strconv.Itoa(length), length, b.dim)
	if err != nil {
		return nil, "", err
	}
	opened := time.Since(start)
	// As in serving, one failed prediction does not retire the function.
	// The line has the very first prediction's time, also after a retry.
	var first time.Duration
	for try := 1; ; try++ {
		var took time.Duration
		took, err = validateFunction(fn, length, b.dim)
		if try == 1 {
			first = took
		}
		if err == nil {
			break
		}
		if try == aneMaxFailures {
			fn.close()
			return nil, "", fmt.Errorf("it does not predict: %w", err)
		}
	}
	// The first prediction's time is in the line: off the Neural Engine
	// it is several times what it is on it.
	info := fmt.Sprintf("open %s, first predict %s, validate %s", opened.Round(time.Millisecond),
		first.Round(100*time.Microsecond), (time.Since(start) - opened).Round(time.Millisecond))
	return scrubbedFunction{fn, b}, info, nil
}

// scrubbed is an error whose text names no path under the models
// directory: it ends up in a status line, which is logged.
type scrubbed struct {
	msg string
	err error
}

func (e scrubbed) Error() string { return e.msg }
func (e scrubbed) Unwrap() error { return e.err }

func (b *coreMLBackend) scrub(err error) error {
	if err == nil {
		return nil
	}
	msg := statusErr(err)
	for _, part := range [][2]string{{b.compiled, "the compiled model"}, {b.dir, "the package"}, {filepath.Dir(b.dir), "the models directory"}} {
		msg = strings.ReplaceAll(msg, part[0], part[1])
	}
	if msg == err.Error() {
		return err
	}
	return scrubbed{msg: msg, err: err}
}

// scrubbedFunction is a function whose prediction errors are scrubbed
// like the backend's own.
type scrubbedFunction struct {
	aneFunction
	b *coreMLBackend
}

func (f scrubbedFunction) embed(tokens []int32, out []float32) error {
	return f.b.scrub(f.aneFunction.embed(tokens, out))
}

// validateFunction has a fresh function predict two short texts and two of
// its full length, each pair alike but for its tokens: every output a unit
// vector, no two alike. first times the first prediction, failed or not.
func validateFunction(fn aneFunction, length, dim int) (first time.Duration, err error) {
	full, other := make([]int32, length), make([]int32, length)
	for i := range full {
		full[i] = int32(1000 + i%512)
		other[i] = full[i]
		if i >= length/2 { // the same start, another text after it
			other[i] = int32(20000 + i%512)
		}
	}
	full[0], full[length-1] = 2, 1
	other[0], other[length-1] = 2, 1
	probes := [][]int32{{2, 1001, 1002, 1003, 1}, {2, 5001, 7002, 9003, 1}, full, other}
	outs := make([][]float32, len(probes))
	for i, tokens := range probes {
		outs[i] = make([]float32, dim)
		start := time.Now()
		err := fn.embed(tokens, outs[i])
		if i == 0 {
			first = time.Since(start)
		}
		if err != nil {
			return first, err
		}
		if !unitVector(outs[i]) {
			return first, errors.New("output is not a unit vector")
		}
	}
	for i := range outs {
		for j := i + 1; j < len(outs); j++ {
			var dot, ni, nj float64
			for k := range outs[i] {
				dot += float64(outs[i][k]) * float64(outs[j][k])
				ni += float64(outs[i][k]) * float64(outs[i][k])
				nj += float64(outs[j][k]) * float64(outs[j][k])
			}
			// By both norms: an output only has to be near unit length.
			if dot/math.Sqrt(ni*nj) > validateMaxCosine {
				return first, errors.New("different texts got one vector")
			}
		}
	}
	return first, nil
}

// validateMaxCosine is how alike the vectors of two unrelated texts may
// be. The same text twice differs by far less than this leaves room for.
const validateMaxCosine = 0.999

type coreMLFunction struct{ fn *coreml.Function }

func (f coreMLFunction) embed(tokens []int32, out []float32) error { return f.fn.Embed(tokens, out) }
func (f coreMLFunction) close()                                    { f.fn.Close() }

// aneStderrLog is the child's log: a prefixed stderr line, which the
// server logs.
func aneStderrLog(format string, args ...any) {
	fmt.Fprintf(os.Stderr, aneLogPrefix+format+"\n", args...)
}
