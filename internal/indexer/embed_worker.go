//go:build llamacpp && !android && !ios

package indexer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
)

const (
	// workerStartTimeout bounds spawn -> `ready` (loading a ~640 MB GGUF
	// from cold storage).
	workerStartTimeout = 2 * time.Minute
	// workerDefaultRequestTimeout bounds one frame — one decode group of
	// up to batchDocs texts, seconds even for long editor windows on
	// CPU. Generous on purpose: its job is to unwedge a hung GPU, which
	// stops answering rather than failing, not to police slow hardware.
	workerDefaultRequestTimeout = 3 * time.Minute
	// workerStopGrace is how long a child gets to exit on stdin EOF
	// before it is killed.
	workerStopGrace  = 5 * time.Second
	workerMinBackoff = time.Second
	workerMaxBackoff = time.Minute
	// workerStderrTail is how many of the child's last stderr lines are
	// quoted when it dies, or of those printed before a crash report.
	workerStderrTail = 12
	// workerCrashHead is how many lines of a crash report are quoted after
	// them: a ggml abort line and its native backtrace, then the signal
	// and the top frames of the Go runtime's dump.
	workerCrashHead = 32
	// workerIdleTimeout retires a child that has served no frame for this
	// long, returning the memory its decode buffers keep
	// (docs/13-index.md § The embedder child process).
	workerIdleTimeout = 5 * time.Minute
	// workerAskTimeout bounds the idle ask, which holds the slot: a child
	// answers it without touching a runtime.
	workerAskTimeout = 10 * time.Second
)

// workerEmbedder runs the local model in a child process (`any run
// embedder`, the same binary re-exec'd) and speaks the frame protocol
// over its stdin/stdout.
//
// Why a process: llama.cpp faults are not recoverable in Go. A Vulkan
// device-lost throws vk::DeviceLostError out of vk::Queue::submit and
// the C++ exception unwinds into a purego frame with no handler, so
// std::terminate aborts; GGML_ASSERT calls abort() outright. An abort
// here kills only the child — the round fails, its docs stay `pending`,
// and the next tick retries, the outage semantics the pipeline already
// has for an unreachable embedder.
//
// One child, spawned lazily on the first request, shared by every space
// worker and retired after idleTimeout without a frame. The stream
// carries one request at a time, owned through slot: EmbedDocs sends one
// decode group per frame and takes the slot per frame, and a query takes
// it ahead of waiting doc frames, so a search waits for at most the
// decode in flight — never for a batch.
type workerEmbedder struct {
	dl           *modelDownload
	pkgDl        *packageDownload // the Core ML package; nil = none wanted or already on disk
	lg           logger.CtxLogger
	reqTimeout   time.Duration
	startTimeout time.Duration
	// idleTimeout is workerIdleTimeout; 0 never retires an idle child.
	// askTimeout is workerAskTimeout.
	idleTimeout time.Duration
	askTimeout  time.Duration
	// bulkWait is aneBulkWait, pkgWait anePackageWait; pkgWaitUntil is when
	// the wait for the package's download ends, set by its first waiter.
	bulkWait     time.Duration
	pkgWait      time.Duration
	pkgPoll      time.Duration // how often the wait looks for the package
	pkgWaitFrom  sync.Once
	pkgWaitUntil time.Time // set under pkgWaitFrom
	pkgWaitOver  sync.Once
	// noPackage: there is no Neural Engine path only because the package
	// is neither on disk nor downloaded.
	noPackage bool
	// newCmd builds the child command from a spec; injectable so tests
	// can drive a helper process instead of a real model.
	newCmd func(localSpec) (*exec.Cmd, error)
	now    func() time.Time

	// live is the running child's process and closing the shutdown
	// flag — both read without a lock so Close can kill a child while a
	// request holds the slot.
	live      atomic.Pointer[os.Process]
	closing   atomic.Bool
	closeOnce sync.Once
	// preparing counts the PrepareBulk calls in flight, holds the HoldBulk
	// ones not released: no child is retired as idle under either.
	// bulkWaited: a PrepareBulk ran into the deadline; no later call waits.
	preparing  atomic.Int32
	holds      atomic.Int32
	bulkWaited atomic.Bool
	gaveUp     chan struct{} // closed with bulkWaited
	// settledChild is the last child whose Neural Engine path settled.
	// It answers PrepareBulk until the worker lets go of it (detached).
	settledChild atomic.Pointer[embedChild]
	// pkgOnDisk reports whether the Core ML package is complete on disk;
	// nil = not asked.
	pkgOnDisk func() bool
	// wake closes, and is replaced, when texts a bulk frame left without a
	// vector are worth sending again (DeferredWake).
	wakeMu sync.Mutex
	wake   chan struct{}
	// routed counts where bulk texts went, for the log.
	routed bulkRouting

	// idleTimer runs retireIdle. Created stopped and never reassigned,
	// so its callback uses it without the slot.
	idleTimer *time.Timer

	// slot is ownership of the child's stream. Its holder alone touches
	// child, backoff, nextTry and lastFrame.
	slot *childSlot
	// Slot-guarded.
	child   *embedChild
	backoff time.Duration
	nextTry time.Time
	// lastFrame is when the last frame ended, in real time like the timer.
	lastFrame time.Time

	mu sync.Mutex // guards spec, gpuDisabled, coremlDisabled, coremlRestored, hw — nothing else
	// spec holds the parameters every spawn is rendered from. Two
	// writers: the GPU demotion below, and SetThreads. A child whose
	// spec no longer matches is retired on its next use.
	spec localSpec
	// gpuDisabled is set when a child dies abnormally while GPU offload
	// was active. In-memory by design: every `any run` starts on the GPU
	// again, so a driver or hardware fix recovers on its own.
	gpuDisabled bool
	// coremlDisabled is set when a child dies abnormally after it
	// began using Core ML. It goes before the GPU does, and comes back
	// once (coremlRestored) when a child started without it dies too.
	coremlDisabled bool
	coremlRestored bool
	// hw is what the last started child reported about this machine —
	// kept for logging now, for hardware/error/speed statistics later.
	hw Hardware
}

