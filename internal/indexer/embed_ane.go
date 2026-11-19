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
// longer than every function, and a query until its function is loaded;
// a document waits for it. Both runtimes write one index
// (docs/13-index.md § Neural Engine).

const (
	// aneRetryInterval spaces the attempts to start on a package that is
	// still downloading.
	aneRetryInterval = 30 * time.Second
	// aneIdleRelease frees a function unused this long. The smallest one
	// stays: it serves queries. Long enough that a backlog with few long
	// texts does not reload the large functions over and over.
	aneIdleRelease = 5 * time.Minute
	// aneBulkWait bounds how long bulk work waits for a function: for the
	// path's first one (workerEmbedder.PrepareBulk), and for the one a
	// document needs, from when it was asked for (neuralEngine.embedBulk).
	aneBulkWait = 10 * time.Minute
	// aneAuditMax is how long a placement audit may hold an idle child.
	// The plan's own timeout ends well inside it; reading the plan is not
	// bounded by that one.
	aneAuditMax = 15 * time.Minute
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
	// aneAuditStarted and aneAuditFinished follow "s<length> " around a
	// placement audit; the second comes however the audit went.
	aneAuditStarted  = "placement audit started"
	aneAuditFinished = "placement audit finished"
)

// aneAuditing reads a status line for the function whose placement audit
// it says started, or finished.
func aneAuditing(status string) (started, finished string) {
	name, rest, ok := strings.Cut(status, " ")
	switch {
	case !ok || !strings.HasPrefix(name, "s"):
	case rest == aneAuditStarted:
		return name, ""
	case rest == aneAuditFinished:
		return "", name
	}
	return "", ""
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

// errOffNeuralEngine is a function whose core Core ML plans off the
// Neural Engine: it is not used.
var errOffNeuralEngine = errors.New("core ml plans it off the neural engine")

// aneBackend is the Core ML side of the Neural Engine path.
type aneBackend interface {
	// present reports, cheaply, whether the package is on disk: prepare
	// then has something to work on.
	present() bool
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
// runs from the retry timer. Loads, placement audits and idle releases
// run on goroutines of their own and never touch a predicting function.
type neuralEngine struct {
	lengths  []int // ascending
	dim      int
	backend  aneBackend
	idle     time.Duration // aneIdleRelease
	retryIn  time.Duration // aneRetryInterval
	wait     time.Duration // aneBulkWait
	auditMax time.Duration // aneAuditMax
	now      func() time.Time
	logf     func(format string, args ...any)

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
	owed map[int]time.Time
	late map[int]*time.Timer
	// verified are the lengths whose placement on the Neural Engine is
	// on record; audits, the loaded ones still to check, each once.
	verified  map[int]bool
	audited   map[int]bool
	audits    []int
	auditing  bool      // the auditor runs
	auditOf   int       // length inside backend.audit now; 0 = none
	auditFrom time.Time // when that audit began
	quiet     bool      // quiesce was accepted: no audit starts any more
	// offReason is the off line still to write: it waits for the Core ML
	// calls in flight, so a child that dies in one died with Core ML on.
	offReason string
	inflight  int         // length the loader is loading; 0 = none
	inUse     int         // length predicting now; 0 = none
	loading   bool        // the loader runs
	miss      string      // why embed last left a text to llama.cpp (EmbedReason*)
	placed    bool        // the function embed last used is a verified one
	release   *time.Timer // runs releaseTick; nil until a function can go idle
	retry     *time.Timer // runs start again after a package that was not on disk
	loads     sync.WaitGroup
}

func newNeuralEngine(backend aneBackend, lengths []int, dim int, logf func(string, ...any)) *neuralEngine {
	return &neuralEngine{
		lengths:  lengths,
		dim:      dim,
		backend:  backend,
		idle:     aneIdleRelease,
		retryIn:  aneRetryInterval,
		wait:     aneBulkWait,
		auditMax: aneAuditMax,
		asked:    map[int]time.Time{},
		owed:     map[int]time.Time{},
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
	defer n.sayOffLocked()
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
// belongs on (want). A nil fn is a miss, with n.miss the reason, unless
// deferred.
func (n *neuralEngine) routeLocked(tokens []int32, bulk bool) (fn aneFunction, use, want int, deferred bool) {
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
		return nil, 0, want, false
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
	if fn != nil {
		return fn, use, want, false
	}
	switch {
	case n.failed[want]:
		n.miss = EmbedReasonANEFailed
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
// time, the smallest first. Requests it drops while the package is not
// ready come back with the next texts.
func (n *neuralEngine) loader() {
	defer n.loads.Done()
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() {
		n.loading = false
		n.startAuditorLocked()
	}()
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
			n.prepared = true
			// A function on record as refused is not waited for.
			for _, l := range n.lengths {
				if err := n.backend.refused(l); err != nil && !n.failed[l] {
					n.failed[l] = true
					n.settleOwedLocked(l)
					n.logf("s%d not used: %v", l, statusErr(err))
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
			delete(n.asked, length)
			continue
		}
		n.inflight = length
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
			n.logf("s%d not used: %v", length, statusErr(err))
			n.offIfUselessLocked()
		case n.off || n.failed[length] || n.fns[length] != nil:
			// Refused by its audit while it loaded again, among others.
			fn.close()
		default:
			n.fns[length] = fn
			n.used[length] = n.now()
			// Nobody is left waiting for a function that loaded late.
			if t := n.owed[length]; !t.IsZero() && !n.now().Before(t) {
				n.settleOwedLocked(length)
			}
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
		// After a discarded handle is closed: that is a Core ML call too.
		n.inflight = 0
		n.sayOffLocked()
	}
}

// startAuditorLocked starts the auditor when functions wait for it and
// the loader has nothing to load.
func (n *neuralEngine) startAuditorLocked() {
	if n.off || n.auditing || n.loading || len(n.audits) == 0 {
		return
	}
	n.auditing = true
	n.loads.Add(1)
	go n.auditor()
}

// auditor checks the placement of the functions waiting for it, one at
// a time. It starts an audit only while the loader is idle and never
// holds it back: a function asked for under an audit loads beside it.
func (n *neuralEngine) auditor() {
	defer n.loads.Done()
	n.mu.Lock()
	defer n.mu.Unlock()
	defer func() { n.auditing = false }()
	for !n.off && !n.loading && !n.quiet && len(n.audits) > 0 {
		length := n.audits[0]
		n.audits = n.audits[1:]
		if n.failed[length] || n.verified[length] {
			continue
		}
		n.logf("s%d %s", length, aneAuditStarted)
		n.auditOf, n.auditFrom = length, n.now()
		n.mu.Unlock()
		info, err := n.backend.audit(length)
		n.mu.Lock()
		n.auditOf = 0
		switch {
		case n.off:
		case errors.Is(err, errOffNeuralEngine):
			n.failed[length] = true
			n.settleOwedLocked(length)
			// One that is predicting is closed when it returns (embedFor).
			if fn := n.fns[length]; fn != nil && n.inUse != length {
				fn.close()
				delete(n.fns, length)
			}
			n.logf("s%d not used: %v", length, statusErr(err))
			n.offIfUselessLocked()
		case err != nil:
			n.logf("s%d placement not verified: %v", length, statusErr(err))
		default:
			n.verified[length] = true
			n.logf("s%d placement verified (%s)", length, info)
		}
		// Whatever the outcome: an audit the server saw start and not
		// finish is Core ML work a child died in.
		n.logf("s%d %s", length, aneAuditFinished)
		n.sayOffLocked()
	}
}

// quiesce is the server asking to stop an idle child. It is refused
// while an audit runs, whose verdict would be lost (overdue: for longer
// than any audit takes); once accepted, no audit starts any more.
func (n *neuralEngine) quiesce() (ok, overdue bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.auditOf != 0 {
		return false, n.now().Sub(n.auditFrom) >= n.auditMax
	}
	n.quiet = true
	return true, false
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

// armReleaseLocked schedules releaseTick for the moment the next loaded
// function, the smallest and those bulk documents wait for aside, has
// gone unused for the idle time.
func (n *neuralEngine) armReleaseLocked() {
	now := n.now()
	wait := time.Duration(-1)
	for _, l := range n.lengths[1:] {
		if n.fns[l] == nil || !n.owed[l].IsZero() {
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
// for the idle time. The next text that needs one reloads it. A function
// a deferred document waits for stays until that wait is over.
func (n *neuralEngine) releaseTick() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.off {
		return
	}
	now := n.now()
	for _, l := range n.lengths[1:] {
		fn := n.fns[l]
		if fn == nil || l == n.inUse || !n.owed[l].IsZero() || now.Sub(n.used[l]) < n.idle {
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
	n.offReason = reason
	n.sayOffLocked()
}

// sayOffLocked writes the off line once no Core ML call is in flight.
func (n *neuralEngine) sayOffLocked() {
	if n.offReason == "" || n.inUse != 0 || n.inflight != 0 || n.auditOf != 0 {
		return
	}
	n.logf(aneOffStatus+"llama.cpp embeds everything: %s", n.offReason)
	n.offReason = ""
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
// next to the package and is shared by every process under the root, and
// so are the records of where Core ML places its functions.
type coreMLBackend struct {
	pkg      *coreMLPackage
	dir      string // the .mlpackage
	compiled string // the .mlmodelc
	dim      int
	logf     func(format string, args ...any)
	// record is what the placement files beside the compiled model are
	// named after, and host the machine setup a placement holds for;
	// "" = nothing is kept between processes.
	record string
	host   string
	// placements is what is known about each function: read from its
	// record, or audited by this process. Guarded by mu: a load and an
	// audit can run side by side.
	mu         sync.Mutex
	placements map[int]placement
	plan       func(compiled, function string) (coreml.Placement, error)
	open       func(compiled, function string, length, dim int) (aneFunction, error)
}

// placementVersion is the version of the placement records. Raise it when
// the rule that reads a plan changes (coreml.Placement.OnNeuralEngine).
const placementVersion = 1

// placement is the audited plan of one function. recorded: it came from
// a record, not from this process's own audit.
type placement struct {
	On           bool
	NeuralEngine int
	GPU          int
	CPU          int
	Unknown      int // operations the plan named no device for
	CPUCore      []string
	recorded     bool
}

// plan is the placement as the compute plan gave it, less the names of
// its core operators of unknown placement: a verdict does not hang on
// them (coreml.Placement.Conclusive).
func (p placement) plan() coreml.Placement {
	return coreml.Placement{NeuralEngine: p.NeuralEngine, GPU: p.GPU, CPU: p.CPU, Unknown: p.Unknown, CPUCore: p.CPUCore}
}

// valid reports whether a recorded placement is a verdict the placement
// rule gives for its own counts. An entry cut short or edited is not.
func (p placement) valid() bool {
	if p.NeuralEngine < 0 || p.GPU < 0 || p.CPU < 0 || p.Unknown < 0 || len(p.CPUCore) > p.CPU {
		return false
	}
	return p.plan().Conclusive() && p.On == p.plan().OnNeuralEngine()
}

func (p placement) refusal() error {
	return fmt.Errorf("%w: %d operations there, %d on the gpu, %d on the cpu, %d unknown, cpu core %v",
		errOffNeuralEngine, p.NeuralEngine, p.GPU, p.CPU, p.Unknown, p.CPUCore)
}

// placementRecord is one function's file: its placement for one model on
// one machine setup. A field that is absent makes it no record.
type placementRecord struct {
	Version      *int     `json:"version"`
	Model        *string  `json:"model"` // coreMLPackage.id
	Host         *string  `json:"host"`
	Function     *string  `json:"function"`
	On           *bool    `json:"on"`
	NeuralEngine *int     `json:"neuralEngine"`
	GPU          *int     `json:"gpu"`
	CPU          *int     `json:"cpu"`
	Unknown      *int     `json:"unknown"`
	CPUCore      []string `json:"cpuCore,omitempty"`
}

func newCoreMLBackend(pkg *coreMLPackage, dir string, dim int, logf func(string, ...any)) *coreMLBackend {
	base := strings.TrimSuffix(dir, ".mlpackage")
	return &coreMLBackend{
		pkg:        pkg,
		dir:        dir,
		compiled:   base + ".mlmodelc",
		dim:        dim,
		logf:       logf,
		record:     base + ".placement",
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

func (b *coreMLBackend) present() bool { return b.pkg.complete(b.dir) }

func (b *coreMLBackend) prepare() (err error) {
	defer func() { err = b.scrub(err) }()
	if !b.present() {
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
func (b *coreMLBackend) load(length int) (_ aneFunction, _ string, err error) {
	defer func() { err = b.scrub(err) }()
	name := "s" + strconv.Itoa(length)
	b.mu.Lock()
	known, audited := b.placements[length]
	b.mu.Unlock()
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
	return scrubbedFunction{fn, b}, info, nil
}

func (b *coreMLBackend) placed(length int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.placements[length].On
}

func (b *coreMLBackend) refused(length int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, audited := b.placements[length]; audited && !p.On {
		return fmt.Errorf("%w (on record)", p.refusal())
	}
	return nil
}

// audit reads the function's compute plan and records the verdict, so no
// later process on this machine setup plans it again.
func (b *coreMLBackend) audit(length int) (_ string, err error) {
	defer func() { err = b.scrub(err) }()
	start := time.Now()
	plan, err := b.plan(b.compiled, "s"+strconv.Itoa(length))
	if err != nil {
		return "", err
	}
	// No verdict from a plan that leaves it open.
	if !plan.Conclusive() {
		return "", fmt.Errorf("the compute plan names no device for %d operations, core %v", plan.Unknown, plan.UnknownCore)
	}
	p := placement{On: plan.OnNeuralEngine(), NeuralEngine: plan.NeuralEngine, GPU: plan.GPU, CPU: plan.CPU,
		Unknown: plan.Unknown, CPUCore: plan.CPUCore}
	b.mu.Lock()
	b.placements[length] = p
	b.mu.Unlock()
	if err := b.writePlacement(length, p); err != nil {
		b.logf("s%d placement not recorded: %s", length, statusErr(b.scrub(err)))
	}
	if !p.On {
		return "", p.refusal()
	}
	return fmt.Sprintf("plan %s, %d operations on the neural engine, %d on the cpu",
		time.Since(start).Round(time.Millisecond), p.NeuralEngine, p.CPU), nil
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

// recordPath is the placement file of one function on this machine
// setup, which its name carries: another setup's verdicts stay.
func (b *coreMLBackend) recordPath(length int) string {
	setup := sha256.Sum256([]byte(b.host))
	return b.record + "." + hex.EncodeToString(setup[:4]) + ".s" + strconv.Itoa(length) + ".json"
}

// readPlacements takes over the records of the package's functions for
// this machine setup. One that is missing, unreadable, incomplete,
// another model's or another setup's is none.
func (b *coreMLBackend) readPlacements() {
	if b.host == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, length := range b.pkg.lengths {
		if _, mine := b.placements[length]; mine {
			continue
		}
		raw, err := os.ReadFile(b.recordPath(length))
		if err != nil {
			continue
		}
		var r placementRecord
		if json.Unmarshal(raw, &r) != nil ||
			r.Version == nil || r.Model == nil || r.Host == nil || r.Function == nil ||
			r.On == nil || r.NeuralEngine == nil || r.GPU == nil || r.CPU == nil || r.Unknown == nil ||
			*r.Version != placementVersion || *r.Model != b.pkg.id() || *r.Host != b.host ||
			*r.Function != "s"+strconv.Itoa(length) {
			continue
		}
		p := placement{On: *r.On, NeuralEngine: *r.NeuralEngine, GPU: *r.GPU, CPU: *r.CPU, Unknown: *r.Unknown,
			CPUCore: r.CPUCore, recorded: true}
		if p.valid() {
			b.placements[length] = p
		}
	}
}

// writePlacement replaces one function's record, whole or not at all.
// A file per function: processes auditing different ones have nothing
// to merge and need no lock. An unreadable record is left alone.
func (b *coreMLBackend) writePlacement(length int, p placement) error {
	if b.host == "" {
		return nil
	}
	path := b.recordPath(length)
	if _, err := os.ReadFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	version, model, name := placementVersion, b.pkg.id(), "s"+strconv.Itoa(length)
	raw, err := json.Marshal(placementRecord{Version: &version, Model: &model, Host: &b.host, Function: &name,
		On: &p.On, NeuralEngine: &p.NeuralEngine, GPU: &p.GPU, CPU: &p.CPU, Unknown: &p.Unknown, CPUCore: p.CPUCore})
	if err != nil {
		return err
	}
	tmp := stagePath(path, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// validateFunction has a freshly opened function predict four texts: two
// short ones and two of its full length, each pair alike but for its
// tokens. Each output is the model's unit vector, and no two are alike.
func validateFunction(fn aneFunction, length, dim int) error {
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
		if err := fn.embed(tokens, outs[i]); err != nil {
			return err
		}
		if !unitVector(outs[i]) {
			return errors.New("output is not a unit vector")
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
