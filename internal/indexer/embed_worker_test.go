//go:build llamacpp && !android && !ios

package indexer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/config"
)

// TestEmbedWorkerHelper is the child process for the tests below: they
// re-exec this test binary with ANY_EMBED_WORKER_HELPER set, and the
// helper speaks the frame protocol on stdout in place of a real model
// (mode "real" runs the actual serve loop against a real GGUF).
func TestEmbedWorkerHelper(t *testing.T) {
	mode := os.Getenv("ANY_EMBED_WORKER_HELPER")
	if mode == "" {
		return // an ordinary test run, not the helper
	}
	os.Exit(runEmbedHelper(mode))
}

const helperDim = 4

func runEmbedHelper(mode string) int {
	// "slow" answers each request, and "slowstart" the handshake, after
	// this delay; with ANY_EMBED_WORKER_CRASH_AFTER=n the child crashes on
	// request n+1 (a text-dependent llama.cpp abort mid-batch).
	delay, _ := time.ParseDuration(os.Getenv("ANY_EMBED_WORKER_DELAY"))
	crashAfter, _ := strconv.Atoi(os.Getenv("ANY_EMBED_WORKER_CRASH_AFTER"))
	switch mode {
	case "exit": // dies before the handshake, e.g. libs missing
		fmt.Fprintln(os.Stderr, "helper: cannot load model")
		return 3
	case "garbage": // a C library printing on stdout
		fmt.Fprint(os.Stdout, strings.Repeat("X", 32))
		exitWithParent()
	case "slowstart": // a cold model load
		time.Sleep(delay)
	case "real":
		// CPU by default so comparisons do not depend on a GPU;
		// ANY_EVAL_LOCAL_GPU_LAYERS=-1 takes llama.cpp's default (offload
		// all) and ANY_EVAL_LOCAL_THREADS sets the decode threads.
		gpu := 0
		if v, err := strconv.Atoi(os.Getenv("ANY_EVAL_LOCAL_GPU_LAYERS")); err == nil {
			gpu = v
		}
		threads, _ := strconv.Atoi(os.Getenv("ANY_EVAL_LOCAL_THREADS"))
		cfg := EmbedWorkerConfig{
			ModelPath: os.Getenv("ANY_EVAL_LOCAL_MODEL"),
			LibDir:    os.Getenv("ANY_EVAL_LOCAL_LIBDIR"),
			GpuLayers: gpu,
			Threads:   threads,
		}
		if err := RunEmbedWorker(context.Background(), cfg, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	in := bufio.NewReaderSize(os.Stdin, 1<<16)
	out := bufio.NewWriter(os.Stdout)
	hw := &Hardware{
		OS: "testos", Arch: "testarch", LibVersion: "btest",
		Backends: []string{"CPU from libggml-cpu-test.so"},
		Devices:  []string{"Test Device"},
	}
	if err := writeFrame(out, workerResp{Op: workerOpReady, Dim: helperDim, Hardware: hw}, nil); err != nil {
		return 1
	}
	served := 0
	for {
		var req workerReq
		if _, err := readFrame(in, &req); err != nil {
			return 0 // parent closed stdin: clean exit, like the real child
		}
		if crashAfter > 0 && served >= crashAfter {
			fmt.Fprintln(os.Stderr, "GGML_ASSERT(false) failed")
			return 134
		}
		served++
		switch mode {
		case "crash": // what a llama.cpp abort looks like from outside
			fmt.Fprintln(os.Stderr, "terminate called after throwing an instance of 'vk::DeviceLostError'")
			return 134
		case "hang": // a wedged GPU: never answers
			exitWithParent()
		case "errframe": // healthy child, failed call
			if err := writeFrame(out, workerResp{Op: workerOpError, Error: "decode returned 1"}, nil); err != nil {
				return 1
			}
			continue
		case "slow": // a long decode
			time.Sleep(delay)
		}
		// Deterministic vectors: v[j] = len(text)*10 + j, except the last
		// component, which carries the frame's text count so a test can
		// see how the parent split a batch.
		vecs := make([][]float32, len(req.Texts))
		for i, txt := range req.Texts {
			v := make([]float32, helperDim)
			for j := range v {
				v[j] = float32(len(txt)*10 + j)
			}
			v[helperDim-1] = float32(len(req.Texts))
			vecs[i] = v
		}
		bin, dim, err := encodeVectors(vecs)
		if err != nil {
			return 1
		}
		if err := writeFrame(out, workerResp{Op: workerOpVectors, N: len(vecs), Dim: dim}, bin); err != nil {
			return 1
		}
	}
}

// exitWithParent blocks forever but dies when stdin closes, so a test
// binary that panics does not leave hung helpers behind.
func exitWithParent() {
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// helperSpawner stands in for embedderCmd: it records the spec of every
// spawn (what the flags would be rendered from) and runs the helper in
// the mode for that spawn index, the last mode repeating.
type helperSpawner struct {
	modes []string
	env   []string

	mu    sync.Mutex
	specs []localSpec
	cmds  []*exec.Cmd
}

func (h *helperSpawner) cmd(s localSpec) (*exec.Cmd, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	mode := h.modes[min(len(h.specs), len(h.modes)-1)]
	h.specs = append(h.specs, s)
	c := exec.Command(os.Args[0], "-test.run=TestEmbedWorkerHelper")
	c.Env = append(append(os.Environ(), h.env...), "ANY_EMBED_WORKER_HELPER="+mode)
	h.cmds = append(h.cmds, c)
	return c, nil
}

// spawns is how many children were started so far.
func (h *helperSpawner) spawns() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.specs)
}

func (h *helperSpawner) spec(i int) localSpec {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.specs[i]
}

func (h *helperSpawner) cmdAt(i int) *exec.Cmd {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cmds[i]
}

func (h *helperSpawner) specsCopy() []localSpec {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]localSpec(nil), h.specs...)
}