func newWorkerEmbedder(cfg config.IndexLocal, modelsDir, legacyModelsDir string, onProcess func(ProcessUpdate)) (*workerEmbedder, error) {
	// Config checks come first: resolveLocalSpec can start the model
	// download, and only the returned embedder's Close stops it.
	reqTimeout := workerDefaultRequestTimeout
	if cfg.RequestTimeout != "" {
		d, err := time.ParseDuration(cfg.RequestTimeout)
		if err != nil {
			return nil, fmt.Errorf("indexer: parse index.local.requestTimeout %q: %w", cfg.RequestTimeout, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("indexer: index.local.requestTimeout must be positive, got %q", cfg.RequestTimeout)
		}
		reqTimeout = d
	}
	pkg, noPackage, err := planNeuralEnginePackage(cfg, modelsDir)
	if err != nil {
		return nil, err
	}
	spec, dl, err := resolveLocalSpec(cfg, modelsDir, legacyModelsDir, onProcess)
	if err != nil {
		return nil, err
	}
	if pkg != nil {
		spec.coremlDir = pkg.dir(modelsDir)
	}
	w := newWorkerEmbedderFromSpec(spec, reqTimeout)
	w.dl = dl
	w.noPackage = noPackage
	if pkg != nil {
		dir := spec.coremlDir
		w.pkgOnDisk = func() bool { return pkg.complete(dir) }
	}
	// With a model path nothing is downloaded, the package's licence
	// texts included.
	if pkg != nil && cfg.ModelPath == "" && (!pkg.complete(spec.coremlDir) || !pkg.noticesComplete(modelsDir)) {
		w.pkgDl = startPackageDownload(pkg, modelsDir, dl, nil, onProcess)
	}
	return w, nil
}

// localEmbedderBuilt: this build has the local embedder.
const localEmbedderBuilt = true

// newLocalEmbedder backs index.embedder "local" and the "auto" fallback
// (embed_local_off.go is the build without it).
func newLocalEmbedder(cfg config.IndexLocal, modelsDir, legacyModelsDir string, onProcess func(ProcessUpdate)) (Embedder, error) {
	w, err := newWorkerEmbedder(cfg, modelsDir, legacyModelsDir, onProcess)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// newWorkerEmbedderFromSpec wires the supervisor around a resolved spec
// (tests build one around a helper process).
func newWorkerEmbedderFromSpec(spec localSpec, reqTimeout time.Duration) *workerEmbedder {
	w := &workerEmbedder{
		spec:         spec,
		lg:           logger.NewNamed("indexer.embedder"),
		reqTimeout:   reqTimeout,
		startTimeout: workerStartTimeout,
		idleTimeout:  workerIdleTimeout,
		askTimeout:   workerAskTimeout,
		bulkWait:     aneBulkWait,
		pkgWait:      anePackageWait,
		pkgPoll:      time.Second,
		gaveUp:       make(chan struct{}),
		wake:         make(chan struct{}),
		routed:       bulkRouting{every: bulkLogEvery},
		newCmd:       embedderCmd,
		now:          time.Now,
		slot:         newChildSlot(),
	}
	w.idleTimer = time.AfterFunc(time.Hour, w.retireIdle)
	w.idleTimer.Stop()
	return w
}

// embedderCmd re-execs this binary as the hidden `any run embedder`
// subcommand. Self-exec keeps distribution to one signed artifact — the
// same reason embed_local.go finds llamacpp/ next to the executable —
// and the child inherits the environment, so YZMA_LIB and friends carry
// over.
func embedderCmd(s localSpec) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("indexer: local embedder: resolve executable: %w", err)
	}
	args := []string{"run", "embedder",
		"--model", s.modelPath,
		"--ctx", strconv.Itoa(s.nCtx),
		"--batch-docs", strconv.Itoa(s.batchDocs),
	}
	if s.libDir != "" {
		args = append(args, "--lib-dir", s.libDir)
	}
	if s.profile != "" {
		args = append(args, "--profile", s.profile)
	}
	if s.queryPrefix != "" {
		args = append(args, "--query-prefix", s.queryPrefix)
	}
	if s.docPrefix != "" {
		args = append(args, "--doc-prefix", s.docPrefix)
	}
	if s.pooling != "" {
		args = append(args, "--pooling", s.pooling)
	}
	if s.threads > 0 {
		args = append(args, "--threads", strconv.Itoa(s.threads))
	}
	if s.outDim > 0 {
		args = append(args, "--dim", strconv.Itoa(s.outDim))
	}
	if s.gpuLayers >= 0 {
		args = append(args, "--gpu-layers", strconv.Itoa(s.gpuLayers))
	}
	if s.niceness > 0 {
		args = append(args, "--niceness", strconv.Itoa(s.niceness))
	}
	if s.coremlDir != "" {
		args = append(args, "--coreml", s.coremlDir)
	}
	cmd := exec.Command(exe, args...)
	applyLowPriority(cmd, s.niceness) // Windows sets it here; Unix nices in the child
	return cmd, nil
}

// EmbedDocs sends the batch one decode group per frame — batchDocs
// texts, one llama_decode in the child — and re-takes the slot for
// every frame, which is where a waiting query gets in (the frames of
// one call may straddle a respawn). A failure mid-batch returns the
// vectors embedded so far with the error (a prefix of texts): the
// caller lands those, and only the rest stays pending for the next
// round.
func (w *workerEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	return w.EmbedTitledDocs(ctx, nil, texts)
}

func (w *workerEmbedder) Stamp() (string, bool) { return w.childSpec().stamp() }

// EmbedsTitles: the child's document prompt has a title slot.
func (w *workerEmbedder) EmbedsTitles() bool {
	return strings.Contains(w.childSpec().docPrefix, titlePlaceholder)
}

func (w *workerEmbedder) EmbedTitledDocs(ctx context.Context, titles, texts []string) ([][]float32, error) {
	vecs, _, err := w.embedDocs(ctx, titles, texts, false)
	return vecs, err
}

var (
	_ BulkDeferrer = (*workerEmbedder)(nil)
	_ BulkHolder   = (*workerEmbedder)(nil)
)

// EmbedDocsRouted is EmbedTitledDocs for bulk work, with the runtimes
// that embedded the batch. A text whose Neural Engine function is
// loading comes back without a vector (docs/13-index.md § Neural Engine).
func (w *workerEmbedder) EmbedDocsRouted(ctx context.Context, titles, texts []string) ([][]float32, EmbedRoute, error) {
	return w.embedDocs(ctx, titles, texts, true)
}

// DeferredWake returns a channel that closes when a function of the
// Neural Engine path was loaded or given up, or the child changed: texts
// EmbedDocsRouted left without a vector since the call may embed now.
func (w *workerEmbedder) DeferredWake() <-chan struct{} {
	w.wakeMu.Lock()
	defer w.wakeMu.Unlock()
	return w.wake
}

func (w *workerEmbedder) wakeDeferred() {
	w.wakeMu.Lock()
	defer w.wakeMu.Unlock()
	close(w.wake)
	w.wake = make(chan struct{})
}

// HoldBulk keeps the child from being retired as idle until release: a
// drain holds it across its waits, when it sends no frame.
func (w *workerEmbedder) HoldBulk() (release func()) {
	w.holds.Add(1)
	var once sync.Once
	return func() { once.Do(func() { w.holds.Add(-1) }) }
}

func (w *workerEmbedder) embedDocs(ctx context.Context, titles, texts []string, bulk bool) ([][]float32, EmbedRoute, error) {
	if len(texts) == 0 {
		return nil, EmbedRoute{}, nil
	}
	group := max(w.childSpec().batchDocs, 1)
	out := make([][]float32, 0, len(texts))
	var tally routeTally
	for start := 0; start < len(texts); start += group {
		end := min(start+group, len(texts))
		var frameTitles []string
		if len(titles) == len(texts) {
			frameTitles = titles[start:end]
		}
		r := w.run(ctx, workerReq{Op: workerOpEmbed, Role: workerRoleDoc, Texts: texts[start:end], Titles: frameTitles, Bulk: bulk})
		out = append(out, r.vecs...)
		tally.add(r)
		if r.err != nil {
			return out, tally.route(), r.err
		}
	}
	return out, tally.route(), nil
}

