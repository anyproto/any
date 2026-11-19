//go:build llamacpp && !android && !ios

package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
// longer than every function, and a query until its function is loaded.
// A document whose function is loading is left for a later round
// instead. Both runtimes write one index (docs/13-index.md § Neural
// Engine).

const (
	// aneRetryInterval spaces the attempts to start on a package that is
	// still downloading.
	aneRetryInterval = 30 * time.Second
	// aneIdleRelease frees a function unused this long. The smallest one
	// stays: it serves queries. Long enough that a backlog with few long
	// texts does not reload the large functions over and over.
	aneIdleRelease = 5 * time.Minute
	// aneBulkWait bounds how long bulk work waits for a function: for
	// the path's first one before any text (workerEmbedder.PrepareBulk),
	// and for the one a document needs, counted from when it was first
	// asked for (neuralEngine.embedBulk). A first load on a machine takes
	// minutes.
	aneBulkWait = 10 * time.Minute
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
	// aneAuditStarted follows "s<length> " when a placement audit begins.
	// The next line about that function's placement, or that it is not
	// used, ends it (aneAuditing).
	aneAuditStarted = "placement audit started"
)

// aneAuditing reads a status line for the audit the child runs: started
// is the function it began on, ended one whose audit this line closes.
func aneAuditing(status string) (started, ended string) {
	name, rest, ok := strings.Cut(status, " ")
	switch {
	case strings.HasPrefix(status, aneOffStatus):
		return "", "*"
	case !ok || !strings.HasPrefix(name, "s"):
		return "", ""
	case rest == aneAuditStarted:
		return name, ""
	case strings.HasPrefix(rest, "placement ") || strings.HasPrefix(rest, "not used"):
		return "", name
	}
	return "", ""
}

// aneSettles reports whether a status line ends the wait for the path's
// first function: one is loaded ("s128 ready (…)"), one failed to load
// ("s128 not used: …"), or the path is off.
func aneSettles(status string) bool {
	if strings.HasPrefix(status, aneOffStatus) {
		return true
	}
	name, rest, ok := strings.Cut(status, " ")
	return ok && strings.HasPrefix(name, "s") && (strings.HasPrefix(rest, "ready") || strings.HasPrefix(rest, "not used"))
}

// errANENotReady is a package that is not on disk yet: a wait, not a
// failure.
var errANENotReady = errors.New("core ml package not on disk yet")

// errOffNeuralEngine is a function whose core Core ML plans off the
// Neural Engine: it is not used.
var errOffNeuralEngine = errors.New("core ml plans it off the neural engine")

// aneBackend is the Core ML side of the Neural Engine path.
type aneBackend interface {
	// prepare makes the compiled model available. errANENotReady asks
	// for a later attempt; any other error turns the path off.
	prepare() error
	// load readies the function for one length, checks that it predicts
	// and describes it for the log. It refuses a function on record as
	// planned off the Neural Engine (errOffNeuralEngine).
	load(length int) (aneFunction, string, error)
	// placed reports whether the function's placement on the Neural
	// Engine is on record; refused, after prepare, the error of one on
	// record as planned off it.
	placed(length int) bool
	refused(length int) error
	// audit reads where Core ML plans the function, records it and
	// describes it for the log; errOffNeuralEngine is a function planned
	// off the Neural Engine. It needs no loaded function.
	audit(length int) (string, error)
}

type aneFunction interface {
	embed(tokens []int32, out []float32) error
	close()
}

