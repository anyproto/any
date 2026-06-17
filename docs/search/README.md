# Search — chunking & hybrid-ranking evaluation

This folder is the **evaluation and decision record** for the local search
index: why the chunker and hybrid ranking are shaped the way they are, and
the measured evidence behind the defaults. The **implementation contract**
(chunker interface, store layout, removal semantics, endpoints) lives in
[`../13-index.md`](../13-index.md) — read that for *how* it works; read
this for *why* and *how well*.

## TL;DR

- The original index emitted **one doc per editor block** → chunks were
  tiny (editor mean ~98 chars, ~half under 50), which hurts vector recall
  and BM25 length normalization.
- We switched editor to **coalesced ~1.5 KB windows**, added **memory**
  and **program-doc** indexing, excluded **debug logs** and **program
  source**, made re-embedding **incremental** (content hash), and added
  three hybrid knobs (**stop-words**, **weighted RRF**, **vector floor**).
- Measured on a real labeled benchmark (BEIR SciFact, exact deployed
  model): **hybrid > vector > fts**, **equal RRF weights are optimal**,
  and a **static vector-similarity floor doesn't work for this model**.

## What changed (and where)

| Area | Change | Reference |
|---|---|---|
| Editor | one-doc-per-block → coalesced windows (heading + ~1.5 KB budget), `recordId = win_<anchor>` | `internal/editor/window.go`, `chunker.go` |
| Memory | `agent_memory_items` indexed per-record, scope `agent` | `internal/agentmem/chunker.go` |
| Programs | `program_description` + `program_methods` indexed (scope `program`); **source not** indexed | `internal/program/chunker.go` |
| Debug | `agent_debug_log` excluded — no chunker, and prop chunker skips debug objects | `internal/server/sdk.go`, `internal/index/prop.go` |
| Incremental embed | per-doc content hash → reconcile diff (editor) + per-record no-op skip (chat/memory) | `internal/indexer/{worker,store}.go` |
| Hybrid knobs | `index.search.stopWords` / `ftsWeight` / `vectorWeight` / `minVectorSim` | `internal/indexer/{indexer,rrf,store,stopwords}.go`, `../05-config.md` |

## Evaluation harnesses

All gated/opt-in tests in `internal/indexer` (build tags `fts vector`):

- **`eval_test.go`** — labeled-query harness over a small synthetic
  corpus, scoring recall@k / MRR / nDCG@k for fragmented-vs-coalesced ×
  fts/vector/hybrid, plus a knob sweep. Deterministic hash embedder by
  default; real embedder via `ANY_EVAL_EMBEDDER`.
- **`beir_test.go`** — standard labeled IR benchmark (BEIR format:
  `corpus.jsonl` / `queries.jsonl` / `qrels/test.tsv`). Reports nDCG@10 /
  recall@10 / MRR per mode + knob sweep. Parallel API embedding.
- **`live_probe_test.go`** — probes the cosine-score distribution of a
  **real** index (on-topic vs off-topic vs nonsense) to evaluate the
  vector-similarity floor.
- **`append_scaling_test.go`** — reconcile read-cost scaling (append path).

### Reproduce

Live chunk-length distribution (server stopped — it holds the DB open):

```bash
C=<spaceCollectionName>   # = spaceId; contains '.', so use db['<C>']
{ echo "db['$C'].find({}).project({data:1,dataset:1})"; \
  for i in $(seq 1 400); do echo it; done; echo exit; } \
| any-store-cli2 ~/.any/index/index.db 2>/dev/null | grep '^{' > /tmp/idx.jsonl
# then bucket len(.data) per .dataset
```

BEIR with an OpenAI-compatible API (DeepInfra hosts the exact model;
parallel batches make it ~minutes instead of ~35 min on local CPU):

```bash
curl -sSL -o /tmp/scifact.zip \
  https://public.ukp.informatik.tu-darmstadt.de/thakur/BEIR/datasets/scifact.zip
unzip -d /tmp -o /tmp/scifact.zip

ANY_BEIR_DIR=/tmp/scifact \
ANY_EVAL_EMBEDDER=openai \
ANY_EVAL_OPENAI_BASE_URL=https://api.deepinfra.com/v1/openai \
ANY_EVAL_OPENAI_MODEL=Qwen/Qwen3-Embedding-0.6B \
ANY_EVAL_OPENAI_API_KEY=$DEEPINFRA_KEY \
ANY_EVAL_QUERY_PREFIX=$'Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:' \
ANY_BEIR_EMBED_CONCURRENCY=8 ANY_BEIR_EMBED_BATCH=100 \
go test -tags 'fts vector' -run TestSearchEvalBEIR -v -timeout 20m ./internal/indexer
```

Provider notes: **OpenRouter** has no embeddings; **Together** lacks
Qwen3 and caps e5 at 512 tokens (rejects SciFact abstracts); **DeepInfra**
hosts `Qwen/Qwen3-Embedding-0.6B` (fp16 vs the local Q8). For the local
model itself, set `ANY_EVAL_EMBEDDER=local ANY_EVAL_LOCAL_MODEL=… ANY_EVAL_LOCAL_LIBDIR=…`.

## Results

### Chunk-length distribution — before/after (live `bao` index)

| editor_blocks | mean | p50 | p90 | % < 50 chars |
|---|---|---|---|---|
| before (one doc per block) | 98 | 49 | — | ~50% |
| after (coalesced windows) | **392** | **255** | 972 | **9%** |

