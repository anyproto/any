---
title: Search
description: A local BM25 + vector search index over every space, built from the change feed and queried through one endpoint.
order: 0
---
# Search

Every `any` server keeps a local search index — BM25 full-text plus semantic vectors — over the chats, documents, property values and runtime-dataset records of every space it holds. One endpoint, `POST /v1/spaces/:spaceId/search`, queries it in three modes; the index itself never leaves the machine.

## The shape of it

```
   space change feed            index.db (per space)             /search
 ┌──────────────────┐   chunkers   ┌───────────────────┐  fts    ┌──────────┐
 │ chat_messages    │ ───────────▶ │ doc: objectId:    │ ──────▶ │          │
 │ editor_blocks    │  text only   │      dataset:     │         │  hybrid  │
 │ objects (props)  │              │      recordId     │  vector │  (RRF)   │
 │ runtime datasets │              │ data, scope, hash │ ──────▶ │          │
 └──────────────────┘              │ vector?, pending? │         └──────────┘
                                   └───────────────────┘
```

- **Indexing** is a background consumer of each space's change feed. It never blocks writes and never touches the CRDT — the index is derived state that can be deleted and rebuilt. The same pass maintains the link index behind backlinks.
- **Full-text** search is always available, with no external dependency. New writes are searchable within about a 250 ms debounce.
- **Vector** search activates when an embedder is configured. The default, `auto`, embeds through an online API serving the same model the server also runs on the device (llama.cpp in a child process, auto-downloaded) as its fallback; `local` embeds on the device only. An unreachable embedder only pauses the vector side — full-text keeps working. See [Embedders](embedders.html).
- **Hybrid** is the default query mode: both legs fused by reciprocal rank, degrading to full-text on its own when the embedder cannot help. The reply says which mode actually ran.

> **Why it matters.** Search over an encrypted, local-first database has to run where the plaintext is — on the device. There is no server-side index to leak, and the same index works offline; with `index.embedder: local` or `none`, no indexed text or query leaves the device at all.

## A first query

The server must be a search-enabled build (`make build` or a release tarball): a bare `go build` compiles search out and the only symptom is zero hits.

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

Hits carry identity, not full records — hydrate them with a [dataset query](../database/reading-data.html) when you need the whole row. `data` is a window of at most `maxData` runes (default 512, `-1` for the whole chunk) around the first matching term; `dataOffset` and `dataTotal` locate it in the indexed text. Long records are indexed in chunks of roughly 2000 runes; the hit is the record's best-ranked chunk (`chunk` says which), one hit per record, and `limit` counts records. Ask for `passages: N` (max 10) to get a record's next best matching chunks on `hit.passages`.

## What is indexed

| Content | Dataset in hits | Scope | Unit |
|---|---|---|---|
| Chat messages | `chat_messages` | `chat` | one message (text only) |
| Editor documents | the editor storage collection (`editor_blocks` or `<typeId>_<key>`) | `basic` | a ~1.5 KB window of consecutive blocks |
| Object name / description | `prop` | `basic` | one entry per built-in |
| User property values | `prop` | `props` (full-text only) | `"<prop name>: <value>"` |
| Runtime-dataset records | the dataset's own name | `basic` (or the declared scope) | one record, by its `x-search` mapping — split into ~2000-rune chunks when long |

Runtime datasets declared without an `x-search` mapping (a program's source, for one) and file bytes are never indexed.

## Filtering by host object

An objects-query `filter` in the search body restricts which objects may contribute hits — `{"any.collections":{"$nin":["bin"]}}` drops binned objects. The filter is evaluated against the host object's current row, not against the fields of the matching chat message or runtime record.

`truncated: true` in the reply means a leg's read budget ran out under the filter before the page filled. More matches may exist: narrow the query or the filter, and never treat that short page as exhaustive. [Hybrid ranking](hybrid.html#filtering-by-host-object) has the full contract.

## Reading further

<div class="cards">
<a href="full-text.html"><strong>Full-text search</strong><span>BM25, phrases, prefixes, require/exclude, stop words</span></a>
<a href="vector.html"><strong>Vector search</strong><span>Semantic recall, the ANN index, similarity scores</span></a>
<a href="hybrid.html"><strong>Hybrid ranking</strong><span>Reciprocal-rank fusion, vectorStatus, weighting knobs</span></a>
<a href="indexing.html"><strong>How indexing works</strong><span>Chunkers, scopes, gating, removal, rebuilds, the link index</span></a>
<a href="embedders.html"><strong>Embedders</strong><span>local, ollama, openai, auto, none — and outage semantics</span></a>
<a href="evaluation.html"><strong>Evaluation</strong><span>What was measured and why the defaults are what they are</span></a>
</div>
