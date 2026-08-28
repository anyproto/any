//go:build vector && !gomobile

package indexer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	switch mode {
	case "exit": // dies before the handshake, e.g. libs missing
		fmt.Fprintln(os.Stderr, "helper: cannot load model")
		return 3
	case "garbage": // a C library printing on stdout
		fmt.Fprint(os.Stdout, strings.Repeat("X", 32))
		select {}
	case "real":
		cfg := EmbedWorkerConfig{
			ModelPath: os.Getenv("ANY_EVAL_LOCAL_MODEL"),
			LibDir:    os.Getenv("ANY_EVAL_LOCAL_LIBDIR"),
			GpuLayers: 0, // CPU: the comparison must not depend on a GPU
		}
		if err := RunEmbedWorker(context.Background(), cfg, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}

	in := bufio.NewReaderSize(os.Stdin, 1<<16)
	out := bufio.NewWriter(os.Stdout)
	if err := writeFrame(out, workerResp{Op: workerOpReady, Dim: helperDim}, nil); err != nil {
		return 1
	}
	for {
		var req workerReq
		if _, err := readFrame(in, &req); err != nil {
			return 0 // parent closed stdin: clean exit, like the real child
		}
		switch mode {
		case "crash": // what a llama.cpp abort looks like from outside
			fmt.Fprintln(os.Stderr, "terminate called after throwing an instance of 'vk::DeviceLostError'")
			return 134
		case "hang": // a wedged GPU: never answers
			select {}
		case "errframe": // healthy child, failed call
			if err := writeFrame(out, workerResp{Op: workerOpError, Error: "decode returned 1"}, nil); err != nil {
				return 1
			}
			continue
		}
		vecs := make([][]float32, len(req.Texts))
		for i, txt := range req.Texts {
			v := make([]float32, helperDim)
			for j := range v {
				v[j] = float32(len(txt)*10 + j)
			}
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

// helperSpawner stands in for embedderCmd: it records the spec of every
// spawn (what the flags would be rendered from) and runs the helper in
// the mode for that spawn index, the last mode repeating.
type helperSpawner struct {
	modes []string
	specs []localSpec
	cmds  []*exec.Cmd
	env   []string
}

func (h *helperSpawner) cmd(s localSpec) (*exec.Cmd, error) {
	mode := h.modes[min(len(h.specs), len(h.modes)-1)]
	h.specs = append(h.specs, s)
	c := exec.Command(os.Args[0], "-test.run=TestEmbedWorkerHelper")
	c.Env = append(append(os.Environ(), h.env...), "ANY_EMBED_WORKER_HELPER="+mode)
	h.cmds = append(h.cmds, c)
	return c, nil
}

func newTestWorker(t *testing.T, h *helperSpawner, gpuLayers int) *workerEmbedder {
	t.Helper()
	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(model, []byte("gguf"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &workerEmbedder{
		spec:         localSpec{modelPath: model, nCtx: 2048, batchDocs: 1, gpuLayers: gpuLayers},
		lg:           logger.NewNamed("indexer.test"),
		reqTimeout:   10 * time.Second,
		startTimeout: 30 * time.Second,
		newCmd:       h.cmd,
		now:          time.Now,
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// clearBackoff skips the restart wait a failure imposes, so a test can
// assert the respawn without sleeping.
func clearBackoff(w *workerEmbedder) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextTry = time.Time{}
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
	if len(h.specs) != 1 {
		t.Fatalf("spawned %d children, want 1", len(h.specs))
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
	if !w.gpuDisabled {
		t.Fatal("crash with GPU offload active did not demote to CPU")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatalf("respawn failed: %v", err)
	}
	if len(h.specs) != 2 {
		t.Fatalf("spawned %d children, want 2", len(h.specs))
	}
	if h.specs[0].gpuLayers != -1 {
		t.Fatalf("first spawn gpuLayers = %d, want -1 (llama default)", h.specs[0].gpuLayers)
	}
	if h.specs[1].gpuLayers != 0 {
		t.Fatalf("respawn gpuLayers = %d, want 0 (CPU only)", h.specs[1].gpuLayers)
	}
}

func TestWorkerEmbedder_CrashWithGPUOffKeepsSpec(t *testing.T) {
	h := &helperSpawner{modes: []string{"crash", "ok"}}
	w := newTestWorker(t, h, 0)
	if _, err := w.EmbedDocs(context.Background(), []string{"boom"}); err == nil {
		t.Fatal("want error")
	}
	if w.gpuDisabled {
		t.Fatal("demoted a child that was already CPU-only")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatal(err)
	}
	if h.specs[1].gpuLayers != 0 {
		t.Fatalf("respawn gpuLayers = %d, want 0", h.specs[1].gpuLayers)
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
	if st := h.cmds[0].ProcessState; st == nil {
		t.Fatal("hung child was not reaped")
	}
	if !w.gpuDisabled {
		t.Fatal("a killed child with GPU offload active should demote to CPU")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatalf("respawn failed: %v", err)
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
	if len(h.specs) != 1 {
		t.Fatalf("spawned %d children, want 1 (child stays usable)", len(h.specs))
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
	if len(h.specs) != 1 {
		t.Fatalf("spawned %d children during the backoff, want 1", len(h.specs))
	}
}

func TestWorkerEmbedder_SetThreadsRespawns(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	w.SetThreads(3)
	if w.child != nil {
		t.Fatal("idle child was not retired")
	}
	if st := h.cmds[0].ProcessState; st == nil || !st.Success() {
		t.Fatalf("retired child exited %v, want a clean exit on stdin EOF", st)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if len(h.specs) != 2 || h.specs[1].threads != 3 {
		t.Fatalf("spawns %d, threads %v; want a respawn with threads 3", len(h.specs), h.specs)
	}
	// Setting the same value again is a no-op.
	w.SetThreads(3)
	if w.child == nil {
		t.Fatal("child retired for an unchanged thread count")
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
	if st := h.cmds[0].ProcessState; st == nil || !st.Exited() {
		t.Fatalf("child not reaped on Close: %v", st)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"x"}); err == nil {
		t.Fatal("want an error after Close")
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
//	go test -tags 'fts vector' -run TestWorkerEmbedder_RealChild ./internal/indexer
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
