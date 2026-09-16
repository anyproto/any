---
title: Search
description: Find relevant text with full-text or semantic search, choose an embedder, and interpret ranked results.
order: 0
---
# Search

Search finds relevant text in a space's documents, chat messages, properties, and declared runtime datasets. Each search-enabled server builds its own local index from the data it holds; the index is derived state and can be rebuilt.

Use a [database query](../database/reading-data.html) when you need exact enumeration or filters over records. Use search when you want records ranked by words or meaning.

## Choose a task

| I want to… | Guide |
|---|---|
| Find exact terms, phrases, prefixes, or property values | [Full-text search](full-text.html) |
| Find similar meanings even when the words differ | [Vector search](vector.html) |
| Combine word matches and semantic ranking | [Hybrid ranking](hybrid.html) |
| Run embeddings on this device or choose a provider | [Embedders](embedders.html) |
| Understand what is indexed, freshness, and rebuilds | [How indexing works](indexing.html) |
| Review measurements behind the tuning defaults | [Evaluation](evaluation.html) |

## The shape of it

The indexer reads changed records in the background, extracts text, and stores searchable chunks. It also maintains the link index used by backlinks. Indexing does not change your records or block their writes.

| Query mode | Ranking | What it needs |
|---|---|---|
| `fts` | BM25: a score based on matching words and their frequency | the full-text index; no embedding model |
| `vector` | similarity between numeric representations of text | an available embedder and indexed vectors |
| `hybrid` (default) | combines the rankings of both methods | uses full-text alone if embedding is unavailable |

Full-text normally catches up after a 250 ms debounce plus processing time. Vector results also wait for embedding. Read the response's `mode` and `vectorStatus` to understand what ran; an empty filtered set has its own status rules in [Hybrid ranking](hybrid.html#filtering-by-host-object).

### Embeddings on your device

An **embedding** is a vector of numbers representing text. With `index.embedder: local`, the server computes vectors on this device using its local model. A first run can download the model; after it is available, embedding needs no online service. The model download is separate from processing your text.

```yaml
index:
  embedder: local
```

Use `none` for full-text only. Other provider modes are available; where embedding text is processed depends on the effective `index.embedder` setting and endpoint configuration. See [Embedders](embedders.html) for prerequisites, standalone defaults, and mobile behavior.

## A first query

**Before you start:** run a search-enabled build (`make build` or a matching release) with the account that can read `$SPACE`. Give the indexer time to process some text. A plain Go build without the search tags does not provide searchable results.


```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "zeppelin disaster", "limit": 5}'
```

```bash
any search $SPACE "zeppelin disaster" --limit 5
```

```json
{
  "hits": [
    { "scope": "chat", "objectId": "…", "dataset": "chat_messages",
      "recordId": "…", "data": "the zeppelin disaster of 1937",
      "dataTotal": 29, "score": 0.0328 }
  ],
  "mode": "hybrid",
  "vectorStatus": "used"
}
```

Hits identify their source and include a text excerpt. Use a [query](../database/reading-data.html) to read the full source record. Editor hits represent windows of blocks, while `prop` hits refer to object properties; use `objectId` and the source type to select the appropriate read. `data` is a window of at most `maxData` runes (default 512, `-1` for the whole chunk) around the first matching term; `dataOffset` and `dataTotal` locate it in the indexed text. Long records are indexed in chunks of roughly 2000 runes; the hit is the record's best-ranked chunk (`chunk` says which), one hit per record, and `limit` counts records. Ask for `passages: N` (max 10) to get a record's next best matching chunks on `hit.passages`.

## What is indexed

| Content | Dataset in hits | Scope | Unit |
|---|---|---|---|
| Chat messages | `chat_messages` | `chat` | one message (text only) |
| Editor documents | the editor storage collection (`editor_blocks` or `<typeId>_<key>`) | `basic` | a ~1.5 KB window of consecutive blocks |
| Object name / description | `prop` | `basic` | one entry per built-in |
| User property values | `prop` | `props` (full-text only) | `"<prop name>: <value>"` |
| Runtime-dataset records | the dataset's own name | `basic` (or the declared scope) | one record, by its `x-search` mapping — split into ~2000-rune chunks when long |

Runtime datasets declared without an `x-search` mapping (a program's source, for one) and file bytes are never indexed.

## Filter the host object

Add an objects-query `filter` to restrict the objects that can contribute hits. For example, `{"any.collections":{"$nin":["bin"]}}` excludes binned objects. This checks the host object's current row, not the fields of a matching chat message or runtime record.

If a response says `truncated: true`, the filtered search exhausted a read budget before filling the requested page. More matches may exist. Narrow the query or filter; do not treat that short page as exhaustive. [Hybrid ranking](hybrid.html#filtering-by-host-object) explains the full contract.

## Reading further

<div class="cards">
<a href="full-text.html"><strong>Full-text search</strong><span>BM25, phrases, prefixes, require/exclude, stop words</span></a>
<a href="vector.html"><strong>Vector search</strong><span>Semantic recall, the ANN index, similarity scores</span></a>
<a href="hybrid.html"><strong>Hybrid ranking</strong><span>Reciprocal-rank fusion, vectorStatus, weighting knobs</span></a>
<a href="indexing.html"><strong>How indexing works</strong><span>Chunkers, scopes, gating, removal, rebuilds, the link index</span></a>
<a href="embedders.html"><strong>Embedders</strong><span>local, ollama, openai, auto, none — and outage semantics</span></a>
<a href="evaluation.html"><strong>Evaluation</strong><span>What was measured and why the defaults are what they are</span></a>
</div>
