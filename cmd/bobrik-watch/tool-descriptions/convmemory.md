# convmemory

## Tool Description

Conversation history + long-term memory over the server's agent data layer. Three indexed datasets back it: raw `agent_turns` (every past agent invocation, append-only, full fidelity), `agent_chunks` (compressed summaries that carry explicit `fromSeq..toSeq` pointers into the raw turns), and `agent_memory_items` (typed facts/preferences/lessons on the per-space brain object).

**The layering contract:** your prompt's "[Earlier context, compressed]" block shows chunks with handles like `[chunk #3, turns 12..21]`. Nothing old is ever lost — `expandChunk(3)` fetches the exact raw turns that summary covers, and `turnRange`/`turnsByPeriod` slice the raw log directly. Drill down instead of guessing what a summary elided.

**⚠ Semantic search is NOT available yet** — a separate vector-search service is planned but not built. `search()` works in degraded mode only: explicit/detected time ranges, category filters, and recency. It does NOT rank by similarity. Prefer the precise methods (`memoryByCategory`, `memoryByPeriod`, `expandChunk`) over `search` until then.

**Writes are typed and classifier-free**: `addMemory(text, {category, context})` — YOU supply the category and one-line context; there is no auto-extraction LLM hop.

### Shape cheat sheet

- **Write**: `convmemory.addMemory(text, {category: "preference", context: "..."})` — **singular** `category`, both REQUIRED.
- **Read filter**: `convmemory.memoryByCategory({categories: ["preference"]})` — **plural** `categories`.
- History methods take `{chatId: args.chatId}` — usually auto-resolved from the invocation, pass explicitly when in doubt.

### When to use which method

| Situation | Verb |
|---|---|
| Check preferences/lessons before acting | `memoryByCategory({categories: ["preference", "lesson"]})` |
| A compressed chunk in your context looks relevant | `expandChunk(chunkSeq)` |
| User references a time range ("last week", dates) | `turnsByPeriod({from, until})` / `memoryByPeriod({from, until})` |
| Walk compressed history chronologically | `listChunks({limit})` |
| Need older raw turns than your window shows | `recentTurns({limit})` / `turnRange({fromSeq, toSeq})` |
| Save something durable this turn produced | `addMemory(text, {category, context, tags?})` |
| Adjust an existing memory (salience, tags, body) | `evolveMemory(itemId, fields)` |
| What categories exist | `listCategories()` |

## Tool Schema

### recentTurns(opts) [getter]

Newest N raw turn records for a chat, oldest-first.

**Input:** `opts`: `limit` (default 8), `chatId` (default: current chat).

**Output:** `{ok, turns: [{seq, createdAt, userName, userText, think, replies, effects, messageIds, debugRef, llm}]}`.

```js
convmemory.recentTurns({ limit: 12 });
```

### turnRange(opts) [getter]

Inclusive raw-turn slice by seq — the drill-down primitive behind expandChunk.

**Input:** `opts`: `fromSeq` (required), `toSeq` (required), `chatId`.

**Output:** `{ok, turns: [...]}` ascending by seq.

```js
convmemory.turnRange({ fromSeq: 12, toSeq: 21 });
```

### expandChunk(chunkSeq, opts) [getter]

Expand a compressed chunk into the exact raw turns it summarizes. Use the `#N` from the `[chunk #N, turns A..B]` handle in your compressed-context block.

**Input:** `chunkSeq` (number, required); `opts`: `chatId`.

**Output:** `{ok, chunk: {seq, summary, periodStart, periodEnd, fromSeq, toSeq, turnsCovered}, turns: [...]}`.

```js
convmemory.expandChunk(3);
```

### listChunks(opts) [getter]

Page through compressed-history chunks, newest-first by default.

**Input:** `opts`: `limit` (default 20), `offset`, `order` (`"asc"`|`"desc"`), `chatId`.

**Output:** `{ok, chunks: [...]}`.

### turnsByPeriod(opts) [getter]

Raw turns in a time window (indexed on createdAt).

**Input:** `opts`: `from` and `until` (required — ISO strings or unix seconds; until exclusive), `chatId`.

**Output:** `{ok, turns: [...]}` ascending by time.

```js
convmemory.turnsByPeriod({ from: "2026-06-01", until: "2026-06-03" });
```

### search(query, opts) [getter]

Degraded-mode recall — **semantic search is a non-functional TODO** until the external vector service lands. Dispatch: explicit `{periodFrom, periodUntil}` or a detected temporal phrase ("yesterday", "last week", "in march") → indexed period slice; `{categories}` → category filter; else → recency. The response's `mode` + `note` say which path ran. Results are NOT similarity-ranked.

**Input:** `query` (string); `opts`: `k` (default 8), `periodFrom`/`periodUntil`, `categories` (string[]).

**Output:** `{ok, mode: "period"|"period_detected"|"category"|"recent"|"semantic", note?, detected?, results: [...]}`.

```js
convmemory.search("what did we do yesterday?");                       // → period_detected
convmemory.search("", { categories: ["decision"], k: 5 });            // → category
```

### addMemory(text, opts) [mutator]

Save one typed memory item to the space brain. You classify it — `category` and `context` are required; nothing is auto-extracted.

**Input:**
- `text` (string) — the memory body (markdown).
- `opts`:
  - `category` (string, REQUIRED) — lowercase slug. Builtins: claim, preference, decision, lesson, episode, taskstate, insight. Inventing new slugs is allowed; REUSE existing ones first (see the Memory categories prompt section).
  - `context` (string, REQUIRED) — one-line summary (≤ 512 bytes; this is the display + recall snippet).
  - `tags`, `entities`, `keywords` (string[], optional).
  - `confidence` (0..10), `importance` (1..10) — defaults 5/5.
  - `edges` (`[{to, type, strength?}]`, optional) — typed links to other item ids.

**Output:** `{ok, id, category, context}` or `{ok: false, error}`.

```js
convmemory.addMemory("User prefers updating an existing page over creating duplicates.", {
  category: "preference", context: "edit-before-create preference", tags: ["workflow"]
});
```

### evolveMemory(itemId, fields) [mutator]

Patch an item's mutable fields. Allow-list: `salience` (0..10), `accessCount`, `confidence` (0..10), `importance` (1..10), `context`, `body`, `tags`, `edges`. Author-only; everything else (category, timestamps) is immutable — recategorizing means a new item.

**Input:** `itemId` (string), `fields` (object with one or more allow-listed keys).

**Output:** `{ok, id}` or `{ok: false, error}`.

### memoryByCategory(opts) [getter]

Newest items in the given categories (indexed filter).

**Input:** `opts`: `categories` (string[], required), `limit` (default 20).

**Output:** `{ok, items: [{id, category, context, body, tags, entities, keywords, confidence, importance, salience, validFrom, createdAt, edges, chatId}]}`.

```js
convmemory.memoryByCategory({ categories: ["preference", "lesson"] });
```

### memoryByPeriod(opts) [getter]

Items valid in a time window (indexed on validFrom).

**Input:** `opts`: `from`/`until` (required — ISO or unix seconds), `categories` (optional narrow), `limit`.

**Output:** `{ok, items: [...]}` ascending by validFrom.

### recentMemories(opts) [getter]

Newest items, optionally category-narrowed.

**Input:** `opts`: `limit` (default 20), `categories` (optional).

**Output:** `{ok, items: [...]}`.

### listCategories() [getter]

Category inventory — builtins plus every slug actually observed in the store.

**Output:** `{builtin: [...], observed: [{name, count, builtin}]}`.
