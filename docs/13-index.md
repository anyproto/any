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
    Scope    string // open slug set; "basic"/"chat"/"agent"/"program" are the vocabulary
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
| `agentmem.NewChunker()` | `agent_memory_items`     | `agent_memory`  | `agent`          | per item: context + body + category + keywords/entities/tags |
| `index.NewPropChunker(excl…)`| `prop` (virtual)    | — (ungated)     | `props` (default) / per-prop override | property values, `"<name>: <value>"` (see below) |

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
- **Memory = one record per chunk, scope `agent`.** `agent_memory_items`
  indexes one doc per item (`agentmem.NewChunker`), so each item stays
  independently retrievable and filterable (by category / recency /
  confidence) — coalescing would destroy that. `Data` = the item's
  semantic + lexical text (context, body, category, keywords, entities,
  tags); numeric/structural fields (confidence, salience, accessCount,
  edges, timestamps) are excluded, so a metadata-only bump leaves the
  content hash unchanged and the indexer skips re-embedding (see below) —
  important because memory items are bumped often (accessCount on recall,
  fields on evolution) but their text rarely changes.
- **Programs are not indexed.** The `program` type carries only
  `program_source`, and it has no chunker — code (docstrings included)
  is not a search target (anybao ADR-010 §5). A program's one-liner
  lives in its `summary` property (not indexed either — builtin type
  decls carry no `meta["index"]` flag; revisit only if evidence
  demands program recall).
- **Scopes are an open set** of slugs (`index.ValidScope`: 1..64 chars
  of `[a-z0-9_-]`); `basic` / `chat` / `agent` / `history` / `props`
  are the established vocabulary, and property meta flags can mint new
  ones. `props` is FTS-only (see the prop chunker below).
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
  field-length normalization and surface as top hits) plus the
  wired-in `enrich_proposal` (ephemeral review scaffolding).
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

### Agent history (turns + chunks) — scope `history`

`agent_turns` and `agent_chunks` are indexed under scope **`history`**
by two chunkers on the `agent_log` type (`agentlog.NewTurnChunker` /
`NewChunkChunker`), gated on agent_log membership like the chat chunker
(both datasets live on the chat object). Turns index `userText` + joined
`replies` (Title-boosted on `userText` — the question is the strongest
recall anchor); chunks index their `summary`. `think` / `effects` /
scalars are excluded (narration and metadata are recall noise; the raw
record stays reachable by seq for drill-down). This is what makes deep
history semantically reachable without exact seqs — `search` with scopes
`[agent, history, basic]` spans memory, conversation history, and
content in one call (ADR-006 §2 / the anybao recall tool).

### Excluded from indexing entirely

`program_source` and `miniapp` have **no chunker**;
`Registry.ForDataset` returns nothing for them.

## Removal semantics

Three granularities, all addSeq-consistent (discovered through the same
`ChangedSince` window, applied in the same page transaction):

