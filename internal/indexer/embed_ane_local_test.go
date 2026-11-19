//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/config"
)

// loadedEG2 returns EmbeddingGemma 2 loaded from ANY_TEST_LOCAL_EG2_MODEL.
func loadedEG2(t *testing.T) *Local {
	t.Helper()
	path := os.Getenv("ANY_TEST_LOCAL_EG2_MODEL")
	if path == "" {
		t.Skip("set ANY_TEST_LOCAL_EG2_MODEL to the EmbeddingGemma 2 GGUF")
	}
	libDir := os.Getenv("ANY_INDEX_LOCAL_LIB_DIR")
	if libDir == "" {
		libDir = filepath.Join("..", "..", "bin", "llamacpp")
	}
	l, err := NewLocal(config.IndexLocal{Model: config.LocalModelEmbeddingGemma2, ModelPath: path, LibDir: libDir}, t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Dim(context.Background()); err != nil {
		t.Fatal(err)
	}
	return l
}

// The model's own tokenizer feeds the Core ML functions: a text starts
// with BOS (2), ends with EOS (1) and goes to the smallest function that
// holds it; a longer one stays with llama.cpp.
func TestLocal_NeuralEngineRouting(t *testing.T) {
	l := loadedEG2(t)
	f := &fakeANE{}
	n := newNeuralEngine(f, []int{128, 256}, int(l.nEmbd), t.Logf)
	t.Cleanup(func() {
		n.close()
		n.loads.Wait()
	})
	l.ane = n
	n.start()
	n.mu.Lock()
	n.requestLocked(256)
	n.mu.Unlock()
	n.loads.Wait()

	ctx := context.Background()
	q, err := l.EmbedQuery(ctx, "what produces energy in a cell")
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("The mitochondria is the powerhouse of the cell. ", 120)
	medium := strings.Repeat("Quarterly revenue grew twelve percent. ", 20)
	docs, err := l.EmbedTitledDocs(ctx, []string{"Biology", "Finance", ""}, []string{long, medium, "Short note."})
	if err != nil {
		t.Fatal(err)
	}
	// The long text is llama.cpp's, counted under its reason.
	if rt := l.lastRoutes(); rt.ANE+rt.CoreML != 2 || rt.Llama != 1 || rt.Misses[EmbedReasonTooLong] != 1 || len(rt.Misses) != 1 {
		t.Fatalf("routes %+v, want two texts on core ml and one on llama.cpp as too long", rt)
	}
	_, served, _, _ := f.snapshot()
	if want := []int{128, 256, 128}; len(served) != 3 || served[0] != want[0] || served[1] != want[1] || served[2] != want[2] {
		t.Fatalf("served by %v, want %v (query, medium doc, short doc)", served, want)
	}
	f.mu.Lock()
	tokens := f.tokens
	f.mu.Unlock()
	for i, ids := range tokens {
		if ids[0] != 2 || ids[len(ids)-1] != 1 {
			t.Errorf("text %d: tokens run %d … %d, want BOS 2 … EOS 1", i, ids[0], ids[len(ids)-1])
		}
		for _, id := range ids[1 : len(ids)-1] {
			if id == 0 {
				t.Errorf("text %d: a padding token inside the text", i)
				break
			}
		}
	}
	// The fake's vector is the first axis; every served text is that,
	// finished the way a llama.cpp vector is.
	want := l.finish(append([]float32{1}, make([]float32, int(l.nEmbd)-1)...))
	for i, v := range [][]float32{q, docs[1], docs[2]} {
		if c := cosine(v, want); c < 0.999999 {
			t.Errorf("served vector %d: cosine %v to the function's output", i, c)
		}
	}
	if c := cosine(docs[0], want); c > 0.9 {
		t.Errorf("the long text took the function's vector (cosine %v); llama.cpp embeds it", c)
	}
	if n := norm(docs[0]); math.Abs(n-1) > 1e-3 {
		t.Errorf("long text: norm %v", n)
	}
}

// countingBackend counts the texts each function serves.
type countingBackend struct {
	aneBackend
	mu     sync.Mutex
	served map[int]int
}

func (b *countingBackend) load(length int) (aneFunction, string, error) {
	fn, info, err := b.aneBackend.load(length)
	if err != nil {
		return nil, "", err
	}
	return countingFn{fn, b, length}, info, nil
}

func (b *countingBackend) counts() map[int]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[int]int{}
	for k, v := range b.served {
		out[k] = v
	}
	return out
}

type countingFn struct {
	aneFunction
	b      *countingBackend
	length int
}

func (f countingFn) embed(tokens []int32, out []float32) error {
	err := f.aneFunction.embed(tokens, out)
	if err == nil {
		f.b.mu.Lock()
		f.b.served[f.length]++
		f.b.mu.Unlock()
	}
	return err
}

