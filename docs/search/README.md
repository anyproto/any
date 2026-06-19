# Search — chunking & hybrid-ranking evaluation

This folder is the **evaluation and decision record** for the local search
index: why the chunker and hybrid ranking are shaped the way they are, and
the measured evidence behind the defaults. The **implementation contract**
(chunker interface, store layout, removal semantics, endpoints) lives in
[`../13-index.md`](../13-index.md) — read that for *how* it works; read
this for *why* and *how well*.

## TL;DR

- The original index emitted **one doc per editor block** → chunks were
  tiny (editor mean ~98 chars, ~half under 50), hurting vector recall and
  BM25 length normalization. Coalescing to ~1.5 KB windows fixed it
  (mean **98 → 392** chars, tiny-fragment share ~50% → ~9%).
- Shipped: **coalesced editor windows**, **memory** + **program-doc**
  indexing (debug logs + program source excluded), **incremental
  re-embedding** (per-doc content hash → editor append / memory bumps
  re-embed only what changed), a configurable **`index.vector.mode`**, and
  hybrid knobs (**stop-words**, **weighted RRF**, **vector floor**).
- Measured on real labeled benchmarks (BEIR SciFact 5k + FiQA 57k, exact
  Qwen3-0.6B):
  - **ANN index is a real trade-off, and the default is IVF-SQ.** HNSW
    has ≈-exact recall (IVF-SQ −3–4 recall@10), **but** on the production
    incremental embed-loop path HNSW ingest is **~15–47× slower than IVF
    and super-linear** (serial graph inserts), plus its deletes
    tombstone + rebuild. For a local, continuously-written, churn-y index
    with bulk imports, that cost outweighs the recall edge → **default
    IVF-SQ**; HNSW (`mode: btree`) is opt-in for read-heavy / quality-max
    deployments. (I initially defaulted to HNSW on a flawed build
    benchmark; corrected — see § index mode.)
  - **Hybrid vs single-leg is corpus-dependent**: hybrid wins on
    lexical-friendly SciFact, but vector-alone wins on paraphrastic FiQA
    (weak BM25). Equal RRF weights are a safe default, not a universal
    optimum — the weight knobs are the lever.
  - **A static vector-similarity floor doesn't work for this model**
    (gibberish scores as high as on-topic) → `minVectorSim` stays 0.
  - FTS matches reference BM25; the deployed model matches its card once
    the index is exact — the earlier shortfall was the approximate index.

## What changed (and where)

| Area | Change | Reference |
|---|---|---|
| Editor | one-doc-per-block → coalesced windows (heading + ~1.5 KB budget), `recordId = win_<anchor>` | `internal/editor/window.go`, `chunker.go` |
| Memory | `agent_memory_items` indexed per-record, scope `agent` | `internal/agentmem/chunker.go` |
| Programs | `program_description` + `program_methods` indexed (scope `program`); **source not** indexed | `internal/program/chunker.go` |
| Debug | `agent_debug_log` excluded — no chunker, and prop chunker skips debug objects | `internal/server/sdk.go`, `internal/index/prop.go` |
| Incremental embed | per-doc content hash → reconcile diff (editor) + per-record no-op skip (chat/memory) | `internal/indexer/{worker,store}.go` |
| ANN index | configurable `index.vector.mode` (default **IVF-SQ**; btree/hnsw/hybrid/bruteforce) | `internal/indexer/store.go`, `../05-config.md` |
| Embedder fallback | `embedder: auto` — online primary + local fallback, circuit breaker, same model | `internal/indexer/embed_fallback.go` |
| Embed throughput | parallel embed loop, `index.embedConcurrency` (1 local / 4 online) | `internal/indexer/{worker,indexer}.go` |
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
Other BEIR sets drop in by name (e.g. `fiqa.zip`, 57.6k docs); add
`ANY_BEIR_EXACT=1` for the exact-vs-index comparison and
`ANY_INDEX_VECTOR_MODE=btree|ivfsq|bruteforce` to compare ANN modes.

