//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"github.com/hybridgroup/yzma/pkg/llama"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/anyproto/any/internal/config"
)

// No test reaches for the real package: the host is no Mac unless a test
// says so.
func init() { aneHost = func() bool { return false } }

func stubANEHost(t *testing.T, capable bool) {
	t.Helper()
	prev := aneHost
	aneHost = func() bool { return capable }
	t.Cleanup(func() { aneHost = prev })
}

// editEG2Package swaps the pinned model's Core ML package for an edited
// copy for one test.
func editEG2Package(t *testing.T, edit func(*coreMLPackage)) *coreMLPackage {
	t.Helper()
	for i := range localModels {
		if localModels[i].id != config.LocalModelEmbeddingGemma2 {
			continue
		}
		prev := localModels[i].coreml
		pkg := *prev
		edit(&pkg)
		localModels[i].coreml = &pkg
		t.Cleanup(func() { localModels[i].coreml = prev })
		return &pkg
	}
	t.Fatal("no EmbeddingGemma 2 in the model table")
	return nil
}

func writePackage(t *testing.T, pkg *coreMLPackage, dir string) {
	t.Helper()
	for _, f := range pkg.files {
		p := filepath.Join(dir, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanNeuralEngine(t *testing.T) {
	gguf := filepath.Join(t.TempDir(), "eg2.gguf")
	if err := os.WriteFile(gguf, []byte("gguf"), 0o600); err != nil {
		t.Fatal(err)
	}
	eg2 := config.LocalModelEmbeddingGemma2
	cases := []struct {
		name     string
		cfg      config.IndexLocal
		host     bool
		onDisk   bool
		source   bool   // the package has a download source
		want     bool   // auto: the package is used
		onRefuse string // on: refused with this text; "" = used
	}{
		{name: "pinned model, package on disk", cfg: config.IndexLocal{Model: eg2}, host: true, onDisk: true, want: true},
		{name: "package to download", cfg: config.IndexLocal{Model: eg2}, host: true, source: true, want: true},
		{name: "no package and no source", cfg: config.IndexLocal{Model: eg2}, host: true, onRefuse: "not in the models directory"},
		{name: "not a mac", cfg: config.IndexLocal{Model: eg2}, onDisk: true, onRefuse: "Apple Silicon"},
		{name: "the default model", cfg: config.IndexLocal{}, host: true, onDisk: true, want: true},
		{name: "a model with no package", cfg: config.IndexLocal{Model: config.LocalModelQwen3}, host: true, onDisk: true, onRefuse: "no Core ML package"},
		{name: "unnamed gguf", cfg: config.IndexLocal{ModelPath: gguf}, host: true, onDisk: true, onRefuse: "no Core ML package"},
		{name: "other pooling", cfg: config.IndexLocal{Model: eg2, Pooling: "last"}, host: true, onDisk: true, onRefuse: "pooling"},
		{name: "the model's own pooling", cfg: config.IndexLocal{Model: eg2, Pooling: "mean"}, host: true, onDisk: true, want: true},
		{name: "model path, package on disk", cfg: config.IndexLocal{Model: eg2, ModelPath: gguf}, host: true, onDisk: true, want: true},
		{name: "model path downloads nothing", cfg: config.IndexLocal{Model: eg2, ModelPath: gguf}, host: true, source: true, onRefuse: "not downloaded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubANEHost(t, c.host)
			pkg := editEG2Package(t, func(p *coreMLPackage) {
				p.url = ""
				if c.source {
					p.url = "https://models.invalid/pkg"
				}
			})
			models := t.TempDir()
			if c.onDisk {
				writePackage(t, pkg, pkg.dir(models))
			}
			for _, mode := range []string{"", config.NeuralEngineAuto} {
				cfg := c.cfg
				cfg.NeuralEngine = mode
				got, err := planNeuralEngine(cfg, models)
				if err != nil {
					t.Fatalf("mode %q: %v", mode, err)
				}
				if (got != nil) != c.want {
					t.Fatalf("mode %q: package used = %v, want %v", mode, got != nil, c.want)
				}
			}
			cfg := c.cfg
			cfg.NeuralEngine = config.NeuralEngineOn
			got, err := planNeuralEngine(cfg, models)
			switch {
			case c.onRefuse == "" && (err != nil || got == nil):
				t.Fatalf("on: package %v, err %v", got != nil, err)
			case c.onRefuse != "" && (err == nil || !strings.Contains(err.Error(), c.onRefuse)):
				t.Fatalf("on: err = %v, want one naming %q", err, c.onRefuse)
			}
			cfg.NeuralEngine = config.NeuralEngineOff
			if got, err := planNeuralEngine(cfg, models); got != nil || err != nil {
				t.Fatalf("off: package %v, err %v", got != nil, err)
			}
		})
	}
	if _, err := planNeuralEngine(config.IndexLocal{NeuralEngine: "yes"}, t.TempDir()); err == nil {
		t.Fatal("an unknown index.local.neuralEngine value must be refused")
	}
}

// packageServer serves a package's files and records the paths asked for.
type packageServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func (s *packageServer) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// newPackageServer returns a server for bodies (path → content) and the
// package that pins them. A path in wrong gets a checksum its body fails.
func newPackageServer(t *testing.T, bodies map[string]string, wrong ...string) (*packageServer, *coreMLPackage) {
	t.Helper()
	s := &packageServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/pkg/")
		s.mu.Lock()
		s.paths = append(s.paths, p)
		s.mu.Unlock()
		body, ok := bodies[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	pkg := &coreMLPackage{name: "model-1", url: s.URL + "/pkg", lengths: []int{128}}
	paths := make([]string, 0, len(bodies))
	for p := range bodies {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sum := sha256.Sum256([]byte(bodies[p]))
		for _, w := range wrong {
			if w == p {
				sum = sha256.Sum256([]byte("another file"))
			}
		}
		pkg.files = append(pkg.files, coreMLFile{path: p, sha256: hex.EncodeToString(sum[:])})
	}
	return s, pkg
}

func waitPackage(t *testing.T, p *packageDownload) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("package download did not end")
	}
}

func TestPackageDownload(t *testing.T) {
	bodies := map[string]string{
		"Manifest.json":                            `{"manifest":1}`,
		"Data/com.apple.CoreML/model.mlmodel":      strings.Repeat("graph ", 100),
		"Data/com.apple.CoreML/weights/weight.bin": strings.Repeat("weights ", 1000),
	}
	srv, pkg := newPackageServer(t, bodies)
	models := t.TempDir()
	dir := pkg.dir(models)
	// A file already in place is not fetched again.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Manifest.json"), []byte(bodies["Manifest.json"]), 0o600); err != nil {
		t.Fatal(err)
	}
	var frames frameLog
	p := startPackageDownload(pkg, models, nil, srv.Client(), frames.report)
	waitPackage(t, p)
	p.Close()

	if !pkg.complete(dir) {
		t.Fatal("package incomplete after the download ended")
	}
	// The package directory holds its files and nothing else.
	var got []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		got = append(got, filepath.ToSlash(rel))
		b, err := os.ReadFile(path)
		if err == nil && string(b) != bodies[filepath.ToSlash(rel)] {
			t.Errorf("%s: content differs", rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"Data/com.apple.CoreML/model.mlmodel", "Data/com.apple.CoreML/weights/weight.bin", "Manifest.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("package holds %v, want %v", got, want)
	}
	asked := srv.asked()
	sort.Strings(asked)
	if want := []string{"Data/com.apple.CoreML/model.mlmodel", "Data/com.apple.CoreML/weights/weight.bin"}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("fetched %v, want %v", asked, want)
	}
	// One process row per fetched file, named so the process view takes
	// the name as a target, with the side files under the same name
	// outside the package.
	target := regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	names := map[string]bool{}
	for _, u := range frames.all() {
		names[u.Name] = true
	}
	if !names[pkg.id()+".weight.bin"] || !names[pkg.id()+".model.mlmodel"] || len(names) != 2 {
		t.Fatalf("process rows %v, want one per fetched file", names)
	}
	for name := range names {
		if !target.MatchString(name) {
			t.Errorf("process row name %q is not a valid event target", name)
		}
		if _, err := os.Stat(filepath.Join(models, name+".lock")); err != nil {
			t.Errorf("no download lock beside the package for %s: %v", name, err)
		}
	}
}

