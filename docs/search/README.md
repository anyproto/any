# Search — chunking & hybrid-ranking evaluation

This folder is the **evaluation and decision record** for the local search
index: why the chunkers and hybrid ranking are shaped the way they are, and
the measured evidence behind the defaults. The **implementation contract**
(chunker interface, store layout, removal semantics, endpoints) lives in
[`../13-index.md`](../13-index.md) — read that for *how* it works; read
this for *why* and *how well*.

## TL;DR

- **Editor content indexes as coalesced ~1.5 KB windows**, not one doc per
  block. One doc per block gives a mean chunk of ~98 chars with about half
  under 50, which hurts vector recall and BM25 length normalization;
  windows raise the mean to **392** chars and cut the tiny-fragment share
  to ~9%.
- **Embedding is incremental**: every index doc carries a content hash, so
  an editor append or a non-text field edit re-embeds only what changed.
- Measured on labeled benchmarks (BEIR SciFact 5k + FiQA 57k,
  Qwen3-Embedding-0.6B):
  - **The ANN index is a trade-off; the default is IVF-SQ.** HNSW has
    ≈-exact recall (IVF-SQ −3–4 recall@10), but on the production
    incremental path HNSW ingest is ~15–47× slower than IVF and
    super-linear, and its deletes tombstone and rebuild. For a local,
    continuously written index that cost outweighs the recall edge. HNSW
    (`index.vector.mode: btree`) is opt-in.
  - **Hybrid vs single-leg is corpus-dependent**: hybrid wins on
    lexical-friendly SciFact; vector alone wins on paraphrastic FiQA
    (weak BM25). Equal RRF weights are a safe default, not a universal
    optimum — the weight knobs are the lever.
  - **A static vector-similarity floor doesn't work for this model**
    (gibberish scores as high as on-topic queries) → `minVectorSim` is 0.
  - FTS matches reference BM25; the model matches its card once the index
    is exact.

## Mechanisms (and where they live)

| Area | Mechanism | Reference |
|---|---|---|
| Editor | coalesced windows (break before each heading, ~1.5 KB budget), `recordId = win_<anchor>` | `internal/editor/window.go`, `chunker.go` |
| Runtime datasets | records indexed by their `x-search` mapping under the declared scope; a dataset without `x-search` is not text-indexed | `internal/index/schema.go` |
| Long records | split into ~2000-rune chunk docs; `limit` counts records, other matching chunks ride as `passages` | `internal/indexer/chunk.go`, `rrf.go` |
| Incremental embed | per-doc content hash → reconcile diff (editor) + per-record skip (streaming chunkers) | `internal/indexer/{worker,store}.go` |
| ANN index | `index.vector.mode` (default **IVF-SQ**; `btree`/`hnsw`, `hybrid`, `bruteforce`) | `internal/indexer/store.go`, `../05-config.md` |
| Embedder fallback | `embedder: auto` — online primary + local fallback, circuit breaker, same model | `internal/indexer/embed_fallback.go` |
| Embed throughput | parallel embed loop, `index.embedConcurrency` (1 local / 4 online) | `internal/indexer/worker.go`, `internal/server/sdk.go` |
| Hybrid knobs | `index.search.stopWords` / `ftsWeight` / `vectorWeight` / `adaptiveWeights` / `defaultOperator` / `minVectorSim` / `titleWeight` / `bm25B` / `bm25K1` | `internal/indexer/{indexer,rrf,store,stopwords}.go`, `../05-config.md` |

## Evaluation harnesses

Gated / opt-in tests in `internal/indexer` (build tags `fts vector` unless
noted):

- **`eval_test.go`** — labeled-query harness over a small synthetic corpus,
  scoring recall@k / MRR / nDCG@k for one-doc-per-block vs coalesced ×
  fts / vector / hybrid, plus a knob sweep. Deterministic hash embedder by
  default; a real embedder via `ANY_EVAL_EMBEDDER=local|ollama|openai`.
- **`beir_test.go`** — the BEIR benchmark format (`corpus.jsonl` /
  `queries.jsonl` / `qrels/test.tsv`). Reports nDCG@10 / recall@10 / MRR
  per mode plus a knob sweep; parallel API embedding.
