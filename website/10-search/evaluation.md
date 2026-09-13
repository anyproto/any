---
title: Evaluation
description: What was measured — chunk sizes, BEIR benchmarks, ANN index modes, the similarity floor — and why the defaults are what they are.
order: 60
---
# Evaluation

The search defaults are decisions backed by measurements on labeled benchmarks and on a live index, not guesses. This page records what was measured and what each result decided, so you know when to move a knob.

## Chunk size: coalesced editor windows

One document per editor block would index tiny chunks — blocks average ~98 characters, about half under 50 — which hurts vector recall (little to embed) and BM25 length normalization (short docs win on field length). Coalescing consecutive blocks into ~1.5 KB windows broken before headings fixes both, measured on a live index:

| `editor_blocks` | mean | p50 | p90 | % < 50 chars |
|---|---|---|---|---|
| one doc per block | 98 | 49 | — | ~50% |
| coalesced windows | **392** | **255** | 972 | **9%** |

Chat (~232 chars) and property entries (~13, exact-match labels) stay per record.

## Hybrid vs single legs: BEIR SciFact and FiQA

Measured with the exact deployed model (Qwen3-Embedding-0.6B) on two standard labeled IR sets.

**SciFact** (5,183 docs, 300 queries; scientific claims with high lexical overlap), production knobs:

| mode | nDCG@10 | recall@10 | MRR |
|---|---|---|---|
| fts (BM25) | 0.667 | 0.793 | 0.635 |
| vector | 0.677 | 0.796 | 0.646 |
| **hybrid** | **0.695** | **0.834** | **0.658** |

Hybrid beats both legs; the FTS number matches published BM25 for SciFact (~0.665), confirming the lexical leg is implemented correctly. A weight sweep found equal RRF weights optimal (fts×1.5 → 0.6815, vec×1.5 → 0.6779), and stop-words neutral (0.6944 vs 0.6945).

**FiQA** (57,600 docs, 648 queries; paraphrastic financial Q&A):

| mode | nDCG@10 | recall@10 | MRR |
|---|---|---|---|
| fts (BM25) | 0.233 | 0.294 | 0.289 |
| vector (HNSW) | 0.466 | 0.542 | 0.550 |
| hybrid, equal weights | 0.385 | — | — |

Here vector alone beats hybrid: a weak BM25 list drags equal-weight fusion below the dense leg. **Decision:** equal weights stay the corpus-agnostic default; `ftsWeight` / `vectorWeight` are the lever for corpora where one leg is much weaker.

### Adaptive weighting

`index.search.adaptiveWeights` down-weights the FTS leg per query by how concentrated its scores are (hybrid nDCG@10 / recall@10):

| corpus | equal 1/1 | adaptive |
|---|---|---|
| FiQA (weak BM25) | 0.368 / 0.460 | **0.403 / 0.490** |
| SciFact (strong BM25) | **0.696 / 0.843** | 0.685 / 0.797 |

A big win where the lexical leg is weak, a small regression where it is strong — so it ships off. Turn it on for semantic or conversational corpora.

### AND as the default operator

Measured on the pure FTS leg (nDCG@10 / recall@10):

| corpus | OR | AND |
|---|---|---|
| SciFact | 0.663 / 0.790 | 0.025 / 0.024 |
| FiQA | 0.227 / 0.282 | 0.027 / 0.028 |

AND over natural-language queries collapses — few documents contain every term. **Decision:** OR stays the default; phrases, `require` and `exclude` give precision without the recall cliff.

## ANN index mode: recall vs ingest cost

Comparing index strategies on the same SciFact vectors isolated the recall side:

| index mode | vector nDCG@10 | vector recall@10 | hybrid recall@10 |
|---|---|---|---|
| IVF-SQ (default) | 0.681 | 0.801 | 0.834 |
| HNSW (`btree`) | **0.703** | **0.837** | **0.855** |
| brute force (exact) | 0.701 | 0.831 | 0.855 |

HNSW is effectively exact; IVF-SQ leaves ~3 recall@10 points on the table (~4 on FiQA at 57k docs, where HNSW 0.542 vs exact 0.546 confirms the ordering holds at scale). Exact recall matches the model card, so the model was never the problem.

