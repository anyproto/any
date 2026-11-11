---
title: Hybrid ranking
description: The default mode fuses the lexical and semantic legs by reciprocal rank, degrades gracefully, and tells you what actually ran.
order: 30
---
# Hybrid ranking

`hybrid` runs both legs and fuses them with reciprocal-rank fusion, in a form each query picks from its own legs. It is the default because it is the safe choice across corpora: it wins outright on lexical-friendly content and never collapses when one leg is weak — and when the embedder cannot help at all, it becomes full-text search by itself and says so in `vectorStatus`.

## The default call

```bash
curl -s http://127.0.0.1:2046/v1/spaces/$SPACE/search \
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
      "recordId": "…", "data": "…", "dataTotal": 29, "score": 0.92 }
  ],
  "mode": "hybrid",
  "vectorStatus": "used",
  "scoreScale": "unit"
}
```

`limit` counts records (one hit per record), defaults to 10 and caps at 100. `score` is the fused RRF score divided by its maximum: 0–1, comparable across responses, and marked by `scoreScale: "unit"` on the reply ([Reciprocal-rank fusion](#reciprocal-rank-fusion)). Objects whose name matches the typed words come first, marked with `match` and scored by BM25 within their block ([Titles first](full-text.html#titles-first)); when they fill the page the legs do not run and `vectorStatus` reads `skipped` (`disabled` on a server without an embedder, where `mode` reads `fts`).

## Reciprocal-rank fusion

Each leg returns a ranked list; a document's fused score is the sum over the legs that returned it of `weight / (k + rank)`. Rank, not raw score, is what gets combined, so BM25 and cosine never have to be put on one scale. When two legs fuse, a document found by both rises above one found by only one.

```
fused(doc) = ftsWeight / (k + rank_fts) + vectorWeight / (k + rank_vec)
score(doc) = fused(doc) / ((ftsWeight + vectorWeight) / (k + 1))
```

Ranks start at 1, so a document ranked first by every fused leg scores 1. The score reads 0–1 whatever the weights and k, so a client keeps one threshold.

By default each query picks its weights and k from its own legs. The first test is how many of the query's words appear in the best of the top three lexical chunks:

| query words found | stronger leg | fusion |
|---|---|---|
| under 80% | cosine flat (top 1 − top 5 < 0.103) | vector-heavy: `1 / 2`, k 20 |
| under 80% | cosine peaked | the vector leg alone, k 0 |
| 80% or more | BM25 flat (relative top 1 − top 2 < 0.184) | lexical-heavy: `1 / 0.5`, k 20 |
| 80% or more | BM25 peaked | the lexical leg alone, k 0 |

A single leg fuses at k 0: its order doesn't change, and its score becomes 1 / rank, so a threshold above 0.5 admits only its first hit — the one a single leg reliably gets right. The hits only the other leg holds follow, scored as the next ranks, so the page is never shorter for it. A hit missing from a fused leg can't reach 1, an empty leg included. A `word*` in the query counts as found when any word starts with it. One fixed weight can't serve both kinds of query: above about 1.2, every vector-only hit outranks every lexical-only one, the lexical first hit included, which costs word, name and id lookups.

Full-text operators (`"phrases"`, `prefix*`) shape the lexical leg's list; the vector leg always embeds the whole `query` verbatim, stop words included. `require` / `exclude` bind every hit regardless of leg: vector hits are post-filtered against the FTS index before fusion, so a fused result never contains a doc the lexical constraint would have refused.

## Read `vectorStatus` before trusting recall

The reply's `mode` is the mode that actually ran, and `vectorStatus` says whether semantic recall participated and, if not, why. Agents in particular should branch on it before concluding that "nothing matched".

| `vectorStatus` | Meaning | What to do |
|---|---|---|
| `used` | the vector leg ran and contributed | trust the fused ranking |
| `unavailable` | an embedder is configured but did not embed the query — unreachable, or not within `index.search.queryEmbedTimeout` — or embedded it at another dimension than the index ([Dimension pinning](vector.html#dimension-pinning)), so results are lexical-only | retry later; results may differ, except for a dimension change, which lasts until the index is rebuilt |
| `disabled` | this server has no embedder — vector can never run until config changes | adjust the query toward exact terms; don't retry |
| `skipped` | the caller asked for `mode: "fts"` or `"title"`, a `filter` matched no object, or the title hits filled the page, so no leg ran | the echo of your own choice, an empty filtered set, or a page of titles |

When a leg runs, `unavailable` or `disabled` makes `mode` in the reply read `fts` even though the request said `hybrid`. Pure `vector` mode does not degrade this way — it errors instead (see [Vector search](vector.html)).

The query embedding is bounded by `index.search.queryEmbedTimeout` (default 5 s), so a model still loading, a wedged local embedder or a slow API turns into a lexical-only answer instead of a stalled search. The local embedder serves a search query ahead of queued index documents, so a busy re-index delays a query by at most the one decode in flight. Under `auto` the online API gets half the budget, leaving the local fallback time to answer.

## Filtering by host object

`filter` keeps only hits whose host object matches, in the [objects query](../database/reading-data.html) filter grammar verbatim — `any.type`, `any.collections`, `<ownerId>.<propId>`, `modifiedAt`, `id`. It binds every mode, like `require` / `exclude`, and checks the object's live row, so a property write counts at once. `limit` still counts matching records.

```bash
curl -s http://127.0.0.1:2046/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "reranker", "filter": {"any.collections": {"$nin": ["bin", "template"]}}}'
```

```bash
any search $SPACE reranker --filter '{"any.collections": {"$nin": ["bin", "template"]}}'
```

Two replies need care:

- A filter no object matches answers `hits: []` without running a leg: `mode` echoes the request, and `vectorStatus` reads `skipped` (`disabled` on a server without an embedder).
- `truncated: true` means a leg's read budget ran out under the filter while the page held fewer than `limit` records. The index may hold more matches, so a short page is not exhaustive — narrow the filter or the query. Without a filter the field is absent.

> **Why it matters.** The embedder is the one moving part that can be absent or temporarily down — a model still downloading, an API outage, a laptop without the shared libraries. Degrading to full-text keeps search answering; reporting it lets the caller decide how much to trust a thin result.

## Weighting knobs

All under `index.search.*` in [configuration](../operations/configuration.html); an absent block takes the defaults.

| Key | Default | Effect |
|---|---|---|
| `ftsWeight` | unset | fixed RRF weight of the lexical leg (k 60); with `vectorWeight` also unset, each query picks its form |
| `vectorWeight` | unset | fixed RRF weight of the dense leg (k 60) |
| `adaptiveWeights` | false | turns fixed weights on (1 / 1 unless set) and, per query, down-weights the FTS leg when its BM25 scores are flat/weak |
| `stopWords` | true | strip stop words from the FTS-leg query |
| `defaultOperator` | `or` | how bare FTS terms combine |
| `minVectorSim` | 0 | cosine floor applied to vector hits before fusion |
| `queryEmbedTimeout` | `5s` | budget for embedding the query; past it hybrid answers lexical-only |

Fixed weights suit a corpus measured on its own. Equal weights (1/1) are optimal on the lexical-friendly reference corpus. Corpora where one leg is much weaker want a tilt: on paraphrastic Q&A the dense leg alone beats equal-weight hybrid, because a weak BM25 list drags the fusion down. `adaptiveWeights` automates that tilt from the query's own score distribution — a big win where the lexical leg is weak, a small cost where it is strong, hence off by default. It is asymmetric on purpose: only the FTS leg is ever down-weighted, since cosine scores are not calibrated enough to judge.

```yaml
index:
  search:
    adaptiveWeights: true      # semantic / conversational corpora
```

## Choosing a mode

- **`hybrid`** (default) for anything a person or agent types: titles first, then fused content.
- **`fts`** for identifiers, error codes, exact names, and property-value lookups (`props` is full-text only anyway): titles first, then lexical content.
- **`title`** for a search box while the user types: the title block alone, one hit per object, no embedding.
- **`vector`** when paraphrase recall matters more than precision and you want a hard failure rather than a silent degrade; no title block.

The measurements behind these recommendations are on [Evaluation](evaluation.html).
