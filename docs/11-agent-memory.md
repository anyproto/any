# Agent data layer — turns, chunks, memory

The agent's conversational history and long-term memory live in three
built-in, validated, indexed datasets — replacing the legacy scheme of
a rolling markdown transcript plus a runtime-created `agent_memory`
property type (hex vectors in text props, CSV-in-text arrays,
category-in-`tags[0]`). Go packages: `internal/agentlog`,
`internal/agentmem`.

## The layering model

Design requirement: **keep all raw data, keep the live context lean,
make every summary drill down to the raw range it covers.**

```
chat object  (multitype: chat + agent_log — attached on first agent write)
├── chat_messages       what humans & agents said            (internal/chat)
├── agent_turns         one record per agent invocation —    append-only,
│                       userText / think / replies / effects   never edited,
│                       / messageIds / traceRef / llm          never deleted
└── agent_chunks        one record per compression event —
                        summary + fromSeq..toSeq + period      immutable

space brain object  (type agent_memory, deterministic derived id)
└── agent_memory_items  typed memory items: category/context/body,
                        tags/entities/keywords arrays, confidence/
                        importance/salience numbers, edges graph

```

Heavy per-LLM-turn diagnostics (tool cells, raw responses) are NOT a
dataset: the anybao runtime keeps them as device-local trace objects
(`anyrt trace show/follow`), referenced from turns via `traceRef`.

The agent's boot window is bounded queries (`sort:["-seq"], limit:N`),
not file size. Compression produces chunks for long-range recall; it
never mutates or deletes turns. Every layer points at the layer below:

```
chunk{fromSeq,toSeq}  →  agent_turns {seq:{$gte,$lte}}   (same object)
turn.messageIds       →  chat_messages records           (same object)
turn.traceRef         →  run trace object                (full cells)
```

Every hop is an indexed range query; nothing is ever bulk-loaded.

## Record shapes

### `agent_turns` — one record per agent invocation

```jsonc
{
  "id":        "00000007",          // zero-padded seq: lexical == insertion order
  "seq":       7,                   // per-chat monotonic, caller-assigned (last+1)
  "creator":   "<accountId>",       // server-stamped
  "createdAt": 1700000000,          // server-stamped (unix seconds)
  "fromAgent": "bobrik",            // optional, opaque agent name tag
  "userName":  "alice",             // optional
  "userText":  "what's the status?",
  "think":     "<model narration>",
  "replies":   ["All good.", "Anything else?"],
  "effects":   ["created Book [Dune](any://s/o)"],
  "messageIds":["<chat msg id>"],
  "traceRef":  "<run trace object id>",
  "llm": { "stopReason": "end_turn", "inTokens": 1200, "outTokens": 300,
           "cacheRead": 8000, "cacheWrite": 0, "model": "..." }
}
```

`seq` is required; everything else optional. Heavy per-LLM-turn detail
(tool cells, raw API responses) deliberately stays in the run's trace —
the turn record is the lean conversation-replay unit. Append-only:
modify and delete are rejected by the handler. A duplicate-seq append
turns into a modify and is rejected — callers treat that as a seq
collision (probe `sort:["-seq"] limit:1` and retry).

Indexes: `(seq)`, `(createdAt)`.

### `agent_chunks` — one record per compression event

```jsonc
{
  "id":          "00000000",
  "seq":         0,                 // chunk counter
  "creator":     "...", "createdAt": 1700003600,   // stamped
  "fromAgent":   "bobrik",
  "summary":     "<dense paragraph>",
  "periodStart": 1700000000,        // unix seconds — indexable range fields
  "periodEnd":   1700003600,
  "fromSeq":     0,                 // INCLUSIVE pointers into agent_turns
  "toSeq":       9,
  "turnsCovered":10
}
```

Required: seq, summary, periodStart ≤ periodEnd, fromSeq ≤ toSeq.
Immutable. Indexes: `(seq)`, `(periodEnd)`.

### `agent_memory_items` — typed memory on the brain object

```jsonc
{
  "id":          "<derived from changeId>",
  "creator":     "...", "createdAt": 1700000000, "modifiedAt": 1700000000,
  "fromAgent":   "bobrik",
  "category":    "preference",      // required lowercase slug — OPEN set;
                                    // builtins: claim, preference, decision,
                                    // lesson, episode, taskstate, insight
  "context":     "user prefers dark mode",   // required one-liner
  "body":        "<markdown>",
  "tags":        ["ui", "style"],   // real arrays — no CSV, no tags[0]=category
  "entities":    ["dark mode"],
  "keywords":    ["theme"],
  "confidence":  5,                 // 0..10, defaults 5
  "importance":  5,                 // 1..10, defaults 5
  "salience":    10,                // 0..10, defaults 10, mutable (decay)
  "accessCount": 0,                 // mutable (recall tracking)
  "validFrom":   1700000000,        // defaults to createdAt
  "edges":       [{"to": "<itemId>", "type": "related_to", "strength": 0.8}],
  "chatId":      "<chat object id>" // optional provenance
}
```

Edge `type` is an open slug; known values: `related_to`, `caused_by`,
`leads_to`, `contradicts`, `supersedes`, `supported_by`.

