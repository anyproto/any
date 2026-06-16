//go:build vector && !gomobile

package indexer

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hybridgroup/yzma/pkg/llama"

	"github.com/anyproto/any/internal/config"
)

// Factory + pre-ready behavior — no llama.cpp libs or model needed.
// ModelPath points at a missing file so no download is spawned.

func TestNewEmbedder_Local(t *testing.T) {
	dir := t.TempDir()
	e, err := NewEmbedder(config.Index{
		Embedder: "local",
		Local:    config.IndexLocal{ModelPath: filepath.Join(dir, "absent.gguf")},
	}, filepath.Join(dir, "models"), "")
	if err != nil {
		t.Fatal(err)
	}
	l, ok := e.(*Local)
	if !ok {
		t.Fatalf("want *Local, got %T", e)
	}
	defer l.Close()

	// Air-gapped config (ModelPath set) must not spawn a download.
	if l.dl != nil {
		t.Error("ModelPath override must not start a download")
	}
	// Custom model ⇒ no implicit Qwen query prefix.
	if l.queryPrefix != "" {
		t.Errorf("custom model must not inherit the default query prefix, got %q", l.queryPrefix)
	}

	// Not ready ⇒ ordinary error (ridden by the embed loop's retry), no panic.
	if _, err := l.EmbedDocs(context.Background(), []string{"x"}); err == nil {
		t.Error("EmbedDocs without model must error")
	}
	if _, err := l.EmbedQuery(context.Background(), "x"); err == nil {
		t.Error("EmbedQuery without model must error")
	}
}