- **`live_probe_test.go`** — probes the cosine-score distribution of a
  **real** index (on-topic vs off-topic vs nonsense queries) to evaluate
  the vector-similarity floor.
- **`append_scaling_test.go`** (untagged) — reconcile read-cost scaling on
  the append path.
- **`vector_mode_bench_test.go`** — ANN mode cost profiles
  (`TestVectorModeProfile` bulk, `TestVectorModeIngestProfile` bulk vs
  incremental).
- **`cutoff_bench_test.go`** / **`cutoff_leak_test.go`** — what any-store
  charges for reading past a `Limit` (`$text` cursor closed early, `$knn`
  at growing K, the endpoint end to end on a corpus with 17-chunk records)
  and that an early-closed iterator leaks nothing; the numbers behind
  `limit` counting records (`13-index.md` § Tuning).

### Reproduce

Live chunk-length distribution: open `<data-dir>/index/index.db` with the
any-store CLI, read `data` and `dataset` from the space's collection (named
by the space id), and bucket `len(data)` per `dataset`.

BEIR with an OpenAI-compatible API (DeepInfra hosts the exact model;
parallel batches take minutes instead of ~35 min on local CPU):

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

Provider notes: **OpenRouter** has no embeddings; **Together** lacks Qwen3
and caps e5 at 512 tokens (rejects SciFact abstracts); **DeepInfra** hosts
`Qwen/Qwen3-Embedding-0.6B` (fp16 vs the local Q8). For the local model,
set `ANY_EVAL_EMBEDDER=local ANY_EVAL_LOCAL_MODEL=… ANY_EVAL_LOCAL_LIBDIR=…`.
Other BEIR sets drop in by name (e.g. `fiqa.zip`, 57.6k docs); add
`ANY_BEIR_EXACT=1` for the exact-vs-index comparison and
`ANY_INDEX_VECTOR_MODE=btree|ivfsq|bruteforce` to compare ANN modes
(`ANY_BEIR_MAX_DOCS` / `ANY_BEIR_MAX_QUERIES` cap the run).

Index-mode cost profile (random vectors; no API):

```bash
ANY_VEC_BENCH=1 ANY_VEC_BENCH_SIZES=100000,200000 \
go test -tags 'fts vector' -run TestVectorModeProfile -v -timeout 50m ./internal/indexer
```

Live cosine-distribution probe (server stopped — the probe opens the index
db itself):

```bash
ANY_LIVE_INDEX=<data-dir>/index/index.db ANY_LIVE_SPACE=<spaceId> \
ANY_EVAL_LOCAL_MODEL=… ANY_EVAL_LOCAL_LIBDIR=… \
[ANY_LIVE_ONTOPIC="q1,q2" ANY_LIVE_OFFTOPIC="q3,q4"] \
go test -tags 'fts vector' -run TestLiveVectorScoreProbe -v ./internal/indexer
```

## Results

The BEIR and eval numbers below score the top k chunks; `/search` returns
k distinct records (`limit` counts records), which moves recall@k up.

### Chunk-length distribution — one doc per block vs coalesced windows

Measured on a live agent-space index:

| editor_blocks | mean | p50 | p90 | % < 50 chars |
|---|---|---|---|---|
| one doc per block | 98 | 49 | — | ~50% |
| coalesced windows | **392** | **255** | 972 | **9%** |

~4× larger chunks. Other datasets on the same index: memory records ~105,
chat ~232. `prop` stays tiny (~13) — names and tags, exact-match FTS docs.

### Editor reconcile & the append cost (incremental embedding)

