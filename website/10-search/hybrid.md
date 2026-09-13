---
title: Hybrid ranking
description: The default mode fuses the lexical and semantic legs by reciprocal rank, degrades gracefully, and tells you what actually ran.
order: 30
---
# Hybrid ranking

`hybrid` runs both legs and fuses them with reciprocal-rank fusion (k = 60). It is the default because it is the safe choice across corpora: it wins outright on lexical-friendly content and never collapses when one leg is weak — and when the embedder cannot help at all, it becomes full-text search by itself.

## The default call

```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "what did we decide about the reranker?",
       "scopes": ["chat", "basic"],
       "limit": 10}'
```

```bash
any search $SPACE "what did we decide about the reranker?" --scopes chat,basic --limit 10
```

```json
{
  "hits": [
    { "scope": "chat", "objectId": "…", "dataset": "chat_messages",
      "recordId": "…", "data": "…", "dataTotal": 29, "score": 0.0328 }
  ],
  "mode": "hybrid",
  "vectorStatus": "used"
}
```

`limit` counts records (one hit per record), defaults to 10 and caps at 100. `score` is the fused RRF score — small numbers, comparable only within this one response.

## Reciprocal-rank fusion

Each leg returns a ranked list; a document's fused score is the sum over the legs that returned it of `weight / (k + rank)`. Rank, not raw score, is what gets combined, so BM25 and cosine never have to be put on one scale. A document found by both legs rises above one found by only one.

```
fused(doc) = ftsWeight / (60 + rank_fts) + vectorWeight / (60 + rank_vec)
```

Full-text operators (`"phrases"`, `prefix*`) shape the lexical leg's list; the vector leg always embeds the whole `query` verbatim, stop words included. `require` / `exclude` bind every hit regardless of leg: vector hits are post-filtered against the FTS index before fusion, so a fused result never contains a doc the lexical constraint would have refused.

## Read `vectorStatus` before trusting recall

The reply's `mode` is the mode that actually ran, and `vectorStatus` says whether semantic recall participated and, if not, why. Agents in particular should branch on it before concluding that "nothing matched".

| `vectorStatus` | Meaning | What to do |
|---|---|---|
| `used` | the vector leg ran and contributed | trust the fused ranking |
| `unavailable` | an embedder is configured but did not embed the query — unreachable, or not within `index.search.queryEmbedTimeout` — so results are lexical-only | retry later; results may differ |
| `disabled` | this server has no embedder — vector can never run until config changes | adjust the query toward exact terms; don't retry |
| `skipped` | the caller asked for `mode: "fts"` | the echo of your own choice |

With `unavailable` or `disabled`, `mode` in the reply reads `fts` even though the request said `hybrid`. Pure `vector` mode does not degrade this way — it errors instead (see [Vector search](vector.html)).

The query embedding is bounded by `index.search.queryEmbedTimeout` (default 5 s), so a model still loading, a wedged local embedder or a slow API turns into a lexical-only answer instead of a stalled search. The local embedder serves a search query ahead of queued index documents, so a busy re-index delays a query by at most the one decode in flight. Under `auto` the online API gets half the budget, leaving the local fallback time to answer.

> **Why it matters.** The embedder is the one moving part that can be absent or temporarily down — a model still downloading, an API outage, a laptop without the shared libraries. Degrading to full-text keeps search answering; reporting it lets the caller decide how much to trust a thin result.

## Weighting knobs

All under `index.search.*` in [configuration](../operations/configuration.html); defaults reproduce the untuned behaviour, so an absent block changes nothing.

| Key | Default | Effect |
|---|---|---|
| `ftsWeight` | 1 | RRF weight of the lexical leg |
| `vectorWeight` | 1 | RRF weight of the dense leg |
| `adaptiveWeights` | false | per query, down-weight the FTS leg when its BM25 scores are flat/weak |
| `stopWords` | true | strip stop words from the FTS-leg query |
| `defaultOperator` | `or` | how bare FTS terms combine |
| `minVectorSim` | 0 | cosine floor applied to vector hits before fusion |
| `queryEmbedTimeout` | `5s` | budget for embedding the query; past it hybrid answers lexical-only |

Equal weights (1/1) are optimal on the lexical-friendly reference corpus and a safe default elsewhere. Corpora where one leg is much weaker want a tilt: on paraphrastic Q&A the dense leg alone beats equal-weight hybrid, because a weak BM25 list drags the fusion down. `adaptiveWeights` automates that tilt from the query's own score distribution — a big win where the lexical leg is weak, a small cost where it is strong, hence off by default. It is asymmetric on purpose: only the FTS leg is ever down-weighted, since cosine scores are not calibrated enough to judge.

```yaml
index:
  search:
    adaptiveWeights: true      # semantic / conversational corpora
```

## Choosing a mode

- **`hybrid`** (default) for anything a person or agent types.
- **`fts`** for identifiers, error codes, exact names, and property-value lookups (`props` is full-text only anyway).
- **`vector`** when paraphrase recall matters more than precision and you want a hard failure rather than a silent degrade.

The measurements behind these recommendations are on [Evaluation](evaluation.html).
