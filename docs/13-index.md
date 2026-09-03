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

This doc is the **contract** (how it works). For the **evaluation and
decision record** — chunk-length before/after, BEIR results, why the
defaults are what they are — see [`search/README.md`](search/README.md).

## The contract

```go
type IndexEntry struct {
    Scope    string // open slug set; "basic"/"chat"/"props" are the vocabulary
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

// Optional: chunkers whose index unit spans several records implement
// Reconciler. The indexer prefers it over ChunksSince and applies
// {PrefixDelete, Upserts} in the same page transaction.
type Reconciler interface {
    Chunker
    Reconcile(ctx, sp space.Space, objectId string, since uint64) (Reconciliation, error)
}
type Reconciliation struct { PrefixDelete bool; Upserts []IndexEntry }
```

A chunker turns one object's records (for one dataset) into a stream of
`IndexEntry` values ordered by `AddSeq`. The indexer persists a cursor
(its last-seen `AddSeq`) and calls `ChunksSince(cursor)` to pull only
what changed. A **`Reconciler`** chunker (editor) is called via
`Reconcile` instead: it returns the object's full current doc set plus a
`PrefixDelete` flag, because a coalesced window can't be expressed as a
per-record delta — a window's text needs sibling records below the
cursor, and a deleted block's position is wiped from its tombstone, so
the affected window can't be located incrementally.

## Chunkers, scopes, gating

| Chunker                 | Dataset (doc-id segment) | `TypeId()` gate | Scope of entries | `Data` |
|-------------------------|--------------------------|-----------------|------------------|--------|
| `editor.NewChunker()`   | `editor_blocks`          | `editor`        | `basic`          | a **coalesced window** of consecutive blocks (recordId `win_<anchor>`) |
| `chat.NewChunker()`     | `chat_messages`          | `chat`          | `chat`           | the message's `text` only |
| `index.NewPropChunker(excl…)`| `prop` (virtual)    | — (ungated)     | `props` (default) / per-prop override | property values, `"<name>: <value>"` (see below) |
| `index.NewSchemaChunker(static…)`| `schema` (virtual) | — (self-gated per dataset) | `basic` (default) / per-dataset `x-search.scope` | runtime-dataset records by their x-search mapping (see below) |

- **Chat = one record per chunk.** `chat_messages` indexes one entry per
  message (creator / reactions / attachments excluded — text only).
- **Editor = coalesced windows.** `editor_blocks` does NOT index one doc
  per block: consecutive blocks (in document order — the `List` tree
  walk) are grouped into ~1.5 KB windows broken before each heading
  (`internal/editor/window.go`), one index doc per window, anchored on
  the window's first block (`recordId = win_<firstBlockId>`), `Data` =
  the member texts joined by newline with the heading leading. Tiny
  one-block chunks (mean ~98 chars) hurt vector recall and BM25 length
  normalization; coalescing fixes both (chunker-hybrid-search-report
  § 3–4, eval § 9.1). Because a window spans several records, the editor
  chunker is a **`index.Reconciler`** — it returns the object's full
  current window set and the indexer diffs it against the stored docs by
  **content hash** (see below): only changed/new windows re-embed,
  unchanged ones keep their vectors. So an append re-embeds one window,
  not the whole doc. The read is still O(doc) per edit (re-reads the
  blocks to form windows), but that's cheap against the local DB; the
  expensive axis (embedding) is incremental.
- **Programs are not indexed.** `program` is a harness-declared user
  type (anybao ADR-010 §5): its `program_source` runtime dataset is
  declared without a `search` mapping, so the schema chunker skips it
  — code (docstrings included) is not a search target — and its
  `summary` property is added with `meta.index: none` (revisit only if
  evidence demands program recall).
- **Scopes are an open set** of slugs (`index.ValidScope`: 1..64 chars
  of `[a-z0-9_-]`); `basic` / `chat` / `props` are the established
  vocabulary, and property meta flags can mint new ones. `props` is FTS-only (see the prop chunker below).
- **`TypeId()` gating**: the indexer runs a gated chunker only while the
  type literal is in the object's `any.types`; when it is not, it
  prefix-evicts `objectId:<dataset>:` instead (see eviction below).

### The prop chunker (`internal/index/prop.go`)

Indexes property VALUES from the shared `objects` collection under the
virtual dataset `prop` — one entry per (object, indexed property), doc
id `objectId:prop:<propId>`:

- **User properties index BY DEFAULT under the dedicated scope
  `props`** — never interleaved with `basic` ranking; a search that
  wants pure content passes `scopes` without `props`. The property
  definition's `meta["index"]` (set at `AddProperty` time — SDK
  `PropertyDraft.Meta`, HTTP `meta` field) is a 3-state override:
  absent/empty ⇒ `props`; `"<scope>"` ⇒ that scope; the literal
  `"none"` ⇒ excluded (the opt-out for blobs and noisy enums). An
  invalid slug excludes rather than silently landing in the default.
- **Entry text is self-describing**: `"<prop name>: <value>"`
  ("Score: 9", "Publisher: Gollancz") — property-NAME search works
  (property definitions are indexed nowhere else) and bare numbers get
  context. The name is the definition's display `name`, falling back
  to `xKey`. Valueless rows stay `Data ""` (a removal signal) — never
  a bare name prefix.
- **Kinds**: string; array (newline join of string and number
  elements); number (canonical JSON rendering — integers without a
  decimal point; distinctive numerals like 85600 are real discovery
  anchors, and small-number noise is scope-contained). Booleans, null
  and object kinds never index.
- **The `props` scope is FTS-only**: the store never marks props-scope
  docs pending, so they are never embedded — short "name: value"
  entries embed badly and would pollute vector recall. A `meta.index`
  override into another scope re-enters the vector pipeline.
- **Built-ins `any.name` and `any.description` are always indexed**
  under scope `basic`, reserved recordIds `name` / `description`, raw
  (no name prefix) — EXCEPT for objects whose `any.types` names an
  excluded type. The exclusion list always contains `__type__`
  (type-definition rows — schema, not knowledge; discovery is
  `GET /types`, and their one-word names otherwise win BM25 on
  field-length normalization and surface as top hits).
