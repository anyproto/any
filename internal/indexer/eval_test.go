//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/index"
)

// Labeled-query search-quality eval harness (chunker-hybrid-search-report
// § P2, pulled earlier to P0 so the hybrid knobs are tuned against signal
// rather than guessed). It ingests one corpus two ways — "fragmented"
// (one index doc per block, today's behavior) and "coalesced" (blocks
// grouped into ~window-sized docs, the proposed chunker) — runs a labeled
// query set through Indexer.Search in fts/vector/hybrid, and reports
// recall@k / MRR / nDCG@k per (strategy, mode).
//
// Relevance is judged at OBJECT granularity: a hit on any block/window of
// the right object counts, so the metric is stable across chunking
// strategies (which change the doc unit but not which object is right).
//
// Two embedders: a deterministic bag-of-words hash embedder (default —
// fast, no model, makes vector ≈ lexical so the scaffold is reproducible
// in CI) and, opt-in via ANY_EVAL_EMBEDDER=local|ollama|openai, the real
// configured embedder (the only way to measure genuine semantic recall).
//
// Run: go test -tags 'fts vector' -run TestSearchEval -v ./internal/indexer
//      ANY_EVAL_EMBEDDER=ollama go test -tags 'fts vector' -run TestSearchEval -v ./internal/indexer

// evalObject is one logical object: an ordered list of blocks. A block
// prefixed with "# " is a heading (the coalescer breaks windows there).
type evalObject struct {
	id     string
	scope  string
	blocks []string
}

// evalQuery is a natural-language query with the set of object ids that
// should be retrieved for it.
type evalQuery struct {
	q        string
	relevant []string
}

// evalCorpus mixes rich paragraphs with short-fragment objects (the
// report's noise case: single-word headings/list items that each become a
// standalone embedding + BM25 doc under the fragmented strategy).
var evalCorpus = []evalObject{
	{id: "crm", scope: index.ScopeBasic, blocks: []string{
		"# CRM sync setup",
		"We connect the CRM to the warehouse with a nightly job that pulls contacts and deals.",
		"The sync runs at 02:00 UTC and writes into the staging schema before the merge step.",
		"Failures page the on-call engineer; retries use exponential backoff up to five attempts.",
	}},
	{id: "deploy", scope: index.ScopeBasic, blocks: []string{
		"# Deployment pipeline",
		"Pushes to main trigger a build, run the test suite, then ship a container image.",
		"Rollouts are canary first: ten percent of traffic, watch error rates, then full.",
		"A failed canary auto-rolls-back to the previous image within two minutes.",
	}},
	{id: "vector-search", scope: index.ScopeBasic, blocks: []string{
		"# How semantic search works",
		"Documents are embedded into dense vectors and indexed with an approximate nearest neighbor structure.",
		"At query time the question is embedded and the closest passages are returned by cosine similarity.",
		"Hybrid retrieval fuses these dense results with a keyword BM25 ranking for better recall.",
	}},
	{id: "sourdough", scope: index.ScopeBasic, blocks: []string{
		"# Sourdough starter",
		"Feed equal weights of flour and water once a day and keep it at room temperature.",
		"It is ready to bake with when it doubles in size within four to six hours of feeding.",
		"A mature starter smells pleasantly sour, not like acetone.",
	}},
	{id: "oncall", scope: index.ScopeBasic, blocks: []string{
		"# On-call rotation",
		"Each engineer takes one week of primary on-call per quarter.",
		"Acknowledge pages within five minutes; escalate to secondary after fifteen.",
		"Post-incident reviews are blameless and written within two business days.",
	}},
	{id: "budget", scope: index.ScopeBasic, blocks: []string{
		"# Quarterly budget review",
		"Cloud spend grew twelve percent quarter over quarter, driven by the new GPU fleet.",
		"We are renegotiating the storage contract to claw back roughly fifteen thousand a month.",
		"Headcount stays flat through the next two quarters.",
	}},
	{id: "auth", scope: index.ScopeBasic, blocks: []string{
		"# Authentication",
		"Accounts are derived from a mnemonic seed phrase; the same phrase restores the same account.",
		"Each device gets a fresh device key — copying the wallet file collides peer ids and breaks sync.",
		"There is no password; the mnemonic is the only recovery path, so store it offline.",
	}},
	// Short-fragment objects — the report's noise case. Each block is a
	// standalone tiny doc under the fragmented strategy.
	{id: "glossary", scope: index.ScopeBasic, blocks: []string{
		"# Glossary",
		"CRM", "BM25", "IVF", "RRF", "ANN", "GPU", "TTL", "CID",
	}},
	{id: "tags", scope: index.ScopeBasic, blocks: []string{
		"# Tags", "draft", "done", "urgent", "later", "idea", "✍️",
	}},
	// Chat-scope object.
	{id: "chat-deploy", scope: index.ScopeChat, blocks: []string{
		"can someone explain how the rollback works when a canary fails?",
		"yeah the deploy auto reverts to the last good image if error rates spike",
		"nice, and that happens automatically within a couple minutes right",
	}},
	{id: "chat-bread", scope: index.ScopeChat, blocks: []string{
		"my sourdough never rises, what am I doing wrong",
		"is your starter active? it should double in a few hours after you feed it",
		"try keeping it warmer, room temp matters a lot",
	}},
}

