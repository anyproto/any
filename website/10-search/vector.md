---
title: Vector search
description: The semantic leg — embeddings computed on-device, an ANN index per space, and what the similarity scores do and don't mean.
order: 20
---
# Vector search

The dense leg embeds every text-bearing index document with an embedding model and answers queries by cosine similarity over an approximate-nearest-neighbour index. It gives paraphrase recall — "what did we decide about the reranker?" finds a message that never uses the word "decide" — at the cost of an embedder.

## Pinning the mode

```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "what did we decide about the reranker?", "mode": "vector"}'
```

```bash
any search $SPACE "what did we decide about the reranker?" --mode vector
```

`score` is the cosine similarity. Unlike `hybrid`, pure `vector` mode does not degrade: without an embedder configured it answers `400 index.no_embedder`, and while a configured embedder is unreachable — or does not embed the query within `index.search.queryEmbedTimeout`, default 5 s — it answers `503 index.embedder_unavailable`, retryable. The vector leg embeds the whole `query` verbatim, so `"phrases"` and `prefix*` have no effect here; `require` and `exclude` still bind every hit, because vector hits are post-filtered against the full-text index.

## How documents get vectors

Indexing and embedding are decoupled. The advance loop writes text into the index immediately, marked `pending`; a second per-space loop drains `pending` docs in batches (`index.embedBatch`, default 64), calls the embedder, writes the vectors back in one transaction, and ensures the vector index exists. So a fresh write is full-text-searchable at once and gains vector recall one embed round later.

```
write ──▶ advance (FTS, 250ms) ──▶ pending ──▶ embed batch ──▶ vector index
                                        ▲                          │
                                        └── retry every 1m on failure
```

A re-written record goes back to `pending`, but only if its indexed text actually changed: every doc stores a content hash, and a record that re-streams with the same text (a chat message gaining a reaction, a memory item bumping a counter) is not re-embedded. Editor documents are reconciled per window — an append re-embeds one window, not the page.

Some documents never get vectors by design: everything in the `props` scope, and any `prop`-dataset entry under 64 bytes. Short name-like strings land in a flat similarity band for relevant and irrelevant queries alike and would fill top-N slots without discriminating; BM25 is the right tool for labels.

## The ANN index

One vector index per space, over a `vector` field, created lazily once at least one embedded document exists (the IVF trainer needs real data). The strategy is `index.vector.mode`:

| Mode | Recall | Ingest | When |
|---|---|---|---|
| `ivfsq` (default) | ~3–4 recall@10 below exact | cheap, near-flat, physical deletes | a continuously written local index |
| `btree` / `hnsw` | ≈ exact | serial, super-linear; deletes tombstone and rebuild | read-heavy, quality-first deployments |
| `hybrid` | HNSW + RAM cache | as HNSW | as HNSW |
| `bruteforce` | exact | none | small spaces (O(N) per query) |

The default was chosen after measuring the production ingest path, not a one-shot bulk build: HNSW's per-doc graph maintenance is 15–47× slower there and the gap grows with size. Details in [Evaluation](evaluation.html). An existing index keeps its mode until rebuilt.

Vector search latency is a few milliseconds for both ANN modes; disk use is dominated by the stored float32 vectors, so the modes differ little there.

## Dimension pinning

The vector dimension is learned from the first successful embedding batch (or fixed with `index.vector.dim`) and pinned in the index database. Changing the embedder to a model with a different dimension against a populated index is a loud boot error advising `rm <data-dir>/index` — never silent corruption. `index.local.dim` can truncate the default model's Matryoshka output to shrink the index.

## Similarity scores and the floor

`index.search.minVectorSim` drops vector hits at or below a cosine cutoff before ranking; `0` (the default) keeps the built-in "> 0" noise floor only.

> **Note.** For the default local model a static floor is a poor noise filter. Measured on a live index, gibberish queries score ~0.6 cosine — as high as on-topic queries, and *above* real off-topic ones — so any cutoff that drops noise also drops signal. Keep `0` for Qwen3-Embedding and let [hybrid fusion](hybrid.html) plus the full-text leg do the discrimination; raise it only for a better-calibrated embedder.

## Long records

A record longer than about 2000 runes is indexed as several chunk documents, and each chunk is embedded whole, so vector recall covers the entire record; the hit shows the record's best-ranked chunk, and `passages` returns its other matching chunks. The local embedder clamps each text to `index.local.contextSize / index.local.batchDocs` tokens (default 2048 / 1, end-of-sequence token preserved because the model uses last-token pooling) — well above a prose chunk, though a chunk of dense code or CJK text can exceed it and embed head-only. Full-text covers every chunk in full regardless.

## Errors

| Status | Code | When |
|---|---|---|
| 400 | `index.no_embedder` | `mode: vector` on a server with `index.embedder: none` (or a build without the vector leg) |
| 503 | `index.embedder_unavailable` | `mode: vector` while the embedder is down, the model is still downloading, or the query embedding exceeds `queryEmbedTimeout`; retry |
| 409 | `index.disabled` | indexer off (`index.enabled: false`) |

Which embedders exist, and what happens when one is unreachable, is on [Embedders](embedders.html).