// bulkLogEvery spaces the log lines about bulk texts that ran on
// llama.cpp or were left pending.
const bulkLogEvery = 30 * time.Second

// bulkRouting counts where the texts of bulk frames went since the last
// log line about them; a stretch with nothing to say stays counted.
type bulkRouting struct {
	mu            sync.Mutex
	every         time.Duration // bulkLogEvery
	last          time.Time     // the last line
	from          time.Time     // the first batch counted since
	ane, coreml   int
	llama         map[string]int // by EmbedReason*; "" = none to give
	llamaBackends map[string]bool
	deferred      int
	deferredFrom  time.Time // the first deferred text counted
}

// noteFrame counts a bulk frame for the log, where a Neural Engine path
// is configured to miss. Under the slot: a child that goes right after
// has the frame in its last line.
func (w *workerEmbedder) noteFrame(req workerReq, res frameResult) {
	w.mu.Lock()
	planned := w.spec.coremlDir != ""
	w.mu.Unlock()
	if !req.Bulk || !planned || res.routes == nil {
		return
	}
	var t routeTally
	t.add(res)
	w.noteBulk(t)
}

// noteBulk counts bulk work and logs when a line is due.
func (w *workerEmbedder) noteBulk(t routeTally) {
	r := &w.routed
	r.mu.Lock()
	now := time.Now()
	if r.from.IsZero() {
		r.from = now
	}
	r.ane += t.ane
	r.coreml += t.coreml
	for reason, n := range t.reasons {
		if r.llama == nil {
			r.llama = map[string]int{}
		}
		r.llama[reason] += n
	}
	for b, n := range t.llama {
		if n > 0 {
			if r.llamaBackends == nil {
				r.llamaBackends = map[string]bool{}
			}
			r.llamaBackends[b] = true
		}
	}
	if t.deferred > 0 {
		if r.deferred == 0 {
			r.deferredFrom = now
		}
		r.deferred += t.deferred
	}
	r.mu.Unlock()
	w.logBulk(false)
}

// logBulk writes the line about the batches counted since the last one,
// if a text of theirs ran on llama.cpp or was left pending, once that
// line is bulkLogEvery old. now: the child is going; do not wait.
func (w *workerEmbedder) logBulk(now bool) {
	r := &w.routed
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.from.IsZero() || (!now && time.Since(r.last) < r.every) {
		return
	}
	llama := 0
	var reasons, backends []string
	for reason, n := range r.llama {
		llama += n
		reasons = append(reasons, orDefaultString(reason, "none")+"="+strconv.Itoa(n))
	}
	for b := range r.llamaBackends {
		backends = append(backends, b)
	}
	slices.Sort(reasons)
	slices.Sort(backends)
	if llama > 0 || r.deferred > 0 {
		fields := []zap.Field{
			zap.Duration("window", time.Since(r.from).Round(time.Millisecond)),
			zap.Int("ane", r.ane), zap.Int("coreml", r.coreml), zap.Int("llama", llama),
		}
		if llama > 0 {
			fields = append(fields, zap.String("llamaBackends", strings.Join(backends, ",")),
				zap.String("llamaReasons", strings.Join(reasons, " ")))
		}
		if r.deferred > 0 {
			fields = append(fields, zap.Int("deferred", r.deferred),
				zap.Duration("deferredFor", time.Since(r.deferredFrom).Round(time.Millisecond)))
		}
		w.lg.Info("local embedder bulk routing", fields...)
		r.last = time.Now()
		r.from, r.deferredFrom = time.Time{}, time.Time{}
		r.ane, r.coreml, r.deferred, r.llama, r.llamaBackends = 0, 0, 0, nil, nil
	}
}

// firstFunction names the first Core ML function of the spec's model.
func firstFunction(spec localSpec) string {
	if spec.profile == "" {
		return ""
	}
	m, err := lookupLocalModel(spec.profile)
	if err != nil || m.coreml == nil || len(m.coreml.lengths) == 0 {
		return ""
	}
	return "s" + strconv.Itoa(m.coreml.lengths[0])
}