var evalQueries = []evalQuery{
	{q: "how do we set up the CRM sync to the warehouse", relevant: []string{"crm"}},
	{q: "what happens when a canary deployment fails", relevant: []string{"deploy", "chat-deploy"}},
	{q: "how does semantic vector search and hybrid retrieval work", relevant: []string{"vector-search"}},
	{q: "why is my sourdough starter not rising", relevant: []string{"sourdough", "chat-bread"}},
	{q: "how fast must I acknowledge an on-call page", relevant: []string{"oncall"}},
	{q: "why did cloud spending go up this quarter", relevant: []string{"budget"}},
	{q: "how do I recover my account if I lose my device", relevant: []string{"auth"}},
	{q: "what does RRF mean", relevant: []string{"glossary"}},
}

// --- ingest strategies ---------------------------------------------------

// fragmentedDocs emits one index doc per block (today's chunker shape).
func fragmentedDocs(o evalObject) []index.IndexEntry {
	var out []index.IndexEntry
	seq := uint64(0)
	for i, b := range o.blocks {
		seq++
		out = append(out, index.IndexEntry{
			Scope:    o.scope,
			ObjectId: o.id,
			Dataset:  "editor_blocks",
			RecordId: fmt.Sprintf("b%d", i),
			Data:     strings.TrimPrefix(b, "# "),
			ApplySeq: seq,
		})
	}
	return out
}

// coalescedDocs groups consecutive blocks into ~budget-char windows,
// breaking before a heading. The heading text leads the window (the
// report's "poor-man's field boost"). Mirrors the proposed editor chunker.
func coalescedDocs(o evalObject) []index.IndexEntry {
	const budget = 280 // small corpus → small windows so >1 window forms
	var out []index.IndexEntry
	seq := uint64(0)
	var cur []string
	anchor := 0
	flush := func(nextAnchor int) {
		if len(cur) == 0 {
			return
		}
		seq++
		out = append(out, index.IndexEntry{
			Scope:    o.scope,
			ObjectId: o.id,
			Dataset:  "editor_blocks",
			RecordId: fmt.Sprintf("win:b%d", anchor),
			Data:     strings.Join(cur, "\n"),
			ApplySeq: seq,
		})
		cur = nil
		anchor = nextAnchor
	}
	for i, b := range o.blocks {
		isHeading := strings.HasPrefix(b, "# ")
		text := strings.TrimPrefix(b, "# ")
		curLen := 0
		for _, c := range cur {
			curLen += len(c) + 1
		}
		if len(cur) > 0 && (isHeading || curLen+len(text) > budget) {
			flush(i)
		}
		cur = append(cur, text)
	}
	flush(len(o.blocks))
	return out
}

// --- deterministic hash embedder ----------------------------------------

// hashEmbedder maps text to an L2-normalized bag-of-words vector: each
// token bumps a hashed dimension. Deterministic and model-free, so the
// harness runs in CI; vector similarity collapses to lexical overlap, so
// it measures the pipeline/fusion machinery, NOT real semantic recall
// (use ANY_EVAL_EMBEDDER for that). It still reproduces the short-chunk
// noise effect: a one-word doc becomes a near-one-hot vector.
type hashEmbedder struct{ dim int }

