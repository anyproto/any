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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"

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
		{name: "default model has no package", cfg: config.IndexLocal{}, host: true, onDisk: true, onRefuse: "no Core ML package"},
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
		pkg := editEG2Package(t, func(p *coreMLPackage) { p.url, p.files = served.url, served.files })
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
		pkg := editEG2Package(t, func(p *coreMLPackage) { p.name = "on-disk" })
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

// A death of a child that used Core ML does not say which runtime
// caused it: the Neural Engine path goes first, GPU offload on the next
// one.
func TestWorkerEmbedder_CrashDemotesNeuralEngineFirst(t *testing.T) {
	h := &helperSpawner{modes: []string{"crash", "crash", "ok"}, env: []string{"ANY_EMBED_WORKER_ANE=1"}}
	w := newTestWorker(t, h, -1)
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
	if specs[0].coremlDir == "" || specs[1].coremlDir != "" || specs[2].coremlDir != "" {
		t.Fatalf("coremlDir per spawn: %q %q %q", specs[0].coremlDir, specs[1].coremlDir, specs[2].coremlDir)
	}
	if specs[1].gpuLayers != -1 || specs[2].gpuLayers != 0 {
		t.Fatalf("gpuLayers per spawn: %d %d %d, want -1 -1 0", specs[0].gpuLayers, specs[1].gpuLayers, specs[2].gpuLayers)
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
	vec, ok := l.embedANE([]llama.Token{2, 7, 1})
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
	if _, ok := (&Local{}).embedANE([]llama.Token{2, 7, 1}); ok {
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
	t.Logf("downloaded and verified in %s at %s", time.Since(start).Round(time.Second), dir)
}

// A function longer than any text the embedder sends is never asked
// for, so it is not listed.
func TestUsableLengths(t *testing.T) {
	all := []int{128, 256, 512, 1024, 2048}
	for _, c := range []struct {
		maxTokens int
		want      []int
	}{
		{2048, all},
		{4096, all},
		{1024, []int{128, 256, 512, 1024}},
		{1025, all},
		{300, []int{128, 256, 512}},
		{64, []int{128}},
	} {
		if got := usableLengths(all, c.maxTokens); !reflect.DeepEqual(got, c.want) {
			t.Errorf("texts of up to %d tokens: lengths %v, want %v", c.maxTokens, got, c.want)
		}
	}
}