~4× larger chunks; the tiny-fragment share fell from ~half to ~9%. Other
datasets land sensibly (program description ~1814, methods ~590, memory
~105, chat ~232). `prop` stays tiny (~13) — names/tags, exact-match FTS
docs.

### BEIR SciFact — 5183 docs / 300 queries, Qwen3-Embedding-0.6B

Production knobs (stop-words on, RRF 1/1, floor 0):

| mode | nDCG@10 | recall@10 | MRR |
|---|---|---|---|
| fts (BM25) | 0.667 | 0.793 | 0.635 |
| vector | 0.677 | 0.796 | 0.646 |
| **hybrid** | **0.695** | **0.834** | **0.658** |

Hybrid knob sweep: no-stopwords `0.6944` ≈ stopwords `0.6945`; fts×1.5
`0.6815`; vec×1.5 `0.6779` (nDCG@10).

- **Hybrid beats both legs** — RRF fusion earns its place on a corpus
  with real distractors (recall@10 0.834 vs ~0.79).
- **Equal RRF weights (1/1) are optimal** — tilting toward either leg
  lowers nDCG.
- **Stop-words ≈ neutral on SciFact** (terse claim queries); they help
  conversational queries and don't hurt here, so they stay on.
- **FTS ≈ reference BM25** (published SciFact BM25 ~0.665) — the lexical
  leg is implemented correctly.
- **The vector gap was the approximate index, not the model** — see below.

### Index mode: HNSW vs IVF-SQ vs exact (the vector-recall fix)

The vector leg first measured below the model card (~0.74). Comparing ANN
strategies on the same SciFact set + vectors (`ANY_INDEX_VECTOR_MODE`,
plus an in-test exact brute force) isolated the cause:

| index mode | vector nDCG@10 | vector recall@10 | hybrid nDCG@10 | hybrid recall@10 |
|---|---|---|---|---|
| IVF-SQ (old default) | 0.681 | 0.801 | 0.700 | 0.834 |
| **HNSW / btree (new default)** | **0.703** | **0.837** | **0.724** | **0.855** |
| brute force (exact) | 0.701 | 0.831 | 0.723 | 0.855 |

- **IVF-SQ was leaving ~3 recall@10 points (and ~2 nDCG) on the table** —
  its default NProbe (16) scans only ~12% of cells on this corpus.
- **HNSW ≈ exact**, at log(N) search cost (vs brute force's O(N)) — it
  recovers essentially all the loss. Exact vector (0.70) ≈ the model
  card modulo fp16/Q8 + formatting, so the model was never the problem.
- **Decision: default `index.vector.mode` = HNSW (`btree`).** IVF-SQ stays
  available for very large spaces (cheapest build / lowest RAM);
  `bruteforce` is exact for small spaces; `hybrid` adds a RAM cache for
  latency. Existing indexes keep their mode until rebuilt.

### Vector similarity floor — measured, kept at 0

Cosine scores on the live index (`live_probe_test.go`):

| query class | top-1 | top-10 |
|---|---|---|
| on-topic | 0.66–0.80 | 0.59–0.69 |
| off-topic (real, unrelated) | 0.45–0.54 | 0.42–0.52 |
| **nonsense (gibberish)** | **0.59–0.69** | **0.57–0.66** |

Gibberish scores **as high as on-topic** (an OOV-near-centroid trait of
the Qwen instruction model), and above real off-topic queries — so no
absolute cosine cutoff separates signal from noise. `minVectorSim`
defaults to **0**; rely on RRF + FTS for discrimination. The lever
remains for better-calibrated embedders.

## Decisions / defaults

| Knob | Default | Why |
|---|---|---|
| editor chunk unit | coalesced ~1.5 KB windows | distribution + recall (above) |
| `vector.mode` | HNSW (`btree`) | recall ≈ exact; +3 recall@10 vs IVF-SQ |
| memory indexing | per-record, scope `agent` | independently filterable; cheap incremental |
| `ftsWeight` / `vectorWeight` | 1 / 1 | optimal on BEIR; tilting hurts |
| `stopWords` | on | helps conversational, neutral on BEIR |
| `minVectorSim` | 0 | static floor can't separate signal/noise for the local model |

## Open items / follow-ups

- ~~Vector under-performs the model card~~ **RESOLVED** — was the IVF-SQ
  approximate index; switched the default to HNSW (recall ≈ exact, see
  above). Possible follow-up: an HNSW build-time/RAM check on a very large
  space to confirm IVF-SQ stays the right opt-in there.
- **Per-object summary doc** (`name` + `description` + lead paragraph) for
  "what is this object" recall — not built.
- **`EmbedSkip`** — FTS-index short fragments (tiny props) but keep them
  out of the vector population — not built.
- **Real-data labeled set** — BEIR measures the stack on out-of-domain
  scientific text; a small labeled set over actual notes/chat/memory would
  measure our content directly.
- **Reranker tier** — a cross-encoder over the fused top-N would give a
  calibrated relevance score (enabling true "nothing relevant"
  abstention, which the floor can't). Deferred; the RLM `search`/`ask`
  loop ([`../12-rlm-search.md`](../12-rlm-search.md)) is the heavyweight
  stand-in.
- **Upstream FTS asks** — phrase/required-term operators, per-field
  weights, prefix matching, configurable BM25 `b` (`../13-index.md`).