// neuralEngine routes tokenized texts to loaded functions. embed and
// close are called by one goroutine at a time (Local.mu); start also
// runs from the retry timer. Functions load on a goroutine of their own
// and idle ones are released from a timer; neither touches a function
// that is predicting. A function serves as soon as it is loaded; the
// loader audits its placement afterwards, when it has nothing to load.
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
	// wanted is when each function now queued or loading was asked for;
	// late holds the timers that run lateTick, wait after that.
	wanted map[int]time.Time
	late   map[int]*time.Timer
	queue  []int // lengths waiting for the loader
	// verified are the lengths whose placement on the Neural Engine is
	// on record; audits, the loaded ones still to check, each once.
	verified map[int]bool
	audited  map[int]bool
	audits   []int
	inflight int // length the loader is loading; 0 = none
	inUse    int // length predicting now; 0 = none
	loading  bool
	miss     string      // why embed last left a text to llama.cpp (EmbedReason*)
	placed   bool        // the function embed last used is a verified one
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
		wanted:   map[int]time.Time{},
		late:     map[int]*time.Timer{},
		verified: map[int]bool{},
		audited:  map[int]bool{},
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
// and llama.cpp is not to embed the text either, because the function
// it needs is loading and was asked for less than the bulk wait ago.
// The caller sends the text again later.
func (n *neuralEngine) embedBulk(tokens []int32) (vec []float32, ok, deferred bool) {
	return n.embedFor(tokens, true)
}

// fallsBack reports whether embed would leave the text to llama.cpp
// right now, and why (EmbedReason*), without embedding it.
func (n *neuralEngine) fallsBack(tokens []int32, bulk bool) (miss string, falls bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	fn, _, deferred := n.routeLocked(tokens, bulk)
	return n.miss, fn == nil && !deferred
}

func (n *neuralEngine) embedFor(tokens []int32, bulk bool) (vec []float32, ok, deferred bool) {
	n.mu.Lock()
	fn, use, deferred := n.routeLocked(tokens, bulk)
	if fn == nil {
		n.mu.Unlock()
		return nil, false, deferred
	}
	n.used[use] = n.now()
	n.inUse = use
	n.placed = n.verified[use]
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
	// Refused by an audit, or the path went off, while it predicted.
	if (n.off || n.failed[use]) && n.fns[use] == fn {
		fn.close()
		delete(n.fns, use)
	}
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
		n.miss = EmbedReasonANEFailed
		return nil, false, false
	}
	n.failures[use] = 0
	return out, true, false
}

// routeLocked picks the function for a text and asks for the one it
// belongs on. A nil fn is a miss, with n.miss the reason, unless
// deferred.
func (n *neuralEngine) routeLocked(tokens []int32, bulk bool) (fn aneFunction, use int, deferred bool) {
	want := 0
	for _, l := range n.lengths {
		if len(tokens) <= l {
			want = l
			break
		}
	}
	if n.off || len(tokens) == 0 || want == 0 {
		n.miss = EmbedReasonTooLong
		if n.off {
			n.miss = EmbedReasonANEOff
		}
		return nil, 0, false
	}
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
		switch {
		case n.failed[want]:
			n.miss = EmbedReasonANEFailed
		case !n.prepared:
			n.miss = EmbedReasonPackageNotReady
		case bulk && n.wanted[want].IsZero():
			// Nothing is loading it: not a wait.
			n.miss = EmbedReasonFunctionLoading
		case bulk && n.now().Sub(n.wanted[want]) >= n.wait:
			n.miss = EmbedReasonReadinessTimeout
		case bulk:
			return nil, 0, true
		default:
			n.miss = EmbedReasonFunctionLoading
		}
		return nil, 0, false
	}
	return fn, use, false
}

// lastMiss is why embed last answered ok = false.
func (n *neuralEngine) lastMiss() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.miss
}

// lastPlaced reports whether the function embed last answered from has
// its placement on the Neural Engine on record.
func (n *neuralEngine) lastPlaced() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.placed
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
	if n.wanted[length].IsZero() {
		n.wanted[length] = n.now()
		n.late[length] = time.AfterFunc(n.wait, func() { n.lateTick(length) })
	}
	if !n.loading {
		n.loading = true
		n.loads.Add(1)
		go n.loader()
	}
}