// The package waits for the model's own download: one process row
// serves both, one at a time. With no model there is no package either.
func TestPackageDownload_WaitsForModel(t *testing.T) {
	for _, installed := range []bool{true, false} {
		srv, pkg := newPackageServer(t, map[string]string{"Manifest.json": "{}"})
		models := t.TempDir()
		model := &modelDownload{finished: make(chan struct{})}
		p := startPackageDownload(pkg, models, model, srv.Client(), nil)
		time.Sleep(50 * time.Millisecond)
		if asked := srv.asked(); len(asked) != 0 {
			t.Fatalf("fetched %v while the model was still downloading", asked)
		}
		model.mu.Lock()
		model.done = installed
		model.err = nil
		if !installed {
			model.err = errDownloadStopped
		}
		model.mu.Unlock()
		close(model.finished)
		waitPackage(t, p)
		p.Close()
		if got := pkg.complete(pkg.dir(models)); got != installed {
			t.Fatalf("model installed %v: package complete = %v", installed, got)
		}
	}
}

// A file that fails its checksum ends the package download: the files
// after it are not fetched.
func TestPackageDownload_StopsOnChecksum(t *testing.T) {
	shortDownload(t)
	bodies := map[string]string{"a.bin": "first", "b.bin": "second", "c.bin": "third"}
	srv, pkg := newPackageServer(t, bodies, "b.bin")
	models := t.TempDir()
	p := startPackageDownload(pkg, models, nil, srv.Client(), nil)
	waitPackage(t, p)
	p.Close()
	if pkg.complete(pkg.dir(models)) {
		t.Fatal("package complete with a file that failed its checksum")
	}
	for _, path := range srv.asked() {
		if path == "c.bin" {
			t.Fatal("fetched the file after the one that failed")
		}
	}
}

func TestPackageDownload_Close(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	pkg := &coreMLPackage{name: "m", url: srv.URL, files: []coreMLFile{{"a.bin", ""}}, lengths: []int{128}}
	p := startPackageDownload(pkg, t.TempDir(), nil, srv.Client(), nil)
	closed := make(chan struct{})
	go func() {
		p.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not stop the package download")
	}
}