func (e hashEmbedder) embed(text string) []float32 {
	v := make([]float32, e.dim)
	for _, tok := range tokenize(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		v[h.Sum32()%uint32(e.dim)]++
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm > 0 {
		inv := float32(1 / math.Sqrt(norm))
		for i := range v {
			v[i] *= inv
		}
	}
	return v
}

func (e hashEmbedder) EmbedDocs(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.embed(t)
	}
	return out, nil
}

func (e hashEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return e.embed(text), nil
}

func (e hashEmbedder) Dim(context.Context) (int, error) { return e.dim, nil }

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// --- metrics -------------------------------------------------------------

type metrics struct {
	recallAtK float64
	mrr       float64
	ndcgAtK   float64
}

// rankedObjects dedups a hit list to object ids in rank order (best rank
// per object wins, since any block of a relevant object satisfies it).
func rankedObjects(hits []api.SearchHit) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hits {
		if !seen[h.ObjectId] {
			seen[h.ObjectId] = true
			out = append(out, h.ObjectId)
		}
	}
	return out
}

func evalOne(ranked []string, relevant []string, k int) metrics {
	rel := map[string]bool{}
	for _, r := range relevant {
		rel[r] = true
	}
	var hitsInK int
	var mrr, dcg float64
	for i, obj := range ranked {
		if !rel[obj] {
			continue
		}
		if i < k {
			hitsInK++
			dcg += 1 / math.Log2(float64(i+2))
		}
		if mrr == 0 {
			mrr = 1 / float64(i+1)
		}
	}
	// Ideal DCG: all relevant docs at the top (capped at k).
	ideal := len(relevant)
	if ideal > k {
		ideal = k
	}
	var idcg float64
	for i := 0; i < ideal; i++ {
		idcg += 1 / math.Log2(float64(i+2))
	}
	m := metrics{mrr: mrr}
	if len(relevant) > 0 {
		m.recallAtK = float64(hitsInK) / float64(len(relevant))
	}
	if idcg > 0 {
		m.ndcgAtK = dcg / idcg
	}
	return m
}

// --- runner --------------------------------------------------------------

