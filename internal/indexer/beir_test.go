//go:build fts && vector && !gomobile

package indexer

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"math"
	"path/filepath"
	"sort"
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
	// docVecs are the full-precision (L2-normalized) doc vectors, kept for
	// the exact-vs-IVF comparison below.
	ix := newEvalIndexer(t, emb, Options{StopWords: true})
	docVecs := ingestBEIR(t, ix, emb, corpus)

	t.Logf("=== %s: production knobs (stopwords on, RRF 1/1, floor 0) — nDCG@%d / recall@%d / MRR ===", name, k, k)
	t.Logf("%-8s  %7s  %7s  %7s", "mode", "ndcg", "recall", "mrr")
	for _, mode := range []string{api.SearchModeFTS, api.SearchModeVector, api.SearchModeHybrid} {
		m := eval(ix, mode)
		t.Logf("%-8s  %7.4f  %7.4f  %7.4f", mode, m.ndcgAtK, m.recallAtK, m.mrr)
	}

	// Exact (brute-force cosine over full-precision vectors) vs the IVF-SQ
	// approximate index — isolates how much recall the index costs. Gated
	// (extra pass): ANY_BEIR_EXACT=1.
	if envInt("ANY_BEIR_EXACT", 0) != 0 {
		ix.opts = Options{StopWords: true}.withDefaults() // production knobs
		ix.opts.Embedder = emb
		const fetch = 30 // matches Search's over-fetch for limit=10
		var sumV, sumH metrics
		for _, qq := range qs {
			qv, err := emb.EmbedQuery(ctx, qq.text)
			if err != nil {
				t.Fatalf("embed query %q: %v", qq.id, err)
			}
			exact := cosineTopK(l2norm(qv), docVecs, fetch)
			mv := evalOne(exact[:min(k, len(exact))], qq.rel, k)
			// Exact-hybrid: fuse the FTS leg with the exact vector leg.
			ftsResp, err := ix.Search(ctx, beirSpace, api.SearchRequest{Query: qq.text, Mode: api.SearchModeFTS, Limit: fetch})
			if err != nil {
				t.Fatalf("fts %q: %v", qq.id, err)
			}
			var ftsIDs []string
			seen := map[string]bool{}
			for _, h := range ftsResp.Hits {
				if !seen[h.RecordId] {
					seen[h.RecordId] = true
					ftsIDs = append(ftsIDs, h.RecordId)
				}
			}
			fused := fuseRRF([][]Hit{beirHits(ftsIDs), beirHits(exact)}, nil, k)
			var hIDs []string
			for _, h := range fused {
				hIDs = append(hIDs, h.RecordId)
			}
			mh := evalOne(hIDs, qq.rel, k)
			sumV.recallAtK += mv.recallAtK
			sumV.mrr += mv.mrr
			sumV.ndcgAtK += mv.ndcgAtK
			sumH.recallAtK += mh.recallAtK
			sumH.mrr += mh.mrr
			sumH.ndcgAtK += mh.ndcgAtK
		}
		n := float64(len(qs))
		t.Logf("=== %s: EXACT (brute-force) vs IVF — nDCG@%d / recall@%d / MRR ===", name, k, k)
		t.Logf("%-16s  %7.4f  %7.4f  %7.4f", "exact vector", sumV.ndcgAtK/n, sumV.recallAtK/n, sumV.mrr/n)
		t.Logf("%-16s  %7.4f  %7.4f  %7.4f", "exact hybrid", sumH.ndcgAtK/n, sumH.recallAtK/n, sumH.mrr/n)
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

type vecDoc struct {
	id  string
	vec []float32 // L2-normalized
}

// ingestBEIR embeds + indexes the corpus and returns the full-precision
// (L2-normalized) doc vectors for the exact-vs-IVF comparison.
func ingestBEIR(t *testing.T, ix *Indexer, emb Embedder, corpus []beirDoc) []vecDoc {
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
	out := make([]vecDoc, len(corpus))
	for bi, b := range batches {
		vecs := vecsByBatch[bi]
		ups := make([]DocUpsert, len(b.texts))
		for j := range b.texts {
			idx := b.start + j
			ups[j] = DocUpsert{
				Entry: index.IndexEntry{
					Scope: index.ScopeBasic, ObjectId: beirSpace, Dataset: beirSpace,
					RecordId: corpus[idx].id, Data: corpus[idx].text,
				},
				Vector: vecs[j], // store normalizes internally for cosine
			}
			out[idx] = vecDoc{id: corpus[idx].id, vec: l2norm(vecs[j])}
		}
		if err := ix.store.Apply(ctx, beirSpace, ups, nil, nil); err != nil {
			t.Fatalf("apply [%d:]: %v", b.start, err)
		}
	}
	if ok, err := ix.store.EnsureVectorIndex(ctx, beirSpace); err != nil || !ok {
		t.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
	}
	return out
}

// l2norm returns a unit-length copy of v (cosine == dot of normalized).
func l2norm(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	if s == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(s))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// cosineTopK returns the top-n doc ids by cosine to the (normalized) query
// — exact brute force, the ground truth the IVF index approximates.
func cosineTopK(qn []float32, docs []vecDoc, n int) []string {
	type sc struct {
		id string
		s  float32
	}
	scored := make([]sc, len(docs))
	for i, d := range docs {
		var dot float32
		for k := range qn {
			dot += qn[k] * d.vec[k]
		}
		scored[i] = sc{d.id, dot}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].s > scored[j].s })
	m := min(n, len(scored))
	out := make([]string, m)
	for i := 0; i < m; i++ {
		out[i] = scored[i].id
	}
	return out
}

func beirHits(ids []string) []Hit {
	h := make([]Hit, len(ids))
	for i, id := range ids {
		h[i] = Hit{Dataset: beirSpace, RecordId: id}
	}
	return h
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