func TestNewWorkerEmbedder_NeuralEngine(t *testing.T) {
	models := t.TempDir()
	eg2, err := lookupLocalModel(config.LocalModelEmbeddingGemma2)
	if err != nil {
		t.Fatal(err)
	}
	// The GGUF is in place, so nothing reaches for the network.
	if err := os.WriteFile(filepath.Join(models, eg2.file), []byte("gguf"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.IndexLocal{Model: config.LocalModelEmbeddingGemma2}

	t.Run("on is refused where it cannot run", func(t *testing.T) {
		stubANEHost(t, false)
		c := cfg
		c.NeuralEngine = config.NeuralEngineOn
		if _, err := newWorkerEmbedder(c, models, "", nil); err == nil || !strings.Contains(err.Error(), "neuralEngine") {
			t.Fatalf("err = %v, want a refusal naming index.local.neuralEngine", err)
		}
	})
	t.Run("auto without a mac is llama.cpp alone", func(t *testing.T) {
		stubANEHost(t, false)
		w, err := newWorkerEmbedder(cfg, models, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if w.spec.coremlDir != "" || w.pkgDl != nil {
			t.Fatalf("coremlDir %q, download %v: want neither", w.spec.coremlDir, w.pkgDl != nil)
		}
	})
	t.Run("a missing package downloads", func(t *testing.T) {
		stubANEHost(t, true)
		srv, served := newPackageServer(t, map[string]string{"Manifest.json": "{}"})
		pkg := editEG2Package(t, func(p *coreMLPackage) {
			p.url, p.files = served.url, served.files
			p.noticesURL, p.notices = "", nil
		})
		w, err := newWorkerEmbedder(cfg, models, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if w.spec.coremlDir != pkg.dir(models) {
			t.Fatalf("coremlDir = %q, want %q", w.spec.coremlDir, pkg.dir(models))
		}
		if w.pkgDl == nil {
			t.Fatal("no download for a package that is not on disk")
		}
		waitPackage(t, w.pkgDl)
		if !pkg.complete(pkg.dir(models)) {
			t.Fatalf("package incomplete; server saw %v", srv.asked())
		}
	})
	t.Run("a package on disk downloads nothing", func(t *testing.T) {
		stubANEHost(t, true)
		pkg := editEG2Package(t, func(p *coreMLPackage) { p.name, p.notices = "on-disk", nil })
		writePackage(t, pkg, pkg.dir(models))
		w, err := newWorkerEmbedder(cfg, models, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if w.spec.coremlDir != pkg.dir(models) || w.pkgDl != nil {
			t.Fatalf("coremlDir %q, download %v", w.spec.coremlDir, w.pkgDl != nil)
		}
	})
}

func TestEmbedderCmdArgs_CoreML(t *testing.T) {
	spec := localSpec{modelPath: "/m.gguf", nCtx: 2048, batchDocs: 1, gpuLayers: -1}
	cmd, err := embedderCmd(spec)
	if err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(cmd.Args[1:], " "); strings.Contains(args, "--coreml") {
		t.Fatalf("args %q carry --coreml with no package", args)
	}
	spec.coremlDir = "/models/eg2.mlpackage"
	if cmd, err = embedderCmd(spec); err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(cmd.Args[1:], " "); !strings.Contains(args, "--coreml /models/eg2.mlpackage") {
		t.Fatalf("args %q missing the package", args)
	}
}

// A child that never touched Core ML, or whose Core ML gave up, dies of
// something else: the Neural Engine path stays and the GPU is demoted,
// as without a package.
func TestWorkerEmbedder_CrashWithoutCoreMLKeepsNeuralEngine(t *testing.T) {
	for _, env := range [][]string{nil, {"ANY_EMBED_WORKER_ANE=off"}} {
		crashWithoutCoreML(t, env)
	}
}

func crashWithoutCoreML(t *testing.T, env []string) {
	h := &helperSpawner{modes: []string{"crash", "ok"}, env: env}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()
	if _, err := w.EmbedDocs(context.Background(), []string{"boom"}); err == nil {
		t.Fatal("want error")
	}
	if !gpuDisabled(w) {
		t.Fatal("a death before Core ML was in use did not demote the GPU")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatal(err)
	}
	if got := h.spec(1); got.coremlDir == "" || got.gpuLayers != 0 {
		t.Fatalf("respawn coremlDir %q gpuLayers %d, want the package kept and CPU only", got.coremlDir, got.gpuLayers)
	}
}

// One runtime per death, the Neural Engine first. A death that repeats
// without it clears it: it comes back on as the GPU is demoted.
func TestWorkerEmbedder_CrashDemotesNeuralEngineFirst(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	h := &helperSpawner{modes: []string{"crash", "crash", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1"}}
	w := newTestWorker(t, h, -1)
	w.lg = logger.CtxLogger{Logger: zap.New(core)}
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()

	if _, err := w.EmbedDocs(context.Background(), []string{"boom"}); err == nil {
		t.Fatal("want error")
	}
	if gpuDisabled(w) {
		t.Fatal("the first death demoted the GPU with the neural engine still on")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"boom"}); err == nil {
		t.Fatal("want error")
	}
	if !gpuDisabled(w) {
		t.Fatal("the second death did not demote the GPU")
	}
	clearBackoff(w)
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatal(err)
	}
	specs := h.specsCopy()
	if len(specs) != 3 {
		t.Fatalf("spawned %d children, want 3", len(specs))
	}
	if specs[0].coremlDir == "" || specs[1].coremlDir != "" || specs[2].coremlDir == "" {
		t.Fatalf("coremlDir per spawn: %q %q %q, want it on, off, on again", specs[0].coremlDir, specs[1].coremlDir, specs[2].coremlDir)
	}
	if specs[0].gpuLayers != -1 || specs[1].gpuLayers != -1 || specs[2].gpuLayers != 0 {
		t.Fatalf("gpuLayers per spawn: %d %d %d, want -1 -1 0", specs[0].gpuLayers, specs[1].gpuLayers, specs[2].gpuLayers)
	}
	// The warnings say what the next child runs, not more.
	warns := logs.All()
	if len(warns) != 2 {
		t.Fatalf("logged %d warnings, want one per death", len(warns))
	}
	if msg := warns[0].Message; !strings.Contains(msg, "the next child runs llama.cpp alone") {
		t.Errorf("first death: %q", msg)
	}
	if msg := warns[1].Message; !strings.Contains(msg, "llama.cpp stays on CPU") || !strings.Contains(msg, "the neural engine is back on") {
		t.Errorf("second death: %q", msg)
	}
}

// With the GPU off by configuration there is no GPU to demote: the
// second death still clears the Neural Engine.
func TestWorkerEmbedder_NeuralEngineRestoredWithGPUOff(t *testing.T) {
	h := &helperSpawner{modes: []string{"crash", "crash", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1"}}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, err := w.EmbedDocs(context.Background(), []string{"boom"}); err == nil {
			t.Fatal("want error")
		}
		clearBackoff(w)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatal(err)
	}
	specs := h.specsCopy()
	if len(specs) != 3 {
		t.Fatalf("spawned %d children, want 3", len(specs))
	}
	if specs[0].coremlDir == "" || specs[1].coremlDir != "" || specs[2].coremlDir == "" {
		t.Fatalf("coremlDir per spawn: %q %q %q, want it on, off, on again", specs[0].coremlDir, specs[1].coremlDir, specs[2].coremlDir)
	}
	if gpuDisabled(w) {
		t.Fatal("demoted a GPU that was off")
	}
}

// The Neural Engine comes back once. A child that dies with it on again
// loses it for the rest of the process.
func TestWorkerEmbedder_NeuralEngineRestoredOnce(t *testing.T) {
	h := &helperSpawner{modes: []string{"crash", "crash", "crash", "crash", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1"}}
	w := newTestWorker(t, h, -1)
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()

	for i := 0; i < 4; i++ {
		if _, err := w.EmbedDocs(context.Background(), []string{"boom"}); err == nil {
			t.Fatal("want error")
		}
		clearBackoff(w)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"again"}); err != nil {
		t.Fatal(err)
	}
	specs := h.specsCopy()
	if len(specs) != 5 {
		t.Fatalf("spawned %d children, want 5", len(specs))
	}
	for i, wantOn := range []bool{true, false, true, false, false} {
		if on := specs[i].coremlDir != ""; on != wantOn {
			t.Errorf("spawn %d: neural engine on = %v, want %v", i, on, wantOn)
		}
	}
	for i, want := range []int{-1, -1, 0, 0, 0} {
		if specs[i].gpuLayers != want {
			t.Errorf("spawn %d: gpuLayers = %d, want %d", i, specs[i].gpuLayers, want)
		}
	}
}

// aneWorker is a test worker with a Neural Engine path planned and a
// helper child that reports on it as env says.
func aneWorker(t *testing.T, env ...string) (*workerEmbedder, *helperSpawner) {
	t.Helper()
	h := &helperSpawner{modes: []string{"ok"}, env: env}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()
	w.bulkWait = 30 * time.Second
	return w, h
}

// waitAttached blocks until a PrepareBulk call waits with the slot free.
func waitAttached(t *testing.T, w *workerEmbedder) {
	t.Helper()
	waitLive(t, w)
	waitReady(t, w)
	for deadline := time.Now().Add(10 * time.Second); w.preparing.Load() == 0 || !w.slot.tryAcquire(); {
		if time.Now().After(deadline) {
			t.Fatal("the wait never freed the slot")
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.slot.release(false)
}

// Bulk work waits for the first function, on the child it then embeds
// on, and the wait ends on the report, not on the deadline. A child that
// has settled answers at once, without the slot.
func TestWorkerEmbedder_PrepareBulkWaitsForTheFirstFunction(t *testing.T) {
	const delay = 300 * time.Millisecond
	w, h := aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")...)
	start := time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited < delay-50*time.Millisecond || waited > w.bulkWait/2 {
		t.Fatalf("returned after %s, want the first function's %s", waited, delay)
	}
	if w.bulkWaited.Load() {
		t.Fatal("the wait ended on the deadline")
	}
	// Settled: nothing to wait for, and nothing to take the slot for.
	if err := w.slot.acquire(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	asked := make(chan struct{})
	go func() {
		defer close(asked)
		w.PrepareBulk(context.Background(), nil)
	}()
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("a settled child was asked through the slot")
	}
	w.slot.release(false)
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// A child that replaced a settled one is waited on like the first: its
// Neural Engine path starts over.
func TestWorkerEmbedder_PrepareBulkWaitsOnAReplacement(t *testing.T) {
	const delay = 300 * time.Millisecond
	w, h := aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")...)
	w.PrepareBulk(context.Background(), nil)
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
	w.SetThreads(3) // the running child is stale: the next frame replaces it
	start := time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited < delay-50*time.Millisecond || h.spawns() != 2 || w.bulkWaited.Load() {
		t.Fatalf("after a replacement: waited %s on %d children, deadline hit %v; want the new child's first function", waited, h.spawns(), w.bulkWaited.Load())
	}
}

// A settled child that is being retired is still the live process until
// it is reaped. It does not answer for its replacement: the first text
// after an idle retirement waits for the new child's own first function.
func TestWorkerEmbedder_PrepareBulkIgnoresARetiringChild(t *testing.T) {
	const delay = 300 * time.Millisecond
	h := &helperSpawner{modes: []string{"linger", "ok"}, env: append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()
	w.bulkWait = 30 * time.Second
	w.PrepareBulk(context.Background(), nil)
	if h.spawns() != 1 || w.bulkWaited.Load() {
		t.Fatalf("first child: spawned %d, deadline hit %v", h.spawns(), w.bulkWaited.Load())
	}
	// Retire it the way the idle timer does. It ignores the closed stdin,
	// so it stays live under the slot until the grace ends.
	if err := w.slot.acquire(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	retired := make(chan struct{})
	go func() {
		defer close(retired)
		w.stopChild(true)
		w.slot.release(false)
	}()
	for deadline := time.Now().Add(5 * time.Second); !hasChildDetached(w); {
		if time.Now().After(deadline) {
			t.Fatal("the child was never detached")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if w.live.Load() == nil {
		t.Fatal("the retiring child is not live any more: the window this test needs is gone")
	}
	asked := make(chan struct{})
	go func() {
		defer close(asked)
		w.PrepareBulk(context.Background(), nil)
	}()
	select {
	case <-asked:
		t.Fatal("the retiring child answered for its replacement")
	case <-time.After(delay):
	}
	_ = h.cmdAt(0).Process.Kill() // end the grace
	<-retired
	select {
	case <-asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the replacement's first function was never reported")
	}
	if h.spawns() != 2 || w.bulkWaited.Load() {
		t.Fatalf("replacement: spawned %d, deadline hit %v", h.spawns(), w.bulkWaited.Load())
	}
	if c := w.settledChild.Load(); c == nil || c.detached.Load() || c.cmd != h.cmdAt(1) {
		t.Fatal("the wait did not end on the replacement's own report")
	}
}

// A child that reported on its Neural Engine path and then failed to
// start was never the worker's: it does not answer for the next one.
func TestWorkerEmbedder_PrepareBulkIgnoresAChildThatNeverStarted(t *testing.T) {
	const delay = 300 * time.Millisecond
	h := &helperSpawner{modes: []string{"settleexit", "ok"}, env: append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()
	w.bulkWait = 30 * time.Second
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want the start failure")
	}
	clearBackoff(w)
	start := time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited < delay-50*time.Millisecond || h.spawns() != 2 || w.bulkWaited.Load() {
		t.Fatalf("waited %s on %d children, deadline hit %v; want the second child's first function", waited, h.spawns(), w.bulkWaited.Load())
	}
}

// hasChildDetached reports whether the last settled child was let go.
func hasChildDetached(w *workerEmbedder) bool {
	c := w.settledChild.Load()
	return c != nil && c.detached.Load()
}

// Without a Neural Engine path, or with its package not on disk, there
// is nothing to wait for and no child is started for it; a package that
// lands is waited for at the next call.
func TestWorkerEmbedder_PrepareBulkWithoutNeuralEngine(t *testing.T) {
	h := &helperSpawner{modes: []string{"ok"}}
	w := newTestWorker(t, h, 0)
	w.PrepareBulk(context.Background(), nil)
	if h.spawns() != 0 {
		t.Fatalf("no path: spawned %d children, want none", h.spawns())
	}

	const delay = 300 * time.Millisecond
	w, h = aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")...)
	onDisk := false
	w.pkgOnDisk = func() bool { return onDisk }
	start := time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited > delay/2 || h.spawns() != 0 || w.bulkWaited.Load() {
		t.Fatalf("package not on disk: waited %s, spawned %d, deadline hit %v", waited, h.spawns(), w.bulkWaited.Load())
	}
	onDisk = true
	start = time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited < delay-50*time.Millisecond || h.spawns() != 1 {
		t.Fatalf("package landed: waited %s on %d children, want the first function of one", waited, h.spawns())
	}
}

// A path that turned itself off, or whose first function failed to
// load, is not waited for.
func TestWorkerEmbedder_PrepareBulkEndsOnAFailedPath(t *testing.T) {
	for _, report := range []string{"off", "failed"} {
		w, _ := aneWorker(t, "ANY_EMBED_WORKER_ANE="+report)
		start := time.Now()
		w.PrepareBulk(context.Background(), nil)
		if waited := time.Since(start); waited > w.bulkWait/2 || w.bulkWaited.Load() {
			t.Fatalf("%s: waited %s, deadline hit %v", report, waited, w.bulkWaited.Load())
		}
	}
}

// A load that neither finishes nor fails holds bulk work until the
// deadline, once.
func TestWorkerEmbedder_PrepareBulkTimesOutOnce(t *testing.T) {
	w, h := aneWorker(t, "ANY_EMBED_WORKER_ANE=1")
	w.bulkWait = 200 * time.Millisecond
	start := time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited < w.bulkWait || !w.bulkWaited.Load() {
		t.Fatalf("returned after %s, deadline hit %v", waited, w.bulkWaited.Load())
	}
	start = time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited > w.bulkWait/2 {
		t.Fatalf("waited %s a second time", waited)
	}
	if _, err := w.EmbedDocs(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// The deadline belongs to the child: a caller that joins late leaves
// with the first one.
func TestWorkerEmbedder_PrepareBulkSharesOneDeadline(t *testing.T) {
	w, _ := aneWorker(t, "ANY_EMBED_WORKER_ANE=1")
	w.bulkWait = 600 * time.Millisecond
	first := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		w.PrepareBulk(context.Background(), nil)
		first <- time.Since(start)
	}()
	waitAttached(t, w)
	time.Sleep(w.bulkWait / 2)
	joined := time.Now()
	w.PrepareBulk(context.Background(), nil)
	late := time.Since(joined)
	if late >= w.bulkWait {
		t.Fatalf("the late caller waited %s, a deadline of its own", late)
	}
	select {
	case d := <-first:
		if d < w.bulkWait {
			t.Fatalf("the first caller left after %s, before the deadline", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first caller is still waiting")
	}
}

// A child that exits ends the wait, and is not the deadline.
func TestWorkerEmbedder_PrepareBulkEndsWithTheChild(t *testing.T) {
	w, h := aneWorker(t, "ANY_EMBED_WORKER_ANE=1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.PrepareBulk(context.Background(), nil)
	}()
	waitAttached(t, w)
	_ = h.cmdAt(0).Process.Kill()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("still waiting on a dead child")
	}
	if w.bulkWaited.Load() {
		t.Fatal("a dead child counted as the deadline")
	}
}

// A deadline that one caller ran into ends the wait of every other one,
// and no later caller waits.
func TestWorkerEmbedder_GivingUpWakesEveryWaiter(t *testing.T) {
	w, _ := aneWorker(t, "ANY_EMBED_WORKER_ANE=1")
	w.bulkWait = time.Minute
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.PrepareBulk(context.Background(), nil)
	}()
	waitAttached(t, w)
	w.giveUpBulk()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a waiter outlived the give-up")
	}
	start := time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("a later caller waited %s after the give-up", waited)
	}
}

// A caller whose ctx ends leaves at once, also while another caller's
// frame holds the slot; a search is served during the wait.
func TestWorkerEmbedder_PrepareBulkLeavesWithItsContext(t *testing.T) {
	const frame = 2 * time.Second
	h := &helperSpawner{modes: []string{"slow"}, env: append(delayEnv(frame), "ANY_EMBED_WORKER_ANE=1")}
	w := newTestWorker(t, h, 0)
	w.mu.Lock()
	w.spec.coremlDir = "/models/eg2.mlpackage"
	w.mu.Unlock()
	w.bulkWait = time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.PrepareBulk(ctx, nil)
	}()
	waitAttached(t, w)

	// A search is served while bulk work waits; its frame holds the slot.
	served := make(chan error, 1)
	go func() {
		_, err := w.EmbedQuery(context.Background(), "q")
		served <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(frame / 2):
		t.Fatal("the cancelled caller waited behind another frame")
	}
	if err := <-served; err != nil {
		t.Fatalf("search during the wait: %v", err)
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// The child is not retired as idle while bulk work waits on it.
func TestWorkerEmbedder_PrepareBulkHoldsOffIdleRetirement(t *testing.T) {
	const delay = 400 * time.Millisecond
	w, h := aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")...)
	w.idleTimeout = 50 * time.Millisecond
	start := time.Now()
	w.PrepareBulk(context.Background(), nil)
	if waited := time.Since(start); waited < delay-50*time.Millisecond || w.bulkWaited.Load() {
		t.Fatalf("returned after %s, deadline hit %v: the child was retired under the wait", waited, w.bulkWaited.Load())
	}
	if h.spawns() != 1 {
		t.Fatalf("spawned %d children, want 1", h.spawns())
	}
}

// A batch's route comes from what the child counted in its frames, from
// the runtime llama.cpp has in that child, and from the worker's state
// when each frame was served.
func TestWorkerEmbedder_EmbedDocsRouted(t *testing.T) {
	ctx := context.Background()
	embed := func(w *workerEmbedder) EmbedRoute {
		t.Helper()
		vecs, rt, err := w.EmbedDocsRouted(ctx, nil, []string{"a", "b", "c"})
		if err != nil || len(vecs) != 3 {
			t.Fatalf("EmbedDocsRouted: %d vectors, %v", len(vecs), err)
		}
		return rt
	}
	is := func(rt EmbedRoute, reason string, backends ...string) bool {
		return rt.Reason == reason && slices.Equal(rt.Backends, backends)
	}
	const device = "ANY_EMBED_WORKER_MODEL_DEVICE=Vulkan0 (Test GPU)"
	planned := func(w *workerEmbedder) *workerEmbedder {
		w.mu.Lock()
		w.spec.coremlDir = "/models/eg2.mlpackage"
		w.mu.Unlock()
		return w
	}

	// Every text on the Neural Engine: no llama.cpp runtime, no reason.
	w, _ := aneWorker(t, "ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_ROUTES=1,0,")
	if rt := embed(w); !is(rt, "", EmbedBackendANE) {
		t.Errorf("all on the neural engine: %+v", rt)
	}

	// Functions whose placement is not verified yet are Core ML, not the
	// Neural Engine.
	w, _ = aneWorker(t, "ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_ROUTES=0,0,,1")
	if rt := embed(w); !is(rt, "", EmbedBackendCoreML) {
		t.Errorf("all on unverified functions: %+v", rt)
	}
	w, _ = aneWorker(t, "ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_ROUTES=1,0,,1")
	if rt := embed(w); !is(rt, "", EmbedBackendANE, EmbedBackendCoreML) {
		t.Errorf("verified and unverified functions: %+v", rt)
	}

	// A mixed batch on a child that loaded the model on a device.
	h := &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_ROUTES=1,2," + EmbedReasonFunctionLoading, device}}
	w = planned(newTestWorker(t, h, -1))
	if rt := embed(w); !is(rt, EmbedReasonFunctionLoading, EmbedBackendANE, EmbedBackendGPU) {
		t.Errorf("mixed batch: %+v", rt)
	}
	// Once bulk work gave up waiting, a function still loading is that.
	w.giveUpBulk()
	if rt := embed(w); !is(rt, EmbedReasonReadinessTimeout, EmbedBackendANE, EmbedBackendGPU) {
		t.Errorf("after the readiness timeout: %+v", rt)
	}

	// Offload asked for, and the model loaded on no device: the CPU, with
	// no reason to give. That llama.cpp can offload proves nothing.
	h = &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ROUTES=0,1,"}}
	w = newTestWorker(t, h, -1)
	if rt := embed(w); !is(rt, "", EmbedBackendCPU) {
		t.Errorf("no device: %+v", rt)
	}

	// The GPU off by configuration, whatever devices there are.
	h = &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ROUTES=0,1,", device}}
	w = newTestWorker(t, h, 0)
	if rt := embed(w); !is(rt, EmbedReasonGPUDisabled, EmbedBackendCPU) {
		t.Errorf("gpu off by configuration: %+v", rt)
	}

	// A frame served on the GPU keeps its route when the next one kills
	// the child and demotes the GPU; the batch after it is the CPU's.
	h = &helperSpawner{modes: []string{"ok", "ok"}, env: []string{"ANY_EMBED_WORKER_ROUTES=0,1,", device, "ANY_EMBED_WORKER_CRASH_AFTER=1"}}
	w = newTestWorker(t, h, -1)
	vecs, rt, err := w.EmbedDocsRouted(ctx, nil, []string{"a", "b"})
	if err == nil || len(vecs) != 1 {
		t.Fatalf("a batch whose second frame dies: %d vectors, %v", len(vecs), err)
	}
	if !is(rt, "", EmbedBackendGPU) {
		t.Errorf("the frame served before the death: %+v", rt)
	}
	clearBackoff(w)
	if vecs, rt, err = w.EmbedDocsRouted(ctx, nil, []string{"c"}); err != nil || len(vecs) != 1 || !is(rt, EmbedReasonGPUFailed, EmbedBackendCPU) {
		t.Errorf("after the gpu was demoted: %d vectors, %v, %+v", len(vecs), err, rt)
	}

	// The Neural Engine demoted after a death: llama.cpp alone, and why.
	h = &helperSpawner{modes: []string{"crash", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1", "ANY_EMBED_WORKER_ROUTES=0,1,", device}}
	w = planned(newTestWorker(t, h, -1))
	if _, err := w.EmbedDocs(ctx, []string{"boom"}); err == nil {
		t.Fatal("want error")
	}
	clearBackoff(w)
	if rt := embed(w); !is(rt, EmbedReasonANEOff, EmbedBackendGPU) {
		t.Errorf("neural engine demoted: %+v", rt)
	}

	// The package not ready: the child says so, and it is passed on.
	h = &helperSpawner{modes: []string{"ok"}, env: []string{"ANY_EMBED_WORKER_ROUTES=0,1," + EmbedReasonPackageNotReady, device}}
	w = planned(newTestWorker(t, h, -1))
	if rt := embed(w); !is(rt, EmbedReasonPackageNotReady, EmbedBackendGPU) {
		t.Errorf("package not ready: %+v", rt)
	}
}

func TestLlamaBackend(t *testing.T) {
	onDevice := &Hardware{ModelDevices: []string{"Vulkan0 (Test GPU)"}}
	for _, c := range []struct {
		gpuLayers int
		hw        *Hardware
		want      string
	}{
		{-1, onDevice, EmbedBackendGPU},
		{-1, &Hardware{Devices: []string{"enumerated, model not on it"}}, EmbedBackendCPU},
		{0, onDevice, EmbedBackendCPU},
		{-1, nil, EmbedBackendLlama},
	} {
		if got := llamaBackend(localSpec{gpuLayers: c.gpuLayers}, c.hw); got != c.want {
			t.Errorf("gpuLayers %d, hardware %+v: %q, want %q", c.gpuLayers, c.hw, got, c.want)
		}
	}
}

// The wait says when it starts: before a child is spawned for it, once.
// A settled child is not waited on, and nothing is said.
func TestWorkerEmbedder_PrepareBulkSaysWhenItWaits(t *testing.T) {
	const delay = 200 * time.Millisecond
	w, _ := aneWorker(t, append(delayEnv(delay), "ANY_EMBED_WORKER_ANE=ready")...)
	var said []time.Duration
	start := time.Now()
	w.PrepareBulk(context.Background(), func() { said = append(said, time.Since(start)) })
	if len(said) != 1 || said[0] > delay/2 {
		t.Fatalf("waiting was called at %v, want once, before the child was up", said)
	}
	w.PrepareBulk(context.Background(), func() { said = append(said, time.Since(start)) })
	if len(said) != 1 {
		t.Fatalf("a settled child was announced as a wait: %v", said)
	}
}

func TestANESettles(t *testing.T) {
	for status, want := range map[string]bool{
		"s128 ready (open 3.4s, validate 1.3s, placement on record, plan skipped; asked for 5s ago)": true,
		"s128 not used: core ml plans it off the neural engine":                                      true,
		"s128 not used any more: 3 failures in a row":                                                true,
		aneOffStatus + "llama.cpp embeds everything: no function can be used":                        true,
		// Another function's report is not the first one's.
		"s2048 not used: core ml plans it off the neural engine (on record)": false,
		"s256 ready (open 1s, validate 1s, placement not verified yet)":      false,
		"starting":                        false,
		"package compiled in 6.909s":      false,
		"s512 released after 5m0s unused": false,
		"s128 failed: prediction":         false,
		"s128 placement audit finished":   false,
	} {
		if got := aneSettles(status, "s128"); got != want {
			t.Errorf("aneSettles(%q) = %v, want %v", status, got, want)
		}
	}
	// With no first function named, any function's report settles.
	if !aneSettles("s2048 not used: refused", "") || aneSettles("starting", "") {
		t.Error("aneSettles without a first function")
	}
	if got := firstFunction(localSpec{profile: config.LocalModelEmbeddingGemma2}); got != "s128" {
		t.Errorf("firstFunction = %q, want s128", got)
	}
}

// A Neural Engine vector gets the same projection and width as a
// llama.cpp one.
func TestLocal_EmbedANEFinishesVector(t *testing.T) {
	f := &fakeANE{}
	n, _ := newTestANE(t, f)
	n.start()
	n.loads.Wait()
	l := &Local{ane: n, project: []float32{0.6, 0.8, 0, 0}, outDim: 2}
	vec, ok, _ := l.embedANE([]llama.Token{2, 7, 1}, false)
	if !ok {
		t.Fatal("not served")
	}
	// [1,0,0,0] less its part along the direction is [0.64,-0.48,0,0].
	want := []float32{0.8, -0.6}
	if len(vec) != len(want) {
		t.Fatalf("vec %v, want width %d", vec, len(want))
	}
	for i := range want {
		if math.Abs(float64(vec[i]-want[i])) > 1e-6 {
			t.Fatalf("vec %v, want %v", vec, want)
		}
	}
	if _, ok, _ := (&Local{}).embedANE([]llama.Token{2, 7, 1}, false); ok {
		t.Fatal("served with no neural engine")
	}
}

// The pinned package: an immutable source, a checksum per file, lengths
// the embedder's own truncation never exceeds.
func TestEmbeddingGemma2Package(t *testing.T) {
	eg2, err := lookupLocalModel(config.LocalModelEmbeddingGemma2)
	if err != nil {
		t.Fatal(err)
	}
	pkg := eg2.coreml
	if pkg == nil {
		t.Fatal("EmbeddingGemma 2 has no Core ML package")
	}
	if !regexp.MustCompile(`^https://huggingface\.co/[^/]+/[^/]+/resolve/[0-9a-f]{40}/[^/]+\.mlpackage$`).MatchString(pkg.url) {
		t.Errorf("source %q is not pinned to a revision", pkg.url)
	}
	if len(pkg.files) != 3 {
		t.Fatalf("%d files, want the manifest, the model and the weights", len(pkg.files))
	}
	for _, f := range pkg.files {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f.sha256) {
			t.Errorf("%s has no sha256", f.path)
		}
	}
	if !sort.IntsAreSorted(pkg.lengths) || len(pkg.lengths) == 0 {
		t.Fatalf("lengths %v are not ascending", pkg.lengths)
	}
	if longest := pkg.lengths[len(pkg.lengths)-1]; longest != localDefaultCtx {
		t.Errorf("longest function holds %d tokens, the default context %d", longest, localDefaultCtx)
	}
	if got, want := filepath.Base(pkg.dir("models")), "embeddinggemma-2-858f354d.mlpackage"; got != want {
		t.Errorf("package directory %s, want %s: a changed pin needs this test changed with it", got, want)
	}
	// Its licence texts: the licence, the notice and the one licence the
	// notice refers to, from a pinned revision.
	if !regexp.MustCompile(`^https://huggingface\.co/[^/]+/[^/]+/resolve/[0-9a-f]{40}$`).MatchString(pkg.noticesURL) {
		t.Errorf("licence texts' source %q is not pinned to a revision", pkg.noticesURL)
	}
	var texts []string
	for _, f := range pkg.notices {
		texts = append(texts, f.path)
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f.sha256) {
			t.Errorf("%s has no sha256", f.path)
		}
	}
	if want := []string{"LICENSE", "NOTICE", "licenses/QVAC-MIT.txt"}; !reflect.DeepEqual(texts, want) {
		t.Errorf("licence texts %v, want %v", texts, want)
	}
}

// TestEmbeddingGemma2Package_Download fetches the pinned package from its
// source. ANY_TEST_COREML_DOWNLOAD names an empty directory to fetch it
// into (about 575 MB).
func TestEmbeddingGemma2Package_Download(t *testing.T) {
	models := os.Getenv("ANY_TEST_COREML_DOWNLOAD")
	if models == "" {
		t.Skip("set ANY_TEST_COREML_DOWNLOAD to a directory to download the package into")
	}
	eg2, err := lookupLocalModel(config.LocalModelEmbeddingGemma2)
	if err != nil {
		t.Fatal(err)
	}
	pkg := eg2.coreml
	var frames frameLog
	start := time.Now()
	p := startPackageDownload(pkg, models, nil, nil, frames.report)
	select {
	case <-p.done:
	case <-time.After(30 * time.Minute):
		p.Close()
		t.Fatal("the package did not arrive in 30 minutes")
	}
	p.Close()
	dir := pkg.dir(models)
	if !pkg.complete(dir) {
		t.Fatalf("package incomplete; last frame %+v", frames.last())
	}
	if err := pkg.verify(dir); err != nil {
		t.Fatal(err)
	}
	if !pkg.noticesComplete(models) {
		t.Fatalf("licence texts missing from %s", pkg.noticesDir(models))
	}
	t.Logf("downloaded and verified in %s at %s", time.Since(start).Round(time.Second), dir)
}

// textServer serves bodies by URL path and records the paths asked for.
func textServer(t *testing.T, bodies map[string]string) *packageServer {
	t.Helper()
	s := &packageServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		s.mu.Lock()
		s.paths = append(s.paths, p)
		s.mu.Unlock()
		body, ok := bodies[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func pinOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// The package's licence texts land beside it, after it, in a directory
// of their own; a text of another revision is replaced; one that cannot
// be had does not hold the package back.
func TestPackageDownload_Notices(t *testing.T) {
	// The text that fails its checksum stops on its second try, long
	// before this bound.
	prev := noticeFetchTimeout
	noticeFetchTimeout = 10 * time.Second
	t.Cleanup(func() { noticeFetchTimeout = prev })
	shortDownload(t)

	bodies := map[string]string{
		"pkg/Manifest.json":           "{}",
		"texts/LICENSE":               "the licence",
		"texts/NOTICE":                "the notice",
		"texts/licenses/QVAC-MIT.txt": "the mit text",
		"texts/licenses/UNPINNED.txt": "another file",
	}
	srv := textServer(t, bodies)
	pkg := &coreMLPackage{
		name:       "model-1",
		url:        srv.URL + "/pkg",
		files:      []coreMLFile{{"Manifest.json", pinOf("{}")}},
		lengths:    []int{128},
		noticesURL: srv.URL + "/texts",
		notices: []coreMLFile{
			{"LICENSE", pinOf("the licence")},
			{"NOTICE", pinOf("the notice")},
			{"licenses/UNPINNED.txt", pinOf("not what the source serves")},
			{"licenses/QVAC-MIT.txt", pinOf("the mit text")},
		},
	}
	models := t.TempDir()
	texts := pkg.noticesDir(models)
	// The licence is already there; the notice is an older revision's.
	if err := os.MkdirAll(texts, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"LICENSE": "the licence", "NOTICE": "an older notice"} {
		if err := os.WriteFile(filepath.Join(texts, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if pkg.noticesComplete(models) {
		t.Fatal("complete with a stale notice and a missing text")
	}

	var frames frameLog
	p := startPackageDownload(pkg, models, nil, srv.Client(), frames.report)
	waitPackage(t, p)
	p.Close()

	if !pkg.complete(pkg.dir(models)) {
		t.Fatal("package incomplete")
	}
	// Only the package reports a process; the texts are too small for one.
	reported := frames.all()
	if len(reported) == 0 {
		t.Fatal("the package file reported no process")
	}
	for _, u := range reported {
		if u.Name != pkg.id()+".Manifest.json" {
			t.Errorf("process row %q for a licence text", u.Name)
		}
	}
	for name, want := range map[string]string{"LICENSE": "the licence", "NOTICE": "the notice", "licenses/QVAC-MIT.txt": "the mit text"} {
		got, err := os.ReadFile(filepath.Join(texts, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, want)
		}
	}
	// The text whose pin the source does not match is given up on, and
	// the one after it still arrives.
	if _, err := os.Stat(filepath.Join(texts, "licenses", "UNPINNED.txt")); err == nil {
		t.Error("installed a text that fails its checksum")
	}
	if pkg.noticesComplete(models) {
		t.Error("complete with a text that fails its checksum")
	}
	asked := srv.asked()
	if len(asked) == 0 || asked[0] != "pkg/Manifest.json" {
		t.Fatalf("fetched %v, want the package first", asked)
	}
	for _, path := range asked {
		if path == "texts/LICENSE" {
			t.Error("fetched a licence text that was already in place")
		}
	}
	// Nothing but the package's files in the package directory.
	entries, err := os.ReadDir(pkg.dir(models))
	if err != nil || len(entries) != 1 {
		t.Fatalf("package directory holds %d entries, %v; want its one file", len(entries), err)
	}
}

// A package already on disk still gets its licence texts; with a model
// path nothing is fetched.
func TestNewWorkerEmbedder_NeuralEngineNotices(t *testing.T) {
	stubANEHost(t, true)
	srv := textServer(t, map[string]string{"texts/LICENSE": "the licence"})
	models := t.TempDir()
	eg2, err := lookupLocalModel(config.LocalModelEmbeddingGemma2)
	if err != nil {
		t.Fatal(err)
	}
	gguf := filepath.Join(models, eg2.file)
	if err := os.WriteFile(gguf, []byte("gguf"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg := editEG2Package(t, func(p *coreMLPackage) {
		p.name = "with-texts"
		p.noticesURL = srv.URL + "/texts"
		p.notices = []coreMLFile{{"LICENSE", pinOf("the licence")}}
	})
	writePackage(t, pkg, pkg.dir(models))

	cfg := config.IndexLocal{Model: config.LocalModelEmbeddingGemma2, ModelPath: gguf}
	w, err := newWorkerEmbedder(cfg, models, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.pkgDl != nil {
		t.Fatal("a download was started with a model path set")
	}
	_ = w.Close()

	cfg.ModelPath = ""
	if w, err = newWorkerEmbedder(cfg, models, "", nil); err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.pkgDl == nil {
		t.Fatal("no download for the missing licence texts")
	}
	waitPackage(t, w.pkgDl)
	if !pkg.noticesComplete(models) {
		t.Fatalf("licence texts incomplete; server saw %v", srv.asked())
	}
}
