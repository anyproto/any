//go:build fts && vector && !gomobile

package indexer

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// TestSearchEvalBEIR runs a standard labeled IR benchmark (BEIR format:
// corpus.jsonl / queries.jsonl / qrels/test.tsv) through the search stack
// with the real embedder, reporting nDCG@10 / recall@10 / MRR per mode
// plus a knob sweep. This is the rigorous before/after the synthetic
// corpus (eval_test.go) can't give once a real embedder saturates it.
//
//	ANY_BEIR_DIR=/tmp/scifact \
//	ANY_EVAL_EMBEDDER=local ANY_EVAL_LOCAL_MODEL=… ANY_EVAL_LOCAL_LIBDIR=… \
//	[ANY_BEIR_MAX_DOCS=N] [ANY_BEIR_MAX_QUERIES=N] \
//	go test -tags 'fts vector' -run TestSearchEvalBEIR -v -timeout 30m ./internal/indexer
func TestSearchEvalBEIR(t *testing.T) {
	dir := os.Getenv("ANY_BEIR_DIR")
	if dir == "" {
		t.Skip("set ANY_BEIR_DIR to a BEIR dataset dir (corpus.jsonl, queries.jsonl, qrels/test.tsv)")
	}
	name, real := realEvalEmbedder(t)
	if real == nil {
		t.Skip("BEIR needs a real embedder — set ANY_EVAL_EMBEDDER (+ local model envs)")
	}
	emb := &cachingEmbedder{Embedder: real} // memoize repeated query embeds
	ctx := context.Background()

	corpus := readBEIRCorpus(t, filepath.Join(dir, "corpus.jsonl"))
	queries := readBEIRQueries(t, filepath.Join(dir, "queries.jsonl"))
	qrels := readBEIRQrels(t, filepath.Join(dir, "qrels", "test.tsv"))
	corpus = capBEIRCorpus(corpus, qrels, envInt("ANY_BEIR_MAX_DOCS", 0))

	// Queries that have at least one relevant doc present in the corpus.
	type q struct {
		id, text string
		rel      []string
	}
	docPresent := map[string]bool{}
	for _, d := range corpus {
		docPresent[d.id] = true
	}
	var qs []q
	for qid, rel := range qrels {
		text, ok := queries[qid]
		if !ok {
			continue
		}
		var present []string
		for d := range rel {
			if docPresent[d] {
				present = append(present, d)
			}
		}
		if len(present) > 0 {
			qs = append(qs, q{qid, text, present})
		}
	}
	if maxq := envInt("ANY_BEIR_MAX_QUERIES", 0); maxq > 0 && len(qs) > maxq {
		qs = qs[:maxq]
	}
	t.Logf("BEIR %s: corpus=%d queries=%d embedder=%s", filepath.Base(dir), len(corpus), len(qs), name)

	const k = 10
	// eval runs every query through one mode/opts and returns mean metrics.
	eval := func(ix *Indexer, mode string) metrics {
		var sum metrics
		for _, qq := range qs {
			resp, err := ix.Search(ctx, beirSpace, api.SearchRequest{Query: qq.text, Mode: mode, Limit: k})
			if err != nil {
				t.Fatalf("search %q: %v", qq.id, err)
			}
			var ranked []string
			seen := map[string]bool{}
			for _, h := range resp.Hits {
				if !seen[h.RecordId] {
					seen[h.RecordId] = true
					ranked = append(ranked, h.RecordId)
				}
			}
			m := evalOne(ranked, qq.rel, k)
			sum.recallAtK += m.recallAtK
			sum.mrr += m.mrr
			sum.ndcgAtK += m.ndcgAtK
		}
		n := float64(len(qs))
		return metrics{sum.recallAtK / n, sum.mrr / n, sum.ndcgAtK / n}
	}

	// Ingest once (doc embeds are the bulk cost); reuse the store across
	// the per-mode and knob runs (only Options differ, not the data).
	ix := newEvalIndexer(t, emb, Options{StopWords: true})
	ingestBEIR(t, ix, emb, corpus)

	t.Logf("=== %s: production knobs (stopwords on, RRF 1/1, floor 0) — nDCG@%d / recall@%d / MRR ===", name, k, k)
	t.Logf("%-8s  %7s  %7s  %7s", "mode", "ndcg", "recall", "mrr")
	for _, mode := range []string{api.SearchModeFTS, api.SearchModeVector, api.SearchModeHybrid} {
		m := eval(ix, mode)
		t.Logf("%-8s  %7.4f  %7.4f  %7.4f", mode, m.ndcgAtK, m.recallAtK, m.mrr)
	}

	// Knob sweep (hybrid). Rebuild the indexer per knob set so Options
	// take effect; ingest is cheap to repeat only because embeds are
	// cached — but to avoid re-embedding docs we reuse the same store by
	// swapping opts in place.
	t.Logf("=== %s: hybrid knob sweep — nDCG@%d / recall@%d / MRR ===", name, k, k)
	t.Logf("%-34s  %7s  %7s  %7s", "knobs", "ndcg", "recall", "mrr")
	sweep := []struct {
		label string
		opts  Options
	}{
		{"no stopwords, 1/1", Options{}},
		{"stopwords, 1/1", Options{StopWords: true}},
		{"stopwords, fts x1.5", Options{StopWords: true, FtsWeight: 1.5, VectorWeight: 1}},
		{"stopwords, vec x1.5", Options{StopWords: true, FtsWeight: 1, VectorWeight: 1.5}},
	}
	for _, s := range sweep {
		ix.opts = s.opts.withDefaults()
		ix.opts.Embedder = emb
		m := eval(ix, api.SearchModeHybrid)
		t.Logf("%-34s  %7.4f  %7.4f  %7.4f", s.label, m.ndcgAtK, m.recallAtK, m.mrr)
	}
}