// delayEnv sets the helper's per-request ("slow") / handshake
// ("slowstart") delay.
func delayEnv(d time.Duration) []string {
	return []string{"ANY_EMBED_WORKER_DELAY=" + d.String()}
}

func newTestWorker(t *testing.T, h *helperSpawner, gpuLayers int) *workerEmbedder {
	t.Helper()
	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(model, []byte("gguf"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := newWorkerEmbedderFromSpec(localSpec{modelPath: model, nCtx: 2048, batchDocs: 1, gpuLayers: gpuLayers}, 10*time.Second)
	w.lg = logger.NewNamed("indexer.test")
	w.startTimeout = 30 * time.Second
	w.newCmd = h.cmd
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// clearBackoff skips the restart wait a failure imposes, so a test can
// assert the respawn without sleeping.
func clearBackoff(w *workerEmbedder) {
	_ = w.slot.acquire(context.Background(), false)
	w.nextTry = time.Time{}
	w.slot.release(false)
}

func gpuDisabled(w *workerEmbedder) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.gpuDisabled
}

// nextTry / hasChild read slot-guarded state the way the worker does.
func nextTry(w *workerEmbedder) time.Time {
	_ = w.slot.acquire(context.Background(), false)
	defer w.slot.release(false)
	return w.nextTry
}

func hasChild(w *workerEmbedder) bool {
	_ = w.slot.acquire(context.Background(), false)
	defer w.slot.release(false)
	return w.child != nil
}

// waitReady blocks until the child has handshaken (Hardware is set from
// the ready frame), so a timing bound taken after it excludes the
// spawn.
func waitReady(t *testing.T, w *workerEmbedder) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for w.Hardware().OS == "" {
		if time.Now().After(deadline) {
			t.Fatal("child never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitLive blocks until the child process is up (the request reached
// the spawn), bounded.
func waitLive(t *testing.T, w *workerEmbedder) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for w.live.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("child never spawned")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWorkerEmbedder_RoundTrip(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, -1)
	vecs, err := w.EmbedDocs(context.Background(), []string{"alpha", "bb"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || len(vecs[0]) != helperDim {
		t.Fatalf("got %d vectors of dim %d", len(vecs), len(vecs[0]))
	}
	if vecs[0][0] != 50 || vecs[1][0] != 20 {
		t.Fatalf("unexpected vectors: %v", vecs)
	}
	// A second call reuses the same child.
	if _, err := w.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// A batch goes to the child one decode group per frame, so a waiting
// query can get in between groups.
func TestWorkerEmbedder_DocsFrames(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.batchDocs = 2
	w.mu.Unlock()
	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}
	vecs, err := w.EmbedDocs(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	wantFrame := []float32{2, 2, 2, 2, 1}
	for i, v := range vecs {
		if v[0] != float32(len(texts[i])*10) {
			t.Fatalf("vector %d out of order: %v", i, v)
		}
		if v[helperDim-1] != wantFrame[i] {
			t.Fatalf("text %d went in a frame of %v texts, want %v", i, v[helperDim-1], wantFrame[i])
		}
	}
	// batchDocs=1 (the default): one text per frame. The spec change
	// retires the child, so this is a fresh spawn.
	w.mu.Lock()
	w.spec.batchDocs = 1
	w.mu.Unlock()
	vecs, err = w.EmbedDocs(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vecs {
		if v[helperDim-1] != 1 {
			t.Fatalf("text %d went in a frame of %v texts, want 1", i, v[helperDim-1])
		}
	}
	if h.spawns() != 2 || h.spec(1).batchDocs != 1 {
		t.Fatalf("spawns %d, specs %v; want a respawn on the spec change", h.spawns(), h.specsCopy())
	}
}

// A child dying mid-batch returns the frames that landed with the
// error, so the caller keeps them and only the rest is redone.
func TestWorkerEmbedder_MidBatchCrashReturnsPrefix(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_CRASH_AFTER=2"}}
	w := newTestWorker(t, h, 0)
	vecs, err := w.EmbedDocs(context.Background(), []string{"a", "bb", "ccc", "dddd"})
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Fatalf("err = %v, want ErrEmbedderUnavailable", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 10 || vecs[1][0] != 20 {
		t.Fatalf("prefix = %v, want the two frames that landed", vecs)
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("respawn failed: %v", err)
	}
	if h.spawns() != 2 {
		t.Fatalf("spawned %d children, want 2", h.spawns())
	}
}

// The point of per-frame slots: a search does not wait for the batches
// in flight, only for the current decode — and the batches resume with
// their own frames' vectors, nothing misattributed.
func TestWorkerEmbedder_QueryJumpsDocQueue(t *testing.T) {
	const frame = 300 * time.Millisecond
	h := &helperSpawner{modes: []string{"slow"}, env: delayEnv(frame)}
	w := newTestWorker(t, h, 0)
	texts := make([]string, 6)
	for i := range texts {
		texts[i] = "doc"
	}
	type batch struct {
		vecs [][]float32
		err  error
	}
	docsDone := make(chan batch, 2)
	for i := 0; i < 2; i++ {
		go func() {
			vecs, err := w.EmbedDocs(context.Background(), texts)
			docsDone <- batch{vecs, err}
		}()
	}
	waitReady(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	qv, err := w.EmbedQuery(ctx, "q")
	if err != nil {
		t.Fatalf("query during the batches: %v", err)
	}
	// At most one frame ahead of it plus its own.
	if elapsed := time.Since(start); elapsed > 3*frame {
		t.Fatalf("query waited %s behind the batches", elapsed)
	}
	if qv[0] != 10 || qv[helperDim-1] != 1 {
		t.Fatalf("query got a doc frame's vector: %v", qv)
	}
	select {
	case b := <-docsDone:
		t.Fatalf("a batch (%d frames) finished before the query: %v", len(texts), b.err)
	default:
	}
	for i := 0; i < 2; i++ {
		b := <-docsDone
		if b.err != nil {
			t.Fatal(b.err)
		}
		if len(b.vecs) != len(texts) {
			t.Fatalf("batch got %d vectors, want %d", len(b.vecs), len(texts))
		}
		for j, v := range b.vecs {
			if v[0] != 30 || v[helperDim-1] != 1 {
				t.Fatalf("batch vector %d misattributed: %v", j, v)
			}
		}
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// The priority itself, at the slot: a query arriving behind an already
// queued doc frame is served first.
func TestChildSlot_QueryPreemptsQueuedDocs(t *testing.T) {
	s := newChildSlot()
	ctx := context.Background()
	if err := s.acquire(ctx, false); err != nil { // the holder
		t.Fatal(err)
	}
	order := make(chan string, 2)
	go func() {
		_ = s.acquire(ctx, false)
		order <- "doc"
		s.release(false)
	}()
	time.Sleep(50 * time.Millisecond) // let the doc waiter park
	go func() {
		_ = s.acquire(ctx, true)
		order <- "query"
		s.release(true)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for s.urgent.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // and the query
	s.release(false)
	if first := <-order; first != "query" {
		t.Fatalf("first served %q, want query", first)
	}
	if second := <-order; second != "doc" {
		t.Fatalf("second served %q, want doc", second)
	}
	if !s.tryAcquire() {
		t.Fatal("slot not free after both released")
	}
	s.release(false)
	if s.urgent.Load() != 0 {
		t.Fatalf("urgent = %d after the query released", s.urgent.Load())
	}
}

// Queries go first, but not forever: under a saturating query stream
// every doc caller still gets a frame through per round.
func TestWorkerEmbedder_DocsProgressUnderQueryStream(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	stop := make(chan struct{})
	var qwg sync.WaitGroup
	for i := 0; i < 4; i++ {
		qwg.Add(1)
		go func() {
			defer qwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = w.EmbedQuery(context.Background(), "q")
			}
		}()
	}
	texts := make([]string, 8)
	for i := range texts {
		texts[i] = "doc"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	vecs, err := w.EmbedDocs(ctx, texts)
	close(stop)
	qwg.Wait()
	if err != nil {
		t.Fatalf("doc batch starved by queries: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(texts))
	}
}

// A caller that gives up while queued leaves the queue at once — it
// does not park on the slot until the request in flight ends.
func TestWorkerEmbedder_SlotWaitHonorsCtx(t *testing.T) {
	h := &helperSpawner{modes: []string{"hang"}}
	w := newTestWorker(t, h, 0)
	w.reqTimeout = time.Hour
	go func() { _, _ = w.EmbedDocs(context.Background(), []string{"wedged"}) }()
	waitReady(t, w)
	time.Sleep(50 * time.Millisecond) // let the frame reach the child

	for _, call := range []struct {
		name string
		fn   func(context.Context) error
	}{
		{"docs", func(ctx context.Context) error { _, err := w.EmbedDocs(ctx, []string{"second"}); return err }},
		{"query", func(ctx context.Context) error { _, err := w.EmbedQuery(ctx, "q"); return err }},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		start := time.Now()
		err := call.fn(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: err = %v, want DeadlineExceeded", call.name, err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("%s: queued caller took %s to give up", call.name, elapsed)
		}
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
	if st := h.cmdAt(0).ProcessState; st != nil {
		t.Fatal("an abandoned wait must not kill the child")
	}
	if urgent := w.slot.urgent.Load(); urgent != 0 {
		t.Fatalf("urgent count %d after the query left, want 0", urgent)
	}
}

// The whole point of the child: a crash mid-decode is an ordinary
// embedder outage, and the run continues on CPU.
func TestWorkerEmbedder_CrashDemotesToCPU(t *testing.T) {
	h := &helperSpawner{modes: []string{"crash", "ok"}}
	w := newTestWorker(t, h, -1)
	_, err := w.EmbedDocs(context.Background(), []string{"boom"})
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Fatalf("err = %v, want ErrEmbedderUnavailable", err)
	}
	if !gpuDisabled(w) {
		t.Fatal("crash with GPU offload active did not demote to CPU")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatalf("respawn failed: %v", err)
	}
	if h.spawns() != 2 {
		t.Fatalf("spawned %d children, want 2", h.spawns())
	}
	if h.spec(0).gpuLayers != -1 {
		t.Fatalf("first spawn gpuLayers = %d, want -1 (llama default)", h.spec(0).gpuLayers)
	}
	if h.spec(1).gpuLayers != 0 {
		t.Fatalf("respawn gpuLayers = %d, want 0 (CPU only)", h.spec(1).gpuLayers)
	}
}

func TestWorkerEmbedder_CrashWithGPUOffKeepsSpec(t *testing.T) {
	h := &helperSpawner{modes: []string{"crash", "ok"}}
	w := newTestWorker(t, h, 0)
	if _, err := w.EmbedDocs(context.Background(), []string{"boom"}); err == nil {
		t.Fatal("want error")
	}
	if gpuDisabled(w) {
		t.Fatal("demoted a child that was already CPU-only")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatal(err)
	}
	if h.spec(1).gpuLayers != 0 {
		t.Fatalf("respawn gpuLayers = %d, want 0", h.spec(1).gpuLayers)
	}
}

// A hung GPU stops answering rather than failing: the request timeout is
// what keeps the embed loop from blocking forever.
func TestWorkerEmbedder_RequestTimeout(t *testing.T) {
	h := &helperSpawner{modes: []string{"hang", "ok"}}
	w := newTestWorker(t, h, -1)
	w.reqTimeout = 200 * time.Millisecond
	start := time.Now()
	_, err := w.EmbedDocs(context.Background(), []string{"wedged"})
	if !errors.Is(err, ErrEmbedderUnavailable) {
		t.Fatalf("err = %v, want ErrEmbedderUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
	if st := h.cmdAt(0).ProcessState; st == nil {
		t.Fatal("hung child was not reaped")
	}
	if !gpuDisabled(w) {
		t.Fatal("a killed child with GPU offload active should demote to CPU")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatalf("respawn failed: %v", err)
	}
}

// A caller that gives up mid-frame returns at once, and the child is
// kept: the frame it owes is read out by the detached round trip, and
// the next request reuses it. Not a fault — no backoff, no GPU
// demotion.
func TestWorkerEmbedder_AbandonedRequestKeepsChild(t *testing.T) {
	const frame = 500 * time.Millisecond
	h := &helperSpawner{modes: []string{"slow"}, env: delayEnv(frame)}
	w := newTestWorker(t, h, -1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := w.EmbedQuery(ctx, "slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > frame {
		t.Fatalf("abandoned caller waited %s for the frame", elapsed)
	}
	if gpuDisabled(w) {
		t.Error("an abandoned request must not demote the GPU")
	}
	if _, err := w.EmbedQuery(context.Background(), "again"); err != nil {
		t.Fatalf("next request failed: %v", err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1 (the child is kept)", h.spawns())
	}
	if st := h.cmdAt(0).ProcessState; st != nil {
		t.Fatal("an abandoned request must not kill the child")
	}
	if !nextTry(w).IsZero() {
		t.Error("an abandoned request must not impose a restart backoff")
	}
}

// A spawn abandoned by its caller completes anyway and serves the next
// caller — a budgeted query never restarts a cold load.
func TestWorkerEmbedder_AbandonedSpawnCompletes(t *testing.T) {
	h := &helperSpawner{modes: []string{"slowstart"}, env: delayEnv(time.Second)}
	w := newTestWorker(t, h, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := w.EmbedQuery(ctx, "first"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if _, err := w.EmbedQuery(ctx2, "second"); err != nil {
		t.Fatalf("second query after the abandoned spawn: %v", err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// A batch caller that goes away (space dropped, shutdown) returns at
// the next frame boundary; the child stays.
func TestWorkerEmbedder_DocsCancelBetweenFrames(t *testing.T) {
	const frame = 200 * time.Millisecond
	h := &helperSpawner{modes: []string{"slow"}, env: delayEnv(frame)}
	w := newTestWorker(t, h, 0)
	texts := make([]string, 10)
	for i := range texts {
		texts[i] = "doc"
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(frame + frame/2)
		cancel()
	}()
	start := time.Now()
	_, err := w.EmbedDocs(ctx, texts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Duration(len(texts))*frame/2 {
		t.Fatalf("cancelled batch ran on for %s", elapsed)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("next request failed: %v", err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

func TestWorkerEmbedder_GarbageOnStdout(t *testing.T) {
	h := &helperSpawner{modes: []string{"garbage", "ok"}}
	w := newTestWorker(t, h, 0)
	_, err := w.EmbedDocs(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "magic") {
		t.Fatalf("err = %v, want a frame magic error", err)
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("respawn failed: %v", err)
	}
}

// An error frame is a failed call, not a broken child: it must not cost
// a respawn.
func TestWorkerEmbedder_ErrorFrameKeepsChild(t *testing.T) {
	h := &helperSpawner{modes: []string{"errframe"}}
	w := newTestWorker(t, h, 0)
	_, err := w.EmbedDocs(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "decode returned 1") {
		t.Fatalf("err = %v, want the child's message", err)
	}
	if errors.Is(err, ErrEmbedderUnavailable) {
		t.Fatal("a per-call failure must not read as an embedder outage")
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err == nil {
		t.Fatal("want error")
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1 (child stays usable)", h.spawns())
	}
}

func TestWorkerEmbedder_SpawnBackoff(t *testing.T) {
	h := &helperSpawner{modes: []string{"exit"}}
	w := newTestWorker(t, h, 0)
	_, err := w.EmbedDocs(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "cannot load model") {
		t.Fatalf("err = %v, want the child's stderr", err)
	}
	_, err = w.EmbedDocs(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "restarting in") {
		t.Fatalf("err = %v, want the backoff message", err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children during the backoff, want 1", h.spawns())
	}
}

func TestWorkerEmbedder_SetThreadsRespawns(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	w.SetThreads(3)
	if hasChild(w) {
		t.Fatal("idle child was not retired")
	}
	if st := h.cmdAt(0).ProcessState; st == nil || !st.Success() {
		t.Fatalf("retired child exited %v, want a clean exit on stdin EOF", st)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if h.spawns() != 2 || h.spec(1).threads != 3 {
		t.Fatalf("spawns %d, threads %v; want a respawn with threads 3", h.spawns(), h.specsCopy())
	}
	// Setting the same value again is a no-op.
	w.SetThreads(3)
	if !hasChild(w) {
		t.Fatal("child retired for an unchanged thread count")
	}
}

// A thread change must not wait out a frame in flight: the busy child
// finishes its frame and is retired when the next request finds its
// spec stale.
func TestWorkerEmbedder_SetThreadsWhileBusy(t *testing.T) {
	const frame = 500 * time.Millisecond
	h := &helperSpawner{modes: []string{"slow"}, env: delayEnv(frame)}
	w := newTestWorker(t, h, 0)
	errCh := make(chan error, 1)
	go func() {
		_, err := w.EmbedDocs(context.Background(), []string{"a"})
		errCh <- err
	}()
	waitReady(t, w)
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	w.SetThreads(3)
	if elapsed := time.Since(start); elapsed > frame/2 {
		t.Fatalf("SetThreads waited %s for the frame in flight", elapsed)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("the frame in flight failed: %v", err)
	}
	if h.spawns() != 1 {
		t.Fatal("SetThreads disturbed a busy child")
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if h.spawns() != 2 || h.spec(1).threads != 3 {
		t.Fatalf("spawns %d, specs %v; want a respawn with threads 3", h.spawns(), h.specsCopy())
	}
	if st := h.cmdAt(0).ProcessState; st == nil || !st.Success() {
		t.Fatalf("stale child exited %v, want a clean exit on stdin EOF", st)
	}
}

func TestWorkerEmbedder_CloseReapsChild(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if st := h.cmdAt(0).ProcessState; st == nil || !st.Exited() {
		t.Fatalf("child not reaped on Close: %v", st)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err == nil {
		t.Fatal("want an error after Close")
	}
}

// The handshake carries what llama.cpp says the machine is; the server
// keeps it for logging and later statistics.
func TestWorkerEmbedder_HardwareFromHandshake(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	if hw := w.Hardware(); hw.OS != "" {
		t.Fatalf("hardware known before the first spawn: %+v", hw)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	hw := w.Hardware()
	if hw.OS != "testos" || hw.LibVersion != "btest" || len(hw.Backends) != 1 || len(hw.Devices) != 1 {
		t.Fatalf("hardware not captured: %+v", hw)
	}
	if s := hw.String(); !strings.Contains(s, "testos/testarch") || !strings.Contains(s, "llama.cpp btest") ||
		!strings.Contains(s, "libggml-cpu-test.so") || !strings.Contains(s, "Test Device") {
		t.Fatalf("hardware line %q", s)
	}
}

// Shutdown must not wait out a request in flight — a wedged GPU holds
// one for as long as the request timeout.
func TestWorkerEmbedder_CloseInterruptsRequest(t *testing.T) {
	h := &helperSpawner{modes: []string{"hang"}}
	w := newTestWorker(t, h, 0)
	w.reqTimeout = time.Hour // only Close can end this round
	errCh := make(chan error, 1)
	go func() {
		_, err := w.EmbedDocs(context.Background(), []string{"wedged"})
		errCh <- err
	}()
	// Let the request reach the child before closing.
	waitLive(t, w)
	start := time.Now()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("Close waited %s for the in-flight request", elapsed)
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("the interrupted round should fail so its docs stay pending")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the in-flight request never returned")
	}
	if st := h.cmdAt(0).ProcessState; st == nil {
		t.Fatal("child not reaped")
	}
}

// Same, with the caller already gone: the frame runs detached, and a
// child killed by shutdown is not a fault (no GPU demotion).
func TestWorkerEmbedder_CloseWithDetachedRoundTrip(t *testing.T) {
	h := &helperSpawner{modes: []string{"hang"}}
	w := newTestWorker(t, h, -1)
	w.reqTimeout = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := w.EmbedDocs(ctx, []string{"wedged"})
		errCh <- err
	}()
	waitReady(t, w)
	time.Sleep(50 * time.Millisecond) // the frame is on the wire
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if st := h.cmdAt(0).ProcessState; st != nil {
		t.Fatal("an abandoned frame must not reap the child")
	}
	start := time.Now()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("Close waited %s for the detached frame", elapsed)
	}
	if st := h.cmdAt(0).ProcessState; st == nil {
		t.Fatal("child not reaped")
	}
	if gpuDisabled(w) {
		t.Fatal("a child killed by Close must not demote the GPU")
	}
}

// Callers queued behind a request in flight are released by Close
// with an error — nobody parks through shutdown.
func TestWorkerEmbedder_CloseReleasesQueuedCallers(t *testing.T) {
	h := &helperSpawner{modes: []string{"hang"}}
	w := newTestWorker(t, h, 0)
	w.reqTimeout = time.Hour
	errs := make(chan error, 3)
	go func() {
		_, err := w.EmbedDocs(context.Background(), []string{"wedged"})
		errs <- err
	}()
	waitReady(t, w)
	time.Sleep(50 * time.Millisecond)
	go func() {
		_, err := w.EmbedDocs(context.Background(), []string{"queued"})
		errs <- err
	}()
	go func() {
		_, err := w.EmbedQuery(context.Background(), "queued")
		errs <- err
	}()
	time.Sleep(50 * time.Millisecond) // both parked on the slot
	start := time.Now()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("a caller succeeded through shutdown")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a queued caller never returned after Close")
		}
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Close took %s", elapsed)
	}
}

// Shutdown during a cold spawn kills the spawn instead of waiting out
// the model load.
func TestWorkerEmbedder_CloseDuringSpawn(t *testing.T) {
	h := &helperSpawner{modes: []string{"slowstart"}, env: delayEnv(5 * time.Second)}
	w := newTestWorker(t, h, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := w.EmbedDocs(ctx, []string{"x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	start := time.Now()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Close waited %s for the spawn", elapsed)
	}
	if st := h.cmdAt(0).ProcessState; st == nil {
		t.Fatal("spawning child not reaped")
	}
}

func TestWorkerEmbedder_DimFromHandshake(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	dim, err := w.Dim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dim != helperDim {
		t.Fatalf("dim = %d, want %d", dim, helperDim)
	}
	// The pinned default model answers without spawning at all.
	w2 := newTestWorker(t, &helperSpawner{modes: []string{"exit"}}, 0)
	w2.spec.defaultModel = true
	if dim, err := w2.Dim(context.Background()); err != nil || dim != localDefaultDim {
		t.Fatalf("default-model dim = %d, %v", dim, err)
	}
}

func TestEmbedderCmdArgs(t *testing.T) {
	cases := []struct {
		name string
		spec localSpec
		want []string
		skip []string
	}{{
		name: "defaults omit optional flags",
		spec: localSpec{modelPath: "/m.gguf", nCtx: 2048, batchDocs: 1, gpuLayers: -1},
		want: []string{"run", "embedder", "--model", "/m.gguf", "--ctx", "2048", "--batch-docs", "1"},
		skip: []string{"--gpu-layers", "--threads", "--dim", "--query-prefix", "--niceness"},
	}, {
		name: "cpu only",
		spec: localSpec{modelPath: "/m.gguf", nCtx: 512, batchDocs: 2, gpuLayers: 0, threads: 4, outDim: 256, queryPrefix: "Q:", niceness: 10},
		want: []string{"--gpu-layers", "0", "--threads", "4", "--dim", "256", "--query-prefix", "Q:", "--niceness", "10"},
	}}
	for _, c := range cases {
		cmd, err := embedderCmd(c.spec)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(cmd.Args[1:], " ")
		for _, w := range c.want {
			if !strings.Contains(args, w) {
				t.Errorf("%s: args %q missing %q", c.name, args, w)
			}
		}
		for _, s := range c.skip {
			if strings.Contains(args, s) {
				t.Errorf("%s: args %q should not carry %q", c.name, args, s)
			}
		}
	}
}

// Metal logs dozens of "<capability> = true" lines under the same
// prefix as its device line; only real enumeration lines are devices.
func TestDeviceDescription(t *testing.T) {
	cases := map[string]string{
		"ggml_vulkan: 0 = AMD Radeon (RADV) (radv) | uma: 1 | fp16: 1": "AMD Radeon (RADV) (radv) | uma: 1 | fp16: 1",
		"  Device 1: NVIDIA GeForce RTX 4090, compute capability 8.9":  "",
		"ggml_metal_init: picking default device: Apple M2 Pro":        "Apple M2 Pro",
		"ggml_metal_init: GPU name:   Apple M2 Pro":                    "Apple M2 Pro",
		"ggml_metal_init: simdgroup reduction   = true":                "",
		"ggml_metal_init: hasUnifiedMemory      = true":                "",
		"ggml_metal_init: recommendedMaxWorkingSetSize  = 21845.34 MB": "",
		"ggml_cuda_init: found 1 CUDA devices:":                        "",
	}
	for line, want := range cases {
		if got := deviceDescription(line); got != want {
			t.Errorf("deviceDescription(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestWorkerFrameRoundTrip(t *testing.T) {
	var buf strings.Builder
	w := bufio.NewWriter(&buf)
	vecs := [][]float32{{1, 2, 3}, {-0.5, 0, 4.25}}
	bin, dim, err := encodeVectors(vecs)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(w, workerResp{Op: workerOpVectors, N: len(vecs), Dim: dim}, bin); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(w, workerReq{Op: workerOpEmbed, Role: workerRoleQuery, Texts: []string{"hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(strings.NewReader(buf.String()))

	var got workerResp
	gotBin, err := readFrame(r, &got)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeVectors(gotBin, got.N, got.Dim)
	if err != nil {
		t.Fatal(err)
	}
	for i := range vecs {
		for j := range vecs[i] {
			if back[i][j] != vecs[i][j] {
				t.Fatalf("vector %d differs: %v vs %v", i, back[i], vecs[i])
			}
		}
	}
	var req workerReq
	if _, err := readFrame(r, &req); err != nil {
		t.Fatal(err)
	}
	if req.Role != workerRoleQuery || len(req.Texts) != 1 || req.Texts[0] != "hi" {
		t.Fatalf("request round-trip: %+v", req)
	}

	// An empty batch carries no binary block.
	if bin, dim, err := encodeVectors(nil); err != nil || bin != nil || dim != 0 {
		t.Fatalf("encodeVectors(nil) = %v, %d, %v", bin, dim, err)
	}
	if _, err := decodeVectors([]byte{1, 2}, 1, 4); err == nil {
		t.Fatal("want a length mismatch error")
	}
	if _, err := readFrame(bufio.NewReader(strings.NewReader("not a frame at all")), &got); err == nil {
		t.Fatal("want a magic error")
	}
}

// End-to-end through a real child running the real serve loop and model.
// Gated the same way as the other local-model tests:
//
//	ANY_EVAL_LOCAL_MODEL=~/.any/models/<model>.gguf \
//	ANY_EVAL_LOCAL_LIBDIR=./bin/llamacpp \
//	go test -tags llamacpp -run TestWorkerEmbedder_RealChild ./internal/indexer
func TestWorkerEmbedder_RealChild(t *testing.T) {
	modelPath := os.Getenv("ANY_EVAL_LOCAL_MODEL")
	libDir := os.Getenv("ANY_EVAL_LOCAL_LIBDIR")
	if modelPath == "" || libDir == "" {
		t.Skip("set ANY_EVAL_LOCAL_MODEL + ANY_EVAL_LOCAL_LIBDIR")
	}
	ctx := context.Background()
	texts := []string{"the quick brown fox", "distributed consensus needs a quorum"}

	cpu := 0
	local, err := NewLocal(config.IndexLocal{ModelPath: modelPath, LibDir: libDir, GpuLayers: &cpu}, t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	want, err := local.EmbedDocs(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}

	h := &helperSpawner{modes: []string{"real"}}
	w := newTestWorker(t, h, 0)
	w.spec.modelPath = modelPath
	w.spec.libDir = libDir
	w.startTimeout = 5 * time.Minute
	got, err := w.EmbedDocs(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		for j := range want[i] {
			if diff := want[i][j] - got[i][j]; diff > 1e-6 || diff < -1e-6 {
				t.Fatalf("text %d dim %d: worker %v, in-process %v", i, j, got[i][j], want[i][j])
			}
		}
	}
}