func newEvalIndexer(t *testing.T, emb Embedder, opts Options) *Indexer {
	t.Helper()
	dim, err := emb.Dim(context.Background())
	if err != nil {
		t.Fatalf("embedder dim: %v", err)
	}
	store, err := OpenStoreInMemory(context.Background(), dim, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	opts.Embedder = emb
	return &Indexer{
		store: store,
		opts:  opts.withDefaults(),
		lg:    logger.NewNamed("indexer-eval"),
	}
}

// ingest lands every entry with its embedding, then builds the vector
// index — bypassing the SDK-driven advance/embed loops (this harness
// controls chunking itself, the point being to compare strategies).
func ingest(t *testing.T, ix *Indexer, emb Embedder, entries []index.IndexEntry) {
	t.Helper()
	ctx := context.Background()
	texts := make([]string, len(entries))
	for i, e := range entries {
		texts[i] = e.Data
	}
	vecs, err := emb.EmbedDocs(ctx, texts)
	if err != nil {
		t.Fatalf("embed docs: %v", err)
	}
	ups := make([]DocUpsert, len(entries))
	for i, e := range entries {
		ups[i] = DocUpsert{Entry: e, Vector: vecs[i]}
	}
	if err := ix.store.Apply(ctx, "eval", ups, nil, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ok, err := ix.store.EnsureVectorIndex(ctx, "eval"); err != nil || !ok {
		t.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
	}
}

// runStrategy ingests the corpus with the given chunking strategy and
// returns mean metrics per mode, under the given ranking knobs.
func runStrategy(t *testing.T, emb Embedder, chunk func(evalObject) []index.IndexEntry, k int, opts Options) map[string]metrics {
	t.Helper()
	ix := newEvalIndexer(t, emb, opts)
	var entries []index.IndexEntry
	for _, o := range evalCorpus {
		entries = append(entries, chunk(o)...)
	}
	ingest(t, ix, emb, entries)

	modes := []string{api.SearchModeFTS, api.SearchModeVector, api.SearchModeHybrid}
	sum := map[string]metrics{}
	for _, eq := range evalQueries {
		for _, mode := range modes {
			resp, err := ix.Search(context.Background(), "eval", api.SearchRequest{
				Query: eq.q, Mode: mode, Limit: k,
			}, nil)
			if err != nil {
				t.Fatalf("search %q mode=%s: %v", eq.q, mode, err)
			}
			m := evalOne(rankedObjects(resp.Hits), eq.relevant, k)
			agg := sum[mode]
			agg.recallAtK += m.recallAtK
			agg.mrr += m.mrr
			agg.ndcgAtK += m.ndcgAtK
			sum[mode] = agg
		}
	}
	n := float64(len(evalQueries))
	for mode, agg := range sum {
		sum[mode] = metrics{agg.recallAtK / n, agg.mrr / n, agg.ndcgAtK / n}
	}
	return sum
}

// realEvalEmbedder builds the embedder named by ANY_EVAL_EMBEDDER, reusing
// an already-downloaded local model via ANY_EVAL_LOCAL_MODEL /
// ANY_EVAL_LOCAL_LIBDIR (so the eval runs against the SAME model the
// server uses, no re-download). Returns nil when ANY_EVAL_EMBEDDER unset.
func realEvalEmbedder(t *testing.T) (string, Embedder) {
	t.Helper()
	name := os.Getenv("ANY_EVAL_EMBEDDER")
	if name == "" {
		return "", nil
	}
	cfg := config.Index{
		Embedder: name,
		OpenAI: config.IndexOpenAI{
			BaseUrl: os.Getenv("ANY_EVAL_OPENAI_BASE_URL"), // an OpenAI-compatible host serving the eval model
			Model:   os.Getenv("ANY_EVAL_OPENAI_MODEL"),    // e.g. Qwen/Qwen3-Embedding-0.6B
			ApiKey:  os.Getenv("ANY_EVAL_OPENAI_API_KEY"),
		},
		Local: config.IndexLocal{
			ModelPath: os.Getenv("ANY_EVAL_LOCAL_MODEL"),
			LibDir:    os.Getenv("ANY_EVAL_LOCAL_LIBDIR"),
		},
	}
	e, err := NewEmbedder(cfg, t.TempDir(), "", nil)
	if err != nil || e == nil {
		t.Fatalf("real embedder %q: %v", name, err)
	}
	// Instruction-tuned models (e5-instruct, bge) need a query-side
	// instruction prefix to retrieve well; the OpenAI client sends raw
	// text, so apply it here for the query leg only (docs stay raw).
	if p := os.Getenv("ANY_EVAL_QUERY_PREFIX"); p != "" {
		e = prefixQueryEmbedder{Embedder: e, prefix: p}
	}
	return name, e
}

// prefixQueryEmbedder prepends an instruction to EmbedQuery inputs only.
type prefixQueryEmbedder struct {
	Embedder
	prefix string
}

func (p prefixQueryEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return p.Embedder.EmbedQuery(ctx, p.prefix+text)
}

func TestSearchEval(t *testing.T) {
	const k = 5
	embedders := map[string]Embedder{"hash": hashEmbedder{dim: 256}}
	if name, real := realEvalEmbedder(t); real != nil {
		embedders[name] = real
	}

	strategies := []struct {
		name  string
		chunk func(evalObject) []index.IndexEntry
	}{
		{"fragmented", fragmentedDocs},
		{"coalesced", coalescedDocs},
	}

	// Production-like defaults: stop-word stripping on, plain RRF, legacy
	// floor.
	baseOpts := Options{StopWords: true}
	for ename, emb := range embedders {
		t.Logf("=== embedder: %s (recall@%d / MRR / nDCG@%d, mean over %d queries) ===", ename, k, k, len(evalQueries))
		t.Logf("%-12s %-8s  %6s  %6s  %6s", "strategy", "mode", "rec", "mrr", "ndcg")
		var fragHybrid, coalHybrid metrics
		for _, st := range strategies {
			res := runStrategy(t, emb, st.chunk, k, baseOpts)
			for _, mode := range []string{api.SearchModeFTS, api.SearchModeVector, api.SearchModeHybrid} {
				m := res[mode]
				t.Logf("%-12s %-8s  %6.3f  %6.3f  %6.3f", st.name, mode, m.recallAtK, m.mrr, m.ndcgAtK)
			}
			switch st.name {
			case "fragmented":
				fragHybrid = res[api.SearchModeHybrid]
			case "coalesced":
				coalHybrid = res[api.SearchModeHybrid]
			}
		}
		// Smoke floor: the pipeline must actually retrieve. Loose on
		// purpose — this guards wiring regressions, not quality targets
		// (the printed table is the tuning signal).
		if fragHybrid.recallAtK == 0 {
			t.Errorf("[%s] fragmented hybrid recall@%d is 0 — pipeline broken", ename, k)
		}
		_ = coalHybrid // reported above; no hard assertion (strategy delta is the study, not an invariant)
	}
}

// TestSearchEvalKnobs sweeps the P0 ranking knobs (stop-words, RRF
// weights, vector floor) on the fragmented corpus — the noisy case where
// equal-weight hybrid underperforms FTS — so their effect is measurable
// before the chunker rewrite. Reports only; no assertion (the table is
// the tuning signal). Use ANY_EVAL_EMBEDDER for real-embedder numbers,
// where the weight/floor knobs matter most.
func TestSearchEvalKnobs(t *testing.T) {
	const k = 5
	emb := Embedder(hashEmbedder{dim: 256})
	ename := "hash"
	if name, real := realEvalEmbedder(t); real != nil {
		emb, ename = real, name
	}

	knobs := []struct {
		name string
		opts Options
	}{
		{"baseline (no stopwords, 1/1, floor 0)", Options{}},
		{"stopwords", Options{StopWords: true}},
		{"stopwords + vec×0.5", Options{StopWords: true, VectorWeight: 0.5}},
		{"stopwords + vec×0.3 + floor .3", Options{StopWords: true, VectorWeight: 0.3, MinVectorSim: 0.3}},
	}

	t.Logf("=== knob sweep on FRAGMENTED corpus, embedder=%s (hybrid mode, recall@%d / MRR / nDCG@%d) ===", ename, k, k)
	t.Logf("%-40s  %6s  %6s  %6s", "knobs", "rec", "mrr", "ndcg")
	for _, kn := range knobs {
		res := runStrategy(t, emb, fragmentedDocs, k, kn.opts)
		m := res[api.SearchModeHybrid]
		t.Logf("%-40s  %6.3f  %6.3f  %6.3f", kn.name, m.recallAtK, m.mrr, m.ndcgAtK)
	}
}

// guard against accidental metric math regressions.
func TestEvalMetrics(t *testing.T) {
	// Perfect ranking: the one relevant doc at rank 0.
	m := evalOne([]string{"a", "b", "c"}, []string{"a"}, 5)
	if m.recallAtK != 1 || m.mrr != 1 || math.Abs(m.ndcgAtK-1) > 1e-9 {
		t.Fatalf("perfect: %+v", m)
	}
	// Relevant doc at rank 2 (0-based): recall 1, mrr 1/3, ndcg = (1/log2(4))/1.
	m = evalOne([]string{"x", "y", "a"}, []string{"a"}, 5)
	wantNdcg := (1 / math.Log2(4)) / 1
	if m.recallAtK != 1 || math.Abs(m.mrr-1.0/3) > 1e-9 || math.Abs(m.ndcgAtK-wantNdcg) > 1e-9 {
		t.Fatalf("rank2: %+v (wantNdcg=%v)", m, wantNdcg)
	}
	// Miss: relevant doc absent.
	m = evalOne([]string{"x", "y"}, []string{"a"}, 5)
	if m.recallAtK != 0 || m.mrr != 0 || m.ndcgAtK != 0 {
		t.Fatalf("miss: %+v", m)
	}
	// Two relevant, one found within k: recall 0.5.
	m = evalOne([]string{"a", "x"}, []string{"a", "b"}, 5)
	if m.recallAtK != 0.5 {
		t.Fatalf("partial recall: %+v", m)
	}
}