A coalesced window spans several blocks, so the editor chunker rebuilds
the object's whole window set on each change rather than streaming a
per-record delta (a window's text needs sibling blocks below the cursor,
and a deleted block's position is gone from its tombstone).

`append_scaling_test.go` measures the cost: re-reading *and re-embedding*
every window on each change is **O(N²)** for a grow-by-append page. A
windowed read does not fit either — editor render order is the **nav-tree
walk** (`treeOrder`), not flat `nav.pos`, so forming windows is an O(doc)
read.

The per-doc **content hash** (FNV-1a of the indexed text and title) makes
embedding incremental:

- **The editor reconcile diffs by hash** (`worker.reconcileMulti`): it
  re-reads blocks to form windows (O(doc), cheap against the local DB) but
  **re-embeds only changed or new windows** and deletes vanished ones. An
  append re-embeds one window, not the document.
- **Per-record hash skip** (streaming chunkers): a record that re-streams
  with unchanged indexed text — a chat message that got a reaction, a
  runtime record whose non-indexed field changed — is **not re-embedded**.

Verified end to end with a counting embedder
(`TestIndexer_EditorReconcileEmbedReuse`): an editor append embeds exactly
the new window.

### BEIR SciFact — 5183 docs / 300 queries, Qwen3-Embedding-0.6B

Production knobs (stop-words on, RRF 1/1, floor 0):

| mode | nDCG@10 | recall@10 | MRR |
|---|---|---|---|
| fts (BM25) | 0.667 | 0.793 | 0.635 |
| vector | 0.677 | 0.796 | 0.646 |
| **hybrid** | **0.695** | **0.834** | **0.658** |

Hybrid knob sweep: no-stopwords `0.6944` ≈ stopwords `0.6945`; fts×1.5
`0.6815`; vec×1.5 `0.6779` (nDCG@10).

- **Hybrid beats both legs** — RRF fusion earns its place on a corpus with
  real distractors (recall@10 0.834 vs ~0.79).
- **Equal RRF weights (1/1) are optimal here** — tilting toward either leg
  lowers nDCG.
- **Stop-words ≈ neutral on SciFact** (terse claim queries); they help
  conversational queries and don't hurt here, so they stay on.
- **FTS ≈ reference BM25** (published SciFact BM25 ~0.665).
- **The vector gap is the approximate index, not the model** — below.

### Index mode: HNSW vs IVF-SQ vs exact — recall vs ingest cost

Recall, on the same SciFact set and vectors (`ANY_INDEX_VECTOR_MODE`, plus
an in-test exact brute force):

| index mode | vector nDCG@10 | vector recall@10 | hybrid nDCG@10 | hybrid recall@10 |
|---|---|---|---|---|
| IVF-SQ (default) | 0.681 | 0.801 | 0.700 | 0.834 |
| HNSW / btree | **0.703** | **0.837** | **0.724** | **0.855** |
| brute force (exact) | 0.701 | 0.831 | 0.723 | 0.855 |

- **IVF-SQ leaves ~3 recall@10 points (~2 nDCG) on the table** — its
  default NProbe (16) scans only ~12% of cells on this corpus (at FiQA's
  57k it probes ~1.7% and the gap widens to ~4 points).
- **HNSW ≈ exact**, at log(N) search. Exact vector (0.70) matches the model
  card modulo fp16/Q8.

Ingest cost reverses the decision. A one-shot **parallel bulk** build shows
HNSW only ~2× slower, but that is not the production path: the embed loop
creates the index after the first ~64-doc batch and every later batch
inserts into the **existing** index — serial, per-doc HNSW graph
maintenance. Measured on that path (`TestVectorModeIngestProfile`, random
dim-1024):

| N | mode | bulk (one-shot) | **incremental (production)** |
|---|---|---|---|
| 5k | ivfsq | 551ms | 296ms |
| 5k | btree (HNSW) | 1.53s | **4.62s** (~15.6×) |
| 20k | ivfsq | 3.80s | 1.11s |
| 20k | btree (HNSW) | 6.33s | **38.0s** (~34×) |

On the real path HNSW ingest is ~15–47× slower than IVF and super-linear
(the gap grows with N), and HNSW deletes tombstone and rebuild while IVF
deletes are physical.

- **Decision: `index.vector.mode` defaults to IVF-SQ.** For a local,
  continuously written index with bulk imports, cheap near-flat ingest and
  physical deletes outweigh ~3–4 recall@10 points, which hybrid softens
  further. **HNSW (`btree`) is the opt-in** for read-heavy deployments;
  `bruteforce` is exact for small spaces. Existing indexes keep their mode
  until rebuilt.

### Index mode at scale: recall on FiQA (57.6k docs, real)

The exact-vs-HNSW-vs-IVF comparison on BEIR **FiQA** (57.6k docs / 648
queries — 11× SciFact):

| mode | nDCG@10 | recall@10 | MRR |
|---|---|---|---|
| fts (BM25) | 0.233 | 0.294 | 0.289 |
| vector — **HNSW** | 0.466 | **0.542** | 0.550 |
| vector — IVF-SQ | 0.440 | 0.507 | 0.528 |
| vector — exact | 0.469 | 0.546 | 0.552 |

- **HNSW ≈ exact** at 57k (0.542 vs 0.546 recall@10). **IVF-SQ loses ~4
  recall@10 / ~2.5 nDCG** — the gap persists and slightly widens.
- Exact 0.469 nDCG matches the Qwen3-0.6B model-card FiQA number.
- **Hybrid is not universally best.** On FiQA *vector alone* beats hybrid
  (0.466 vs 0.385 nDCG): paraphrastic Q&A makes BM25 weak (0.233), and
  equal-weight RRF drags hybrid below pure dense — the opposite of
  SciFact. `ftsWeight` / `vectorWeight` / `adaptiveWeights` are the lever
  for corpora where one leg is much weaker.

### Index mode: cost at scale (build / search / disk)

Profiled with random dim-1024 vectors (`TestVectorModeProfile`,
`ANY_VEC_BENCH=1`). Random vectors are valid for build / latency / disk
but a **worst case for recall** (nearly equidistant in high dimension —
see the ~5% column); real recall comes from BEIR above.

| N | mode | build | search/query | disk | recall* |
|---|---|---|---|---|---|
| 50k | ivfsq | 12.2s | 1.9ms | — | — |
| 50k | btree | 24.1s | 6.1ms | — | — |
| 100k | ivfsq | 33s | 2.6ms | 1362 MB | 0.06* |
| 100k | btree | 64s | 6.8ms | 1378 MB | 0.06* |
| 100k | bruteforce | 0.2s | 237ms | 479 MB | 1.00 |
| 200k | ivfsq | 91s | 3.8ms | 2716 MB | 0.04* |
| 200k | btree | 152s | 7.4ms | 2756 MB | 0.04* |
| 200k | bruteforce | 0.3s | 472ms | 918 MB | 1.00 |

This is the **one-shot bulk build** path, not the production incremental
ingest (§ Index mode: HNSW vs IVF-SQ vs exact).

- **Search latency**: both ANN modes < 10 ms (IVF ~2× faster); brute force
  (237–472 ms) is unusable past a few thousand docs.
- **Disk: ~no difference** (IVF-SQ 1362 vs HNSW 1378 MB): the collection
  stores full float32 vectors regardless of mode, and SQ compresses only
  the index portion. Brute force is ~half the size (no index). IVF-SQ's
  advantage is ingest cost and churn, **not** disk or RAM.
- **Build is super-linear** for both; the HNSW penalty compounds on the
  serial incremental path.

### Vector similarity floor — measured, kept at 0

Cosine scores on a live index (`live_probe_test.go`):

| query class | top-1 | top-10 |
|---|---|---|
| on-topic | 0.66–0.80 | 0.59–0.69 |
| off-topic (real, unrelated) | 0.45–0.54 | 0.42–0.52 |
| **nonsense (gibberish)** | **0.59–0.69** | **0.57–0.66** |

Gibberish scores **as high as on-topic** queries and above real off-topic
ones, so no absolute cosine cutoff separates signal from noise for this
model. `minVectorSim` defaults to **0**; RRF and the FTS leg do the
discrimination. The knob is for better-calibrated embedders. The same flat
band is why short `prop` docs (< 64 bytes) are never embedded.

### Per-corpus leg weighting

`index.search.adaptiveWeights` down-weights the FTS leg per query by its
score concentration (`legConfidence`): a flat or weak BM25 distribution
(paraphrastic queries) is suppressed so it can't drag hybrid below the
dense leg. Asymmetric — only FTS, never the uncalibrated cosine leg.
Hybrid nDCG@10 / recall@10:

| corpus | equal 1/1 | adaptive |
|---|---|---|
| FiQA (weak BM25) | 0.368 / 0.460 | **0.403 / 0.490** |
| SciFact (strong BM25) | **0.696 / 0.843** | 0.685 / 0.797 |

A clear win where the lexical leg is weak, a small regression where it is
strong — so it is **off by default**; turn it on for semantic /
paraphrastic corpora.

### FTS query operators

Phrases (`"..."`) and prefixes (`foo*`) work inside `query`; `require` /
`exclude` are request arrays enforced on every hit; `index.search.
defaultOperator` switches bare terms between OR and AND. AND on the pure
FTS leg (embedder-independent) collapses over natural-language queries,
because few docs contain *every* term (nDCG@10 / recall@10):

| corpus | fts OR | fts AND |
|---|---|---|
| SciFact | 0.663 / 0.790 | 0.025 / 0.024 |
| FiQA | 0.227 / 0.282 | 0.027 / 0.028 |

OR is the default; AND is for short keyword input the client controls.
Phrase / `require` / `exclude` give precision without the recall cliff.

BM25F title boost and `b` / `k1` (`index.search.{titleWeight, bm25B,
bm25K1}`) default to neutral (0 = engine defaults, no title field). On
SciFact both a title boost and `b=0.4` slightly hurt (its title is a paper
title and answers live in the body; coalescing already removed the length
bias), so neither has a non-zero default.

### Embedder availability & throughput

Embedding is the pipeline's bottleneck by orders of magnitude: the local
CPU model manages ~13 short chat docs/s but ~2 long editor windows/s, while
the vector index inserts thousands per second. Two mechanisms address this
(`../05-config.md`):

- **`embedder: auto` (the default)** — an online OpenAI-compatible API as
  primary, the local model as fallback, behind a circuit breaker (3
  consecutive failures skip the primary for 30 s). Embedding is fast with
  zero setup and stays available through an outage instead of pausing on
  `pending`. **Hard constraint:** primary and fallback must be the *same
  model* (one vector space, one dimension) — Qwen3-Embedding-0.6B online
  (fp16, DeepInfra) and local (Q8); the quantization drift is negligible.
  The default online credentials are compiled into `internal/config`.
- **Parallel embed loop** (`index.embedConcurrency`) — several batches per
  round in parallel. The win is online (parallel HTTP requests); the local
  child serves one frame at a time, so concurrency is safe for any
  backend. Default 1 for local, 4 for `openai` / `auto`. In the BEIR
  harness this took SciFact (5183 docs) from ~35 min (serial local) to
  ~30 s of embedding.

## Decisions / defaults

| Knob | Default | Why |
|---|---|---|
| editor chunk unit | coalesced ~1.5 KB windows | distribution + recall (above) |
| long-record chunk target | 2000 runes | fits the local embedder's 2048-token clamp; a hit's data is the passage |
| `vector.mode` | IVF-SQ | cheap near-flat incremental ingest + physical deletes; HNSW's ≈-exact recall (+3–4 recall@10) isn't worth its ~15–47× serial ingest and tombstone rebuilds for a continuously written local index. `btree` opt-in |
| `ftsWeight` / `vectorWeight` | 1 / 1 | safe corpus-agnostic default (optimal on SciFact); dense-favorable corpora like FiQA want vector-heavier |
| `stopWords` | on | helps conversational queries, neutral on BEIR |
| `minVectorSim` | 0 | a static floor can't separate signal from noise for the local model |
| `adaptiveWeights` | off | wins on paraphrastic corpora (FiQA +3 recall@10), small cost on lexical-friendly ones; opt-in |
| `defaultOperator` | or | AND collapses recall on natural-language queries |
| `titleWeight` / `bm25B` / `bm25K1` | 0 (engine defaults) | non-zero values slightly hurt on SciFact |
| `embedConcurrency` | 1 local / 4 online | parallel batches are the online throughput win; the local child serves one frame at a time |
| `embedder` | `auto` | online primary + local fallback, same model — fast zero-config embedding that degrades to local on outage |

## Not measured

- The benchmarks are out-of-domain (scientific claims, financial Q&A);
  there is no labeled set over notes, chat or agent memory, so domain
  effects of `adaptiveWeights`, `titleWeight` and `bm25B` are unmeasured.
- There is no per-object summary doc (name + description + lead paragraph)
  and no reranker; scores are uncalibrated, so the index cannot say
  "nothing relevant".
- The FTS analyzer does not stem (any-store's analyzer is
  language-agnostic); prefix terms (`foo*`) are the recall tool.