func orDefaultString(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// routeTally sums what the frames of one batch said: the child's counts,
// and the reason each frame had when it was served.
type routeTally struct {
	ane      int
	coreml   int
	llama    map[string]int // per EmbedBackend* of the child that served the frame
	reasons  map[string]int // llama.cpp texts per EmbedReason*; "" = none to give
	deferred int
	reason   string // of the last frame that had one
}

func (t *routeTally) add(r frameResult) {
	rt := r.routes
	if rt == nil {
		return
	}
	t.ane += rt.ANE
	t.coreml += rt.CoreML
	t.deferred += len(rt.Deferred)
	if rt.Llama > 0 {
		if t.llama == nil {
			t.llama = map[string]int{}
		}
		t.llama[r.llama] += rt.Llama
	}
	for reason, n := range r.reasons {
		if t.reasons == nil {
			t.reasons = map[string]int{}
		}
		t.reasons[reason] += n
	}
	if r.reason != "" {
		t.reason = r.reason
	}
}

// route names the runtimes behind a batch's tally.
func (t routeTally) route() EmbedRoute {
	rt := EmbedRoute{Reason: t.reason, Deferred: t.deferred}
	if t.ane > 0 {
		rt.Backends = append(rt.Backends, EmbedBackendANE)
	}
	if t.coreml > 0 {
		rt.Backends = append(rt.Backends, EmbedBackendCoreML)
	}
	for _, b := range []string{EmbedBackendGPU, EmbedBackendCPU, EmbedBackendLlama} {
		if t.llama[b] > 0 {
			rt.Backends = append(rt.Backends, b)
		}
	}
	return rt
}

// llamaBackend names the runtime llama.cpp embeds on in a child: the
// GPU only when it loaded the model on a device.
func llamaBackend(spec localSpec, hw *Hardware) string {
	switch {
	case spec.gpuLayers == 0:
		return EmbedBackendCPU
	case hw == nil:
		return EmbedBackendLlama
	case len(hw.ModelDevices) > 0:
		return EmbedBackendGPU
	}
	return EmbedBackendCPU
}

// frameReason is one reason for texts of a frame running off the fastest
// runtime, as things stand when the frame is served: the Neural Engine's
// when that path is planned (the frame's last text that missed it), else
// the GPU's. Caller holds the slot.
func (w *workerEmbedder) frameReason(rt *workerRoutes) string {
	if rt == nil || rt.Llama == 0 {
		return ""
	}
	return w.missReason(rt.Miss)
}

// frameReasons counts the llama.cpp texts of a frame by their reason,
// read like frameReason's; "" counts those with none to give.
func (w *workerEmbedder) frameReasons(rt *workerRoutes) map[string]int {
	if rt == nil || rt.Llama == 0 {
		return nil
	}
	out := map[string]int{}
	rest := rt.Llama
	for miss, n := range rt.Misses {
		out[w.missReason(miss)] += n
		rest -= n
	}
	// A frame that names only its last miss: one text is known to be it.
	if len(rt.Misses) == 0 && rt.Miss != "" {
		out[w.missReason(rt.Miss)]++
		rest--
	}
	if rest > 0 {
		out[w.missReason("")] += rest
	}
	return out
}

// missReason is the reason for a text that ran on llama.cpp, given why
// the child's Neural Engine path missed it ("" = it did not say).
func (w *workerEmbedder) missReason(miss string) string {
	w.mu.Lock()
	planned, aneOff := w.spec.coremlDir != "", w.coremlDisabled
	gpuFailed, gpuDisabled := w.gpuDisabled, w.spec.gpuLayers == 0
	w.mu.Unlock()
	switch {
	case planned && aneOff:
		return EmbedReasonANEOff
	case planned && miss == EmbedReasonFunctionLoading && w.bulkWaited.Load():
		return EmbedReasonReadinessTimeout
	case planned && miss != "":
		return miss
	case gpuFailed:
		return EmbedReasonGPUFailed
	case gpuDisabled:
		return EmbedReasonGPUDisabled
	}
	return ""
}

func (w *workerEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	// The child prefixes: its Local carries the resolved queryPrefix.
	vecs, err := w.embed(ctx, workerRoleQuery, nil, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// Dim answers without a child for a pinned model at its default path
// (same shortcut the in-process embedder takes); any other file is only
// known once loaded, so it comes from the child's handshake.
func (w *workerEmbedder) Dim(ctx context.Context) (int, error) {
	spec := w.childSpec()
	if spec.modelDim > 0 {
		return min(orDefault(spec.outDim, spec.modelDim), spec.modelDim), nil
	}
	r := w.run(ctx, workerReq{Op: workerOpDim})
	return r.dim, r.err
}

// SetThreads changes the CPU budget the child decodes with. An idle
// child is retired here so the next request comes up with the new
// count; a busy one is undisturbed and retired when its frame ends
// (ensureChild compares the running spec). 0 restores the default of
// NumCPU()-1.
func (w *workerEmbedder) SetThreads(n int) {
	if n < 0 {
		n = 0
	}
	w.mu.Lock()
	if n == w.spec.threads {
		w.mu.Unlock()
		return
	}
	w.spec.threads = n
	w.mu.Unlock()
	w.lg.Info("local embedder thread count changed", zap.Int("threads", n))
	if w.slot.tryAcquire() {
		w.stopChild(true)
		w.slot.release(false)
	}
}

// Hardware reports what the last started child said it runs on (zero
// value before the first spawn). The server logs it; a future stats
// collector reads it.
func (w *workerEmbedder) Hardware() Hardware {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hw
}

// Close retires the child and stops any background model download. An
// idle child exits gracefully on stdin EOF; a busy one is KILLED before
// the slot is taken, because a request in flight holds it for as long
// as requestTimeout (a wedged GPU does exactly that) and shutdown must
// not wait that out. The interrupted frame fails and its docs stay
// pending, which is the normal outage path.
func (w *workerEmbedder) Close() error {
	w.closing.Store(true)
	w.closeOnce.Do(func() {
		if !w.slot.tryAcquire() {
			if p := w.live.Load(); p != nil {
				_ = p.Kill()
			}
			_ = w.slot.acquire(context.Background(), false)
		}
		w.stopChild(true)
		w.idleTimer.Stop()
		w.slot.release(false)
		// The package first: it starts when the model download ends.
		if w.pkgDl != nil {
			w.pkgDl.Close()
		}
		if w.dl != nil {
			w.dl.Close()
		}
	})
	return nil
}

// childSpec is what a child spawned now would run with: the configured
// spec, less the Neural Engine path and GPU offload once each has been
// demoted.
func (w *workerEmbedder) childSpec() localSpec {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.spec
	if w.gpuDisabled {
		s.gpuLayers = 0
	}
	if w.coremlDisabled {
		s.coremlDir = ""
	}
	return s
}

// embed runs one frame of texts in the given role.
func (w *workerEmbedder) embed(ctx context.Context, role string, titles, texts []string) ([][]float32, error) {
	r := w.run(ctx, workerReq{Op: workerOpEmbed, Role: role, Texts: texts, Titles: titles})
	return r.vecs, r.err
}

// frameResult is one frame's outcome: vectors for an embed, the
// handshake dimension for a dim request.
type frameResult struct {
	vecs [][]float32
	dim  int
	err  error
	// routes is what the child said about a document frame; llama, the
	// runtime llama.cpp embeds on in that child (EmbedBackend*); reason,
	// why texts of the frame ran off the fastest runtime (EmbedReason*),
	// and reasons, how many for each.
	routes  *workerRoutes
	llama   string
	reason  string
	reasons map[string]int
}

// run performs one frame on the child, taking the slot for the
// request's class (queries jump the queue). It abandons the wait, not
// the work: a caller whose ctx ends leaves at once — from the slot
// queue, or mid-frame — while the frame completes on its own goroutine
// and releases the slot when it is done. Once a request is on the wire
// its answer must be read for the stream to stay usable, so the child
// is neither interrupted nor retired for a caller that went away; an
// abandoned spawn likewise finishes and leaves a ready child for the
// next caller. The frame stays bounded by reqTimeout and by Close.
func (w *workerEmbedder) run(ctx context.Context, req workerReq) frameResult {
	if w.closing.Load() {
		return frameResult{err: errWorkerClosed}
	}
	urgent := req.Role == workerRoleQuery
	if err := w.slot.acquire(ctx, urgent); err != nil {
		return frameResult{err: err}
	}
	res := make(chan frameResult, 1)
	go func() {
		r := w.serve(ctx, req)
		w.noteFrame(req, r)
		w.armIdle()
		w.slot.release(urgent) // before the send: a caller that returns finds the slot free
		res <- r
	}()
	select {
	case r := <-res:
		return r
	case <-ctx.Done():
		return frameResult{err: ctx.Err()}
	}
}

var errWorkerClosed = fmt.Errorf("%w: local embedder closed", ErrEmbedderUnavailable)

// serve runs one frame. Caller holds the slot. The caller's ctx is
// consulted only before the request is written — after that the
// round trip runs to its own timeout. A query never pays for a stale
// child's retirement and respawn; a doc frame does.
func (w *workerEmbedder) serve(ctx context.Context, req workerReq) frameResult {
	if err := w.ensureChild(req.Role != workerRoleQuery); err != nil {
		return frameResult{err: err}
	}
	if req.Op == workerOpDim {
		return frameResult{dim: w.child.dim}
	}
	if err := ctx.Err(); err != nil {
		return frameResult{err: err} // caller gone before the write: nothing owed
	}
	hdr, bin, err := w.roundTrip(req)
	if err != nil {
		w.failChild(err)
		return frameResult{err: fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)}
	}
	if hdr.Op == workerOpError {
		// The child reported a failure and is still healthy: keep it.
		return frameResult{err: fmt.Errorf("indexer: local embedder: %s", hdr.Error)}
	}
	vecs, err := decodeVectors(bin, hdr.N, hdr.Dim)
	if err == nil {
		// Only a bulk frame may come back short of vectors.
		var deferred []int
		if hdr.Routes != nil && req.Bulk {
			deferred = hdr.Routes.Deferred
		}
		vecs, err = unpackVectors(vecs, deferred, len(req.Texts))
	}
	if err != nil {
		w.failChild(err)
		return frameResult{err: fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)}
	}
	w.backoff = 0
	return frameResult{vecs: vecs, routes: hdr.Routes, llama: w.child.llama,
		reason: w.frameReason(hdr.Routes), reasons: w.frameReasons(hdr.Routes)}
}

// roundTrip writes one request and waits for its response. Every
// non-nil error leaves the stream unusable — a dead child, a timeout
// (the wedged-GPU case) or a desynchronized frame — so the caller
// retires the child rather than reusing it. Caller holds the slot.
func (w *workerEmbedder) roundTrip(req workerReq) (workerResp, []byte, error) {
	return w.roundTripWithin(req, w.reqTimeout)
}

// errChildMute is a child that did not answer in time.
var errChildMute = errors.New("embedder child did not answer")

func (w *workerEmbedder) roundTripWithin(req workerReq, within time.Duration) (workerResp, []byte, error) {
	c := w.child
	if err := writeFrame(c.w, req, nil); err != nil {
		return workerResp{}, nil, fmt.Errorf("write to embedder child: %w", err)
	}
	timer := time.NewTimer(within)
	defer timer.Stop()
	select {
	case m, ok := <-c.resp:
		if !ok {
			return workerResp{}, nil, errors.New("embedder child exited")
		}
		if m.err != nil {
			return workerResp{}, nil, m.err
		}
		return m.hdr, m.bin, nil
	case <-timer.C:
		return workerResp{}, nil, fmt.Errorf("%w in %s", errChildMute, within)
	}
}

// ensureChild spawns the child if needed, honoring the restart backoff.
// With retireStale, a running child whose spec has moved on (threads
// changed, GPU demoted while it was busy) is retired first — the
// respawn is a doc frame's cost, not a query's. Caller holds the slot.
func (w *workerEmbedder) ensureChild(retireStale bool) error {
	if w.closing.Load() {
		return errWorkerClosed
	}
	spec := w.childSpec()
	if w.child != nil {
		if !retireStale || w.child.spec == spec {
			return nil
		}
		w.stopChild(true)
	}
	// An installed file is ready even while the downloader's status
	// still says otherwise (it polls for another process's install).
	if _, err := os.Stat(spec.modelPath); err != nil {
		if w.dl != nil {
			if st := w.dl.Status(); st != nil {
				return fmt.Errorf("indexer: local embedder: model not ready: %w", st)
			}
		}
		return fmt.Errorf("indexer: local embedder: model not found at %s", spec.modelPath)
	}
	if now := w.now(); now.Before(w.nextTry) {
		return fmt.Errorf("%w: embedder child restarting in %s", ErrEmbedderUnavailable, w.nextTry.Sub(now).Round(time.Second))
	}
	c, err := w.startChild(spec)
	if err != nil {
		if !w.closing.Load() {
			w.penalize()
		}
		return fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
	}
	w.child = c
	return nil
}

// startChild spawns the child and waits for its `ready` handshake.
func (w *workerEmbedder) startChild(spec localSpec) (*embedChild, error) {
	cmd, err := w.newCmd(spec)
	if err != nil {
		return nil, err
	}
	// Pipes created before a Start that never happens are never closed
	// by exec.Cmd, so close them on every early return.
	var pipes []io.Closer
	closePipes := func() {
		for _, p := range pipes {
			_ = p.Close()
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stdin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		closePipes()
		return nil, err
	}
	pipes = append(pipes, stdout)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		closePipes()
		return nil, err
	}
	pipes = append(pipes, stderr)
	if err := cmd.Start(); err != nil {
		closePipes()
		return nil, fmt.Errorf("start embedder child: %w", err)
	}
	// Visible to Close from here on: the handshake below can take
	// minutes on a cold model, and shutdown must be able to kill it. A
	// Close that looked before the store saw nothing to kill, so look
	// back at it here.
	w.live.Store(cmd.Process)
	if w.closing.Load() {
		_ = cmd.Process.Kill()
	}
	c := &embedChild{
		cmd:     cmd,
		stdin:   stdin,
		w:       bufio.NewWriter(stdin),
		resp:    make(chan workerFrame, 1),
		stderr:  &lineTail{max: workerStderrTail},
		errDone: make(chan struct{}),
		settled: make(chan struct{}),
		spec:    spec,
	}
	c.first = firstFunction(spec)
	c.onSettle = func() { w.settledChild.Store(c) }
	c.onStatus = func(exited, wakes bool) {
		w.logBulk(exited)
		if wakes {
			w.wakeDeferred()
		}
	}
	go c.readLoop(bufio.NewReaderSize(stdout, 64<<10))
	go c.pumpStderr(stderr, w.lg)

	timer := time.NewTimer(w.startTimeout)
	defer timer.Stop()
	select {
	case m, ok := <-c.resp:
		if !ok || m.err != nil || m.hdr.Op != workerOpReady {
			// Never attached: whatever it reported, it answers for no one.
			c.detached.Store(true)
			// stop() joins the stderr pump, so the tail is complete.
			waitErr := c.stop(false)
			w.live.Store(nil)
			return nil, fmt.Errorf("embedder child failed to start: %s", firstNonEmpty(
				errText(m.err), handshakeText(ok, m.hdr), c.stderr.String(), errText(waitErr), "no output"))
		}
		c.dim = m.hdr.Dim
		c.llama = llamaBackend(spec, m.hdr.Hardware)
		if m.hdr.Hardware != nil {
			w.mu.Lock()
			w.hw = *m.hdr.Hardware
			w.mu.Unlock()
		}
	case <-timer.C:
		c.detached.Store(true)
		waitErr := c.stop(false)
		w.live.Store(nil)
		return nil, fmt.Errorf("embedder child did not become ready in %s: %s", w.startTimeout,
			firstNonEmpty(c.stderr.String(), errText(waitErr), "no output"))
	}
	hw := w.Hardware()
	w.lg.Info("local embedder child started",
		zap.Int("dim", c.dim), zap.Int("gpuLayers", spec.gpuLayers), zap.Int("threads", hw.Threads),
		zap.String("hardware", hw.String()))
	w.lg.Debug("local embedder system info", zap.String("systemInfo", hw.SystemInfo))
	return c, nil
}

// How a child the worker lets go of as failed was found.
const (
	diedInFrame = iota // its stream broke under a request
	diedIdle           // the idle ask found it dead
	muteIdle           // it did not answer the idle ask in time
)

// failChild retires a child whose stream broke under a request and
// classifies the death: any abnormal exit with GPU offload active demotes
// this process to CPU. The check is deliberately coarse. A wedged GPU
// stops answering rather than dying, so a request timeout has to count as
// a fault — at the price of demoting a merely slow frame. That costs one
// run at CPU speed (~4x slower embedding, FTS untouched) against
// repeated multi-minute stalls, and a restart starts on the GPU again.
// On a GPU-less machine the demotion is a no-op. A child that had begun
// using Core ML loses the Neural Engine path first and keeps the GPU. A
// stream that broke because Close killed the child is not a fault:
// retired silently.
// Caller holds the slot.
func (w *workerEmbedder) failChild(cause error) { w.reapChild(cause, diedInFrame) }

// reapChild is failChild for a child found in the state how names. One
// found dead or mute while idle had nothing asked of it: it costs neither
// runtime, unless it died inside a Core ML call of its own (aneCalls),
// which costs the Neural Engine path. Caller holds the slot.
func (w *workerEmbedder) reapChild(cause error, how int) {
	if w.closing.Load() {
		w.stopChild(false)
		return
	}
	c := w.child
	if c == nil {
		w.penalize()
		return
	}
	w.child = nil
	c.detached.Store(true)
	w.live.Store(nil)
	w.logBulk(true)
	w.wakeDeferred()
	gpu := c.spec.gpuLayers != 0
	// stop() joins the stderr pump before returning, so the tail read
	// after it is complete — that is where llama.cpp's abort message is.
	waitErr := c.stop(false)
	fields := []zap.Field{zap.Error(cause), zap.String("childStderr", c.stderr.String())}
	if waitErr != nil {
		fields = append(fields, zap.String("childExit", waitErr.Error()))
	}
	// Read after stop, which joined the stderr pump.
	inCall := c.calls.Load() > 0
	switch {
	case how == muteIdle:
		w.lg.Warn("local embedder child did not answer while idle; stopped", fields...)
		return
	case how == diedIdle && !inCall:
		w.lg.Warn("local embedder child found dead while idle", fields...)
		return
	}
	w.penalize()
	w.mu.Lock()
	// One runtime per death, the Neural Engine path first once the
	// child used Core ML: the death does not say which of the two
	// caused it. A death that repeats in a child started without Core ML
	// clears the Neural Engine, which comes back on, once.
	// A Core ML call that began and did not end is Core ML work too, even
	// with the path off.
	usedCoreML := c.coreml.Load() || inCall
	demoteCoreML := c.spec.coremlDir != "" && usedCoreML && !w.coremlDisabled
	demote := !demoteCoreML && gpu && !w.gpuDisabled
	restoreCoreML := c.spec.coremlDir == "" && w.coremlDisabled && !w.coremlRestored
	if demoteCoreML {
		w.coremlDisabled = true
	}
	if demote {
		w.gpuDisabled = true
	}
	if restoreCoreML {
		w.coremlDisabled = false
		w.coremlRestored = true
	}
	w.mu.Unlock()
	switch {
	case demoteCoreML:
		w.lg.Warn("local embedder child died with the neural engine on; the next child runs llama.cpp alone", fields...)
	case demote && restoreCoreML:
		w.lg.Warn("local embedder child died again, with llama.cpp GPU offload active; llama.cpp stays on CPU for the rest of this run and the neural engine is back on", fields...)
	case demote:
		w.lg.Warn("local embedder child died with llama.cpp GPU offload active; llama.cpp stays on CPU for the rest of this run", fields...)
	case restoreCoreML:
		w.lg.Warn("local embedder child died again without the neural engine; the neural engine is back on", fields...)
	default:
		w.lg.Warn("local embedder child died; restarting on the next round", fields...)
	}
}

var _ BulkPreparer = (*workerEmbedder)(nil)

// PrepareBulk starts the child and waits, with the slot free, until its
// Neural Engine path has settled on its first function, and says how
// that went (Bulk*); docs/13-index.md § Neural Engine has the rules.
func (w *workerEmbedder) PrepareBulk(ctx context.Context, waiting func()) string {
	spec := w.childSpec()
	switch {
	case spec.coremlDir == "" && w.noPackage:
		return BulkNoPackage
	case spec.coremlDir == "" || w.closing.Load():
		return BulkNoWait
	case w.bulkWaited.Load():
		return BulkTimeout
	}
	// Only a child the worker still holds answers: one being retired
	// stays live until it is reaped, and its replacement settles on its
	// own.
	if c := w.settledChild.Load(); c != nil && !c.detached.Load() && c.spec == spec {
		return c.settledAs
	}
	said := false
	say := func() {
		if !said && waiting != nil {
			said = true
			waiting()
		}
	}
	if how := w.awaitPackage(ctx, say); how != "" {
		return how
	}
	// From here on the call waits: for a child to start, or for the
	// running one's path to settle.
	say()
	// Before the spawn: its idle timer must not fire under the wait.
	w.preparing.Add(1)
	defer w.preparing.Add(-1)
	if r := w.run(ctx, workerReq{Op: workerOpDim}); r.err != nil {
		if ctx.Err() != nil {
			return BulkCancelled
		}
		return BulkNoChild
	}
	if err := w.slot.acquire(ctx, false); err != nil {
		return BulkCancelled
	}
	c := w.child
	// Under the slot, on the child that was selected: the Neural Engine
	// can have been demoted and the wait given up since the checks
	// above, and a child without Core ML never reports on it.
	if c == nil || c.spec.coremlDir == "" || w.bulkWaited.Load() {
		w.slot.release(false)
		switch {
		case c == nil:
			return BulkNoChild
		case c.spec.coremlDir == "":
			return BulkNoWait
		}
		return BulkTimeout
	}
	if c.waitUntil.IsZero() {
		c.waitUntil = time.Now().Add(w.bulkWait)
	}
	timer := time.NewTimer(time.Until(c.waitUntil))
	defer timer.Stop()
	w.slot.release(false)

	how := ""
	select {
	case <-c.settled:
		how = c.settledAs
	default:
		select {
		case <-c.settled:
			how = c.settledAs
		case <-w.gaveUp: // another caller's deadline
			how = BulkTimeout
		case <-c.errDone: // the child exited
			how = BulkNoChild
		case <-ctx.Done():
			how = BulkCancelled
		case <-timer.C:
			w.giveUpBulk()
			how = BulkTimeout
		}
	}
	// The idle countdown restarts here, not at the spawn. A busy slot
	// means a frame is in flight, and its end restarts it.
	if w.slot.tryAcquire() {
		w.armIdle()
		w.slot.release(false)
	}
	return how
}

// awaitPackage waits for the Core ML package while its download runs,
// queued behind the model's or transferring. "" = it is on disk; else
// why bulk work goes on without it (Bulk*). waiting runs if it waits.
func (w *workerEmbedder) awaitPackage(ctx context.Context, waiting func()) string {
	if w.pkgOnDisk == nil || w.pkgOnDisk() {
		return ""
	}
	if w.pkgDl == nil {
		return BulkNoPackage
	}
	// One bound for every waiter, from the first: past it nobody waits,
	// whatever the wall clock does (the deadline keeps its monotonic time).
	w.pkgWaitFrom.Do(func() { w.pkgWaitUntil = time.Now().Add(w.pkgWait) })
	left := time.Until(w.pkgWaitUntil)
	failed := func() string {
		if w.pkgOnDisk() {
			return ""
		}
		return BulkDownloadFailed
	}
	select {
	case <-w.pkgDl.done:
		return failed()
	default:
	}
	if left <= 0 {
		return BulkDownloadTimeout
	}
	waiting()
	timer := time.NewTimer(left)
	defer timer.Stop()
	// The package is whole before its licence texts are, which is when
	// the download says it is done.
	poll := time.NewTicker(w.pkgPoll)
	defer poll.Stop()
	for {
		select {
		case <-w.pkgDl.done:
			return failed()
		case <-poll.C:
			if w.pkgOnDisk() {
				return ""
			}
		case <-ctx.Done():
			return BulkCancelled
		case <-timer.C:
			// It can have landed since the last look.
			if w.pkgOnDisk() {
				return ""
			}
			w.pkgWaitOver.Do(func() {
				w.lg.Warn("local embedder: the wait for the core ml package's download ran out; bulk embedding opens on llama.cpp",
					zap.Duration("waited", w.pkgWait))
			})
			return BulkDownloadTimeout
		}
	}
}

// giveUpBulk ends every wait for a first function, now and later.
func (w *workerEmbedder) giveUpBulk() {
	if w.bulkWaited.CompareAndSwap(false, true) {
		close(w.gaveUp)
		w.wakeDeferred()
		w.lg.Warn("local embedder: the wait for the neural engine's first function ran out; bulk embedding opens without it",
			zap.Duration("waited", w.bulkWait))
	}
}

// stopChild retires the current child gracefully (stdin EOF, then kill).
// live stays set until it is reaped, so a Close meanwhile can kill it.
// Caller holds the slot.
func (w *workerEmbedder) stopChild(graceful bool) {
	if w.child == nil {
		return
	}
	c := w.child
	w.child = nil
	c.detached.Store(true)
	_ = c.stop(graceful)
	w.live.Store(nil)
	w.logBulk(true)
	w.wakeDeferred()
}

// armIdle restarts the idle countdown after a frame. Caller holds the
// slot.
func (w *workerEmbedder) armIdle() {
	w.lastFrame = time.Now()
	if w.child != nil && w.idleTimeout > 0 && !w.closing.Load() {
		w.idleTimer.Reset(w.idleTimeout)
	}
}

// retireIdle stops a child that served no frame for idleTimeout, under
// the slot; a busy slot or a recent frame re-arms the timer instead
// (docs/13-index.md § The embedder child process).
func (w *workerEmbedder) retireIdle() {
	if w.closing.Load() || w.idleTimeout <= 0 {
		return
	}
	if !w.slot.tryAcquire() {
		w.idleTimer.Reset(w.idleTimeout)
		if w.closing.Load() { // Close may have stopped the timer before the Reset
			w.idleTimer.Stop()
		}
		return
	}
	defer w.slot.release(false)
	if w.child == nil || w.closing.Load() {
		return
	}
	// Not under a wait for the first function, nor, with a Core ML path,
	// under a drain, which may be waiting for a function.
	coreML := w.child.spec.coremlDir != ""
	if w.preparing.Load() > 0 || (coreML && w.holds.Load() > 0) {
		w.idleTimer.Reset(w.idleTimeout)
		return
	}
	if rest := w.idleTimeout - time.Since(w.lastFrame); rest > 0 {
		w.idleTimer.Reset(rest)
		return
	}
	// A child with a Core ML path is asked: it refuses under a placement
	// audit, whose verdict it would never write, and under a load. One
	// held past the bound of either is stopped: it gave the audit up, and
	// nothing was asked of it, so no runtime is charged. A child of
	// another build does not know the question and is stopped.
	if coreML {
		hdr, _, err := w.roundTripWithin(workerReq{Op: workerOpIdle}, w.askTimeout)
		switch {
		case errors.Is(err, errChildMute):
			w.reapChild(err, muteIdle)
			return
		case err != nil:
			w.reapChild(err, diedIdle)
			return
		case hdr.Op == workerOpIdle && hdr.Busy && hdr.Overdue:
			w.lg.Warn("local embedder child held by core ml work past its bound; stopped while idle")
			w.stopChild(false)
			return
		case hdr.Op == workerOpIdle && hdr.Busy:
			w.idleTimer.Reset(w.idleTimeout)
			return
		}
	}
	w.lg.Info("local embedder child retired while idle", zap.Duration("idle", w.idleTimeout))
	w.stopChild(true)
}

func (w *workerEmbedder) penalize() {
	w.backoff = min(max(w.backoff*2, workerMinBackoff), workerMaxBackoff)
	w.nextTry = w.now().Add(w.backoff)
}

// childSlot is a one-slot semaphore with two classes of waiters:
// queries go ahead of doc frames. A doc caller that wins the slot while
// a query is waiting hands it straight back — the release wakes the
// query parked ahead of it — and queues again behind the queries
// waiting at that moment. So a query waits for at most the decode in
// flight (doc callers hold the slot for one decode group at a time).
// The one exception keeps a saturating query stream from starving
// indexing: after queryBurst consecutive query turns a doc frame runs
// regardless, so docs always get a frame per burst. Acquisition is
// context-aware — a caller that gives up leaves the queue instead of
// parking until its turn.
type childSlot struct {
	slot   chan struct{} // cap 1
	urgent atomic.Int32  // queries waiting or holding
	streak atomic.Int32  // consecutive query turns; a doc turn resets it
}

// queryBurst is how many consecutive query turns a doc frame yields
// to before it runs anyway. Human query rates never reach it; only a
// tight query loop does, and it then still leaves docs one frame in
// every burst.
const queryBurst = 8

func newChildSlot() *childSlot {
	return &childSlot{slot: make(chan struct{}, 1)}
}

// acquire takes the slot for a query (urgent) or a doc frame. It
// returns ctx.Err() without the slot when the caller gives up first.
func (s *childSlot) acquire(ctx context.Context, urgent bool) error {
	if urgent {
		s.urgent.Add(1)
		select {
		case s.slot <- struct{}{}:
		case <-ctx.Done():
			s.urgent.Add(-1)
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil { // select may pick the slot on a ctx already done
			s.release(true)
			return err
		}
		s.streak.Add(1)
		return nil
	}
	for {
		select {
		case s.slot <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			s.release(false)
			return err
		}
		if s.urgent.Load() == 0 || s.streak.Load() >= queryBurst {
			s.streak.Store(0)
			return nil
		}
		s.release(false)
	}
}

// tryAcquire takes a free slot without waiting and without regard to
// class — for maintenance that must not block behind a request.
func (s *childSlot) tryAcquire() bool {
	select {
	case s.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *childSlot) release(urgent bool) {
	<-s.slot
	if urgent {
		s.urgent.Add(-1)
	}
}

// embedChild is one live child process plus the goroutines draining its
// pipes. The reader owns stdout and turns it into frames; stderr is
// logged and tailed so a death can be explained.
type embedChild struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	w       *bufio.Writer
	resp    chan workerFrame
	stderr  *lineTail
	errDone chan struct{}
	spec    localSpec // what it was spawned with; a drifted spec retires it
	dim     int
	llama   string // the runtime llama.cpp embeds on in it (EmbedBackend*)
	// coreml: by the child's last report on its Neural Engine path,
	// Core ML is in use in it.
	coreml atomic.Bool
	// settled is closed once the Neural Engine path has its first
	// function, failed to load one, or is off (aneSettles).
	settled    chan struct{}
	settleOnce sync.Once
	onSettle   func() // run once, before settled closes
	// settledAs is how the path settled (BulkReady, BulkFailed, BulkOff);
	// written before settled closes.
	settledAs string
	// onStatus runs on every report about the Neural Engine path, and
	// when the child's stderr ends (exited). wakes: texts left without a
	// vector may embed now (aneWakes).
	onStatus func(exited, wakes bool)
	// calls counts the Core ML calls the child reported begun and not
	// ended (aneCalls). Left set by a child that died in one: reapChild
	// reads it.
	calls atomic.Int32
	// first names the path's first function ("s128"), the one whose
	// report settles it; "" = any function's does.
	first string
	// aneOff: the child said its path is off. Stderr pump only.
	aneOff bool
	// detached: the worker has let go of this child (retired, failed,
	// never started). Set under the slot, read without it.
	detached atomic.Bool
	// waitUntil is when PrepareBulk stops waiting on this child; zero
	// until its first waiter. Slot-guarded.
	waitUntil time.Time
}

type workerFrame struct {
	hdr workerResp
	bin []byte
	err error
}

func (c *embedChild) readLoop(r *bufio.Reader) {
	defer close(c.resp)
	for {
		var hdr workerResp
		bin, err := readFrame(r, &hdr)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				c.resp <- workerFrame{err: err}
			}
			return
		}
		c.resp <- workerFrame{hdr: hdr, bin: bin}
	}
}