| What happened | Who detects it | Index operation |
|---------------|----------------|-----------------|
| record deleted / value cleared (per-record chunker) | the chunker (streams the tombstoned record / empty value) | entry with `Data == ""` → `DeleteId(objectId:dataset:recordId)` |
| any change to a **coalescing** dataset (editor) | the `Reconciler` chunker + indexer hash-diff | delete the window ids that vanished, upsert the changed/new ones, leave unchanged ones — expresses block edits / deletes / merges that shift a window's shape, without re-embedding untouched windows |
| type detached (`DetachType` — bumps `_addSeq`) | the indexer (gated chunker's `TypeId()` ∉ `any.types`) | prefix delete `objectId:dataset:` |
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
- A `cursors` collection holds one `{id: spaceId, seq}` row per space
  plus a `_meta` row pinning the **schema version** (the version ↔
  layout map lives on `indexSchemaVersion` in `internal/indexer/
  store.go`; a mismatched DB errors at boot with a remove-to-rebuild
  message, no migration — the index is derived state and re-indexes
  from the next change) and the vector dimension — changing the
  embedder dimension is the same kind of boot error.

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
- `local` — **default**: **in-process llama.cpp**, no external service. yzma purego
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
  the IVF index. One llama context per process, mutex-serialized;
  within an `EmbedDocs` call texts pack into multi-sequence decodes —
  up to `index.local.batchDocs` docs (default 16) per `llama_decode`,
  greedy in order under the `contextSize` token budget. Batching
  amortizes per-decode overhead; how much it buys depends on where the
  bottleneck sits. Measured on ~330-token docs: GTX 1080 via Vulkan
  7.4 → 8.5 docs/s (+16%), 24-core CPU 1.8 → 2.0 docs/s (+9%) — both
  legs are compute-bound there, so the win is modest; hardware where
  per-decode overhead dominates (fast GPUs on small models) gains
  more. Batched and single decodes produce identical vectors
  (TestLocal_BatchedMatchesSingle); `batchDocs: 1` restores
  one-doc-per-decode. Compute threads
  (`NThreads`/`NThreadsBatch`)
  default to `runtime.NumCPU()-1` (leave one core free); override with
  `index.local.threads` / `ANY_INDEX_LOCAL_THREADS` — going past the
  physical core count can regress on hyperthreaded CPUs. Loaded cost ≈ 640 MB mmap + ~200 MB context;
  nothing is loaded until the first embed call. Linux needs a system
  `libffi.so.8` (ubiquitous on mainstream distros; NixOS: `nix develop`
  — the flake's dev shell provides it).

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
compute buffers that scale with `contextSize`). Measured on a GTX 1080
(478 editor-window docs, ~330 tokens each, `batchDocs` 16): CPU 240 s
≈ 2.0 docs/s at ~14 cores vs Vulkan 56 s ≈ 8.5 docs/s — a ~4× win with
the CPU left essentially idle. If a GPU dies mid-run, decodes error and the
affected docs stay `pending` (standard outage semantics); a restart
re-selects backends cleanly. CUDA/ROCm builds are deliberately not
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
| Local dev / server (`make build`) | `fts vector` | on | on |
| Release tarball, linux + windows | `fts vector` | on | on |
| Release tarball, **darwin** | `fts` | on | **off (forced)** |
| FTS-only | `fts` | on | off |
| Vector-only | `vector` | off | on |
| None (default `go build`) | *(none)* | off | off |
| Mobile (gomobile) | *(none)* | off | **off (forced)** |
| Mobile + FTS | `fts` | on | **off (forced)** |

The two legs are **separate, positive build flags** — a build opts each
in. `make build` ships both; the default `go build` ships neither. The
rule for `vector` is stronger than for `fts`:

- **`vector` / embeds are *always* off on mobile, regardless of tags.**
  `capVector` is `vector && !gomobile && !mobile`, so even `gomobile
  bind -tags vector` keeps the whole embedding/ANN leg out — no embedder
  is constructed, no model is downloaded, and the embedder
  implementations (ollama, openai, and the in-process llama.cpp `local`)
  are not linked at all. This is deliberate: the `local` embedder links
  the yzma / jupiterrider-ffi llama.cpp bindings, whose libffi CIF
  descriptors resolve `ffi_prep_cif` at package load — a symbol Android
  doesn't provide, which panics the Go runtime at startup. A runtime
  toggle can't prevent a load-time crash, so gomobile force-disables the
  leg at compile time. Embedding has no place in the mobile runtime
  anyway.
- **`vector` is off in the darwin *release tarballs*** — same failure
  class, different policy layer. On macOS the ffi bindings link fine,
  but `jupiterrider/ffi`'s package init extracts an **ad-hoc-signed**
  `libffi.8.dylib` into `os.UserCacheDir()` and `dlopen`s it, again on
  link rather than first use. macOS **library validation** — enabled by
  the App Sandbox and by the hardened runtime unless the host sets
  `com.apple.security.cs.disable-library-validation` — denies that load
  (the dylib has no Team ID, and it materializes at runtime, *after* the
  host app was signed, so no consumer can pre-sign it), and the process
  panics before `main`. `FFI_NO_EMBED=1` is not an escape hatch: it
  falls back to `dlopen("libffi.8.dylib")` and macOS ships only
  `/usr/lib/libffi.dylib`. Unlike the mobile rule this is **not** a
  `capVector` term — it is a build-script decision in
  `scripts/build-any.sh` (per-platform `TAGS`, plus a guard that fails
  the build if the ffi edge reappears in a darwin binary). `make build`
  on macOS still ships `vector` and still works, because a locally built
  binary is unsigned and library validation doesn't apply. Restoring the
  leg on darwin means either splitting the in-process `local` embedder
  behind its own tag (it is the only importer of yzma) or building with
  `-tags ffi_no_embed` plus `-ldflags -X
  github.com/jupiterrider/ffi.filename=@executable_path/llamacpp/libffi.8.dylib`
  and shipping a libffi the consumer signs — both change the tarball
  contract, so they are tracked separately.
- **`fts` is a plain opt-in flag**, available everywhere including
  mobile (`gomobile bind -tags fts` gives full-text search with no
  embedder). Both mobile binds pass it: iOS `-tags 'mobile fts'`, Android
  `ANY_TAGS := gomobile fts` (DROID-44). ~3 KB of AAR — any-store's
  fulltext index links either way, the tag only lifts the gate.

On **linux** the `vector` leg has no embedded libffi at all (the ffi
package embeds one only for darwin and windows), so the tarball binary
`dlopen`s the system `libffi.so.8` at package init and panics at startup
if it is missing. Mainstream distros ship it; minimal or musl-based
images must install it (or run an `fts`-only build).

Mechanics (`internal/indexer`):
- `capFTS` (`fts`) and `capVector` (`vector && !gomobile && !mobile`) are
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

On the mobile embed path (`internal/embedded.Start`, exported to iOS as
`AnyServerStart`) the caller drives `index.enabled` rather than reading
a config file: the boot gate is `indexEnabled && fts`, where `fts` is the
compiled cap and `indexEnabled` is the `AnyServerStart` argument. So an
FTS build can still keep the indexer dormant per engine instance — the
iOS share extension passes `false` (it never searches, and every MB of
appex headroom matters), the app passes `true` if it wants engine search.
`index.embedder` is hard-forced to `"none"` on this path regardless (no
embedder is ever constructed on mobile). The gomobile/Android bind passes
a constant `true` — load-bearing now that it builds with `fts`; Android
has no share extension that would want the index off.

### Search

`POST /v1/spaces/:spaceId/search` `{query, scopes?, limit?, mode?,
require?, exclude?}` →
`{hits: [{scope, objectId, dataset, recordId, data, score}], mode,
vectorStatus}`. Modes: `fts` (BM25), `vector` (cosine ANN; requires an
embedder, hits below the similarity floor are dropped as noise), `hybrid`
(default — both legs fused by reciprocal rank, k=60; degrades to `fts`
when the embedder is missing or the query embedding fails — `mode` in
the reply is the mode that actually ran). Scores are comparable only
within one response. CLI: `any search <spaceId> <query> [--scopes ...]
[--limit N] [--mode ...] [--require T ...] [--exclude T ...]`.

**FTS query operators (lexical leg).** `query` itself understands
`"quoted phrases"` (matched by adjacency) and trailing-`*` prefixes
(`zepp*`). `require` / `exclude` are arrays of extra must / must-not
terms ($require / $exclude — a hit must contain every `require` term and
no `exclude` term); each may itself be a phrase or prefix. A bare term
alongside a `require` is an optional boost, not a filter (Lucene
should-semantics). Operators apply to the FTS leg only and are ignored in
pure `vector` mode. Stop-word stripping is skipped when `query` contains a
`"` so phrases survive intact.

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
