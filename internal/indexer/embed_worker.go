//go:build vector && !gomobile

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
	// workerDefaultRequestTimeout bounds one embed round-trip. Generous:
	// a 64-doc batch of long editor windows measures ~32 s on CPU. Its
	// job is to unwedge a hung GPU, which no longer returns at all.
	workerDefaultRequestTimeout = 3 * time.Minute
	// workerStopGrace is how long a child gets to exit on stdin EOF
	// before it is killed.
	workerStopGrace  = 5 * time.Second
	workerMinBackoff = time.Second
	workerMaxBackoff = time.Minute
	// workerStderrTail is how many of the child's last stderr lines are
	// quoted when it dies — enough for llama.cpp's abort message.
	workerStderrTail = 12
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
// One child, spawned lazily on the first request and shared by every
// space worker; requests serialize on mu, exactly as they did on the
// in-process model's mutex.
type workerEmbedder struct {
	dl           *modelDownload
	lg           logger.CtxLogger
	reqTimeout   time.Duration
	startTimeout time.Duration
	// newCmd builds the child command from a spec; injectable so tests
	// can drive a helper process instead of a real model.
	newCmd func(localSpec) (*exec.Cmd, error)
	now    func() time.Time

	// live is the running child's process and closing the shutdown
	// flag — both read without mu so Close can kill a child while a
	// request holds the mutex.
	live    atomic.Pointer[os.Process]
	closing atomic.Bool

	mu sync.Mutex
	// spec holds the parameters every spawn is rendered from. Two
	// writers: the GPU demotion below, and SetThreads.
	spec  localSpec
	child *embedChild
	// gpuDisabled is set when a child dies abnormally while GPU offload
	// was active. In-memory by design: every `any run` starts on the GPU
	// again, so a driver or hardware fix recovers on its own.
	gpuDisabled bool
	// hw is what the last started child reported about this machine —
	// kept for logging now, for hardware/error/speed statistics later.
	hw      Hardware
	backoff time.Duration
	nextTry time.Time
	closed  bool
}