func TestLocal_DefaultsAndDim(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the model in the shared dir so NewLocal skips the download.
	modelsDir := filepath.Join(dir, "models")
	modelPath := filepath.Join(modelsDir, localModelName)
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := NewLocal(config.IndexLocal{}, modelsDir, filepath.Join(dir, "index", "models"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if l.dl != nil {
		t.Error("present model file must not start a download")
	}
	if l.modelPath != modelPath {
		t.Errorf("model path: want %s got %s", modelPath, l.modelPath)
	}
	if l.nCtx != localDefaultCtx {
		t.Errorf("nCtx default: want %d got %d", localDefaultCtx, l.nCtx)
	}
	if l.queryPrefix != localQueryPrefix {
		t.Errorf("default model should use the Qwen retrieval prefix, got %q", l.queryPrefix)
	}
	// Default model reports its dimension without loading anything.
	if d, err := l.Dim(context.Background()); err != nil || d != localDefaultDim {
		t.Errorf("Dim: want %d,nil got %d,%v", localDefaultDim, d, err)
	}

	// Matryoshka dim caps the reported dimension.
	l2, err := NewLocal(config.IndexLocal{Dim: 256}, modelsDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if d, _ := l2.Dim(context.Background()); d != 256 {
		t.Errorf("Dim with Matryoshka truncation: want 256 got %d", d)
	}
}

func TestLocal_LegacyModelDirFallback(t *testing.T) {
	dir := t.TempDir()
	// Model exists only at the pre-per-account location — keep using it.
	legacyDir := filepath.Join(dir, "index", "models")
	legacyPath := filepath.Join(legacyDir, localModelName)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := NewLocal(config.IndexLocal{}, filepath.Join(dir, "models"), legacyDir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if l.dl != nil {
		t.Error("legacy model file must not start a download")
	}
	if l.modelPath != legacyPath {
		t.Errorf("model path: want legacy %s got %s", legacyPath, l.modelPath)
	}
}

func TestTruncateTokens(t *testing.T) {
	eos := llama.Token(99)
	short := []llama.Token{1, 2, 3}
	if got := truncateTokens(short, 8, eos); len(got) != 3 {
		t.Errorf("under-limit must pass through, got %v", got)
	}
	long := []llama.Token{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	got := truncateTokens(long, 4, eos)
	if len(got) != 4 {
		t.Fatalf("want len 4, got %v", got)
	}
	if got[3] != eos {
		t.Errorf("last token after truncation must be EOS (last-pooling reads it), got %v", got)
	}
	if got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("head must be preserved, got %v", got)
	}
	// Original slice untouched.
	if long[3] != 4 {
		t.Error("truncateTokens must not mutate its input")
	}
}

func TestL2Normalize(t *testing.T) {
	vec := []float32{3, 4}
	l2Normalize(vec)
	if math.Abs(float64(vec[0])-0.6) > 1e-6 || math.Abs(float64(vec[1])-0.8) > 1e-6 {
		t.Errorf("want [0.6 0.8], got %v", vec)
	}
	zero := []float32{0, 0}
	l2Normalize(zero) // must not divide by zero
	if zero[0] != 0 {
		t.Errorf("zero vector must stay zero, got %v", zero)
	}
}

func norm(vec []float32) float64 {
	var s float64
	for _, v := range vec {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s)
}

// Real-model integration: ANY_TEST_LOCAL_EMBEDDER=1 plus llama.cpp libs
// (bin/llamacpp via `make llamacpp`, or YZMA_LIB) and the model
// (ANY_INDEX_LOCAL_MODEL_PATH, or already downloaded into the default
// data dir by a previous run).
func TestLocal_Integration(t *testing.T) {
	if os.Getenv("ANY_TEST_LOCAL_EMBEDDER") == "" {
		t.Skip("set ANY_TEST_LOCAL_EMBEDDER=1 (needs llama.cpp libs + Qwen3 model)")
	}
	cfg := config.IndexLocal{
		ModelPath: os.Getenv("ANY_INDEX_LOCAL_MODEL_PATH"),
		LibDir:    os.Getenv("ANY_INDEX_LOCAL_LIB_DIR"),
	}
	if cfg.ModelPath == "" {
		// Never auto-download inside a test run.
		t.Skip("set ANY_INDEX_LOCAL_MODEL_PATH to the Qwen3 GGUF")
	}
	if cfg.LibDir == "" {
		// repo-root bin/llamacpp relative to this package dir
		cfg.LibDir = filepath.Join("..", "..", "bin", "llamacpp")
	}
	// A ModelPath override disables the implicit Qwen prefix; this test
	// runs the pinned model, so restore it.
	cfg.QueryPrefix = localQueryPrefix
	l, err := NewLocal(cfg, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	ctx := context.Background()
	docs := []string{
		"The mitochondria is the powerhouse of the cell.",
		"Quarterly revenue grew twelve percent on strong subscription sales.",
	}
	vecs, err := l.EmbedDocs(ctx, docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != localDefaultDim {
			t.Errorf("doc %d: want dim %d, got %d", i, localDefaultDim, len(v))
		}
		if n := norm(v); math.Abs(n-1) > 1e-3 {
			t.Errorf("doc %d: want unit norm, got %f", i, n)
		}
	}

	q, err := l.EmbedQuery(ctx, "what part of the cell produces energy?")
	if err != nil {
		t.Fatal(err)
	}
	cos := func(a, b []float32) float64 {
		var s float64
		for i := range a {
			s += float64(a[i]) * float64(b[i])
		}
		return s
	}
	if cos(q, vecs[0]) <= cos(q, vecs[1]) {
		t.Errorf("biology query must rank the biology doc above finance: %f vs %f",
			cos(q, vecs[0]), cos(q, vecs[1]))
	}

	// Long input exercises the truncation path.
	long := strings.Repeat("a fairly ordinary sentence about nothing in particular. ", 500)
	lv, err := l.EmbedDocs(ctx, []string{long})
	if err != nil {
		t.Fatal(err)
	}
	if n := norm(lv[0]); math.Abs(n-1) > 1e-3 {
		t.Errorf("truncated doc: want unit norm, got %f", n)
	}

	// Concurrent callers (per-space embed loops + query) under -race.
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, errs[i] = l.EmbedDocs(ctx, []string{"concurrent doc"})
			} else {
				_, errs[i] = l.EmbedQuery(ctx, "concurrent query")
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent call %d: %v", i, err)
		}
	}
}
