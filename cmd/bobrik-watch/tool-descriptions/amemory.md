## Tool Description

Long-term memory over the agent's persistent store. **Memory content is NOT in your prompt by default** — `amemory.search(...)` is how you read it. The "Earlier context, compressed" prepend you see at turn start is chat-history summarisation, not the full store; preferences, decisions, and lessons from past sessions never enter your prompt until you search.

Treat `amemory.search` as a reflex before acting: before non-trivial answers, before create/edit/write moves, before committing to a design choice. One search costs ~1s; skipping it risks contradicting a stated preference or repeating a known mistake.

**All read methods return compact records** — `{id, name, kind, period?, context, keywords, tags, score?}`. No full markdown body. If you need a body, call `anyHelper.getObject(id)` on the hit you care about.

### Shape cheat sheet (commonly confused)

- **Write**: `amemory.addMemory(text, {category: "preference", ...})` — **singular** `category`, REQUIRED.
- **Search filter**: `amemory.search(query, {categories: ["preference"]})` — **plural** `categories`, optional whitelist.

### When to use which method

| Situation | Verb |
|---|---|
| Look up a past topic / name / ID, or check preferences before acting | `amemory.search(query, {categories: [...]?})` |
| User gives a date range | `amemory.search(query, {periodFrom, periodUntil})` |
| Walk compressed history chronologically (no specific query) | `amemory.listChatChunks(opts)` |
| Expand a chat_chunk hit with ±N neighbours | `amemory.getNeighbours(chunkId, opts)` |
| Save something durable this turn produced | `amemory.addMemory(text, opts)` |
| Check what categories exist (only when needed) | `amemory.listCategories()` |

## Tool Schema

### search(query, opts) [getter]

Hybrid search across all memories. Three dispatch modes:

- `{periodFrom, periodUntil}` set → period slice. `mode: "period"`.
- Query contains a temporal phrase (`"last week"`, `"yesterday"`, `"N days ago"`, `"in march"`) → auto-detected range. `mode: "period_detected"` with `detected: {from, until}`.
- Else → multi-query rewrite + hybrid semantic/FTS/entity scoring. `mode: "semantic"` with `rewrite`.

**Input:**
- `query` (string, optional if both periods set).
- `opts`:
  - `k` (default 8, max 20).
  - `periodFrom` / `periodUntil` (ISO strings, both required together).
  - `categories` (string[]) — whitelist filter. Applied absolutely (including link expansion). When set, cosine cutoff relaxes to 0 so every in-category match surfaces; use this to target preferences/lessons whose wording wouldn't overlap with your topic query.
  - `excludeCategories` (string[]) — blacklist. `{excludeCategories: ["chat_chunk"]}` is the typical way to drop chunk noise.
  - `chatId` (string, optional) — chat object id. Scopes only chat_chunk results; non-chunk memories stay space-global either way. Defaults to the current chat when the bot runs under the middleware. Pass it explicitly in cells that want cross-chat recall.
  - `chatIdFilter` (`"all"` | `"only"` | `"exclude"`, default `"all"`) — interprets `chatId`. `"only"` returns chat_chunks for this chat only; `"exclude"` hides them. `"all"` ignores the field for filtering but still lets you label each result (each compact record carries `chatId`).

**Output:** `{ok, mode, results: [...], detected?, rewrite?}`. Each result carries `chatId` (empty for space-wide memories).

**Examples:**
```js
// Default semantic search
amemory.search("what did we decide about the reranker?", { k: 6 });

// Proactive preference check before acting on "create X"
amemory.search("create new page", { categories: ["preference"] });

// Chat-scoped recall — only chunks from the current chat
amemory.search("the bug we were tracking", { chatId: args.chatId, chatIdFilter: "only" });
```

### listChatChunks(opts) [getter]

Paginated, time-ordered browse of compressed chat history. Chunks only — for mixed time-slices covering typed memories too, use `search` with `periodFrom`/`periodUntil`.

**Input** (all optional): `offset` (0), `limit` (10, max 50), `periodFrom`/`periodUntil`, `order`: `"newest_first"` (default) or `"oldest_first"`.

