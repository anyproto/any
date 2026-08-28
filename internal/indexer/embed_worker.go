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
	"sync"
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
// std::terminate aborts the process; GGML_ASSERT calls abort() outright.
// In-process that killed the server. Here it kills the child: the round
// fails, its docs stay `pending`, and the next tick retries — the outage
// semantics the pipeline already has for an unreachable embedder.
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

	mu sync.Mutex
	// spec holds the parameters every spawn is rendered from. Two
	// writers: the GPU demotion below, and SetThreads.
	spec  localSpec
	child *embedChild
	// gpuDisabled is set when a child dies abnormally while GPU offload
	// was active. In-memory by design: every `any run` starts on the GPU
	// again, so a driver or hardware fix recovers on its own.
	gpuDisabled bool
	backoff     time.Duration
	nextTry     time.Time
	closed      bool
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

// Close retires the child and stops any background model download.
func (w *workerEmbedder) Close() error {
	w.mu.Lock()
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
	hdr, bin, err := w.roundTrip(workerReq{Op: workerOpEmbed, Role: role, Texts: texts})
	if err != nil {
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
// non-nil error means the stream is unusable — a dead child, a timeout
// (the wedged-GPU case), or a desynchronized frame — so the caller
// respawns. Caller holds mu.
func (w *workerEmbedder) roundTrip(req workerReq) (workerResp, []byte, error) {
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
	}
}

// ensureChild spawns the child if needed, honoring the restart backoff.
// Caller holds mu.
func (w *workerEmbedder) ensureChild() error {
	if w.closed {
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
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start embedder child: %w", err)
	}
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
			return nil, fmt.Errorf("embedder child failed to start: %s", firstNonEmpty(
				errText(m.err), handshakeText(ok, m.hdr), c.stderr.String(), errText(waitErr), "no output"))
		}
		c.dim = m.hdr.Dim
	case <-timer.C:
		waitErr := c.stop(false)
		return nil, fmt.Errorf("embedder child did not become ready in %s: %s", w.startTimeout,
			firstNonEmpty(c.stderr.String(), errText(waitErr), "no output"))
	}
	w.lg.Info("local embedder child started",
		zap.Int("dim", c.dim), zap.Int("gpuLayers", spec.gpuLayers), zap.Int("threads", spec.threads))
	return c, nil
}

// failChild retires a child whose stream broke and classifies the
// death: any abnormal exit with GPU offload active demotes this process
// to CPU. The check is deliberately coarse — on a GPU-less machine the
// demotion is a no-op, and every restart starts on the GPU again.
// Caller holds mu.
func (w *workerEmbedder) failChild(cause error) {
	c := w.child
	if c == nil {
		w.penalize()
		return
	}
	w.child = nil
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

func (c *embedChild) pumpStderr(r io.Reader, lg logger.CtxLogger) {
	defer close(c.errDone)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8<<10), 64<<10)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		c.stderr.add(line)
		lg.Debug("embedder child", zap.String("stderr", line))
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