// pumpStderr drains the child's stderr for as long as it lives. It must
// never stop early: an unread pipe fills at 64 KiB and blocks the child
// mid-decode. Over-long lines are truncated, not fatal.
func (c *embedChild) pumpStderr(r io.Reader, lg logger.CtxLogger) {
	defer close(c.errDone)
	// A child that exits has nothing loading: no text waits for it.
	defer func() {
		if c.onStatus != nil {
			c.onStatus(true, true)
		}
	}()
	br := bufio.NewReaderSize(r, 8<<10)
	open := map[string]bool{}
	for {
		line, err := br.ReadString('\n')
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			if len(line) > 4<<10 {
				line = line[:4<<10] + "…"
			}
			c.stderr.add(line)
			if status, ok := strings.CutPrefix(line, aneLogPrefix); ok {
				// Off is final for a child: a late line does not undo it.
				if strings.HasPrefix(status, aneOffStatus) {
					c.aneOff = true
				}
				c.coreml.Store(!c.aneOff)
				aneCalls(open, status)
				c.calls.Store(int32(len(open)))
				if aneSettles(status, c.first) {
					c.settleOnce.Do(func() {
						c.settledAs = aneSettledAs(status)
						if c.onSettle != nil {
							c.onSettle()
						}
						close(c.settled)
					})
				}
				lg.Info("local embedder neural engine", zap.String("status", status))
				if c.onStatus != nil {
					c.onStatus(false, aneWakes(status))
				}
			} else {
				lg.Debug("embedder child", zap.String("stderr", line))
			}
		}
		if err != nil {
			return
		}
	}
}

