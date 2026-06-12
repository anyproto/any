# 13 — Search index (consumer-side)

This governs the search-index pipeline: the chunker contract
(`internal/index`, `internal/editor/chunker.go`,
`internal/chat/chunker.go`, `internal/index/prop.go`) and the indexer
that consumes it (`internal/indexer`): a local BM25 + vector index per
space behind `POST /v1/spaces/:spaceId/search` / `any search`.

The chunkers are wired into the process via `server.NewIndexRegistry`
(stored on `deps.chunkers`); the indexer is built in `server.Run` when
`index.enabled` and drives them through the SDK's per-space change feed
(`Space.Changes()`).

## The contract

```go
type IndexEntry struct {
    Scope    string // open slug set; "basic" / "chat" / "agent" are the vocabulary
    ObjectId string
    Dataset  string
    RecordId string
    Data     string // text to index; empty ⇒ remove this record from the index
    AddSeq   uint64 // peer-local, per-space monotonic
}

type Chunker interface {
    Dataset() string // doc-id middle segment; may be virtual ("prop")
    TypeId() string  // any.types gate; "" = ungated (see eviction below)
    // Streams every entry of objectId with AddSeq > since, ascending.
    // Cleared/deleted records yield removal entries (Data == "").
    ChunksSince(ctx, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error
}
```

A chunker turns one object's records (for one dataset) into a stream of
`IndexEntry` values ordered by `AddSeq`. The indexer persists a cursor
(its last-seen `AddSeq`) and calls `ChunksSince(cursor)` to pull only
what changed.

## Chunkers, scopes, gating

| Chunker                 | Dataset (doc-id segment) | `TypeId()` gate | Scope of entries | `Data` |
|-------------------------|--------------------------|-----------------|------------------|--------|
| `editor.NewChunker()`   | `editor_blocks`          | `editor`        | `basic`          | the block's `text` (inline markdown) |
| `chat.NewChunker()`     | `chat_messages`          | `chat`          | `chat`           | the message's `text` only |
| `index.NewPropChunker()`| `prop` (virtual)         | — (ungated)     | per property     | property values (see below) |

- **One record = one chunk.** Editor blocks and chat messages are
  per-object datasets — one row per block / message, one entry per row.
- **Chat excludes** creator / reactions / attachments — text only.
  **Editor excludes** block type / style — text only.
- **Scopes are an open set** of slugs (`index.ValidScope`: 1..64 chars
  of `[a-z0-9_-]`); `basic` / `chat` / `agent` are the established
  vocabulary, and property meta flags can mint new ones.
- **`TypeId()` gating**: the indexer runs a gated chunker only while the
  type literal is in the object's `any.types`; when it is not, it
  prefix-evicts `objectId:<dataset>:` instead (see eviction below).

### The prop chunker (`internal/index/prop.go`)

Indexes property VALUES from the shared `objects` collection under the
virtual dataset `prop` — one entry per (object, indexed property), doc
id `objectId:prop:<propId>`:

- **Which properties index is declared on the property definitions**:
  `meta["index"] = "<scope>"` (set at `AddProperty` time — SDK
  `PropertyDraft.Meta`, HTTP `meta` field). Only string / array kinds
  index; arrays render as a newline join of their string elements.
- **Built-ins `any.name` and `any.description` are always indexed**
  under scope `basic`, reserved recordIds `name` / `description`.
- Per streamed live row the chunker emits entries for the built-ins and
  for EVERY catalog property, unconditionally: value present and type
  attached ⇒ text; otherwise ⇒ `Data ""` — so cleared values and
  detached-type properties evict record-level, idempotently.