**Output:** `{ok, total, offset, limit, order, chunks: [...]}`.

**Example:**
```js
amemory.listChatChunks({ limit: 5 });  // 5 most recent chunks
```

### getNeighbours(chunkId, opts) [getter]

±N temporal neighbours of a chat_chunk. Strict adjacency — chunks overlapping the target's period are excluded.

**Input:** `chunkId` (string) + `{before?: 1, after?: 1}`.

**Output:** `{ok, target, before, after}` — each a compact chunk record.

**Example:**
```js
var hits = amemory.search("slice 1b design", { k: 4 });
var top = hits.results[0];
if (top.kind === "chat_chunk") {
  amemory.getNeighbours(top.id, { before: 1, after: 1 });
}
```

### addMemory(text, opts) [mutator]

Save a durable fact / decision / lesson / preference / episode / task-state / insight. **You** pick the metadata — no classifier safety net on this path. Budget: **~1–2 saves per turn max**. Saving every turn means you're saving the wrong things.

**One save per concept.** A content-cosine dedup gate (threshold 0.85) catches near-duplicates on write and returns `{ok: true, deduplicated: true, duplicateOf, similarity, existingContext, existingName}`. **That's a success**, not a retry signal — don't "save again to be safe" on a subsequent turn.

**Before saving**, scan this turn's `amemory.search` results for equivalents. Agent-level matching on meaning is cheaper and more accurate than the tool-level cosine gate.

**When to save:**

| ✓ Save | ✗ Skip |
|---|---|
| User preference (stable) | Greetings / capability questions |
| Decision made this session | Meta-observations about the conversation |
| Domain fact about the user | Restatement of what the user just said |
| Lesson learned the hard way | Anything already in injected chat history |
| Persistent outcome (object shipped) | Speculation below confidence ~5 |
| Session episode / taskstate worth propagating |  |

**Input:**
- `text` (required) — the memory content. Sentence or paragraph; stored as the object body.
- `opts` (required):
  - `category` (string, REQUIRED, **singular**) — pick a builtin or invent. Before inventing, check `listCategories()` so you reuse an existing invented category (proliferation like `api_quirk` / `api-quirk` degrades retrieval).
  - `context` (string, REQUIRED) — one-line summary (~80 chars), specific and self-describing. This is how **you** will recognize this memory in future search results. `"code style"` is bad (generic — won't recognize *which* code style later); `"TODO format: // TODO(name): ..."` is good. Don't paste the first N chars of `text`.
  - `entities` (string[], optional) — canonical lowercase names, 1–3 typical.
  - `confidence` (0–10, default 7) — 9–10 for user-stated facts, 5–7 for inferences, <5 means don't save.
  - `importance` (1–10, default 5) — episodes/taskstates 3–5; decisions/preferences 6–8; critical lessons 8–10.
  - `tags` (string[], optional) — extra labels beyond the category.
  - `skipDedup` / `dedupThreshold` — escape hatches, rarely needed.

**Output:**
- Fresh save: `{ok: true, id, category, context, keywords, entities, tags, direct: true}`.
- Dedup hit: `{ok: true, deduplicated: true, duplicateOf, similarity, existingContext, existingName}`.
- Error: `{ok: false, error}`.

**Example:**
```js
amemory.addMemory(
  "User prefers terse responses with no trailing summaries — reads the diff.",
  { category: "preference",
    context: "User wants terse responses, no summary tail",
    entities: ["user"], confidence: 9, importance: 7 }
);
```

### listCategories() [getter]

Returns `{builtin, observed: [{name, count, builtin}, ...]}`. **Don't poll routinely** — call this only when you need live counts or are confirming an invented-category spelling before writing.

**Builtin preferred categories:**

| category | use when |
|---|---|
| `claim` | generic domain fact |
| `preference` | user preference (stable) |
| `decision` | explicit choice made this session |
| `lesson` | learned the hard way |
| `episode` | what happened this turn (short-lived) |
| `taskstate` | in-progress work status |
| `insight` | realization / connection you drew |
| `chat_chunk` | compressed chat history (written by the compression pipeline — don't emit from `addMemory` directly) |