// neuralEngineTexts is a spread of lengths and scripts: titles pair with
// docs by index.
func neuralEngineTexts() (titles, docs []string) {
	sentence := "The mitochondria is the powerhouse of the cell, and its inner membrane carries the proteins of the respiratory chain. "
	russian := "Митохондрии вырабатывают энергию клетки, а их внутренняя мембрана несёт белки дыхательной цепи. "
	chinese := "线粒体是细胞的能量工厂，其内膜上分布着呼吸链的蛋白质。"
	for _, c := range []struct {
		title string
		text  string
	}{
		{"", "Short note."},
		{"Biology", sentence},
		{"Biology", strings.Repeat(sentence, 4)},
		{"Finance", strings.Repeat("Quarterly revenue grew twelve percent on strong subscription sales. ", 12)},
		{"Biology", strings.Repeat(sentence, 14)},
		{"Biology", strings.Repeat(sentence, 28)},
		{"Biology", strings.Repeat(sentence, 60)},
		{"Биология", strings.Repeat(russian, 6)},
		{"生物学", strings.Repeat(chinese, 20)},
		{"生物学", strings.Repeat(chinese, 60)},
	} {
		titles = append(titles, c.title)
		docs = append(docs, c.text)
	}
	return titles, docs
}

// TestLocal_NeuralEnginePackage runs the real Core ML package next to
// llama.cpp on an Apple Silicon Mac. ANY_TEST_LOCAL_EG2_MODEL is the
// GGUF and ANY_TEST_COREML_PACKAGE the .mlpackage; ANY_TEST_COREML_LENGTHS
// lists the function lengths (default: the pinned package's).
func TestLocal_NeuralEnginePackage(t *testing.T) {
	dir := os.Getenv("ANY_TEST_COREML_PACKAGE")
	if dir == "" {
		t.Skip("set ANY_TEST_COREML_PACKAGE to the model's .mlpackage")
	}
	if !neuralEngineHost() {
		t.Skip("needs an Apple Silicon Mac on macOS 15 or later")
	}
	l := loadedEG2(t)
	ctx := context.Background()
	titles, docs := neuralEngineTexts()
	const query = "what produces energy in a cell"

	lengths := l.coreml.lengths
	if spec := os.Getenv("ANY_TEST_COREML_LENGTHS"); spec != "" {
		lengths = nil
		for _, s := range strings.Split(spec, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				t.Fatalf("ANY_TEST_COREML_LENGTHS: %v", err)
			}
			lengths = append(lengths, n)
		}
		sort.Ints(lengths)
	}
	var tokens []int
	l.mu.Lock()
	for i, d := range docs {
		tokens = append(tokens, len(l.tokenize(docPrompt(l.docPrefix, titles[i])+d)))
	}
	l.mu.Unlock()
	t.Logf("doc lengths in tokens: %v; functions: %v", tokens, lengths)

	embedAll := func() ([][]float32, time.Duration) {
		start := time.Now()
		vecs, err := l.EmbedTitledDocs(ctx, titles, docs)
		if err != nil {
			t.Fatal(err)
		}
		took := time.Since(start)
		q, err := l.EmbedQuery(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		return append(vecs, q), took
	}
	embedAll() // warm llama.cpp
	ref, llamaTook := embedAll()
	t.Logf("llama.cpp: %d docs in %s", len(docs), llamaTook.Round(time.Millisecond))

	backend := &countingBackend{aneBackend: newCoreMLBackend(l.coreml, dir, int(l.nEmbd), t.Logf), served: map[int]int{}}
	n := newNeuralEngine(backend, lengths, int(l.nEmbd), t.Logf)
	l.mu.Lock()
	l.ane = n
	l.mu.Unlock()
	n.start()

	// Every function loads on the first text that needs it; llama.cpp
	// serves until then. A first load specializes the model for the
	// Neural Engine and takes tens of seconds per function.
	fits := 0
	for _, c := range tokens {
		if c <= lengths[len(lengths)-1] {
			fits++
		}
	}
	deadline := time.Now().Add(15 * time.Minute)
	started := time.Now()
	for {
		before := 0
		for _, c := range backend.counts() {
			before += c
		}
		embedAll()
		after := 0
		for _, c := range backend.counts() {
			after += c
		}
		if after-before >= fits+1 {
			break
		}
		n.mu.Lock()
		off := n.off
		n.mu.Unlock()
		if off {
			t.Fatal("the neural engine turned itself off; see the log lines above")
		}
		if time.Now().After(deadline) {
			t.Fatalf("functions not loaded in time; served so far: %v", backend.counts())
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("every function ready after %s", time.Since(started).Round(time.Second))

	before := backend.counts()
	got, aneTook := embedAll()
	after := backend.counts()
	for _, length := range lengths {
		t.Logf("s%d served %d texts", length, after[length]-before[length])
	}
	t.Logf("neural engine: %d docs in %s (llama.cpp alone: %s)", len(docs), aneTook.Round(time.Millisecond), llamaTook.Round(time.Millisecond))
	for i := range got {
		c := cosine(got[i], ref[i])
		label := "query"
		if i < len(tokens) {
			label = strconv.Itoa(tokens[i]) + " tokens"
		}
		t.Logf("text %d (%s): cosine %.6f to llama.cpp", i, label, c)
		if c < 0.995 {
			t.Errorf("text %d (%s): cosine %v to the llama.cpp vector", i, label, c)
		}
		if nrm := norm(got[i]); math.Abs(nrm-1) > 1e-3 {
			t.Errorf("text %d: norm %v", i, nrm)
		}
	}
}