- The per-space catalog (flagged props across all non-builtin types) is
  a TTL snapshot (30s): newly flagged properties are picked up within
  the TTL — and only affect rows written afterwards anyway ("index from
  the next change"). `Invalidate(spaceId)` drops it (tests/ops).

### Excluded from indexing entirely

`agent_debug_log`, `program`, `miniapp`, and the agent-data datasets
(`agent_turns` / `agent_chunks` / `agent_memory_items` — a dedicated
gated chunker is a roadmap item) have **no chunker**;
`Registry.ForDataset` returns nothing for them.

## Removal semantics

Three granularities, all addSeq-consistent (discovered through the same
`ChangedSince` window, applied in the same page transaction):

| What happened | Who detects it | Index operation |
|---------------|----------------|-----------------|
| record deleted / value cleared | the chunker (streams the tombstoned record / empty value) | entry with `Data == ""` → `DeleteId(objectId:dataset:recordId)` |
| type detached (`DetachType` — bumps `_addSeq`) | the indexer (gated chunker's `TypeId()` ∉ `any.types`) | prefix delete `objectId:dataset:` |
| object deleted (`Space.Delete` — datasets dropped wholesale) | the indexer (shared objects row tombstoned) | prefix delete `objectId:` |

`Space.Delete` leaves a sticky tombstone: content wiped, `_deletedAt`
set, the delete's `_addSeq` carried onto the row. The SDK's find path
normally **skips** tombstones; the chunkers' `RecordsSince` and the
indexer's objects-row read opt in via `Projection({IncludeDeleted:
true})`. Removing what was never indexed is a no-op everywhere, so all
three operations are safe to apply unconditionally. Re-attach after a
detach does NOT resurrect rows below the cursor — they index on their
next write ("index from the next change").

## AddSeq semantics

`_addSeq` is any-sync's **per-space, peer-local, monotonic delivery
counter** — an opaque ordering key within one space on one device. It
advances on a change to any of an object's datasets. Treat it as a
cursor: compare and persist it, but never assume it matches another
peer's value for the same change.

- **"Index from the next change."** Rows written before this SDK branch
  started stamping `_addSeq` have no value for the field and sort below
  any `since >= 0`, so they're excluded by the `$gt` window — same
  contract as the SDK's own `Changes().ChangedSince`. Pre-existing data
  is indexed only after its next write.
- `RecordsSince` chains `Projection({IncludeDeleted: true})` → a typed
  `_addSeq > since` filter → `Sort "_addSeq"` → `Iter`, so every chunker
  streams ascending by `AddSeq` past the cursor. (All any-store filters
  are built with the typed `any-store/v2/query` package — never map/JSON
  literals; static filters are built once and reused.)

## Phase 2 — the indexer (`internal/indexer`)

The consumer of the chunker feed: a background service started by
`server.Run` when `index.enabled` (default true), holding one local
any-store database at `<data-dir>/index/index.db`, plus the
`POST /v1/spaces/:spaceId/search` endpoint and `any search` CLI.

### Store layout

- **One collection per space** (named by spaceId). Doc shape:
  `{id: objectId+":"+dataset+":"+recordId, scope, objectId, dataset,
  recordId, data, addSeq, vector?, pending?}`. The id shape makes every
  removal a primary-key operation — `objectId:` prefix (object
  deleted), `objectId:dataset:` prefix (type detached), exact id
  (record deleted) — and keeps ids unique even though recordIds repeat
  across objects (propIds do). Prefix ranges use bytewise bounds
  `[P, P[:len-1]+";")` (`;` = `:`+1) and drive the primary btree
  directly; per-doc deletion cleans FTS and vector entries in the same
  transaction.
- Indexes per collection: BM25 **full-text** on `data`
  (`IndexKindFulltext`); sparse range on `pending` (embed queue); and —
  once at least one embedded doc exists — an **IVF-SQ cosine vector
  index** on `vector`. The vector index is created lazily because IVF
  trains its quantizers from existing documents
  (`Store.EnsureVectorIndex`); `CompactRatio: 0.5` re-trains as the
  space outgrows the initial training set.
- A `cursors` collection holds one `{id: spaceId, seq}` row per space
  plus a `_meta` row pinning the **schema version** (v2 — the id shape;
  an old DB errors at boot with a remove-to-rebuild message, no
  migration: the index is derived state) and the vector dimension —
  changing the embedder dimension is the same kind of boot error.

### Advance loop (FTS path) — per-space worker

The single operation is `advance`: page through
`Changes().ChangedSince(cursor, batch)`, and per dirty object:

1. **Read the shared objects row once** (`QueryObjects`,
   `IncludeDeleted`). Tombstoned ⇒ prefix-delete `objectId:` and skip
   the chunkers (the SDK drops a deleted object's datasets wholesale,
   so per-record removals never stream for them).
2. Otherwise, per registered chunker: a non-empty `TypeId()` not in the
   row's `any.types` ⇒ prefix-delete `objectId:<dataset>:` (type
   detached — idempotent, one btree seek when already empty); else run
   `ChunksSince(cursor)` — `Data == ""` → delete the doc, else upsert.
3. **One write transaction per page** (prefix deletes → record deletes
   → upserts), then persist the cursor (the page's max `AddSeq`) and
   loop. Eviction rides the same addSeq window as content — no
   out-of-band purge can race the cursor. Crash-safe: re-applying a
   page is idempotent. Text-bearing upserts land marked `pending` —
   **FTS is searchable immediately**, never waiting on the embedder.

Hot path: `Changes().Subscribe` does a non-blocking send into a cap-1
dirty channel (the callback runs on the SDK apply path); the worker
debounces 250ms and runs `advance`. Lost signals are harmless — advance
is cursor-driven. Space discovery: `Spaces().List` at boot plus
`Service.Subscribe` (added → spawn worker; removed/deleted → stop +
`DropSpace`).

### Embed loop (vector path) — parallel, batched

A second per-space goroutine drains `pending` docs: batch `EmbedDocs`
(default 64 per call) → batch `SetVectors` (one write tx, update-only —
docs deleted meanwhile are skipped) → `EnsureVectorIndex`. Nudged by
advance after each page with new text; a 1-minute ticker retries after
embedder failures. A re-written record goes back to `pending` (its text
changed). `index.embedder: none` ⇒ the loop doesn't run and the index
is FTS-only.

Embedders (`indexer.Embedder`), selected by `index.embedder`
(default `local`; `none` opts out — FTS-only):
- `ollama` — local `/api/embed`, default `embeddinggemma`, doc/query
  task prompts.
- `openai` — any OpenAI-compatible `/embeddings` API.
- `local` — **default**: **in-process llama.cpp**, no external service. yzma purego
  bindings (no CGO) dlopen the prebuilt llama.cpp shared libs from
  `index.local.libDir` (default: `llamacpp/` next to the binary —
  populate with `make llamacpp`; macOS arm64 gets Metal, Linux amd64
  picks the best CPU backend variant). Default model:
  **Qwen3-Embedding-0.6B Q8_0** (Apache-2.0, 1024-dim Matryoshka,
  last-token pooling, L2-normalized; queries carry the Qwen retrieval
  instruction, docs embed bare). The GGUF (639 MB, sha256-pinned) is
  auto-downloaded into `<data-dir>/index/models/` on first boot with
  progress in the server log; the download is resumable and never
  blocks boot. Until it completes the embedder reports unavailable,
  which rides the standard outage semantics below — FTS works
  immediately, vectors flow once the model lands. Air-gapped:
  set `index.local.modelPath` (no download is attempted).
  `index.local.dim` truncates output vectors (Matryoshka) to shrink
  the IVF index. One llama context per process, mutex-serialized;
  texts embed sequentially within a batch (multi-sequence batching is
  a known follow-up). Loaded cost ≈ 640 MB mmap + ~200 MB context;
  nothing is loaded until the first embed call. Linux needs a system
  `libffi.so.8` (ubiquitous on mainstream distros; NixOS: `nix develop`
  — the flake's dev shell provides it).

**An unavailable embedder never breaks the pipeline.** There is no
boot-time probe: whenever an embedder is *configured*, text-bearing
docs are marked `pending` regardless of its reachability, so an outage
— at boot or mid-run — only freezes the vector side while FTS indexes
and answers normally. When the embedder comes back, the next embed
round (nudge or 1-minute tick) drains the queue; the vector dimension
is learned from the first successful batch (or pinned via
`index.vector.dim`) and persisted in `_meta`, so a later model/dim
change against a populated index is a loud error rather than silent
corruption.

### Search

`POST /v1/spaces/:spaceId/search` `{query, scopes?, limit?, mode?}` →
`{hits: [{scope, objectId, dataset, recordId, data, score}], mode,
vectorStatus}`. Modes: `fts` (BM25), `vector` (cosine ANN; requires an
embedder, hits below zero similarity are dropped as noise), `hybrid`
(default — both legs fused by reciprocal rank, k=60; degrades to `fts`
when the embedder is missing or the query embedding fails — `mode` in
the reply is the mode that actually ran). Scores are comparable only
within one response. CLI: `any search <spaceId> <query> [--scopes ...]
[--limit N] [--mode ...]`.

`vectorStatus` (`used` / `unavailable` / `disabled` / `skipped`) tells
the consumer whether semantic recall took part and why not — an agent
can distinguish "lexical-only because the embedder is momentarily down,
retry may differ" (`unavailable`) from "this server never runs vector
search" (`disabled`). Value table in `docs/03-api.md` § search.

This is the one sanctioned endpoint that does not map 1:1 onto an SDK
method — the index is a consumer-side feature, owned by this doc.

**Agent-facing tool.** bobrik-watch wraps this endpoint as the `semsearch`
tool (`cmd/bobrik-watch/programs/semsearch@v1.js` +
`tool-descriptions/semsearch.md`, over `anyHelper.search`) — the **cheap**
recall tool the agent reaches for first, in contrast to the **expensive**
RLM `search`/`ask` loop (`docs/12-rlm-search.md`). The two tool descriptions
cross-reference each other so the agent picks by cost. `semsearch` passes
`opts.space` straight through to `:spaceId`, so the cross-space paradigm
(any space on the account) holds here too.

Errors: `index.disabled` (409, `index.enabled: false`),
`index.no_embedder` (400, `mode=vector` with no embedder configured),
`index.embedder_unavailable` (503, `mode=vector` while the configured
embedder is unreachable — retryable; hybrid degrades instead),
`search.bad_mode` / `search.bad_scope` (400).

### Tuning (measured — `internal/indexer/bench_test.go`)

Defaults in `indexer.Options`, picked from file-backed benchmarks:

| Dial | Default | Why |
|------|---------|-----|
| `BatchLimit` (ChangedSince page = Apply tx) | 256 | FTS insert throughput plateaus at 256 docs/tx (~65k docs/s vs ~52k at 16); one page ≈ 4ms. Bigger pages add page latency, not throughput. |
| `EmbedBatch` (texts per EmbedDocs call) | 64 | Real Ollama embeddinggemma: 54/72/76.5/78.6 texts/s at 8/32/64/128 — ≥97% of max at 64, half the per-call latency of 128. The embedder is the pipeline bottleneck by ~3 orders of magnitude (vs ~30k vecs/s IVF-SQ insert, flat across batch sizes) — which is exactly why embedding lives off the advance path. |
| `Debounce` (dirty → advance) | 250ms | A no-op advance is sub-ms; the dial only coalesces write bursts into one page / fuller embed batches, trading freshness. |
| `RetryBackoff` / `PendingEvery` | 5s / 1m | Failure paths only: advance retry, embed catch-up tick. |

Search at 10k docs (dim 768): FTS ≈ 1.9ms, vector ≈ 1.0ms per query.
Re-measure with `go test ./internal/indexer -bench . -benchtime 30x`
(`ANY_BENCH_OLLAMA=1` adds the real-embedder run).

### Known limits

- "Index from the next change" applies to the whole pipeline: removing
  `<data-dir>/index/` does **not** re-index old content — only rows
  whose `_addSeq` moves afterwards get (re-)indexed.
- Embedder latency only delays the vector leg: fresh writes are FTS-
  searchable immediately and gain vector recall once embedded.
- **Long records are truncated for embedding** (explicit decision, not
  an accident): the `local` embedder clamps input to
  `index.local.contextSize` tokens (default 2048, EOS preserved for
  last-token pooling), so only the head of a very long record carries
  vector recall — FTS still covers the full text, and editor blocks /
  chat messages are naturally far smaller than the bound. The chunker
  contract stays one record = one doc = one vector. TODO: split long
  records into multiple chunks chunker-side (changes the doc-id scheme
  and tombstone handling) if head-only vector recall proves limiting.

## Tests

- `internal/index/stream_test.go` — `RecordsSince` chains the
  IncludeDeleted projection + `_addSeq` window + sort, parses the seq,
  and stops + closes on a yield error; `IsDeleted`.
- `internal/index/agentmemory_test.go` — `hasType`, `memoryData` (join
  order, skip-empties, missing-prop, name-only).
- `internal/editor/chunker_test.go`, `internal/chat/chunker_test.go` —
  text extraction including the tombstone case.
- `internal/server/handlers_index_test.go::TestIndexChunkers_FullFlow` —
  in-process SDK end to end: creation, cursor advance, deletions
  (tombstones), and the non-memory tombstone case.
- `internal/indexer` unit tests — store round-trips (FTS + vector +
  pending lifecycle + purge/drop, in-memory any-store), RRF fusion,
  the HTTP embedder clients against `httptest` servers.
- `internal/indexer/embed_local_test.go` — local embedder factory and
  pre-ready errors (no libs/model needed), `truncateTokens` (EOS
  preservation), `l2Normalize`, Matryoshka dim; plus a gated
  integration test (`ANY_TEST_LOCAL_EMBEDDER=1` +
  `ANY_INDEX_LOCAL_MODEL_PATH`) running the real model: dims, unit
  norms, relevance ordering, truncation path, concurrency under
  `-race`.
- `internal/indexer/embed_local_download_test.go` — download manager
  against `httptest`: happy path, sha256 mismatch, Range resume,
  progress strings.
- `internal/server/handlers_search_test.go` — in-process SDK + in-memory
  store + deterministic fake embedder: all three modes end to end,
  scope filtering, deletion purge, degraded/disabled errors, and the
  asynchronous worker path (`Start` + poll).