Index-mode cost profile (random vectors; no API):

```bash
ANY_VEC_BENCH=1 ANY_VEC_BENCH_SIZES=100000,200000 \
go test -tags 'fts vector' -run TestVectorModeProfile -v -timeout 50m ./internal/indexer
```

Live cosine-distribution probe (server stopped; for the floor decision):

```bash
ANY_LIVE_INDEX=~/.any/index/index.db ANY_LIVE_SPACE=<spaceId> \
ANY_EVAL_LOCAL_MODEL=… ANY_EVAL_LOCAL_LIBDIR=… \
[ANY_LIVE_ONTOPIC="q1,q2" ANY_LIVE_OFFTOPIC="q3,q4"] \
go test -tags 'fts vector' -run TestLiveVectorScoreProbe -v ./internal/indexer
```

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

### Editor reconcile & the append cost (incremental embedding)

A coalesced window spans several blocks, so the editor chunker is a
**`Reconciler`** — it rebuilds the object's whole window set, not a
per-record delta (a window's text needs sibling blocks below the cursor,
and a deleted block's position is gone from its tombstone).

`append_scaling_test.go` measured the danger: rebuilding by re-reading
*and re-embedding* every window on each change is **O(N²)** for a
grow-by-append page. The windowed-read fast path the analysis first
assumed doesn't fit reality — editor render order is the **nav-tree walk**
(`treeOrder`), not flat `nav.pos`, so forming windows is inherently an
O(doc) read.

The fix that shipped is a **per-doc content hash** (FNV-1a of the indexed
text):

- **Editor reconcile diffs by hash** (`worker.reconcile`) — it re-reads
  blocks to form windows (O(doc), but cheap against the local DB) yet
  **re-embeds only changed/new windows**, deleting vanished ones. An
  append re-embeds *one* window, not the document. Embedding — the
  expensive axis — is incremental even though the read isn't.
- **Per-record hash-skip** (chat/memory) — a record that re-streams with
  unchanged indexed text (e.g. a memory item bumped only on `accessCount`
  during recall, or a chat message that got a reaction) is **not
  re-embedded**.

Verified end-to-end with counting embedders (`handlers_index*_test.go`):
an editor append embeds exactly the new window; a memory `accessCount`
evolve re-streams but triggers **0** re-embeds, while a `context` change
does. (Practical note: the heaviest append-loop content — agent debug
logs — isn't indexed at all, so the O(N²) read path has no real victim;
the deferred true fix is a persisted-window-anchor incremental read.)

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

### Index mode: HNSW vs IVF-SQ vs exact — recall vs ingest cost

The vector leg first measured below the model card (~0.74). Comparing ANN
strategies on the same SciFact set + vectors (`ANY_INDEX_VECTOR_MODE`,
plus an in-test exact brute force) isolated the **recall** side:

| index mode | vector nDCG@10 | vector recall@10 | hybrid nDCG@10 | hybrid recall@10 |
|---|---|---|---|---|
| IVF-SQ (default) | 0.681 | 0.801 | 0.700 | 0.834 |
| HNSW / btree | **0.703** | **0.837** | **0.724** | **0.855** |
| brute force (exact) | 0.701 | 0.831 | 0.723 | 0.855 |

- **IVF-SQ leaves ~3 recall@10 points (and ~2 nDCG) on the table** — its
  default NProbe (16) scans only ~12% of cells on this corpus (FiQA at 57k
  probes ~1.7% → the gap widens to ~4 pts).
- **HNSW ≈ exact**, at log(N) search. Exact vector (0.70) ≈ the model card
  modulo fp16/Q8, so the model was never the problem.

**But recall isn't the only axis — and the ingest cost reverses the
decision.** The first cut of this work defaulted to HNSW on the strength
of recall plus a build benchmark showing only ~2× slower build. That
benchmark measured the wrong path (a one-shot **parallel bulk** build).
The **production** path is the embed loop: the index is created after the
first ~64-doc batch, then every later batch inserts into the **existing**
index → **serial, per-doc HNSW graph maintenance**. Measured on that path
(`TestVectorModeIngestProfile`, random dim-1024):

| N | mode | bulk (one-shot) | **incremental (production)** |
|---|---|---|---|
| 5k | ivfsq | 551ms | 296ms |
| 5k | btree (HNSW) | 1.53s | **4.62s** (~15.6×) |
| 20k | ivfsq | 3.80s | 1.11s |
| 20k | btree (HNSW) | 6.33s | **38.0s** (~34×) |

So on the real path HNSW ingest is **~15–47× slower than IVF and
super-linear** (the gap grows with N), and HNSW **deletes tombstone +
rebuild** while IVF deletes are physical (cheaper churn). A fix exists —
defer index creation so cold sync uses the bulk builder (~2×) — but it
needs a "burst settled" heuristic the indexer can't cleanly define, so
it's not worth the complexity here.

- **Decision: default `index.vector.mode` = IVF-SQ.** For a local,
  continuously-written, churn-y index with a bulk-import path
  (`scripts/import-obsidian.mjs`), cheap near-flat ingest + physical
  deletes outweigh ~3–4 recall@10 points (which hybrid further softens).
  **HNSW (`btree`) is the opt-in** for read-heavy / quality-max
  deployments; `bruteforce` is exact for small spaces. Existing indexes
  keep their mode until rebuilt.

### Index mode at scale: recall on FiQA (57.6k docs, real)

Re-ran the exact-vs-HNSW-vs-IVF comparison on BEIR **FiQA** (57.6k docs /
648 queries — 11× SciFact) to confirm the recall ordering holds at scale:

| mode | nDCG@10 | recall@10 | MRR |
|---|---|---|---|
| fts (BM25) | 0.233 | 0.294 | 0.289 |
| vector — **HNSW** | 0.466 | **0.542** | 0.550 |
| vector — IVF-SQ | 0.440 | 0.507 | 0.528 |
| vector — exact | 0.469 | 0.546 | 0.552 |

- **HNSW ≈ exact** (0.542 vs 0.546 recall@10) — near-exact recall holds at
  57k. **IVF-SQ loses ~4 recall@10 / ~2.5 nDCG** — the gap persists and
  slightly widens vs SciFact. (Confirms HNSW's recall edge at scale — but
  the ingest cost above is why the default is still IVF-SQ.)
- Exact 0.469 nDCG matches the Qwen3-0.6B model-card FiQA number — setup
  is correct.
- **Hybrid is not universally best.** On FiQA, *vector alone* beats hybrid
  (0.466 vs 0.385 nDCG): paraphrastic Q&A makes BM25 weak (0.233), and
  equal-weight RRF drags hybrid below pure dense — the opposite of SciFact
  (claims, high lexical overlap, hybrid won). Equal weights is a safe
  corpus-agnostic default, but `ftsWeight`/`vectorWeight` are the lever
  for corpora where one leg is much weaker.

### Index mode: cost at scale (build / search / disk)

Profiled with random dim-1024 vectors (`TestVectorModeProfile`,
`ANY_VEC_BENCH=1`). Random vectors are geometry-valid for build/latency/
disk but a **worst case for recall** (nearly equidistant in high dim — see
the ~5% column), so real recall comes from BEIR (above / FiQA).

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

Note this is the **one-shot bulk build** path (insert all, then build
once), *not* the production incremental ingest — see the much larger
incremental gap in § index mode above.

- **Search latency**: both ANN modes <10ms (IVF ~2× faster); brute force
  (237–472ms) is unusable past a few thousand docs.
- **Disk: ~no difference** (IVF-SQ 1362 vs HNSW 1378 MB): the base
  collection stores full float32 vectors regardless of mode; SQ only
  compresses the index portion, which is small next to the vectors.
  (Brute force is ~half the size — no index.) So IVF-SQ's advantage is
  ingest cost + churn-friendliness, **not** disk/RAM.
- **Build is super-linear** for both; the HNSW penalty compounds on the
  serial incremental path (the real one), which is the basis for keeping
  IVF-SQ the default.

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

### Embedder availability & throughput

The local CPU model embeds at ~76 texts/s — the pipeline's bottleneck by
orders of magnitude (any vector index inserts at thousands/s). Two
mechanisms address this (`docs/05-config.md`):

- **`embedder: auto`** — prefer an online OpenAI-compatible API (fast),
  fall back to the always-downloaded local model on an outage, via a
  circuit breaker (skip the primary for a cooldown after repeated
  failures). So vector search stays fresh during an outage instead of
  pausing on `pending`. **Hard constraint:** primary and fallback must be
  the *same model* (one vector space / dim) — e.g. Qwen3-Embedding-0.6B
  online (fp16) + local (Q8); quantization drift is negligible.
- **Parallel embed loop** (`index.embedConcurrency`) — embed several
  batches per round in parallel. The win is online (parallel HTTP
  requests); the local model serializes internally on its mutex, so
  concurrency is safe regardless of backend. Default 1 for local, 4 for
  online (`openai`/`auto`). In the BEIR harness this took SciFact (5183
  docs) from ~35 min (serial local) to ~30s embedding.

## Decisions / defaults

| Knob | Default | Why |
|---|---|---|
| editor chunk unit | coalesced ~1.5 KB windows | distribution + recall (above) |
| `vector.mode` | IVF-SQ | cheap near-flat incremental ingest + physical deletes; HNSW's ≈-exact recall (+3–4 recall@10) isn't worth its ~15–47× serial-ingest cost + tombstone rebuilds for a write-continuous local index. `btree` opt-in for read-heavy/quality |
| memory indexing | per-record, scope `agent` | independently filterable; cheap incremental |
| `ftsWeight` / `vectorWeight` | 1 / 1 | safe corpus-agnostic default (optimal on SciFact); dense-favorable corpora like FiQA want vector-heavier — that's what the knobs are for |
| `stopWords` | on | helps conversational, neutral on BEIR |
| `minVectorSim` | 0 | static floor can't separate signal/noise for the local model |
| `embedConcurrency` | 1 local / 4 online | parallel batches are the online throughput win; local serializes |
| `embedder: auto` | (opt-in) | online speed + local fallback, same model |

## Open items / follow-ups

- ~~Vector under-performs the model card~~ **EXPLAINED** — it was the
  IVF-SQ approximation (−3–4 recall@10), not the model. Default briefly
  moved to HNSW, then **reverted to IVF-SQ** once the incremental ingest
  cost (~15–47×, super-linear) + tombstone-rebuild deletes were measured;
  HNSW is the opt-in. § index mode.
- ~~Large real-data recall~~ **DONE** — BEIR FiQA (57.6k): HNSW ≈ exact
  (recall@10 0.542 vs 0.546), IVF-SQ ~4 pts lower. Recall edge holds at
  11× SciFact scale.
- **HNSW ingest fix (deferred)** — defer index creation so cold sync /
  bulk import uses the parallel bulk builder (~2× vs ~15–47×), making
  `mode: btree` viable as a default later. Needs a "burst settled"
  heuristic the indexer can't cleanly define; not pursued for now.
- **Per-corpus leg weighting** — FiQA showed vector-alone beating
  equal-weight hybrid (weak BM25). A static default can't know per corpus;
  options for later: a query-adaptive weight, or auto-down-weighting a leg
  whose top scores are weak. The `ftsWeight`/`vectorWeight` knobs exist
  but are unset by default.
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