- **Short prop docs never embed**: prop-dataset entries under 64 bytes
  are not marked `pending` and stay FTS-only, on top of the
  scope-`props` rule above. Short name-like strings land in a flat
  cosine band (~0.55–0.62 for relevant and irrelevant queries alike —
  the `minVectorSim` finding, `docs/search/README.md`), so they fill
  vector top-N slots without discriminating; BM25 is the right
  retrieval for lexical labels. Long descriptions and long
  scope-overridden values still embed.
- Per streamed live row the chunker emits entries for the built-ins and
  for EVERY catalog property, unconditionally: value present and type
  attached ⇒ text; otherwise ⇒ `Data ""` — so cleared values and
  detached-type properties evict record-level, idempotently.
- The per-space catalog (indexable props across all non-builtin types)
  is a TTL snapshot (30s): newly added properties are picked up within
  the TTL — and only affect rows written afterwards anyway ("index from
  the next change"). `Invalidate(spaceId)` drops it (tests/ops).

### The schema chunker (`internal/index/schema.go`)

Indexes records of **runtime-defined datasets** (docs/03-api.md
§ Runtime dataset schemas; SDK contract: its docs/17-user-datasets.md
§ Discovery) by their declaration's `x-search {title, text, scope}`
mapping — one registered chunker covers every searchable runtime
dataset in every space. Entries carry the REAL dataset name (doc ids
`objectId:<dataset>:<recordId>`) under the declared `x-search.scope` —
absent defaults to `basic` (runtime records are user content on par
with editor blocks, so they embed normally; a dataset that declares
scope `props` inherits that scope's FTS-only rule).
`Dataset()` returns the virtual name `schema`, used only for chunker
identity; both virtual names (`prop`, `schema`) are reserved against
user dataset names at the creation API.

- **Mapping**: `x-search.title` → `IndexEntry.Title` (BM25F-boosted)
  and `Data`'s leading line; `x-search.text` → the rest of `Data`.
  `text` is a bare field key or an array of keys: each
  mapped field renders separately and the non-empty values join with a
  blank line, in mapping order — a missing/empty field contributes
  nothing. Either side may be absent; title stays single-field.
  Values render by their ACTUAL type
  (string / number canonical / array newline-join; else empty) — the
  SDK does not validate x-search fields against declared kinds. A
  dataset without `x-search` is not indexed at all; a malformed `text`
  form (wrong JSON type, non-string elements) makes the dataset
  unsearchable, like a malformed doc. Both sides empty
  (cleared values, tombstone) ⇒ `Data ""` removal entry.
- **Scope**: `x-search.scope` picks the scope the dataset's entries
  land under; absent = `basic`. An invalid slug (`index.ValidScope`)
  makes the dataset unsearchable — a broken override must not
  silently land in the default scope (the `resolveIndexedProp`
  stance; `any`'s API validates on write, but the declaration syncs
  from arbitrary peers). A scope patch applies to records as they
  (re-)index: already-indexed docs keep their stored scope until
  their object next goes dirty (same declare-forward semantics as a
  prop `meta.index` change).
- **No catalog cache** (deliberate PropChunker deviation):
  `Space.Datasets()` is an atomic in-memory snapshot the SDK refreshes
  synchronously when a definitions change applies — a fresh read is
  never stale relative to the applySeq window, and a TTL cache could
  skip records in the primary define-then-write flow while the cursor
  advances past them.
- **Gating is per dataset, self-applied**: the chunker implements
  `index.DynamicChunker` — the worker asks it for the object's
  eviction set (`EvictDatasets`: searchable catalog datasets whose
  owning `TypeId` is not attached, every runtime dataset WITHOUT a
  usable x-search — covering a cleared annotation, whose docs would
  otherwise go stale forever — plus retired names) and prefix-deletes
  `objectId:<dataset>:` for each in the same page transaction, then
  streams normally; the chunker skips non-attached datasets itself, so
  an evicted dataset is never also upserted in the page. One catalog
  resolve serves the paired EvictDatasets + ChunksSince calls (a
  one-shot per-space handoff; each space has a single advance
  goroutine).
- **Static skip set**: every compiled-in dataset name (from the
  server's `handler.Type` list) plus the virtual names is never
  treated as runtime — belt-and-braces against definitions synced from
  a peer with a different compiled-in set.

### Chunking long records (`internal/indexer/chunk.go`)

Chunkers emit **one entry per record**; the indexer splits it. An
entry whose `Data` exceeds `Options.ChunkRunes` (default
`DefaultChunkRunes` = 2000 runes, ~500 tokens of prose) is indexed as
several chunk docs — cut at a blank line, a line break, a sentence end
or whitespace found in the second half of the window, hard-cut when
there is none; no overlap, deterministic, so the content hash of an
unchanged chunk is stable. Chunk 0 keeps the entry's head; chunks
`n > 0` re-prefix the entry's `Title` (when set) so a mid-mail passage
keeps its subject for BM25F and the embedder. Each chunk embeds whole
(the target sits well inside the local embedder's 2048-token clamp),
so vector recall covers the entire record, not just its head; and a
hit's `data` is the matching passage, not the whole record. The hit's
`recordId` is the record's; `chunk` says which piece — the record's
best-ranked one, since `/search` returns one hit per record and folds
the other matching chunks into `passages` on request (§ Search).
Editor windows and chat messages are below the bound by construction;
runtime-dataset records (mail bodies) and long property values are
what this is for.

### Content hashes (incremental embedding)

Every index doc stores a `hash` field — a 64-bit FNV-1a of its `Data`,
hex-encoded (`docHash` in `store.go`). The indexer uses it to avoid
re-embedding unchanged content:

- **Reconcile diff (editor).** `worker.reconcile` reads the object's
  stored `(id, hash)` for `objectId:dataset:` (`Store.DocHashes`), diffs
  against the chunker's full window set, and emits deletes for vanished
  ids, upserts for new/changed ones, and **nothing** for unchanged ids —
  their docs (and vectors) stay. An append re-embeds only the new window.
- **Per-record skip (chat / memory).** On an incremental advance,
  `worker.streamChunks` batch-reads the changed records' stored hashes
  (`Store.DocHashesByIds`); a record that re-streamed (its `_applySeq`
  bumped) but whose indexed text is unchanged is skipped — no re-embed.
  Cold sync (cursor 0) skips the hash read and applies blind (nothing is
  stored yet). This is what keeps a memory `accessCount` bump or a chat
  reaction from re-embedding.

Hash collisions are astronomically unlikely (64-bit) and the worst case
is one stale vector. Docs written before the `hash` field existed simply
miss the map and re-upsert once.

### Excluded from indexing entirely

Runtime datasets declared without a `search` mapping (anybao's
`program_source`, `mini_app`) are never indexed — see § Schema chunker.

## Removal semantics

Three granularities, all addSeq-consistent (discovered through the same
`ChangedSince` window, applied in the same page transaction):

| What happened | Who detects it | Index operation |
|---------------|----------------|-----------------|
| record deleted / value cleared (per-record chunker) | the chunker (streams the tombstoned record / empty value) | entry with `Data == ""` → range delete `[objectId:dataset:recordId, +" ")` (the record's every chunk) |
| record shrank to fewer chunks | the indexer (`planDocs` diffs the new chunk set against `DocHashesByRecords`) | delete the trailing chunk ids, upsert the changed ones |
| any change to a **coalescing** dataset (editor) | the `Reconciler` chunker + indexer hash-diff | delete the window ids that vanished, upsert the changed/new ones, leave unchanged ones — expresses block edits / deletes / merges that shift a window's shape, without re-embedding untouched windows |
| type detached (`DetachType` — bumps `_addSeq`) | the indexer (gated chunker's `TypeId()` ∉ `any.types`; runtime datasets via the schema chunker's `EvictDatasets`) | prefix delete `objectId:dataset:` |
| runtime dataset definition removed | the schema chunker (name vanishes from the catalog → per-space retired set, held for the process lifetime) | prefix delete `objectId:dataset:` on each object's NEXT dirty tick |
| object deleted (`Objects().Delete`) | the indexer (`ObjectChange.Deleted` in the change feed) | prefix delete `objectId:` |

Object deletion leaves **no tombstone**: the SDK purges the shared
`objects` row and every per-object dataset collection outright, and
announces the deletion once through the change feed as
`ObjectChange{Deleted: true}` (with an applySeq strictly greater than
the object's last content change). That flag is the ONLY eviction
signal for the object's index docs — nothing re-streams for a purged
object (`space.ChangeIndexAPI`: "Consuming Deleted is MANDATORY for
eviction"). Record-level tombstones (a deleted chat message, a cleared
value) DO survive with `_deletedAt` set; chunkers opt in via
`Projection({IncludeDeleted: true})` and stream them as `Data == ""`.
Removing what was never indexed is a no-op everywhere, so all
operations are safe to apply unconditionally. Re-attach after a
detach does NOT resurrect rows below the cursor — they index on their
next write ("index from the next change").

**Definition-removal eviction is lazy and process-scoped** — two known
residual leaks, accepted for now: an object never dirtied again after
the removal keeps its stale docs, and a restart wipes the retired set
(the SDK wipes the removed def record's content, so the name is
unrecoverable post-restart). Follow-ups: a boot-time per-space sweep of
stored dataset segments against the current catalog, and an SDK
retired-name signal (preserve `name` on the def tombstone).

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

**Observability**: the indexer reports its long-running work onto the
process view (`GET /v1/processes`, docs/22-processes.md § Internal
producers) as device-scope processes — `index.fts.<spaceId>` (chunk
backlog), `index.embed.<spaceId>` (vector drain, done/total docs) and
`index.model_download` (bytes) — so clients can render progress for
"search is still catching up" instead of guessing from empty results
(`Options.OnProcess`, bridged in `internal/server/engine.go`).

### Store layout

- **One collection per space** (named by spaceId). Doc shape:
  `{id, scope, objectId, dataset, recordId, chunk?, data, title, hash,
  applySeq, vector?, pending?}` where `id` is
  `objectId:dataset:recordId` for a record's first chunk and that base
  + `U+001F` + `n` for chunk `n > 0` (`chunk` is stored only when
  non-zero). The id shape makes every removal a primary-key range
  operation — `objectId:` prefix (object deleted), `objectId:dataset:`
  prefix (type detached), `[base, base+" ")` (record deleted — the
  base doc plus every chunk suffix; a chunk id passed the same way
  removes exactly that chunk) — and keeps ids unique even though
  recordIds repeat across objects (propIds do). The chunk separator is
  a control byte no SDK id pattern admits (auto ids are CIDs, user ids
  default to `[A-Za-z0-9._:-]+`), so every byte a real id can continue
  `base` with sorts at or above `0x20` and the record range is exact.
  Prefix ranges use bytewise bounds `[P, P[:len-1]+";")` (`;` = `:`+1)
  and drive the primary btree directly; per-doc deletion cleans FTS and
  vector entries in the same transaction.
- Indexes per collection: BM25 **full-text** on `data`
  (`IndexKindFulltext`); sparse range on `pending` (embed queue); and —
  once at least one embedded doc exists — a **cosine vector index** on
  `vector`. The strategy (`Store.vectorIndexParams`, `index.vector.mode`)
  defaults to **IVF-SQ** (`VectorModeIVFSQ`): cheap near-flat incremental
  ingest + physical deletes, ~3–4 recall@10 below exact — the right fit
  for a local, continuously-written index (docs/search/README.md § index
  mode). Alternatives: `btree`/`hnsw` (recall ≈ exact, but serial
  super-linear ingest + tombstone-rebuild deletes — opt-in for read-heavy
  deployments), `hybrid` (HNSW + RAM cache), `bruteforce` (exact,
  O(N)/query, small spaces). The index is created lazily
  (`Store.EnsureVectorIndex`) so the first build sees real data.
- A `cursors` collection holds one `{id: spaceId, seq, gen}` row per
  space — `gen` is the SDK's per-space `Changes().Generation()`, the
  epoch the cursor belongs to (see Re-index triggers). Writes MERGE:
  advancing the cursor with no epoch in hand keeps the stamped one,
  because erasing it would disable rebuild detection silently. Plus a
  `_meta` row pinning the **schema version** (the version ↔
  layout map lives on `indexSchemaVersion` in `internal/indexer/
  store.go`; a mismatched DB errors at boot with a remove-to-rebuild
  message, no migration — the index is derived state and re-indexes
  from the next change) and the vector dimension — changing the
  embedder dimension is the same kind of boot error.

### Re-index triggers — per-space worker boot

Before the loops start, `alignIndex` checks that the persisted index
still describes the SDK store it was built from, and drops the space's
docs + restarts at cursor 0 when it does not:

- **`Generation()` changed** — the SDK store was rebuilt and the
  applySeq axis restarted at zero.
- **cursor > `MaxApplySeq()`** — an older `sdk.db` was restored from
  backup under a cursor that ran ahead of it.

Either way the cursor names a position the feed will never report
again: `ChangedSince` returns nothing, forever, with no error. A failed
read leaves the cursor alone (freezing the index over a transient error
is worse than the drift) and re-checks on the next boot; the merge rule
above keeps the stamp on record meanwhile.

### Advance loop (FTS path) — per-space worker

The single operation is `advance`: page through
`Changes().ChangedSince(cursor, batch)`, and per dirty object:

1. **Deleted ⇒ evict.** A change with `Deleted: true` prefix-deletes
   `objectId:` and skips the chunkers — the object's projection is
   purged and no later content change follows. Collected page-wide
   before chunking, so a same-page content change can't upsert past
   the eviction. For live objects, **read the shared objects row once**
   (`QueryObjects`, `IncludeDeleted`); a tombstoned row only catches a
   delete racing the row read.
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
   Exceptions: `props`-scope docs and short prop-dataset docs are
   never marked pending (FTS-only — see the prop chunker).

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
- `local` — **default**: **llama.cpp in a child process**, no external service
  (§ The embedder child process). yzma purego
  bindings (no CGO) dlopen the prebuilt llama.cpp shared libs from
  `index.local.libDir` (default: `llamacpp/` next to the binary —
  populated by `make llamacpp`, which also runs as a failure-tolerant
  step of `make build`; GPU-capable with CPU fallback, see § GPU
  offload below). Default model:
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
  the IVF index. One llama context in the child, one request in
  flight; the server sends a batch one decode group per frame —
  up to `index.local.batchDocs` docs (**default 1**) per `llama_decode`,
  greedy in order under the `contextSize` token budget — and a search
  query takes the next frame ahead of waiting doc groups. **Batching costs
  context**: the unified KV cache PARTITIONS `contextSize` across the
  packed sequences, so `batchDocs: N` caps each text at
  `contextSize/N` tokens (rounded up to a 256-token block). `tokenize`
  truncates to that bound (`Local.maxDocTokens`), so a wider batch never
  fails a decode — it silently embeds less of each text, and the server
  logs a warning at construction when the bound falls below
  `contextSize`. Measured on a mixed record stream (the production
  shape), width buys nothing once the bound is held equal: GTX 1080 via
  Vulkan 10.1 docs/s at `batchDocs: 1` vs 9.2 at 4; 32-core CPU 2.16 vs
  2.14. Apparent gains at wide settings come from the truncated bound,
  not from batching. Batched and single decodes produce identical
  vectors (TestLocal_BatchedMatchesSingle). Compute threads
  (`NThreads`/`NThreadsBatch`)
  default to `runtime.NumCPU()-1` (leave one core free); override with
  `index.local.threads` / `ANY_INDEX_LOCAL_THREADS` — going past the
  physical core count can regress on hyperthreaded CPUs. Loaded cost ≈ 640 MB mmap + ~200 MB context;
  nothing is loaded until the first embed call. Linux needs a system
  `libffi.so.8` (ubiquitous on mainstream distros; NixOS: `nix develop`
  — the flake's dev shell provides it).

### The embedder child process

The `local` embedder does not decode in the server process. It re-execs
this binary as `any run embedder` (a hidden subcommand — self-exec keeps
distribution to one signed artifact) and talks to it over stdin/stdout
with a magic-prefixed, length-delimited frame protocol; vectors come
back as raw little-endian float32. One child, spawned lazily on the
first embed call and shared by every space worker. The stream carries
one request at a time, and the server shares it through a one-slot
semaphore with two classes: `EmbedDocs` sends a batch one decode group
per frame (`batchDocs` texts, one `llama_decode`) and re-takes the
slot for every frame, and a search query takes the slot ahead of any
waiting doc frame. A doc frame yields to every waiting query, with one
bound: after 8 consecutive query turns it runs anyway, so a saturating
query stream still leaves indexing a frame per burst instead of
starving it. Once the child is up, a query therefore waits
for at most the decode in flight — ~1–2 s worst case for a 2048-token
doc on CPU, well under that on a GPU — never for a 64-doc batch,
however many spaces are backfilling. A cold spawn or a wedged child
holds the slot longer; that is what the query budget in § Search is
for. Acquisition is context-aware: a caller that gives up leaves the
queue instead of parking until its turn.

**Why.** llama.cpp faults are not recoverable in Go. A Vulkan
device-lost throws `vk::DeviceLostError` out of `vk::Queue::submit` and
the C++ exception unwinds into a purego frame with no handler, so
`std::terminate` aborts the process; `GGML_ASSERT` calls `abort()`
outright. In-process, either one killed the whole server mid-decode. In
the child they kill only the child: the round fails, its docs stay
`pending`, and the next tick retries — the outage semantics this
pipeline already has for an unreachable embedder.

**The child only embeds.** It is handed a model path and decode
parameters and answers with vectors. It never opens the index db, the
data dir, or any-store — the cursor, `pending` marking, `SetVectors` and
the vector index all stay in the server, and so do model discovery and
the background download (`index.model_download` reporting is unchanged).

**Failure handling.** A dead child, a desynchronized stream, or a
frame that outlives `index.local.requestTimeout` (default 3m) kills
the child and fails the round; the next round respawns behind an
exponential backoff (1s → 1m). The timeout matters as much as the
isolation — a wedged GPU stops answering rather than failing, so
without a bound the embed loop waits forever. A timeout therefore counts
as a fault and demotes the GPU, at the price of demoting a merely slow
decode: one run at CPU speed against repeated multi-minute stalls. An
error *frame* is different — the child reporting a failed call is still
healthy and is kept. Its stderr is logged, and the tail is quoted when it dies — that
is where llama.cpp's abort message lands. A caller that goes away is
not a fault either: the server abandons the wait, not the work. Once a
request is on the wire its answer has to be read for the stream to stay
usable, so the frame (or a cold spawn) completes on its own, the child
is kept, and the next caller finds it ready — a budgeted search never
restarts a model load, and a dropped space costs one decode, not a
respawn.

**Priority.** The child runs *below* the server: `index.local.niceness`
(default 10, 0 disables) nices it on Unix and drops it to a
below-normal/idle priority class on Windows, so a full re-index yields
to interactive work instead of competing with it. Nicing happens inside
the child before llama.cpp loads — on Linux every existing task is
niced, and the decode threads llama.cpp spawns later inherit it.

**Hardware.** The child reports what llama.cpp initialized on — OS/arch,
the backends that registered and the shared object each came from, the
devices they found (GPU name and driver), the pinned llama.cpp release,
CPU count and thread budget, plus `llama_print_system_info()` — in the
`ready` frame. The server logs it on every spawn ("local embedder child
started", with the CPU feature string at DEBUG) and keeps the last
report behind `Indexer.EmbedHardware()`, so hardware can later be
correlated with crashes and throughput. It is collected by filtering
llama.cpp's own log callback down to the enumeration lines; everything
else stays silent.

**Threads.** `index.local.threads` is the child's CPU budget — lower it
to keep background indexing off the user's cores. Set it in config and
restart: `Indexer.SetEmbedThreads` changes it in place (the value lands
on the spawn spec and an idle child is retired, so the next request
comes up with the new count), but nothing calls it yet; it is the seam
a settings surface plugs into.

### GPU offload (local embedder)

The shipped llama.cpp bundles are **GPU-capable with automatic CPU
fallback**: macOS arm64 carries the Metal backend; Linux and Windows
carry the **Vulkan** backend (cross-vendor: NVIDIA / AMD / Intel)
alongside every `libggml-cpu-*` variant — the Vulkan archives are strict
supersets of the CPU-only ones. Backend selection happens at model-load
time through ggml's dynamic backend registry: a backend whose
driver/loader is missing (no `libvulkan`, no ICD, headless box) simply
doesn't register, and inference lands on the best CPU variant — same
mechanism, no config, no error. llama.cpp's default model params offload
all layers when a usable GPU device exists; `index.local.gpuLayers: 0`
forces CPU-only inference (the opt-out when the embedder shouldn't take
VRAM — full offload of the default model costs ~2 GB, dominated by
compute buffers that scale with `contextSize`). It also turns
llama.cpp's `op_offload` off: `n_gpu_layers` only places the *weights*,
and with op offload left on a registered GPU still receives whole
matmuls, so the compute never reaches the CPU threads. Measured on an
RDNA2 iGPU with a 1200-token document: 6.6 s offloaded against 1.1 s
across 16 CPU threads. That path is also the one that hangs the compute
ring, so the crash demotion below depends on this too. Measured on a GTX 1080
(478 editor-window docs, ~330 tokens each, `batchDocs` 16): CPU 240 s
≈ 2.0 docs/s at ~14 cores vs Vulkan 56 s ≈ 8.5 docs/s — a ~4× win with
the CPU left essentially idle.

**A GPU that dies mid-run does not take the server with it.** It kills
the embedder child (above), and the parent demotes itself to CPU for the
rest of the run: every later spawn passes `--gpu-layers 0`, one WARN
names the reason and quotes the child's stderr, and the affected docs
re-embed on CPU off the `pending` queue. The demotion is in-memory only
— the next `any run` starts on the GPU again, so a driver or hardware
fix recovers on its own and a permanently bad GPU costs one crashed
child per run instead of a crash loop. Seen in the wild on an AMD iGPU
that also drives the display: embedding load hangs the compute ring
(`ring comp_* timeout` in the kernel log), the kernel resets it, and the
in-flight submit comes back device-lost. On such a machine set
`index.local.gpuLayers: 0` and skip the crashed child entirely.

CUDA/ROCm builds are deliberately not
bundled (per-vendor, hundreds of MB, no upstream Linux CUDA prebuilt);
point `index.local.libDir` at a custom llama.cpp build to use them.

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

### Build tags — `fts` and `vector` (selecting the legs at compile time)

The two search legs are **independently selectable at build time** via
positive build tags, so a build can ship both, one, or neither:

| Build | Tags | FTS | Vector / embeds |
|-------|------|-----|------|
| Desktop / server (`make build`) | `fts vector` | on | on |
| Darwin `-sandbox` tarball | `fts vector ffi_no_embed` | on | on (full, incl. `local`) |
| FTS-only | `fts` | on | off |
| Vector-only | `vector` | off | on |
| None (default `go build`) | *(none)* | off | off |
| Mobile (gomobile) | *(none)* | off | **off (forced)** |
| Mobile + FTS | `fts` | on | **off (forced)** |

The two legs are **separate, positive build flags** — a build opts each
in. `make build` ships both; the default `go build` ships neither. The
rule for `vector` is stronger than for `fts`:

- **`vector` / embeds are *always* off on mobile, regardless of tags.**
  `capVector` is `vector && !gomobile`, so even `gomobile bind -tags
  vector` keeps the whole embedding/ANN leg out — no embedder is
  constructed, no model is downloaded, and the embedder implementations
  (ollama, openai, and the llama.cpp-backed `local`) are not linked
  at all. This is deliberate: the `local` embedder links the yzma /
  jupiterrider-ffi llama.cpp bindings, whose libffi CIF descriptors
  resolve `ffi_prep_cif` at package load — a symbol Android doesn't
  provide, which panics the Go runtime at startup. A runtime toggle
  can't prevent a load-time crash, so gomobile force-disables the leg at
  compile time. Embedding has no place in the mobile runtime anyway.
- **`fts` is a plain opt-in flag**, available everywhere including
  mobile (`gomobile bind -tags fts` gives full-text search with no
  embedder). Both mobile binds pass it: iOS `-tags 'mobile fts'`, Android
  `ANY_TAGS := gomobile fts` (DROID-44). ~3 KB of AAR — any-store's
  fulltext index links either way, the tag only lifts the gate.
- **`ffi_no_embed` is a *packaging* flag, not a capability one.** Unlike
  the mobile rule above it compiles nothing out of the search leg — the
  `local` embedder, the ANN index and every mode keep working. It only
  changes where libffi comes from: the system `/usr/lib/libffi.dylib`
  (pinned by a companion `-ldflags -X`) instead of the copy
  `jupiterrider/ffi` extracts into the user Caches dir at package init,
  which macOS library validation refuses to load. The darwin `-sandbox`
  release tarballs are built this way; the mechanism, the guard and the
  consumer contract live in `docs/18-ci.md` § The darwin `-sandbox`
  variants.

Mechanics (`internal/indexer`):
- `capFTS` (`fts`) and `capVector` (`vector && !gomobile`) are
  build-tagged constants (`caps_fts_*.go`, `caps_vector_*.go`). When
  false the store creates no index for that leg (`spaceColl`) and the
  leg's search method short-circuits to no hits (`SearchFTS` /
  `SearchVector` / `EnsureVectorIndex`); `capVector` false also stops
  docs being marked `pending`, so the embed loop never runs.
- `NewEmbedder` has two build-tagged variants: the real switch under
  `vector && !gomobile` (`embed_factory_vector.go`) and a no-op
  returning `nil` under `!vector || gomobile` (`embed_factory_novector.go`).

Tags decide what is *compiled*; the runtime `index.enabled` /
`index.embedder` config decides what *runs* on top (there is no per-leg
runtime flag — per-leg selection is the build tag). If `index.enabled`
is set but the binary was built with neither leg, the server logs a
warning at startup (`indexer.CompiledCaps`) so the empty-result state is
observable, not silent. Tests covering either leg are tagged to match
(`go test` without tags compiles but skips them; `make test` runs the
full `fts vector` suite).

On the mobile embed path (`internal/embedded.Start`) there is no config
file and no host parameter: the compiled `fts` cap is the whole gate, so
both shims run the indexer exactly when their binary was built with the
tag (both are). `index.embedder` is hard-forced to `"none"` on this path
regardless — no embedder is ever constructed on mobile.

A mobile host that can't open its index gets a distinguishable failure:
`ErrIndexRebuildRequired` (schema version or vector dimension mismatch,
`internal/indexer/errors.go`) reaches the iOS shim as start code `4`, so
the app can offer "reset local data" instead of a generic retry. The index
is a derived cache, so deleting it is always the whole fix.

### Search

`POST /v1/spaces/:spaceId/search` `{query, scopes?, limit?, mode?,
require?, exclude?, maxData?, passages?}` →
`{hits: [{scope, objectId, dataset, recordId, chunk?, data,
dataOffset?, dataTotal, score, passages?}], mode, vectorStatus}`. Modes: `fts` (BM25), `vector` (cosine ANN; requires an
embedder, hits below the similarity floor are dropped as noise), `hybrid`
(default — both legs fused by reciprocal rank, k=60; degrades to `fts`
when the embedder is missing or the query embedding fails or exceeds
its budget — `mode` in the reply is the mode that actually ran). The
query embedding is bounded (`index.search.queryEmbedTimeout`, default
5 s): a cold model load, a wedged child or a slow API degrades the
search instead of holding it, and a caller that disconnects leaves the
embedder's queue at once. Under `auto` the online primary gets half of
the remaining budget so the local fallback still has time to decode. Scores are comparable only
within one response. CLI: `any search <spaceId> <query> [--scopes ...]
[--limit N] [--mode ...] [--require T ...] [--exclude T ...]
[--max-data N] [--passages N]`.

**`limit` counts records.** A hit is one `(objectId, dataset,
recordId)`, shown through its best-ranked chunk; the other chunks of
the record that ranked within the search window come back as
`passages` (`passages: N`, max 10, best first, same window fields).
Each leg reads a window that is at least `fetch = clamp(3·limit, 30,
100)` chunks and continues until it covers enough distinct records —
`2·limit` for the lexical leg, `limit` for the vector leg — capped at
1000 chunks (`maxLegFetch`). The lexical leg is one any-store cursor
opened without `Limit` and pulled to that rule (`Store.openFTS` /
`Indexer.ftsLeg`): any-store ranks every match before the first row
whatever the `Limit`, and only materializes what is pulled, so the
deeper read costs ~1 µs per row and an early `Close` is free
(§ Tuning).
The vector leg has no cursor — `$knn` computes its `K` nearest up
front — so it re-queries with `K` ×4 until covered, or until the index
reports fewer than `K` candidates: an IVF search reaches only the
probed cells (~4√N docs at nprobe 16), and past that no `K` or `ef`
finds more. Under a scope filter any-store sizes its candidate beam
from `K` (a residual thins the beam before the cut to `K`), so a short
round only ends the leg once a wider `K` stopped adding rows
(`vectorStop`); rows dropped by the similarity floor end it at once,
everything farther being noise too. Fusion stays per chunk (`fuseRRF`, keyed by chunk doc id,
so a long record never gains rank mass from chunk count) and
`groupHits` then collapses chunks into records scored by their best
chunk — max, never sum. Two consequences to design against: the fused
order is reciprocal rank over those bounded windows (a record deep in
both legs can outrank one shallow in one leg — as before), and when one
record dominates a whole window the reply can hold fewer than `limit`
records although the index has more. The reply is bounded by `limit ×
(1 + passages) × maxData` runes of text (chunk size, ~2000 runes, in
place of `maxData` when it is -1). Work order in
`Indexer.Search`: query embedding, then the vector leg, then the
lexical cursor — no read transaction is held across the embed wait or
another store call (an open cursor pins a reader slot, a page cache and
a WAL read-mark until `Close`).

**Hit `data` is a window, not the record.** Each hit's `data` is at
most `maxData` runes (default 512; `-1` = the whole chunk text) cut
around the first occurrence of any query / `require` term — the head
when none occurs literally (a vector-only hit) — snapped to word
boundaries. `dataOffset` is the window's rune offset into the chunk's
indexed text and `dataTotal` that text's rune length, so a client can
tell a clipped preview from the full text and ask for more. `chunk`
(omitted when 0) is which chunk of the record the hit is (§ Chunking
long records). The full record is one dataset query away.

**FTS query operators (lexical leg).** `query` itself understands
`"quoted phrases"` (matched by adjacency) and trailing-`*` prefixes
(`zepp*`). `require` / `exclude` are arrays of extra must / must-not
terms ($require / $exclude — a hit must contain every `require` term and
no `exclude` term); each may itself be a phrase or prefix. A bare term
alongside a `require` is an optional boost, not a filter (Lucene
should-semantics). Phrases / prefixes in `query` shape the FTS leg only;
`require` / `exclude` bind every hit: the vector leg is post-filtered
against the FTS index (`Store.FilterTerms`, one `$text` query restricted
to the leg's hit ids — any-store prices that restriction against the
posting lists and probes per candidate when cheaper) before fusion, so
hybrid and pure `vector` honor
them too — the contract is "must contain / must not contain", not "the
lexical leg agreed". Stop-word stripping is skipped when `query` contains
a `"` so phrases survive intact.

**Ranking knobs (`index.search.*`, docs/05-config.md).** Three app-side
dials, all defaulting to pre-tuning behavior so an absent config block
changes nothing (chunker-hybrid-search-report § 5, measured with
`internal/indexer/eval_test.go`):
- **Stop-word stripping** (`stopWords`, default **on**) — a small English
  stop list is removed from the **FTS-leg** query only (the vector leg
  always gets the full query). On a bag-of-words OR engine every common
  word matches a large fraction of the corpus and pulls BM25 toward
  length/frequency noise; dropping them is a precision win. Built-in list
  in `internal/indexer/stopwords.go`; an all-stop-words query is left
  unchanged rather than emptied.
- **Weighted RRF** (`ftsWeight` / `vectorWeight`, default 1/1) — scales
  each leg's fusion contribution. Lower `vectorWeight` to trust the
  lexical leg more while the dense leg is noisy (short chunks / weak
  embedder).
- **Adaptive leg weighting** (`adaptiveWeights`, default **off**) —
  down-weights the FTS leg per query by its score concentration so a
  flat/weak BM25 distribution (paraphrastic queries) can't drag hybrid
  below the dense leg. Asymmetric (FTS only — cosine is uncalibrated).
  Measured win on weak-lexical corpora, small cost on lexical-friendly;
  opt-in. docs/search/README.md § per-corpus leg weighting.
- **Default operator** (`defaultOperator`, default `or`) — `and` makes
  bare FTS terms all-required. **Measured caveat:** AND over
  natural-language queries is catastrophic (BEIR nDCG@10 SciFact
  0.66→0.02, FiQA 0.23→0.03) because few docs contain *every* query term;
  use it only for short keyword input the client controls. Phrase /
  `require` / `exclude` are the precise-query tools that don't have this
  recall cliff.
- **Vector similarity floor** (`minVectorSim`, default 0 = the legacy
  "> 0" floor) — drops vector hits at/below the cutoff before fusion.
  **Measured caveat:** for the default local model
  (Qwen3-Embedding-0.6B) a static floor is a poor noise filter —
  gibberish queries score ~0.6 cosine, on par with on-topic, and *above*
  real off-topic queries (`internal/indexer/live_probe_test.go`), so any
  cutoff that drops noise also drops signal. Keep 0 for that model and
  let RRF + the FTS leg do the discrimination; raise it only for a
  better-calibrated embedder (e.g. OpenAI).

`vectorStatus` (`used` / `unavailable` / `disabled` / `skipped`) tells
the consumer whether semantic recall took part and why not — an agent
can distinguish "lexical-only because the embedder is momentarily down
or too slow, retry may differ" (`unavailable`) from "this server never
runs vector search" (`disabled`). Value table in `docs/03-api.md` § search.

This is the one sanctioned endpoint that does not map 1:1 onto an SDK
method — the index is a consumer-side feature, owned by this doc.

**Agent-facing tool.** The agent harness (anybao) wraps this endpoint as
its recall tool. `spaceId` is a plain path parameter, so one tool covers
every space on the account — recall in another space is the same call
with a different id, not a different code path.

Errors: `index.disabled` (409, `index.enabled: false`),
`index.no_embedder` (400, `mode=vector` with no embedder configured),
`index.embedder_unavailable` (503, `mode=vector` while the configured
embedder is unreachable or did not answer within the query budget —
retryable; hybrid degrades instead),
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

Reading past a fixed `Limit` (`BenchmarkCutoff`, file-backed, dim 256,
one term matching ~half the corpus; medians of `-count 3 -benchtime
100x` on an idle Ryzen 9 9950X, run-to-run spread 1–2%; `rows` = rows
pulled before `Close`):

| leg | 10k docs | 100k docs |
|---|---|---|
| `$text` `Limit(30)`, drained | 1.01 ms | 13.7 ms |
| `$text` no `Limit`, closed after 30 rows | 1.01 ms | 13.5 ms |
| `$text` no `Limit`, closed after 300 rows | 1.26 ms | 13.9 ms |
| `$text` no `Limit`, closed after 1000 rows | 1.79 ms | 14.8 ms |
| `$text` no `Limit`, drained (5077 / 49835 rows) | 9.7 ms | 105 ms |
| `$knn` K=30 / 120 / 480 / 1000 | 0.09 / 0.13 / 0.43 / 0.52 ms (600 rows at K=1000) | 0.17 / 0.22 / 0.59 / 1.15 ms |
| `$knn` K=1000 with a scope residual | 0.53 ms (316 rows) | 1.43 ms (633 rows) |
| `$knn` K=1000, closed after 30 rows | 0.24 ms | 0.55 ms |

So a `Limit` buys nothing on the lexical leg — the BM25 accumulation
and full sort are paid before the first row either way (the same
7.5 MB/op at 100k with or without `Limit`), and `Limit(30)` vs
no-`Limit`-closed-at-30 are equal within the spread; each extra row
pulled costs ~1 µs over the first thousand and ~2 µs deep into a drain
(the page cache stops helping); the vector leg's reach is the probed
IVF cells (K=1000 returns 600 rows at 10k docs), an explicit `ef` of
10000 changes nothing, and an early `Close` saves only the per-row
fetch. An open iterator pins ~0.7 MiB (`$text`) / 0.2 MiB (`$knn`) and
releases it all on `Close` (`TestIteratorEarlyCloseNoLeak`). End to end
on the same corpus plus five 17-chunk records and one short record
sharing a rare term, `limit 10`: fts answered 10 hits of ONE record
before this change and answers the six matching records now in 0.19 ms;
hybrid answered six records before and ten now in 0.42 ms (10k) /
0.51 ms (100k) — the deeper pull stays well inside one millisecond.
Timings drift 2–3× when the box is busy (a local LLM server, the
indexer's own embedder), so re-measure idle, with
`ANY_CUTOFF_BENCH_SIZES=10000,100000 go test -tags 'fts vector' -run '^$'
-bench BenchmarkCutoff -benchmem -benchtime 100x -count 3
./internal/indexer`.

Query embedding while the local child is saturated (three workers
looping 2000-rune frames — `TestWorkerEmbedder_RealChild_QueryLatency`,
Qwen3-Embedding-0.6B Q8, 20 jittered queries): the wait is half a doc
decode on average, never more than one.

| Machine / mode | doc decode under load | query p50 / p90 / max |
|---|---|---|
| Ryzen 9 9950X, CPU 16 threads | 297ms (3.3 frames/s) | 214 / 318 / 352ms |
| Ryzen 9 9950X, iGPU (RADV, Vulkan) | 755ms (0.9/s) — slower than its CPU | 710 / 825 / 836ms |
| Ryzen 9 3900X, CPU 23 threads (default) | 383ms (1.2/s) | 345 / 496 / 600ms |
| Ryzen 9 3900X, CPU 12 threads | 470ms (1.1/s) | 479 / 720 / 753ms |
| GeForce GTX 1080, Vulkan | 181ms (5.6/s) | 107 / 186 / 199ms |

End to end on the 9950X CPU (server + 360-chunk backlog draining, 110
hybrid `/search` calls over HTTP): p50 212ms, p90 306ms, max 446ms,
every reply `mode=hybrid` / `vectorStatus=used`.
Re-measure with `go test ./internal/indexer -bench . -benchtime 30x`
(`ANY_BENCH_OLLAMA=1` adds the real-embedder run).

### Known limits

- "Index from the next change" applies to the whole pipeline: removing
  `<data-dir>/index/` does **not** re-index old content — only rows
  whose `_addSeq` moves afterwards get (re-)indexed.
- Embedder latency only delays the vector leg: fresh writes are FTS-
  searchable immediately and gain vector recall once embedded. At query
  time the local child serves a search ahead of doc frames (wait ≤ one
  decode) and the embedding is capped (`index.search.queryEmbedTimeout`,
  default 5 s), past which hybrid answers lexical-only.
- **Embedder input is still clamped**, per SEQUENCE, to
  `index.local.contextSize / index.local.batchDocs` tokens (default
  2048 / 1 = 2048, EOS preserved for last-token pooling). Chunking
  (§ Chunking long records, 2000-rune target) keeps every chunk inside
  it for prose; a chunk of dense CJK or code can still exceed the clamp
  and embed head-only — FTS covers its full text regardless. Raising
  `batchDocs` lowers this bound proportionally.
- **`require` / `exclude` bind the hit, i.e. the chunk**: a term that
  appears only in another chunk of the same record does not satisfy a
  `require` for this one — and a passage is only ever a chunk that
  satisfied them itself.
- **Passages are the window's, not the record's.** `passages` lists a
  record's other chunks that ranked within the legs' windows (≤ 1000
  chunks deep); a record whose chunks all match still shows only the
  ones the legs reached.

## Tests

- `internal/index/stream_test.go` — `RecordsSince` chains the
  IncludeDeleted projection + `_addSeq` window + sort, parses the seq,
  and stops + closes on a yield error; `IsDeleted`.
- `internal/editor/chunker_test.go`, `internal/chat/chunker_test.go` —
  text extraction including the tombstone case.
- `internal/server/handlers_index_test.go::TestIndexChunkers_FullFlow` —
  in-process SDK end to end: creation, cursor advance, deletions
  (tombstones), and the non-memory tombstone case.
- `internal/indexer` unit tests — store round-trips (FTS + vector +
  pending lifecycle + purge/drop, in-memory any-store), RRF fusion
  and record grouping (`rrf_test.go`), the HTTP embedder clients
  against `httptest` servers.
- `internal/indexer/search_terms_test.go` — `require`/`exclude` in every
  mode, chunk windows + `maxData`, and
  `TestIndexer_SearchLimitCountsRecords`: a 17-chunk record whose every
  chunk outranks a short exact match, `limit 10` in fts / hybrid /
  vector → both records, passages on the long one.
- `internal/indexer/cutoff_leak_test.go` — an any-store `$text` (with
  and without `Limit`) and `$knn` iterator closed after a few rows, 500
  times: no goroutine, no retained heap, reader slots released, a write
  and the DB close go through afterwards.
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
- `internal/indexer/embed_worker_test.go` — the child supervisor
  against a helper process (this test binary re-exec'd, speaking the
  frame protocol in place of a model): per-frame batching, a query
  jumping the doc queue, abandoned callers (queue and mid-frame) keeping
  the child, crash → CPU demotion, request timeout, spawn backoff,
  `SetThreads` idle/busy, `Close` mid-request and mid-spawn; plus a
  gated real-child comparison against the in-process model.
- `internal/indexer/search_budget_test.go` — the query-embedding
  budget in `Search`: hybrid degrades to fts/`unavailable`, vector
  fails as embedder-unavailable, a cancelled caller gets its own
  cancellation.
- `internal/server/handlers_search_test.go` — in-process SDK + in-memory
  store + deterministic fake embedder: all three modes end to end,
  scope filtering, deletion purge, degraded/disabled errors, and the
  asynchronous worker path (`Start` + poll).