// loader prepares the backend, then loads queued functions one at a
// time, the smallest first. Requests it drops while the package is not
// ready come back with the next texts. With nothing left to load it
// audits the loaded functions whose placement is not on record.
func (n *neuralEngine) loader() {
	defer n.loads.Done()
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() { n.loading = false }()
	for !n.off && (len(n.queue) > 0 || len(n.audits) > 0) {
		if len(n.queue) == 0 {
			n.auditLocked()
			continue
		}
		if !n.prepared {
			n.mu.Unlock()
			err := n.backend.prepare()
			n.mu.Lock()
			switch {
			case errors.Is(err, errANENotReady):
				n.nextTry = n.now().Add(n.retryIn)
				n.queue = nil
				n.settleWantedLocked()
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
				n.turnOffLocked(err.Error())
				return
			}
			n.prepared = true
			// A function on record as refused is not waited for. The
			// line about the smallest one settles the path's first wait.
			for _, l := range n.lengths {
				if err := n.backend.refused(l); err != nil && !n.failed[l] {
					n.failed[l] = true
					n.logf("s%d not used: %v", l, err)
				}
			}
			n.offIfUselessLocked()
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
			n.settleWantedLocked(length)
			continue
		}
		n.inflight = length
		n.mu.Unlock()
		fn, info, err := n.backend.load(length)
		n.mu.Lock()
		n.inflight = 0
		asked := n.now().Sub(n.wanted[length]).Round(time.Millisecond)
		n.settleWantedLocked(length)
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
			switch {
			case n.backend.placed(length):
				n.verified[length] = true
			case !n.audited[length]:
				n.audited[length] = true
				n.audits = append(n.audits, length)
			}
			n.logf("s%d ready (%s; asked for %s ago)", length, info, asked)
			n.armReleaseLocked()
		}
	}
}

// auditLocked checks the placement of the next function waiting for it.
// One planned off the Neural Engine is not used any more; a check that
// fails otherwise leaves the function serving, unverified.
func (n *neuralEngine) auditLocked() {
	length := n.audits[0]
	n.audits = n.audits[1:]
	if n.failed[length] || n.verified[length] {
		return
	}
	n.logf("s%d %s", length, aneAuditStarted)
	n.mu.Unlock()
	info, err := n.backend.audit(length)
	n.mu.Lock()
	// The loader could load nothing meanwhile: a function asked for under
	// the audit starts its wait now.
	for _, l := range n.queue {
		if t := n.late[l]; t != nil {
			n.wanted[l] = n.now()
			t.Reset(n.wait)
		}
	}
	switch {
	case n.off:
	case errors.Is(err, errOffNeuralEngine):
		n.failed[length] = true
		// One that is predicting is closed when it returns (embedFor).
		if fn := n.fns[length]; fn != nil && n.inUse != length {
			fn.close()
			delete(n.fns, length)
		}
		n.logf("s%d not used: %v", length, err)
		n.offIfUselessLocked()
	case err != nil:
		n.logf("s%d placement not verified: %v", length, err)
	default:
		n.verified[length] = true
		n.logf("s%d placement verified (%s)", length, info)
	}
}

// settleWantedLocked ends the wait for the given functions, or for all
// of them: loaded, failed, or no longer being loaded.
func (n *neuralEngine) settleWantedLocked(lengths ...int) {
	if len(lengths) == 0 {
		for l := range n.wanted {
			lengths = append(lengths, l)
		}
	}
	for _, l := range lengths {
		delete(n.wanted, l)
		if t := n.late[l]; t != nil {
			t.Stop()
			delete(n.late, l)
		}
	}
}