// stop terminates the child and reaps it. Graceful closes stdin (the
// child's read hits EOF and it exits 0) and kills only if that is
// ignored. Pipe readers are drained first: Wait closes the pipes, so
// calling it under an active read is a data race.
func (c *embedChild) stop(graceful bool) error {
	if graceful {
		_ = c.stdin.Close()
	} else if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if !c.drain(workerStopGrace) {
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		c.drain(0)
	}
	<-c.errDone
	_ = c.stdin.Close()
	return c.cmd.Wait()
}

// drain consumes frames until the reader goroutine exits. Reports false
// when timeout elapses first (0 = wait indefinitely).
func (c *embedChild) drain(timeout time.Duration) bool {
	var tc <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		tc = t.C
	}
	for {
		select {
		case _, ok := <-c.resp:
			if !ok {
				return true
			}
		case <-tc:
			return false
		}
	}
}

// lineTail keeps the last N lines of the child's stderr — written by
// the stderr pump, read when the child dies. A crash report runs to
// hundreds of lines and ends in registers, so the tail stops at the
// report's head: what killed the child is printed there and just above.
type lineTail struct {
	mu    sync.Mutex
	max   int
	lines []string
	crash int // crash report lines kept; 0 = no report yet
}

func (t *lineTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.crash >= workerCrashHead:
		return
	case t.crash > 0 || crashStart(line):
		t.crash++
	case len(t.lines) >= t.max:
		t.lines = t.lines[len(t.lines)-t.max+1:]
	}
	t.lines = append(t.lines, line)
}