But the production ingest path is not a one-shot bulk build: the index is created after the first ~64-doc batch and every later batch inserts into the existing structure, serially. Measured there (random 1024-dim vectors):

| N | mode | bulk build | **incremental (production)** |
|---|---|---|---|
| 5k | ivfsq | 551 ms | 296 ms |
| 5k | HNSW | 1.53 s | **4.62 s** (~15.6×) |
| 20k | ivfsq | 3.80 s | 1.11 s |
| 20k | HNSW | 6.33 s | **38.0 s** (~34×) |

HNSW ingest is 15–47× slower and super-linear, and its deletes tombstone and rebuild while IVF deletes are physical. Query latency is under 10 ms for both (IVF ~2× faster); disk is ~equal (the base collection stores full float32 vectors regardless). **Decision:** default `index.vector.mode: ivfsq` for a continuously written, churn-y local index; `btree` is the opt-in for read-heavy, quality-first deployments; `bruteforce` is exact for small spaces.

## The similarity floor: kept at 0

Cosine scores on a live index by query class:

| query class | top-1 | top-10 |
|---|---|---|
| on-topic | 0.66–0.80 | 0.59–0.69 |
| off-topic (real, unrelated) | 0.45–0.54 | 0.42–0.52 |
| **nonsense (gibberish)** | **0.59–0.69** | **0.57–0.66** |

Gibberish scores as high as on-topic and *above* real off-topic queries — a trait of the instruction-tuned model — so no absolute cutoff separates signal from noise. **Decision:** `minVectorSim: 0`; discrimination comes from RRF plus the full-text leg. The knob remains for better-calibrated embedders.

## BM25 dials

A SciFact FTS sweep showed a title boost and `b = 0.4` both slightly hurt (its titles are paper titles; answers live in the body, and coalescing already fixed length bias). **Decision:** `bm25B`, `bm25K1`, `titleWeight` default to the engine's values.

## Embedder throughput

The local CPU model embeds at ~76 texts/s — the pipeline's bottleneck by orders of magnitude (any vector index inserts at thousands per second), which is why embedding lives off the indexing path. Parallel batches (`embedConcurrency`) are the online win: SciFact went from ~35 minutes serial-local to ~30 s. Batch size 64 reaches ≥97% of peak throughput at half the per-call latency of 128.

Packing several documents into one local decode (`index.local.batchDocs`) buys nothing once each text keeps the same token bound: on a mixed record stream a GTX 1080 over Vulkan embedded 10.1 docs/s at one document per decode against 9.2 at four, and a 32-core CPU 2.16 against 2.14. Wider packing only looks faster because it splits the context between the texts and embeds less of each, so the default is one.

## Defaults at a glance

| Knob | Default | Decided by |
|---|---|---|
| editor chunk unit | ~1.5 KB windows | chunk-length distribution, recall |
| `vector.mode` | `ivfsq` | incremental ingest cost vs ~3–4 recall@10 |
| `ftsWeight` / `vectorWeight` | 1 / 1 | optimal on SciFact, safe elsewhere |
| `adaptiveWeights` | off | wins on paraphrastic, small cost on lexical corpora |
| `defaultOperator` | `or` | AND recall cliff |
| `stopWords` | on | helps conversational, neutral on BEIR |
| `minVectorSim` | 0 | floor cannot separate signal from noise |
| `bm25B` / `bm25K1` / `titleWeight` | engine defaults | sweep showed no gain |
| `embedBatch` / `embedConcurrency` | 64 / 1 local, 4 online | throughput curve |
| `local.batchDocs` | 1 | packing gains nothing at an equal token bound |

## Reproducing

The harnesses are opt-in tests in the server repo, built with the `fts vector` tags: a synthetic labeled-query harness, a BEIR runner that takes any BEIR-format set by directory, an ingest/latency profiler for the ANN modes, and a live-index cosine probe. Each is driven by `ANY_EVAL_*` / `ANY_BEIR_*` environment variables naming the corpus, the embedder and the index mode; see [Testing](../testing/any-e2e.html) for how the repo's test suites are laid out.

Known gaps: BEIR measures the stack on out-of-domain scientific text — a labeled set over real notes, chat and memory would measure the actual content — and there is no reranker tier yet, so true "nothing relevant" abstention is not available.