// lateTick reports a function still not loaded the bulk wait after it
// was asked for: from here llama.cpp embeds the documents that need it.
// The line also has the server send the texts it held back.
func (n *neuralEngine) lateTick(length int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.off || n.wanted[length].IsZero() {
		return
	}
	n.logf("s%d still loading after %s: llama.cpp embeds its texts until it is ready", length, n.wait)
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
	n.audits = nil
	n.settleWantedLocked()
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
// next to the package and is shared by every process under the root, and
// so is the record of where Core ML places its functions.
type coreMLBackend struct {
	pkg      *coreMLPackage
	dir      string // the .mlpackage
	compiled string // the .mlmodelc
	dim      int
	logf     func(format string, args ...any)
	// record is the placement file beside the compiled model and host
	// the machine setup a placement holds for; "" = nothing is kept
	// between processes.
	record string
	host   string
	// placements is what is known about each function: read from the
	// record, or audited by this process. Used by one loader at a time.
	placements map[int]placement
	plan       func(compiled, function string) (coreml.Placement, error)
	open       func(compiled, function string, length, dim int) (aneFunction, error)
}

// placementVersion is the version of the placement record. Raise it when
// the rule that reads a plan changes (coreml.Placement.OnNeuralEngine).
const placementVersion = 1

// placement is the audited plan of one function. recorded: it came from
// the record, not from this process's own audit.
type placement struct {
	On           bool     `json:"on"`
	NeuralEngine int      `json:"neuralEngine"`
	GPU          int      `json:"gpu"`
	CPU          int      `json:"cpu"`
	CPUCore      []string `json:"cpuCore,omitempty"`
	recorded     bool
}

// plan is the placement as the compute plan gave it.
func (p placement) plan() coreml.Placement {
	return coreml.Placement{NeuralEngine: p.NeuralEngine, GPU: p.GPU, CPU: p.CPU, CPUCore: p.CPUCore}
}

// valid reports whether a recorded placement is a verdict the placement
// rule gives for its own counts. An entry cut short or edited is not.
func (p placement) valid() bool {
	return p.NeuralEngine >= 0 && p.GPU >= 0 && p.CPU >= 0 && p.NeuralEngine+p.GPU+p.CPU > 0 &&
		p.On == p.plan().OnNeuralEngine()
}

func (p placement) refusal() error {
	return fmt.Errorf("%w: %d operations there, %d on the gpu, %d on the cpu, cpu core %v",
		errOffNeuralEngine, p.NeuralEngine, p.GPU, p.CPU, p.CPUCore)
}

// placementRecord is the file: the placements of one model's functions
// on one machine setup. A record of another model or setup is not read,
// and is replaced by the first audit here.
type placementRecord struct {
	Version   int                  `json:"version"`
	Model     string               `json:"model"` // coreMLPackage.id
	Host      string               `json:"host"`
	Functions map[string]placement `json:"functions"`
}

func newCoreMLBackend(pkg *coreMLPackage, dir string, dim int, logf func(string, ...any)) *coreMLBackend {
	base := strings.TrimSuffix(dir, ".mlpackage")
	return &coreMLBackend{
		pkg:        pkg,
		dir:        dir,
		compiled:   base + ".mlmodelc",
		dim:        dim,
		logf:       logf,
		record:     base + ".placement.json",
		host:       aneHostSetup(),
		placements: map[int]placement{},
		plan:       coreml.Plan,
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
	b.readPlacements()
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

// load opens the function and has it predict. No compute plan is read
// here: a plan costs a load of its own (audit).
func (b *coreMLBackend) load(length int) (aneFunction, string, error) {
	name := "s" + strconv.Itoa(length)
	known, audited := b.placements[length]
	if err := b.refused(length); err != nil {
		return nil, "", err
	}
	start := time.Now()
	fn, err := b.open(b.compiled, name, length, b.dim)
	if err != nil {
		return nil, "", err
	}
	opened := time.Since(start)
	// As in serving, one failed prediction does not retire the function.
	for try := 1; ; try++ {
		if err = validateFunction(fn, length, b.dim); err == nil {
			break
		}
		if try == aneMaxFailures {
			fn.close()
			return nil, "", fmt.Errorf("it does not predict: %w", err)
		}
	}
	how := "placement not verified yet"
	switch {
	case audited && known.recorded:
		how = "placement on record, plan skipped"
	case audited:
		how = "placement verified"
	}
	info := fmt.Sprintf("open %s, validate %s, %s",
		opened.Round(time.Millisecond), (time.Since(start) - opened).Round(time.Millisecond), how)
	return fn, info, nil
}

func (b *coreMLBackend) placed(length int) bool { return b.placements[length].On }

func (b *coreMLBackend) refused(length int) error {
	if p, audited := b.placements[length]; audited && !p.On {
		return fmt.Errorf("%w (on record)", p.refusal())
	}
	return nil
}

// audit reads the function's compute plan and records the verdict, so no
// later process on this machine setup plans it again.
func (b *coreMLBackend) audit(length int) (string, error) {
	start := time.Now()
	plan, err := b.plan(b.compiled, "s"+strconv.Itoa(length))
	if err != nil {
		return "", err
	}
	// No verdict from a plan that leaves it open.
	if !plan.Conclusive() {
		return "", fmt.Errorf("the compute plan names no device for %d operations, core %v", plan.Unknown, plan.UnknownCore)
	}
	p := placement{On: plan.OnNeuralEngine(), NeuralEngine: plan.NeuralEngine, GPU: plan.GPU, CPU: plan.CPU, CPUCore: plan.CPUCore}
	b.placements[length] = p
	if err := b.writePlacements(); err != nil {
		b.logf("placement record not written: %v", pathless(err))
	}
	if !p.On {
		return "", p.refusal()
	}
	return fmt.Sprintf("plan %s, %d operations on the neural engine, %d on the cpu",
		time.Since(start).Round(time.Millisecond), p.NeuralEngine, p.CPU), nil
}

// readPlacements takes over the record of this machine setup. A record
// that is missing, unreadable or another setup's is none.
func (b *coreMLBackend) readPlacements() {
	for name, p := range b.recorded() {
		length, err := strconv.Atoi(strings.TrimPrefix(name, "s"))
		if _, mine := b.placements[length]; err != nil || mine {
			continue
		}
		p.recorded = true
		b.placements[length] = p
	}
}

func (b *coreMLBackend) recorded() map[string]placement {
	if b.host == "" {
		return nil
	}
	raw, err := os.ReadFile(b.record)
	if err != nil {
		return nil
	}
	var rec placementRecord
	if json.Unmarshal(raw, &rec) != nil || rec.Version != placementVersion || rec.Model != b.pkg.id() || rec.Host != b.host {
		return nil
	}
	for name, p := range rec.Functions {
		if !p.valid() {
			delete(rec.Functions, name)
		}
	}
	return rec.Functions
}

// pathless is err without the file names the os package puts in it.
func pathless(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Op, pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return fmt.Errorf("%s: %w", le.Op, le.Err)
	}
	return err
}

// writePlacements adds this process's audits to the record as it is on
// disk now, read right before the write. The file appears whole or not
// at all. Nothing locks it between processes: an audit lost to another's
// write is run again.
func (b *coreMLBackend) writePlacements() error {
	if b.host == "" {
		return nil
	}
	rec := placementRecord{Version: placementVersion, Model: b.pkg.id(), Host: b.host, Functions: b.recorded()}
	if rec.Functions == nil {
		rec.Functions = map[string]placement{}
	}
	for length, p := range b.placements {
		if !p.recorded {
			rec.Functions["s"+strconv.Itoa(length)] = p
		}
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := stagePath(b.record, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, b.record); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// validateFunction has a freshly opened function predict three texts: two
// short ones of one length with different tokens, and one of its full
// length. Each output is the model's unit vector, and no two are alike.
func validateFunction(fn aneFunction, length, dim int) error {
	full := make([]int32, length)
	for i := range full {
		full[i] = int32(1000 + i%512)
	}
	full[0], full[length-1] = 2, 1
	probes := [][]int32{{2, 1001, 1002, 1003, 1}, {2, 5001, 7002, 9003, 1}, full}
	outs := make([][]float32, len(probes))
	for i, tokens := range probes {
		outs[i] = make([]float32, dim)
		if err := fn.embed(tokens, outs[i]); err != nil {
			return err
		}
		if !unitVector(outs[i]) {
			return errors.New("output is not a unit vector")
		}
	}
	for i := range outs {
		for j := i + 1; j < len(outs); j++ {
			var dot float64
			for k := range outs[i] {
				dot += float64(outs[i][k]) * float64(outs[j][k])
			}
			if dot > validateMaxCosine {
				return errors.New("different texts got one vector")
			}
		}
	}
	return nil
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
