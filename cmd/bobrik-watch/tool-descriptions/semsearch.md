# semsearch

## Tool Description

**Cheap** semantic + full-text search over the server's local index — chat messages, editor-block content, and agent-memory objects, ranked by a hybrid of BM25 lexical match and vector-embedding similarity (pipeline: docs/13-index.md). One HTTP call, no inner LLM loop, **no token burn**, returns in milliseconds. **Reach for this FIRST** whenever you need recall by meaning or by keyword: "what did we decide about X", "find the note that mentioned Y", "where did Z come up".

This is the cheap sibling of the RLM `search` / `ask` tool. Use **`search`/`ask`** instead only when semsearch comes back thin and you need DEEP recall: an inner model that reasons over the corpus, synthesizes a grounded answer with citations, scans records the index does not cover (it only indexes content written *after* indexing started on this server), or guarantees coverage. That tool costs seconds and real tokens (its `stats` tells you exactly how much); semsearch costs neither. Rule of thumb: **semsearch first, escalate to `search`/`ask` on a weak result or when you need a synthesized answer.**

Hybrid (the default) fuses lexical + semantic ranking and silently degrades to lexical-only when no embedder is available — always read `vectorStatus` before concluding from a thin result set: `used` = semantic recall participated; `unavailable` = embedder configured but momentarily down (retry may differ); `disabled` = this server never runs vectors (adjust to keyword-style queries, don't retry); `skipped` = you asked for `mode: "fts"`. Hits carry identity + the short indexed text, not full records — hydrate by querying the dataset (`getObjects`) by `recordId` when you need the whole thing.

## Tool Schema

### search(query, opts?) [getter]

Run a hybrid index search and return ranked hits.

**Input:**
- `query` (string, required) — what to find, natural language or keywords.
- `opts` (object, optional):
  - `scopes` (string[]) — which corpora to search; any of `"chat"` (chat messages), `"basic"` (editor-block page content), `"agent"` (agent-memory objects). Omit to search all.
  - `limit` (number, default 10, max 100) — max hits.
  - `mode` (`"hybrid"` | `"fts"` | `"vector"`, default `"hybrid"`) — `hybrid` fuses lexical + semantic (degrades to fts without an embedder); `fts` is pure BM25 (use for exact ids / names / error strings); `vector` is pure semantic (errors `index.no_embedder` on an FTS-only server).
  - `space` (string) — search ANY space on the account by id (cross-space; defaults to the agent's own space). Pair with the user's current-view `spaceId` to search "this space".

**Output:** `{ok, hits, mode, vectorStatus}` — or `{ok: false, error, code}` on failure.
- `hits`: `[{scope, objectId, dataset, recordId, data, score}]` ranked best-first. `data` is the short indexed text; `score` is comparable only WITHIN one response (BM25 vs cosine vs RRF differ across modes) — rank, don't threshold.
- `mode`: the mode that actually ran (e.g. `fts` when `hybrid` degraded).
- `vectorStatus`: `used` | `unavailable` | `disabled` | `skipped` — whether semantic recall took part, and why not.
- `code` on failure: `index.disabled` (indexer off on this server), `index.no_embedder` (`mode: "vector"` with no embedder), `index.embedder_unavailable` (`mode: "vector"` during an outage — retryable).

```js
var r = semsearch.search("what did we decide about the reranker?", {scopes: ["chat", "agent"]});
r.hits[0]        // {scope: "agent", objectId: "...", dataset: "agent_memory_items", recordId: "...", data: "...", score: 0.031}
r.vectorStatus   // "used" — semantic recall participated

// Cross-space: search the space the user is currently viewing.
var ctx = anyHelper.getUIContext();
semsearch.search("staging deploy notes", {space: ctx.spaceId, scopes: ["basic"]});

// Hydrate a hit's full record:
var hit = r.hits[0];
var rows = anyHelper.getObjects({dataset: hit.dataset, objectId: hit.objectId, filter: {id: hit.recordId}});
```
