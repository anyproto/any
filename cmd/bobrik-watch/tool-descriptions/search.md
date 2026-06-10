# search

## Tool Description

RLM-style **deep, EXPENSIVE** search over this space's data — memory items, chat history, and objects (design: docs/12-rlm-search.md). Each call spins an **isolated** inner LLM loop (its own context — none of the scanning ever enters YOUR context) that pages the corpus through indexed reads and maps batched sub-LLM relevance calls over snippets. You get back only the ranked top-k results plus honest `stats` (time, turns, toolcalls, tokens burned).

**Try `semsearch` FIRST.** `semsearch` is the cheap sibling — one HTTP call to the local vector + full-text index, milliseconds, **zero tokens**. Reach for *this* tool only when `semsearch` comes back thin and you need what an LLM loop buys: recall that reasons over snippets, a synthesized grounded answer with citations (`ask`), coverage guarantees, or scanning records the index does not cover (the index only holds content written after indexing started on this server). Expect a `search` call to take ~5-30s and burn real tokens (`stats` tells you exactly how much); for lookups you can express exactly (a category, a time range, a known type+filter), call `convmemory.*` / `anyHelper.getObjects` directly instead.

Cross-space: the `objects` scope honors `opts.space` (any space id on the account) so you can deep-search another space's objects; `memory` / `history` scopes always target the agent's own space (its brain + the invocation's chat). For cheap cross-space recall over chat/blocks/memory, prefer `semsearch({space})`.

Results carry a `why` line per hit — the inner model's one-line match rationale — so you can trust or discard hits without re-reading the corpus. If the loop hits its caps it wraps up with best-so-far (`stats.wrappedUp: true`, `note` says what wasn't scanned); if it produces nothing, you get `mode: "fallback-recency"` instead of a failure.

## Tool Schema

### search(query, opts?) [getter]

Run an RLM search and return ranked results.

**Input:**
- `query` (string, required) — what to find, natural language.
- `opts` (object, optional):
  - `scope` ("memory" | "history" | "objects" | "auto", default "auto") — which corpus; "auto" lets the inner loop decide from the query + corpus metadata.
  - `k` (number, default 8) — max results.
  - `type` (string) — objects scope: narrow to one type xKey.
  - `categories` (string[]) — memory scope: category narrow.
  - `periodFrom` / `periodUntil` (ISO date strings) — temporal narrow hint.
  - `chatId` (string) — history scope: which chat (defaults to the invocation's chat).
  - `space` (string) — objects scope: deep-search another space's objects by id (cross-space; defaults to the agent's own space). `memory` / `history` scopes ignore it — they are always own-space. For cheap cross-space recall use `semsearch({space})` instead.
  - `maxToolcalls` (number, default 12) / `maxRootTurns` (number, default 8) — inner-loop caps; on exhaustion the loop wraps up with best-so-far, it never truncates silently.
  - `synthesize` (bool, default false) — also produce `answer` + `citations` (or just call `ask`).

**Output:** `{ok, mode, scope, results, note?, answer?, citations?, stats}`
- `mode`: `"rlm"` (LLM loop over indexed reads) | `"rlm+semantic"` (vector service fed candidates) | `"fallback-recency"` (loop produced no final — recency best-effort) | `"error"`.
- `results`: `[{id, kind: "item"|"chunk"|"turn"|"object", score: 0-10|null, title, snippet, why, source: {objectId, link}}]` ranked best-first.
- `stats`: `{rootTurns, toolcalls, subCalls, batchedSubCalls, objectsScanned, tokensIn, tokensOut, ms, wrappedUp}` — `toolcalls` = inner cells executed, `subCalls` = batched classify prompts issued, `tokensIn/Out` = total tokens burned (root turns + batched maps), `ms` = wall time.

```js
var r = search.search("what did we decide about lexid ordering?", {scope: "memory"});
r.results[0]   // {id, kind: "item", score: 9, title: "...", snippet: "...", why: "directly states the lexid decision"}
r.stats        // {rootTurns: 3, toolcalls: 4, subCalls: 5, tokensIn: 18234, tokensOut: 2410, ms: 9120, wrappedUp: false}

search.search("pages about the staging deployment", {scope: "objects", type: "pages", k: 5});
```

### ask(question, opts?) [getter]

`search` with `synthesize: true` — returns a direct `answer` grounded in scanned records, with `citations` (subset of `results` ids backing it). Same opts and output shape as `search`; `answer` is always present on success.

```js
var a = search.ask("which provider did we pick for embeddings and why?");
a.answer      // "OpenAI text-embedding-3-small, chosen for ... [grounded in scanned records]"
a.citations   // [{id: "..."}] — back-references into a.results
a.stats.ms    // how long the whole recursive scan took
```

### createSearch(deps?) [getter]

Advanced/testing: build a search instance with injected collaborators (`{client, conv, llm, batch, spaceId, chatId, maxToolcalls, maxRootTurns, contextCeilingChars, rootTier}`). The plain `search.search(...)` / `search.ask(...)` calls use environment defaults — you almost never need this from a cell.

```js
var inst = search.createSearch({maxToolcalls: 4});
inst.search("quick recall", {scope: "memory", k: 3});
```
