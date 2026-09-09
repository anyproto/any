---
title: Full-text search
description: The BM25 lexical leg — always on, no external dependency, with phrase, prefix, require and exclude operators.
order: 10
---
# Full-text search

The lexical leg of the index is a BM25 full-text index over each document's `data` field. It needs no embedder, no model download and no network, and it is the leg that answers first: a write is searchable within the indexer's 250 ms debounce.

## Pinning the mode

`hybrid` is the default; pass `"mode": "fts"` when you want lexical ranking only — exact terms, identifiers, error strings, names.

```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "index.embedder_unavailable", "mode": "fts"}'
```

```bash
any search $SPACE "index.embedder_unavailable" --mode fts
```

The reply's `vectorStatus` is `skipped` — the echo of your own choice — and `score` is the BM25 score.

## Query operators

The `query` string is parsed by the full-text engine, so two operators work inline; two more arrive as separate arrays.

| Operator | Where | Meaning |
|---|---|---|
| `"quoted phrase"` | in `query` | terms must appear adjacent, in order |
| `prefix*` | in `query` | trailing `*` matches any term starting with `prefix` |
| `require` | body array | every listed term (phrase or prefix allowed) must be present |
| `exclude` | body array | no listed term may be present |

```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "\"zeppelin disaster\" hind*",
       "require": ["1937"],
       "exclude": ["fiction"]}'
```

```bash
any search $SPACE '"zeppelin disaster" hind*' --require 1937 --exclude fiction
```

Bare terms combine with **OR** by default: a hit needs any of them, and more matching terms rank higher. A bare term next to a `require` is an optional boost, not a filter. Phrases and prefixes in `query` shape the lexical leg only; `require` and `exclude` bind every hit — vector hits (hybrid and pure `vector` mode) are post-filtered against the FTS index before fusion, so no mode returns a hit that violates them.

> **Note.** `index.search.defaultOperator: and` makes every bare term required. It is precise for short keyword input a client controls, but on natural-language queries it collapses recall (few documents contain *every* word — measured on BEIR, nDCG@10 fell from 0.66 to 0.02). Prefer phrases, `require` and `exclude`, which add precision without the cliff.

## Stop words

A small English stop-word list is stripped from the FTS-leg query before it runs (`index.search.stopWords`, default on). On a bag-of-words OR engine every common word matches a large share of the corpus and drags BM25 toward length and frequency noise; dropping them is a precision win for conversational queries and neutral on terse ones. Two safeguards: an all-stop-words query is left unchanged rather than emptied, and stripping is skipped whenever `query` contains a `"`, so phrases survive intact. The vector leg always sees the full query.

## Scopes

`scopes` narrows the search to a set of slugs; empty means all. Scopes are an open set (`[a-z0-9_-]`, up to 64 characters) — an unknown but well-formed scope returns no hits, a malformed one is `400 search.bad_scope`.

| Scope | What lands there |
|---|---|
| `basic` | editor windows, object names and descriptions, runtime-dataset records |
| `chat` | chat messages |
| `props` | user property values — **full-text only**, never embedded |
| any other slug | whatever a property's `meta.index` or a dataset's `x-search.scope` minted |

Property values are self-describing entries — `"Publisher: Gollancz"`, `"Score: 9"` — so searching by property *name* works too, and bare numbers carry context. A content-only search passes `scopes` without `props`.

```bash
any search $SPACE "gollancz" --scopes props --mode fts
```

## BM25 tuning

Three engine dials are exposed under `index.search.*`; all default to 0, meaning "engine default", so an absent block changes nothing.

| Key | Effect | Engine default |
|---|---|---|
| `bm25B` | length normalization | 0.75 |
| `bm25K1` | term-frequency saturation | 1.2 |
| `titleWeight` | BM25F boost for the title field (runtime datasets with an `x-search.title`) | 1.0 |

The measured sweep found no non-zero value that helped on the reference corpus, so the knobs stay neutral; see [Evaluation](evaluation.html).

## Reading the scores

BM25 scores are comparable only within one response, and never with the cosine or RRF scores of the other modes. Rank on them, don't threshold.

## Errors

| Status | Code | When |
|---|---|---|
| 409 | `index.disabled` | the server runs with `index.enabled: false` |
| 400 | `search.bad_mode` | `mode` is not `hybrid`, `fts` or `vector` |
| 400 | `search.bad_scope` | a scope slug is malformed |

See [Hybrid ranking](hybrid.html) for how this leg is fused with the vector leg, and [How indexing works](indexing.html) for what makes it into the index in the first place.