const beirSpace = "beir"

type beirDoc struct {
	id   string
	text string
}

func ingestBEIR(t *testing.T, ix *Indexer, emb Embedder, corpus []beirDoc) {
	t.Helper()
	ctx := context.Background()
	batchSize := envInt("ANY_BEIR_EMBED_BATCH", 64)
	// Concurrency 1 (default) keeps the local llama path safe (one context,
	// not thread-safe); set ANY_BEIR_EMBED_CONCURRENCY>1 for a remote
	// OpenAI-compatible API (HTTP client is concurrency-safe) to fire many
	// batches in parallel — the real speedup for a network embedder.
	conc := max(1, envInt("ANY_BEIR_EMBED_CONCURRENCY", 1))

	type batch struct {
		start int
		texts []string
	}
	var batches []batch
	for i := 0; i < len(corpus); i += batchSize {
		end := min(i+batchSize, len(corpus))
		texts := make([]string, end-i)
		for j := i; j < end; j++ {
			texts[j-i] = corpus[j].text
		}
		batches = append(batches, batch{start: i, texts: texts})
	}

	vecsByBatch := make([][][]float32, len(batches))
	var mu sync.Mutex
	var firstErr error
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for bi := range jobs {
				v, err := emb.EmbedDocs(ctx, batches[bi].texts)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				vecsByBatch[bi] = v
				mu.Unlock()
			}
		}()
	}
	for bi := range batches {
		jobs <- bi
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("embed docs: %v", firstErr)
	}

	// Apply sequentially (store writes are serial); order is irrelevant to
	// correctness since ids are unique per doc.
	for bi, b := range batches {
		vecs := vecsByBatch[bi]
		ups := make([]DocUpsert, len(b.texts))
		for j := range b.texts {
			ups[j] = DocUpsert{
				Entry: index.IndexEntry{
					Scope: index.ScopeBasic, ObjectId: beirSpace, Dataset: beirSpace,
					RecordId: corpus[b.start+j].id, Data: corpus[b.start+j].text,
				},
				Vector: vecs[j],
			}
		}
		if err := ix.store.Apply(ctx, beirSpace, ups, nil, nil); err != nil {
			t.Fatalf("apply [%d:]: %v", b.start, err)
		}
	}
	if ok, err := ix.store.EnsureVectorIndex(ctx, beirSpace); err != nil || !ok {
		t.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
	}
}

// cachingEmbedder memoizes EmbedQuery (queries repeat across modes/knobs);
// EmbedDocs passes through (each doc embedded once at ingest).
type cachingEmbedder struct {
	Embedder
	cache map[string][]float32
}

func (c *cachingEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if c.cache == nil {
		c.cache = map[string][]float32{}
	}
	if v, ok := c.cache[text]; ok {
		return v, nil
	}
	v, err := c.Embedder.EmbedQuery(ctx, text)
	if err == nil {
		c.cache[text] = v
	}
	return v, err
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func readBEIRCorpus(t *testing.T, path string) []beirDoc {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []beirDoc
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var d struct {
			ID    string `json:"_id"`
			Title string `json:"title"`
			Text  string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			continue
		}
		text := strings.TrimSpace(d.Title + "\n" + d.Text)
		if d.ID != "" && text != "" {
			out = append(out, beirDoc{id: d.ID, text: text})
		}
	}
	return out
}

func readBEIRQueries(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var q struct {
			ID   string `json:"_id"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sc.Bytes(), &q); err == nil && q.ID != "" {
			out[q.ID] = q.Text
		}
	}
	return out
}

func readBEIRQrels(t *testing.T, path string) map[string]map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]map[string]bool{}
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first { // header: query-id corpus-id score
			first = false
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		score, _ := strconv.Atoi(parts[2])
		if score <= 0 {
			continue
		}
		if out[parts[0]] == nil {
			out[parts[0]] = map[string]bool{}
		}
		out[parts[0]][parts[1]] = true
	}
	return out
}

// capBEIRCorpus optionally subsamples to max docs while keeping every doc
// that is relevant to some qrel query (so recall stays well-defined).
func capBEIRCorpus(corpus []beirDoc, qrels map[string]map[string]bool, max int) []beirDoc {
	if max <= 0 || len(corpus) <= max {
		return corpus
	}
	rel := map[string]bool{}
	for _, docs := range qrels {
		for d := range docs {
			rel[d] = true
		}
	}
	out := make([]beirDoc, 0, max)
	for _, d := range corpus {
		if rel[d.id] {
			out = append(out, d)
		}
	}
	for _, d := range corpus {
		if len(out) >= max {
			break
		}
		if !rel[d.id] {
			out = append(out, d)
		}
	}
	return out
}