func newWorkerEmbedder(cfg config.IndexLocal, modelsDir, legacyModelsDir string, onProcess func(ProcessUpdate)) (*workerEmbedder, error) {
	spec, dl, err := resolveLocalSpec(cfg, modelsDir, legacyModelsDir, onProcess)
	if err != nil {
		return nil, err
	}
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
	return &workerEmbedder{
		spec:         spec,
		dl:           dl,
		lg:           logger.NewNamed("indexer.embedder"),
		reqTimeout:   reqTimeout,
		startTimeout: workerStartTimeout,
		newCmd:       embedderCmd,
		now:          time.Now,
	}, nil
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
	if s.queryPrefix != "" {
		args = append(args, "--query-prefix", s.queryPrefix)
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
	cmd := exec.Command(exe, args...)
	applyLowPriority(cmd, s.niceness) // Windows sets it here; Unix nices in the child
	return cmd, nil
}

func (w *workerEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.embed(ctx, workerRoleDoc, texts)
}

func (w *workerEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The child prefixes: its Local carries the resolved queryPrefix.
	vecs, err := w.embed(ctx, workerRoleQuery, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// Dim answers without a child for the pinned default model (same
// shortcut the in-process embedder takes); a custom model is only known
// once loaded, so it comes from the child's handshake.
func (w *workerEmbedder) Dim(ctx context.Context) (int, error) {
	if w.spec.defaultModel {
		return min(orDefault(w.spec.outDim, localDefaultDim), localDefaultDim), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := w.ensureChild(); err != nil {
		return 0, err
	}
	return w.child.dim, nil
}

// SetThreads changes the CPU budget the child decodes with. It takes
// effect on the next spawn: an idle child is retired here, and a
// request in flight is undisturbed (it holds mu). 0 restores the
// default of NumCPU()-1.
func (w *workerEmbedder) SetThreads(n int) {
	if n < 0 {
		n = 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if n == w.spec.threads {
		return
	}
	w.spec.threads = n
	w.lg.Info("local embedder thread count changed", zap.Int("threads", n))
	if w.child != nil {
		w.stopChild(true)
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
// the mutex is taken, because a request in flight holds it for as long
// as requestTimeout (a wedged GPU does exactly that) and shutdown must
// not wait that out. The interrupted round fails and its docs stay
// pending, which is the normal outage path.
func (w *workerEmbedder) Close() error {
	w.closing.Store(true)
	if !w.mu.TryLock() {
		if p := w.live.Load(); p != nil {
			_ = p.Kill()
		}
		w.mu.Lock()
	}
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	w.stopChild(true)
	if w.dl != nil {
		w.dl.Close()
	}
	return nil
}

// embed runs one round-trip. Caller holds mu.
func (w *workerEmbedder) embed(ctx context.Context, role string, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := w.ensureChild(); err != nil {
		return nil, err
	}
	hdr, bin, err := w.roundTrip(ctx, workerReq{Op: workerOpEmbed, Role: role, Texts: texts})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The caller went away. The child owes us a response we will
			// never read, so it cannot be reused — but nothing is wrong
			// with it, so no backoff and no GPU demotion.
			w.abortChild()
			return nil, ctxErr
		}
		w.failChild(err)
		return nil, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
	}
	if hdr.Op == workerOpError {
		// The child reported a failure and is still healthy: keep it.
		return nil, fmt.Errorf("indexer: local embedder: %s", hdr.Error)
	}
	vecs, err := decodeVectors(bin, hdr.N, hdr.Dim)
	if err == nil && len(vecs) != len(texts) {
		err = fmt.Errorf("indexer: embed worker: got %d vectors for %d texts", len(vecs), len(texts))
	}
	if err != nil {
		w.failChild(err)
		return nil, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
	}
	w.backoff = 0
	return vecs, nil
}

// roundTrip writes one request and waits for its response. Every
// non-nil error leaves the stream unusable — a dead child, a timeout
// (the wedged-GPU case), a desynchronized frame, or an abandoned
// request whose answer nobody will read — so the caller retires the
// child rather than reusing it. Caller holds mu.
func (w *workerEmbedder) roundTrip(ctx context.Context, req workerReq) (workerResp, []byte, error) {
	c := w.child
	if err := writeFrame(c.w, req, nil); err != nil {
		return workerResp{}, nil, fmt.Errorf("write to embedder child: %w", err)
	}
	timer := time.NewTimer(w.reqTimeout)
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
		return workerResp{}, nil, fmt.Errorf("embedder child did not answer in %s", w.reqTimeout)
	case <-ctx.Done():
		return workerResp{}, nil, ctx.Err()
	}
}

// abortChild retires a child whose in-flight response is abandoned. Not
// a fault: no backoff, no GPU demotion, no warning. Caller holds mu.
func (w *workerEmbedder) abortChild() {
	if w.child == nil {
		return
	}
	w.lg.Debug("embedder child retired: request abandoned")
	w.stopChild(false)
}

// ensureChild spawns the child if needed, honoring the restart backoff.
// Caller holds mu.
func (w *workerEmbedder) ensureChild() error {
	if w.closed || w.closing.Load() {
		return errors.New("indexer: local embedder: closed")
	}
	if w.child != nil {
		return nil
	}
	if w.dl != nil {
		if st := w.dl.Status(); st != nil {
			return fmt.Errorf("indexer: local embedder: model not ready: %w", st)
		}
	}
	if _, err := os.Stat(w.spec.modelPath); err != nil {
		return fmt.Errorf("indexer: local embedder: model not found at %s", w.spec.modelPath)
	}
	if now := w.now(); now.Before(w.nextTry) {
		return fmt.Errorf("%w: embedder child restarting in %s", ErrEmbedderUnavailable, w.nextTry.Sub(now).Round(time.Second))
	}
	spec := w.spec
	if w.gpuDisabled {
		spec.gpuLayers = 0
	}
	c, err := w.startChild(spec)
	if err != nil {
		w.penalize()
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
	// minutes on a cold model, and shutdown must be able to kill it.
	w.live.Store(cmd.Process)
	c := &embedChild{
		cmd:     cmd,
		stdin:   stdin,
		w:       bufio.NewWriter(stdin),
		resp:    make(chan workerFrame, 1),
		stderr:  &lineTail{max: workerStderrTail},
		errDone: make(chan struct{}),
		gpu:     spec.gpuLayers != 0,
	}
	go c.readLoop(bufio.NewReaderSize(stdout, 64<<10))
	go c.pumpStderr(stderr, w.lg)

	timer := time.NewTimer(w.startTimeout)
	defer timer.Stop()
	select {
	case m, ok := <-c.resp:
		if !ok || m.err != nil || m.hdr.Op != workerOpReady {
			// stop() joins the stderr pump, so the tail is complete.
			waitErr := c.stop(false)
			w.live.Store(nil)
			return nil, fmt.Errorf("embedder child failed to start: %s", firstNonEmpty(
				errText(m.err), handshakeText(ok, m.hdr), c.stderr.String(), errText(waitErr), "no output"))
		}
		c.dim = m.hdr.Dim
		if m.hdr.Hardware != nil {
			w.hw = *m.hdr.Hardware
		}
	case <-timer.C:
		waitErr := c.stop(false)
		w.live.Store(nil)
		return nil, fmt.Errorf("embedder child did not become ready in %s: %s", w.startTimeout,
			firstNonEmpty(c.stderr.String(), errText(waitErr), "no output"))
	}
	w.lg.Info("local embedder child started",
		zap.Int("dim", c.dim), zap.Int("gpuLayers", spec.gpuLayers), zap.Int("threads", w.hw.Threads),
		zap.String("hardware", w.hw.String()))
	w.lg.Debug("local embedder system info", zap.String("systemInfo", w.hw.SystemInfo))
	return c, nil
}

// failChild retires a child whose stream broke and classifies the
// death: any abnormal exit with GPU offload active demotes this process
// to CPU. The check is deliberately coarse. A wedged GPU stops
// answering rather than dying, so a request timeout has to count as a
// fault — at the price of demoting a merely slow batch. That costs one
// run at CPU speed (~4x slower embedding, FTS untouched) against
// repeated multi-minute stalls, and a restart starts on the GPU again.
// On a GPU-less machine the demotion is a no-op. Caller holds mu.
func (w *workerEmbedder) failChild(cause error) {
	c := w.child
	if c == nil {
		w.penalize()
		return
	}
	w.child = nil
	w.live.Store(nil)
	gpu := c.gpu
	// stop() joins the stderr pump before returning, so the tail read
	// after it is complete — that is where llama.cpp's abort message is.
	waitErr := c.stop(false)
	w.penalize()
	fields := []zap.Field{zap.Error(cause), zap.String("childStderr", c.stderr.String())}
	if waitErr != nil {
		fields = append(fields, zap.String("childExit", waitErr.Error()))
	}
	if gpu && !w.gpuDisabled {
		w.gpuDisabled = true
		w.lg.Warn("local embedder child died with GPU offload active; embedding on CPU for the rest of this run", fields...)
		return
	}
	w.lg.Warn("local embedder child died; restarting on the next round", fields...)
}

// stopChild retires the current child gracefully (stdin EOF, then kill
// if it lingers). Caller holds mu.
func (w *workerEmbedder) stopChild(graceful bool) {
	if w.child == nil {
		return
	}
	c := w.child
	w.child = nil
	w.live.Store(nil)
	_ = c.stop(graceful)
}

func (w *workerEmbedder) penalize() {
	w.backoff = min(max(w.backoff*2, workerMinBackoff), workerMaxBackoff)
	w.nextTry = w.now().Add(w.backoff)
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
	gpu     bool
	dim     int
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
	br := bufio.NewReaderSize(r, 8<<10)
	for {
		line, err := br.ReadString('\n')
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			if len(line) > 4<<10 {
				line = line[:4<<10] + "…"
			}
			c.stderr.add(line)
			lg.Debug("embedder child", zap.String("stderr", line))
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
// the stderr pump, read when the child dies.
type lineTail struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func (t *lineTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
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
