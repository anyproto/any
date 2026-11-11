//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hybridgroup/yzma/pkg/llama"

	"github.com/anyproto/any/internal/config"
)

func qwen3(t *testing.T) localModel {
	t.Helper()
	m, err := lookupLocalModel(config.LocalModelQwen3)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLocalModels(t *testing.T) {
	if m, err := lookupLocalModel(""); err != nil || m.id != localDefaultModel {
		t.Fatalf("default model = %q, %v", m.id, err)
	}
	if _, err := lookupLocalModel("nope"); err == nil {
		t.Fatal("an unknown model must be refused")
	}
	for _, m := range localModels {
		if _, err := poolingType(m.pooling); err != nil {
			t.Errorf("%s: %v", m.id, err)
		}
		if m.project != nil && len(m.project) != m.dim {
			t.Errorf("%s: projection has %d dims, the model %d", m.id, len(m.project), m.dim)
		}
	}
}

// The embedded direction is the published one, and unit length.
func TestEG2Head(t *testing.T) {
	sum := sha256.Sum256(eg2HeadRaw)
	if got := hex.EncodeToString(sum[:]); got != "f547f6ae4382d393e6cd25a27cb47eb5827424345ede417f7ec84c9eb7ed94a2" {
		t.Fatalf("eg2_head.f32 sha256 = %s", got)
	}
	u := decodeFloat32s(eg2HeadRaw)
	if len(u) != 768 {
		t.Fatalf("direction has %d dims", len(u))
	}
	if n := norm(u); math.Abs(n-1) > 1e-5 {
		t.Fatalf("direction norm = %v, want 1", n)
	}
}

func TestProjectOut(t *testing.T) {
	u := []float32{0.6, 0.8, 0}
	v := []float32{1, 2, 3}
	projectOut(v, u)
	var dot float64
	for i := range v {
		dot += float64(v[i]) * float64(u[i])
	}
	if math.Abs(dot) > 1e-6 {
		t.Fatalf("u·v = %v after projection", dot)
	}
	if v[2] != 3 {
		t.Fatalf("an orthogonal component moved: %v", v)
	}
	w := []float32{1, 2}
	projectOut(w, u)
	if !slices.Equal(w, []float32{1, 2}) {
		t.Fatalf("a direction of another width changed the vector: %v", w)
	}
}

// resolveLocalSpec: which file is which model, and what config overrides.
func TestResolveLocalSpec_Model(t *testing.T) {
	dir := t.TempDir()
	touch := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	eg2, err := lookupLocalModel(config.LocalModelEmbeddingGemma2)
	if err != nil {
		t.Fatal(err)
	}
	touch(eg2.file)
	custom := touch("custom.gguf")

	for _, c := range []struct {
		name string
		cfg  config.IndexLocal
		want localSpec
	}{
		{"named model at its default path", config.IndexLocal{Model: config.LocalModelEmbeddingGemma2},
			localSpec{profile: eg2.id, modelPath: filepath.Join(dir, eg2.file), modelDim: 768, pooling: "mean", queryPrefix: eg2.queryPrefix, docPrefix: eg2.docPrefix}},
		{"named model at a given path: loaded to learn its width", config.IndexLocal{Model: config.LocalModelEmbeddingGemma2, ModelPath: custom},
			localSpec{profile: eg2.id, modelPath: custom, pooling: "mean", queryPrefix: eg2.queryPrefix, docPrefix: eg2.docPrefix}},
		{"unnamed GGUF takes only the config", config.IndexLocal{ModelPath: custom, Pooling: "cls", DocPrefix: "passage: "},
			localSpec{modelPath: custom, pooling: "cls", docPrefix: "passage: "}},
		{"config overrides the model's prompts and pooling", config.IndexLocal{Model: config.LocalModelEmbeddingGemma2, QueryPrefix: "q: ", DocPrefix: "d: ", Pooling: "last"},
			localSpec{profile: eg2.id, modelPath: filepath.Join(dir, eg2.file), modelDim: 768, pooling: "last", queryPrefix: "q: ", docPrefix: "d: "}},
	} {
		got, dl, err := resolveLocalSpec(c.cfg, dir, "", nil)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if dl != nil {
			t.Fatalf("%s: a present file started a download", c.name)
		}
		if got.profile != c.want.profile || got.modelPath != c.want.modelPath || got.modelDim != c.want.modelDim ||
			got.pooling != c.want.pooling || got.queryPrefix != c.want.queryPrefix || got.docPrefix != c.want.docPrefix {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}

	if _, _, err := resolveLocalSpec(config.IndexLocal{Model: "nope"}, dir, "", nil); err == nil {
		t.Error("an unknown model must be refused")
	}
	if _, _, err := resolveLocalSpec(config.IndexLocal{ModelPath: custom, Pooling: "max"}, dir, "", nil); err == nil {
		t.Error("an unknown pooling must be refused")
	}

	// The child is told which model the file is, and embeds the same way.
	spec, _, err := resolveLocalSpec(config.IndexLocal{Model: config.LocalModelEmbeddingGemma2}, dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := embedderCmd(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"--profile", eg2.id}, {"--pooling", "mean"}, {"--doc-prefix", eg2.docPrefix}, {"--query-prefix", eg2.queryPrefix}} {
		i := slices.Index(cmd.Args, pair[0])
		if i < 0 || i+1 >= len(cmd.Args) || cmd.Args[i+1] != pair[1] {
			t.Errorf("child args lack %s %q: %v", pair[0], pair[1], cmd.Args)
		}
	}
	l := newLocalFromSpec(spec, nil)
	if l.pooling != llama.PoolingTypeMean || len(l.project) != 768 || l.docPrefix != eg2.docPrefix {
		t.Errorf("Local from the spec: pooling %v, projection %d dims, doc prefix %q", l.pooling, len(l.project), l.docPrefix)
	}
}

// Real-model integration: ANY_TEST_LOCAL_EG2_MODEL is the stock
// EmbeddingGemma 2 GGUF; ANY_TEST_LOCAL_EG2_PATCHED, when set, is the
// same model with the direction removed in its output head, which the
// projected stock vectors must match.
func TestLocal_EmbeddingGemma2(t *testing.T) {
	path := os.Getenv("ANY_TEST_LOCAL_EG2_MODEL")
	if path == "" {
		t.Skip("set ANY_TEST_LOCAL_EG2_MODEL to the EmbeddingGemma 2 GGUF")
	}
	libDir := os.Getenv("ANY_INDEX_LOCAL_LIB_DIR")
	if libDir == "" {
		libDir = filepath.Join("..", "..", "bin", "llamacpp")
	}
	ctx := context.Background()
	docs := []string{
		"The mitochondria is the powerhouse of the cell.",
		"Quarterly revenue grew twelve percent on strong subscription sales.",
		"Home console and handheld games",
	}
	const query = "what produces energy in a cell"
	embed := func(cfg config.IndexLocal) [][]float32 {
		cfg.LibDir = libDir
		l, err := NewLocal(cfg, t.TempDir(), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		vecs, err := l.EmbedDocs(ctx, docs)
		if err != nil {
			t.Fatal(err)
		}
		q, err := l.EmbedQuery(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		return append(vecs, q)
	}
	eg2, _ := lookupLocalModel(config.LocalModelEmbeddingGemma2)
	vecs := embed(config.IndexLocal{Model: config.LocalModelEmbeddingGemma2, ModelPath: path})
	for i, v := range vecs {
		if len(v) != 768 {
			t.Fatalf("vector %d has %d dims, want the 768-dim output width", i, len(v))
		}
		if n := norm(v); math.Abs(n-1) > 1e-3 {
			t.Errorf("vector %d: norm %v", i, n)
		}
		if d := cosine(v, eg2.project); math.Abs(d) > 1e-3 {
			t.Errorf("vector %d keeps %v of the removed direction", i, d)
		}
	}
	if cosine(vecs[3], vecs[0]) <= cosine(vecs[3], vecs[1]) {
		t.Error("the query should sit closer to the on-topic doc")
	}
	if patched := os.Getenv("ANY_TEST_LOCAL_EG2_PATCHED"); patched != "" {
		// The patched file as an unnamed GGUF: same prompts, no projection.
		ref := embed(config.IndexLocal{ModelPath: patched, Pooling: "mean", QueryPrefix: eg2.queryPrefix, DocPrefix: eg2.docPrefix})
		for i := range vecs {
			c := cosine(vecs[i], ref[i])
			t.Logf("vector %d: cosine %.6f to the patched model", i, c)
			if c < 0.9995 {
				t.Errorf("vector %d: cosine %v to the patched model", i, c)
			}
		}
	}
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}