// crashPrefixes open a crash report whatever follows them.
var crashPrefixes = []string{
	"fatal error: ", "panic: ", // the Go runtime
	"Exception 0x", // the Go runtime's dump on Windows
	"*** Terminating app due to uncaught exception", // Objective-C; its call stack follows
}

// crashStart reports whether line opens a crash report: a ggml abort
// ("ggml-metal-device.m:868: …", a native backtrace follows), an
// uncaught Objective-C exception, or a Go runtime dump ("SIGABRT: abort",
// "Exception 0xc0000005 …", "fatal error: …", "panic: …").
func crashStart(line string) bool {
	for _, p := range crashPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	head, _, ok := strings.Cut(line, ": ")
	return ok && (signalName(head) || sourceLine(head))
}

// signalName: "SIGABRT", "SIGSEGV", ….
func signalName(s string) bool {
	if len(s) < 4 || !strings.HasPrefix(s, "SIG") {
		return false
	}
	for _, r := range s[3:] {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// sourceLine: "<file>:<line>" of a C-family source file, the prefix of
// a ggml abort.
func sourceLine(s string) bool {
	i := strings.LastIndexByte(s, ':')
	if i < 0 || i == len(s)-1 {
		return false
	}
	for _, r := range s[i+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	dot := strings.LastIndexByte(s[:i], '.')
	if dot < 0 {
		return false
	}
	switch s[dot:i] {
	case ".c", ".cc", ".cpp", ".h", ".hpp", ".m", ".mm", ".cu", ".cuh":
		return true
	}
	return false
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var b bytes.Buffer
	for i, l := range t.lines {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(l)
	}
	return b.String()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func handshakeText(ok bool, hdr workerResp) string {
	switch {
	case !ok:
		return ""
	case hdr.Op == workerOpError:
		return hdr.Error
	case hdr.Op != workerOpReady:
		return fmt.Sprintf("unexpected %q frame before ready", hdr.Op)
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
