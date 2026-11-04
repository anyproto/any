//go:build llamacpp && !android && !ios

package indexer

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/config"
)

func init() {
	evalLocalHook = func(t *testing.T, cfg config.IndexLocal) (Embedder, error) {
		w, err := newWorkerEmbedder(cfg, t.TempDir(), "", nil)
		if err != nil {
			return nil, err
		}
		if bin := os.Getenv("ANY_BENCH_ANY_BIN"); bin != "" {
			w.newCmd = func(s localSpec) (*exec.Cmd, error) {
				c, err := embedderCmd(s)
				if err != nil {
					return nil, err
				}
				c.Path, c.Args[0] = bin, bin
				return c, nil
			}
		}
		t.Cleanup(func() { _ = w.Close() })
		return w, nil
	}
}

// TestEmbedBench drives the real `any run embedder` child (the
// binary named by ANY_BENCH_ANY_BIN) over a JSONL corpus ({"text": …}
// per line): model load time, per-doc decode latency and throughput,
// query latency, and the child's resident memory (/proc on Linux, ps
// elsewhere). Vectors are dumped for cross-run precision checks.
// ANY_BENCH_CALL texts go per EmbedDocs call (default 1);
// ANY_BENCH_BATCH_DOCS and ANY_BENCH_CTX set batchDocs and contextSize.
func TestEmbedBench(t *testing.T) {
	corpusPath := os.Getenv("ANY_BENCH_CORPUS")
	if corpusPath == "" {
		t.Skip("ANY_BENCH_CORPUS not set")
	}
	bin := os.Getenv("ANY_BENCH_ANY_BIN")
	gpuLayers := -1
	if v := os.Getenv("ANY_EVAL_LOCAL_GPU_LAYERS"); v != "" {
		gpuLayers, _ = strconv.Atoi(v)
	}
	threads, _ := strconv.Atoi(os.Getenv("ANY_EVAL_LOCAL_THREADS"))
	cfg := config.IndexLocal{
		ModelPath:   os.Getenv("ANY_EVAL_LOCAL_MODEL"),
		LibDir:      os.Getenv("ANY_EVAL_LOCAL_LIBDIR"),
		QueryPrefix: os.Getenv("ANY_EVAL_LOCAL_QUERY_PREFIX"),
		DocPrefix:   os.Getenv("ANY_EVAL_LOCAL_DOC_PREFIX"),
		Pooling:     os.Getenv("ANY_EVAL_LOCAL_POOLING"),
		Threads:     threads,
		GpuLayers:   &gpuLayers,
		BatchDocs:   envInt("ANY_BENCH_BATCH_DOCS", 0),
		ContextSize: envInt("ANY_BENCH_CTX", 0),
	}
	if gpuLayers < 0 {
		cfg.GpuLayers = nil
	}
	zero := 0
	cfg.Niceness = &zero
	spec, _, err := resolveLocalSpec(cfg, t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkerEmbedderFromSpec(spec, 10*time.Minute)
	var (
		cmdMu sync.Mutex
		cmd   *exec.Cmd
	)
	w.newCmd = func(s localSpec) (*exec.Cmd, error) {
		c, err := embedderCmd(s)
		if err != nil {
			return nil, err
		}
		if bin != "" {
			c.Path = bin
			c.Args[0] = bin
		}
		cmdMu.Lock()
		cmd = c
		cmdMu.Unlock()
		return c, nil
	}
	defer w.Close()

	var texts []string
	f, err := os.Open(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var d struct{ Text string }
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, d.Text)
	}
	f.Close()
	if n, _ := strconv.Atoi(os.Getenv("ANY_BENCH_MAX_DOCS")); n > 0 && n < len(texts) {
		texts = texts[:n]
	}

	// Child RSS sampler.
	var (
		memMu          sync.Mutex
		peakRSS, hwm   int64
		peakAnon       int64
		peakFile       int64
		stopSampler    = make(chan struct{})
		samplerStopped = make(chan struct{})
	)
	go func() {
		defer close(samplerStopped)
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stopSampler:
				return
			case <-tk.C:
			}
			cmdMu.Lock()
			c := cmd
			cmdMu.Unlock()
			if c == nil || c.Process == nil {
				continue
			}
			rss, h, anon, file := procMem(c.Process.Pid)
			memMu.Lock()
			peakRSS = max(peakRSS, rss)
			hwm = max(hwm, h)
			peakAnon = max(peakAnon, anon)
			peakFile = max(peakFile, file)
			memMu.Unlock()
		}
	}()

	ctx := context.Background()
	t0 := time.Now()
	dim, err := w.Dim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	load := time.Since(t0)
	t.Logf("child up + model loaded in %v, dim %d, hw %+v", load, dim, w.hw.Devices)

	// Warm-up: one doc, one query.
	if _, err := w.EmbedDocs(ctx, texts[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.EmbedQuery(ctx, "warm up"); err != nil {
		t.Fatal(err)
	}

	docVecs := make([][]float32, 0, len(texts))
	lat := make([]float64, 0, len(texts))
	chars := 0
	start := time.Now()
	call := max(1, envInt("ANY_BENCH_CALL", 1))
	for i := 0; i < len(texts); i += call {
		batch := texts[i:min(i+call, len(texts))]
		t1 := time.Now()
		v, err := w.EmbedDocs(ctx, batch)
		if err != nil {
			t.Fatal(err)
		}
		per := float64(time.Since(t1).Microseconds()) / 1000 / float64(len(batch))
		for _, tx := range batch {
			lat = append(lat, per)
			chars += len([]rune(tx))
		}
		docVecs = append(docVecs, v...)
	}
	docWall := time.Since(start)
	memMu.Lock()
	docRSS, docHWM := peakRSS, hwm
	memMu.Unlock()

	queries := []string{
		"bird strike forced landing on a river",
		"which console had the best selling handheld",
		"how does the indexer handle deleted records",
		"почему синхронизация тормозит после логина",
		"push notification keys for the receiver",
		"engine failure after takeoff caused by maintenance error",
		"memory leak in subscription snapshots",
		"как настроить локальный эмбеддер на GPU",
		"release process for the sdk tags",
		"midair collision over Europe",
		"what did we decide about chat read markers",
		"Sega cartridge based 16-bit system",
		"ошибка при открытии базы данных",
		"vector index recall and IVF probes",
		"pilot error in bad weather approach",
		"the type and collection object model",
	}
	var qlat []float64
	var qVecs [][]float32
	for round := 0; round < 3; round++ {
		for _, q := range queries {
			t1 := time.Now()
			v, err := w.EmbedQuery(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			qlat = append(qlat, float64(time.Since(t1).Microseconds())/1000)
			if round == 0 {
				qVecs = append(qVecs, v)
			}
		}
	}
	close(stopSampler)
	<-samplerStopped

	nan := 0
	for _, v := range append(append([][]float32{}, docVecs...), qVecs...) {
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				nan++
				break
			}
		}
	}

	res := map[string]any{
		"model":           spec.modelPath,
		"gpuLayers":       gpuLayers,
		"threads":         threads,
		"batchDocs":       spec.batchDocs,
		"contextSize":     spec.nCtx,
		"textsPerCall":    call,
		"dim":             dim,
		"loadSec":         load.Seconds(),
		"docs":            len(texts),
		"chars":           chars,
		"docWallSec":      docWall.Seconds(),
		"docsPerSec":      float64(len(texts)) / docWall.Seconds(),
		"kCharsPerSec":    float64(chars) / docWall.Seconds() / 1000,
		"docLatMs":        pct(lat),
		"queryLatMs":      pct(qlat),
		"childPeakRSSMB":  float64(max(docRSS, peakRSS)) / 1024,
		"childHWMMB":      float64(max(docHWM, hwm)) / 1024,
		"childPeakAnonMB": float64(peakAnon) / 1024,
		"childPeakFileMB": float64(peakFile) / 1024,
		"vectorsWithNaN":  nan,
		"devices":         w.hw.Devices,
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	t.Logf("RESULT %s", out)
	if p := os.Getenv("ANY_BENCH_OUT"); p != "" {
		if err := os.WriteFile(p+".json", out, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeVecs(p+".docs.f32", docVecs); err != nil {
			t.Fatal(err)
		}
		if err := writeVecs(p+".queries.f32", qVecs); err != nil {
			t.Fatal(err)
		}
	}
}

func pct(xs []float64) map[string]float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	at := func(p float64) float64 { return s[min(len(s)-1, int(p*float64(len(s))))] }
	sum := 0.0
	for _, x := range s {
		sum += x
	}
	return map[string]float64{"min": s[0], "p50": at(.5), "p90": at(.9), "p99": at(.99), "max": s[len(s)-1], "mean": sum / float64(len(s))}
}

// procMem returns VmRSS, VmHWM, RssAnon and RssFile in KiB.
func procMem(pid int) (rss, hwm, anon, file int64) {
	if runtime.GOOS != "linux" {
		out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return 0, 0, 0, 0
		}
		rss, _ = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return rss, 0, 0, 0
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		fs := strings.Fields(line)
		if len(fs) < 2 {
			continue
		}
		n, _ := strconv.ParseInt(fs[1], 10, 64)
		switch fs[0] {
		case "VmRSS:":
			rss = n
		case "VmHWM:":
			hwm = n
		case "RssAnon:":
			anon = n
		case "RssFile:":
			file = n
		}
	}
	return rss, hwm, anon, file
}

// writeVecs dumps vectors as: uint32 count, uint32 dim, then float32 LE.
func writeVecs(path string, vecs [][]float32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	dim := 0
	if len(vecs) > 0 {
		dim = len(vecs[0])
	}
	_ = binary.Write(bw, binary.LittleEndian, uint32(len(vecs)))
	_ = binary.Write(bw, binary.LittleEndian, uint32(dim))
	for _, v := range vecs {
		_ = binary.Write(bw, binary.LittleEndian, v)
	}
	return bw.Flush()
}