Mutable post-create (author-only, `modifiedAt` bumped): `salience`,
`accessCount`, `confidence`, `importance`, `context`, `body`, `tags`,
`edges`. Everything else is immutable — recategorizing means writing a
new item. Delete is author-only.

Indexes: `(category)`, `(createdAt)`, `(validFrom)`.

### The brain object

Datasets are per-object; memory is space-wide, so it lives on one
well-known host: the brain object, derived from the fixed seed
`any/agent-brain/v1` via the SDK's deterministic `Objects().Derive`
(same mechanic as `spaceIndex` and the UI's primary chat). Every peer
computes the same id — no discovery query, no create race.
`GET /v1/spaces/:spaceId/agent/brain` returns it.

## Endpoints

Writes are bespoke (validated at the HTTP layer for clean 400s, then
again in the handlers for peer-side defense); reads go through the
query primitive — no bespoke read endpoints, per the project invariant.

| Method | Path | Purpose |
|--------|------|---------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/agent/turns`  | append one turn record |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/agent/chunks` | create one chunk |
| GET    | `/v1/spaces/:spaceId/agent/brain`                    | resolve brain object id |
| POST   | `/v1/spaces/:spaceId/agent/memory`                   | create memory item |
| PATCH  | `/v1/spaces/:spaceId/agent/memory/:itemId`           | evolve mutable fields (author only) |
| DELETE | `/v1/spaces/:spaceId/agent/memory/:itemId`           | delete (author only) |

All writes return the shared `api.ModifyResult`; `recordIds[0]` is the
zero-padded seq (turns/chunks) or the derived item id (memory). CLI:
`any agent turn-append / chunk-create / brain / memory add|evolve|delete`.

## Query recipes

```jsonc
// Boot window: last 8 turns, newest first (render reversed)
POST /v1/spaces/:s/query
{ "objectId": "<chatObjId>", "dataset": "agent_turns",
  "sort": ["-seq"], "limit": 8 }

// Boot context: last 5 chunks
{ "objectId": "<chatObjId>", "dataset": "agent_chunks",
  "sort": ["-seq"], "limit": 5 }

// Drill down: expand a chunk to its raw turns
{ "objectId": "<chatObjId>", "dataset": "agent_turns",
  "filter": { "seq": { "$gte": <fromSeq>, "$lte": <toSeq> } },
  "sort": ["seq"] }

// Turns in a period (indexed on createdAt)
{ "objectId": "<chatObjId>", "dataset": "agent_turns",
  "filter": { "createdAt": { "$gte": 1700000000, "$lt": 1700086400 } },
  "sort": ["createdAt"] }

// Memory by category (indexed)
{ "objectId": "<brainId>", "dataset": "agent_memory_items",
  "filter": { "category": "preference" }, "sort": ["-createdAt"] }

// Memory valid in a period (indexed on validFrom)
{ "objectId": "<brainId>", "dataset": "agent_memory_items",
  "filter": { "validFrom": { "$gte": 1700000000, "$lt": 1700604800 } } }
```

Liveness: the same bodies against `/query/subscribe` stream
added/updated/removed events (see `docs/04-events.md`).

## Semantic search — the built-in index (now live for memory items)

Memory **items** (`agent_memory_items`) are indexed by the built-in
search index (`docs/13-index.md`) and are hybrid-searchable via `POST
/v1/spaces/:id/search` under scope **`agent`**. The datasets store no
vectors themselves — the old hex-string-embeddings-in-text-properties
scheme and the full-load cosine loop in JS are gone; the indexer keeps
the vectors in its own store.

What's indexed per item: `context` + `body` + `category` +
`keywords`/`entities`/`tags` (the memory chunker, `internal/agentmem/
chunker.go`). Numeric/structural fields (`confidence`, `salience`,
`accessCount`, `edges`, timestamps) are **not** in the indexed text, so a
metadata-only bump (e.g. `accessCount` on recall) does not re-embed the
item — the indexer skips it via a content hash (`docs/13-index.md` §
content hashes). This is what makes the planned **evolution / reindex**
cheap: only items whose text actually changed re-embed.

Recall: `/search` returns hits as `{scope, objectId, dataset, recordId,
…}`; the caller hydrates full items via `/query
{"filter":{"id":{"$in":[recordId,…]}}}` on the brain object. The indexed
fallback paths (recency, category, period) above remain available for
non-semantic queries.

**Not yet indexed:** `agent_turns` / `agent_chunks` (conversation
history) — a dedicated gated chunker is a roadmap item; until then they
use the indexed recency/period reads. Similarity dedup, link generation,
and decay passes can now build on `/search` for the item layer.

**No version history:** `agent_turns` and `agent_chunks` set
`handler.Dataset.SkipHistory` — turns/chunks are
append-only immutable, so every record
has exactly one version and a history index would just duplicate the
data. They never appear on the `/history` endpoints (docs/03-api.md
§ Version history). `agent_memory_items` keeps history: items evolve
in place, so their per-record timeline is meaningful.

## Migration from the legacy scheme

Fresh start — deliberate. Old markdown transcript objects and
runtime-typed `agent_memory` objects are abandoned in place (harmless;
delete manually if wanted). Old `chat_chunk` memories carry only
period timestamps, not turn pointers, so faithful drill-down could not
be reconstructed anyway. The bobrik-watch JS rewrite
(cmd/bobrik-watch) starts writing the new datasets on first run.
