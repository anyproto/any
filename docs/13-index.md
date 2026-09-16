# 13 — Search index (consumer-side)

This governs the search-index pipeline: the chunker contract
(`internal/index` — `index.go`, `module.go`, `prop.go`, `schema.go`,
`links.go`; `internal/editor/chunker.go`, `internal/chat/chunker.go`)
and the indexer that consumes it (`internal/indexer`): a local BM25 +
vector index per space behind `POST /v1/spaces/:spaceId/search` /
`any search`, plus the link index behind the backlinks endpoints.

The chunker registry is built by `server.NewIndexRegistry` when the
account engine boots (`bootEngine`, stored on the engine as
`chunkers`); the indexer is opened next to it when `index.enabled` and
drives the chunkers through the SDK's per-space change feed
(`Space.Changes()`).

This doc is the **contract**. The evaluation and decision record —
chunk-length measurements, BEIR results, why the defaults are what they
are — is [`search/README.md`](search/README.md).

## The contract

```go
type IndexEntry struct {
    Scope    string // open slug set; "basic"/"chat"/"props" are the vocabulary
    ObjectId string
    Dataset  string // the real storage collection (doc-id middle segment)
    RecordId string
    Data     string // text to index; empty ⇒ remove this record from the index
    Title    string // optional BM25F field; re-prefixed onto chunks past the first
    ApplySeq uint64 // peer-local, per-space monotonic apply counter
    Links    []LinkEntry // the edges the entry's record(s) hold (§ Links)
}

type Chunker interface {
    Dataset() string // unique chunker name; a doc-id segment unless the chunker is dynamic
    TypeId() string  // any.type gate; "" = ungated
    // Streams every entry of objectId with ApplySeq > since, ascending.
    // Cleared/deleted records yield removal entries (Data == "").
    ChunksSince(ctx, sp space.Space, objectId string, since uint64, yield func(IndexEntry) error) error
}

// Optional capabilities:
type Reconciler interface { // index unit spans several records on one dataset
    Chunker
    Reconcile(ctx, sp, objectId string, since uint64) ([]IndexEntry, error)
}
type DynamicChunker interface { // dataset set resolved per space at runtime
    Chunker
    EvictDatasets(ctx, sp, attached map[string]bool) ([]string, error)
}
type MultiReconciler interface { // Reconciler over several storage collections at once
    DynamicChunker
    Reconciles() bool
    ReconcileAll(ctx, sp, objectId string, since uint64) (map[string][]IndexEntry, error)
}
type TextEvictor interface { EvictText(ctx, sp, attached map[string]bool) []string }
type CatalogInvalidator interface { Invalidate(spaceId string) }
type WholeCollectionLinks interface { LinksReplaceCollection() }
```

A chunker turns one object's records into `IndexEntry` values ordered by
`ApplySeq`. The indexer persists a cursor (its last-seen `ApplySeq`) and
calls `ChunksSince(cursor)` to pull only what changed. A reconciling
chunker (editor) returns the object's **full** current entry set
instead, which the indexer diffs against what is stored: a coalesced
window can't be expressed as a per-record delta — its text needs
sibling records below the cursor, and a deleted block's position is
wiped from its tombstone.

## Chunkers, scopes, gating

Every gate below is a membership test. An objects row's **members**
(`index.Members`) are its one type (`any.type`) and its collections
(`any.collections`), plus — when `any.type` holds a definition marker
(`__type__` / `__collection__`) — the row's **own id**, because a
definition hosts its own datasets and values. Nothing else enters the
set, and a row's members are read once per page from the row itself.

| Chunker | `Dataset()` → doc-id segment | Gate | Scope | `Data` |
|---|---|---|---|---|
| `editor.NewChunker()` | `editor` (virtual) → each editor storage collection: `editor_blocks` and every namespaced `<typeId>_<key>` instance | per storage collection: an owner among the row's members | `basic` | a **coalesced window** of consecutive blocks (recordId `win_<anchor>`) |
| `chat.NewChunker()` | `chat` (virtual) → `chat_messages` | per storage collection: an owner among the row's members | `chat` | the message's `text` only |
| `index.NewPropChunker()` | `prop` (virtual) | ungated | `props` (default) / per-property override | property values, `"<name>: <value>"` |
| `index.NewSchemaChunker(static…)` | `schema` (virtual) → each runtime records storage collection | per dataset, self-applied | `basic` (default) / per-dataset `x-search.scope` | runtime-dataset records by their `x-search` mapping |

- **Module chunkers index every storage collection a module serves.**
  The editor and chat chunkers are `index.ModuleChunker`s: one
  registered chunker per module, resolved per space from
  `Space.Datasets` — the module's canonical storage collection plus
  every namespaced instance a type's part declares. Entries carry the
  real storage collection as `Dataset`, so doc ids stay per storage
  collection (`objectId:<collection>:`). The gate is ownership (the
  discovery document's `owners`): as a `DynamicChunker` the chunker
  tells the worker to prefix-evict `objectId:<collection>:` for every
  storage collection none of whose owners is among the row's members,
  plus storage collections that left the catalog since process start
  (a removed part — § Removal semantics).
- **Chat = one entry per message.** Creator, reactions and attachments
  are not indexed text.
- **Editor = coalesced windows.** Consecutive blocks in document order
  (the `List` tree walk) group into ~1.5 KB windows, breaking before
  each heading (`internal/editor/window.go`); one index doc per window,
  anchored on its first block (`recordId = win_<firstBlockId>`), `Data`
  = the member texts joined by newline with the heading leading, `Title`
  = that heading. One-block chunks are too short for vector recall and
  skew BM25 length normalization (`search/README.md`). The editor
  chunker is an `index.MultiReconciler`: it returns the full window set
  per editor storage collection the object holds, and the indexer diffs
  each set against that collection's stored docs by content hash (§ Content
  hashes) — only changed or new windows re-embed. An append re-embeds
  one window; an edit in a part's own editor never touches the shared
  body's docs. Forming windows is an O(doc) read per edit; embedding is
  incremental.
- **Scopes are an open set** of slugs (`index.ValidScope`: 1..64 chars
  of `[a-z0-9_-]`); `basic` / `chat` / `props` are the established
  vocabulary, and property or dataset declarations can name others.
- **`TypeId()` gating**: a static chunker naming a type runs only while
  that type is among the row's members — it is the object's `any.type`,
  or the row is that type's own definition; otherwise the indexer
  prefix-evicts `objectId:<dataset>:`. No compiled-in chunker uses it —
  module and runtime datasets gate per storage collection through
  `DynamicChunker`.
- **Not indexed**: saved views (`dataviews` / `views`) have no chunker;
  a runtime dataset declared without an `x-search` mapping produces no
  text docs (§ The schema chunker).

### The prop chunker (`internal/index/prop.go`)

Indexes property VALUES from the shared `objects` storage collection
under the virtual dataset `prop` — one entry per (object, indexed property), doc
id `objectId:prop:<propId>`:

- **User properties index by default under the scope `props`**, never
  interleaved with `basic` ranking; a search that wants pure content
  passes `scopes` without `props`. The definition's `meta["index"]`
  (`meta` on property create, or `PATCH …/properties/:propId` with
  `{"set": {"meta.index": …}}`) overrides it: absent/empty ⇒ `props`;
  `"<scope>"` ⇒ that scope; `"none"` ⇒ excluded from the TEXT index (a
  property carrying a link marker still reports its edges, § Links). An
  invalid slug excludes rather than landing in the default.
- **Entry text is self-describing**: `"<prop name>: <value>"`
  ("Score: 9", "Publisher: Gollancz"), so property-name search works and
  bare numbers get context. The name is the definition's `name`, falling
  back to `xKey`. A valueless row is `Data ""` (a removal signal), never
  a bare name. The name also rides `IndexEntry.Title`, re-prefixed onto
  chunks past the first, so a value long enough to split keeps its name
  on every chunk (§ Chunking long records); the `title` field itself
  joins the BM25F index only when `index.search.titleWeight` is set.
- **Kinds**: string; number (canonical JSON — integers without a decimal
  point); datetime (RFC 3339 UTC, so `2026-08` matches a month); array
  (newline join of its string and number elements). Boolean and object
  kinds never index.
- **The `props` scope is FTS-only**: props-scope docs are never marked
  pending, so they are never embedded — short "name: value" entries
  embed badly and pollute vector recall. A `meta.index` override into
  another scope re-enters the vector pipeline.
- **Built-ins `any.name` and `any.description` always index** under
  scope `basic`, reserved recordIds `name` / `description`, raw (no name
  prefix, no title) — except for rows with an excluded member. The
  exclusion list always holds `__type__` and `__collection__`: definition rows are
  schema, not knowledge (discovery is `GET …/types`), and their one-word
  names would otherwise win BM25 on field-length normalization.
- **Short prop docs never embed**: a `prop` entry under 64 bytes is not
  marked pending. Short name-like strings land in a flat cosine band
  against relevant and irrelevant queries alike, so they fill vector
  top-N slots without discriminating (`search/README.md` § Vector
  similarity floor). Long descriptions and long scope-overridden values
  still embed.
- Per streamed live row the chunker emits the built-ins and one entry
  for EVERY catalog property: value present and its owner among the
  row's members ⇒ text; otherwise `Data ""`. Cleared values and values
  whose owner left the row therefore evict record-level, idempotently.
- The per-space catalog (indexable properties of every non-built-in
  type AND collection — property definitions work the same on both) is
  a 30 s TTL snapshot. The worker invalidates it whenever a definition
  object changes in the feed (§ Links → Catalog freshness), so a value
  written right after its definition indexes on that write.

### The schema chunker (`internal/index/schema.go`)

Indexes records of **runtime-defined datasets** (`03-api.md` § Runtime
dataset schemas) by their declaration's `x-search {title, text, scope}`
mapping — one registered chunker covers every searchable runtime
dataset in every space. Entries carry the real storage collection (doc ids
`objectId:<collection>:<recordId>`) under `x-search.scope`, default
`basic` (runtime records are user content and embed normally; a dataset
that declares `props` inherits that scope's FTS-only rule). `Dataset()`
returns the virtual name `schema`, used only for chunker identity.

- **Mapping**: `x-search.title` → `IndexEntry.Title` and the leading
  line of `Data`; `x-search.text` → the rest of `Data`. `text` is a
  field key or an array of keys: each mapped field renders separately
  and the non-empty values join with a blank line, in mapping order.
  Either side may be absent; title is single-field. Values render by
  their ACTUAL type — string, number (canonical), datetime (RFC 3339),
  array (newline join of those); anything else is empty — because the
  SDK does not check x-search fields against declared kinds. A malformed
  `text` form (wrong JSON type, non-string elements) makes the dataset
  unsearchable. Both sides empty (cleared values, tombstone) ⇒ `Data ""`.
- **Scope**: an invalid `x-search.scope` slug makes the dataset
  unsearchable rather than landing in the default (the declaration syncs
  from arbitrary peers). A scope change applies as records re-index:
  stored docs keep their scope until their object next goes dirty — the
  same forward-only semantics as a property `meta.index` change.
- **No catalog cache**: `Space.Datasets()` is an in-memory snapshot the
  SDK refreshes synchronously when a definitions change applies, so a
  fresh read is never stale relative to the applySeq window being
  processed; a TTL cache could skip records in the define-then-write
  flow while the cursor advances past them.
- **Gating is per dataset, self-applied** (`DynamicChunker`): the worker
  asks for the object's eviction set (`EvictDatasets`: searchable
  datasets whose owning type is not the row's, every runtime dataset
  without a usable `x-search` and without link fields, plus retired
  names) and prefix-deletes `objectId:<collection>:` for each in the
  same page transaction, then streams. A dataset with link fields but no
  usable `x-search` loses its text docs (`TextEvictor`) and is still
  streamed for its edges. One catalog resolve serves the paired
  `EvictDatasets` + `ChunksSince` calls (each space has a single advance
  goroutine).
- **Static skip set**: compiled-in dataset names (the server's
  `handler.Type` datasets and the modules' canonical storage
  collections) and the virtual names are never treated as runtime;
  module-served storage collections belong to the module chunkers
  (discovery `module` other than `records`).

### Chunking long records (`internal/indexer/chunk.go`)

Chunkers emit **one entry per record**; the indexer splits it. An entry
whose `Data` exceeds `Options.ChunkRunes` (`DefaultChunkRunes` = 2000
runes, ~500 tokens of prose) becomes several chunk docs — cut at a blank
line, a line break, a sentence end or whitespace in the second half of
the window, hard-cut when there is none; no overlap, deterministic, so
an unchanged chunk keeps its content hash. Chunk 0 keeps the entry's
head; chunks `n > 0` are prefixed with the entry's `Title` (clamped to
half the bound) so a mid-record passage keeps its subject for BM25F and
the embedder. Each chunk sits inside the local embedder's token clamp,
so vector recall covers the whole record, and a hit's `data` is the
matching passage. The hit's `recordId` is the record's; `chunk` names
the record's best-ranked piece (§ Search). Editor windows and chat
messages are below the bound by construction; long runtime-dataset
records and long property values are what this is for.

### Content hashes (incremental embedding)

Every index doc stores `hash` — a 64-bit FNV-1a of `Data` and `Title`,
hex-encoded (`docHash` in `store.go`). The title is included because a
title-only edit changes the text of chunks past the first but not of
chunk 0; hashing `Data` alone would leave chunk 0 serving the old title.

- **Reconcile diff (editor).** `worker.reconcileMulti` reads, per editor
  storage collection the object holds, the stored `(id, hash)` under
  `objectId:<collection>:` (`Store.DocHashes`), diffs against the full
  window set, and emits deletes for vanished ids, upserts for new or
  changed ones, and nothing for unchanged ids — their docs and vectors
  stay.
- **Per-record skip (streaming chunkers).** On an incremental advance,
  `worker.streamChunks` reads the re-streamed records' stored chunk
  hashes (`Store.DocHashesByRecords`, one range seek per record); a
  record whose indexed text is unchanged is skipped (no re-embed), and
  one that now splits into fewer chunks drops its trailing chunk docs.
  On the cold cursor (0) nothing is stored and entries apply blind. This
  is what keeps a chat reaction, or any non-indexed field edit on a
  runtime record, from re-embedding.

A hash collision (64-bit) costs at worst one stale vector.

## Links

The chunker feed carries a second output next to text: the **edges** a
record holds — every `any://` reference, classified. The worker lands
them in a per-space link collection of `index.db` in the same page
transaction as the text docs, behind the same cursor, so eviction is
applySeq-consistent with content and the rebuild triggers cover both.
This is the index behind `GET …/objects/:id/backlinks`,
`GET …/objects/:id/links` and `GET /v1/backlinks` (`03-api.md` § Links
and backlinks); the SDK has no reverse index of its own.

### Contract

```go
type LinkEntry struct {
    ObjectId, Dataset, RecordId string // the source place (DatasetProp + propId for a value)
    TypeId   string     // a property value's type (prop sources only)
    Field    string     // the runtime-record field the reference was read from
    Kind     string     // mention | link | card | embed | relation (open set)
    Target   anyuri.URI // canonical (URI.Canonical): never a space
}
```

- A **streaming** chunker's entry replaces its record's edges — nil
  means the record links nothing (a tombstone, a cleared value, a value
  whose owner is not on the row). A **reconciling** chunker's set
  replaces the whole collection's edges, as does every stream of a
  `WholeCollectionLinks` chunker (the prop chunker). Either way the
  source place is per record: a coalesced editor window reports each
  member block's links under that block's own id.
- **Canonical targets.** `URI.Canonical` resolves what was written to
  one key: the bare in-space form and the global form become
  `any://o/<sp>/<id>`; a record path stays a record path (a block link
  is a block link — the object is not counted twice); `p` / `m` / `f`
  keep their kind, params and fragments dropped; `s` and unknown kinds
  are not targets. A link to the source object itself, as a whole, is
  dropped (the parent is not a backlink).
- **What reports edges**, by field descriptor (`27-descriptors.md`
  § `xFormat`): `xFormat.links` marks a field — `link` (the string is
  one reference), `links` (the array lists references), `markdown` (the
  text is scanned), `none` (never scanned) — and the `relation` slug
  implies `links`, the `markdown` slug implies `markdown`. Plain `text` /
  `longtext` is never scanned; `meta.index: none` keeps a property out
  of the TEXT index only. Kinds: a value field yields `relation`;
  scanned text yields `mention` for an `m` reference and `link` for the
  rest.
- **Catalog freshness.** A definition object changing in the feed (a
  property added, patched or removed) invalidates every `CatalogInvalidator`'s
  per-space snapshot before the rest of the object is extracted, so a
  value written right after its definition indexes on that write. The
  prop chunker's stream is complete per row, so a removed definition's
  edges fall out on the row's next change.
- **Per source:**

| Source | Fields | Kinds |
|---|---|---|
| editor blocks (every editor storage collection) | `text` (markdown) | `link` / `mention`; `card` when a paragraph is exactly one whole-line `[…](any://o/…)` or `[…](any://f/…)` link; `embed` for the synced-block reference envelope (an `html` block `<!-- any:block {…"kind":"synced-block","data":{"role":"reference","ref":"any://o/…/editor_blocks/…"}} -->`) |
| chat messages | `text` (markdown), `attachments.<k>.link`, `agent.debugLink` | `link` / `mention` |
| property values (the virtual `prop` dataset) | every property whose descriptor carries a marker; values whose owner is not on the row yield nothing | `relation` (`link` / `mention` under a `markdown` marker) |
| runtime records | every field whose `x-format` carries a marker; a dataset with link fields and no `x-search` is streamed for its edges only | as above |

### Store

- `<spaceId>_links`, one doc per edge:
  `{id, objectId, dataset, recordId, typeId?, field?, kind, target{kind,
  spaceId, objectId?, dataset?, recordId?, propId?, identity?, fileId?},
  targetKey, targetObject?, applySeq}`. `id` is the text-doc base
  `objectId:dataset:recordId` + `U+001F` + `<hash(kind, field, target)>`
  — the chunk separator, so a record id containing `:` can never be
  confused with a sibling's prefix: structural removals are the
  `:`-terminated ranges (`objectId:`, `objectId:dataset:`), a record's
  edges are `[base+U+001F, base+U+0020)`, and the hash merges duplicates
  from one place. `targetKey` is the canonical target, `targetObject`
  the object it belongs to (absent for identities and files). Indexes:
  `(targetObject, id)` (sparse) and `(targetKey, id)`, so a capped read
  is a prefix scan in id order.
- **Content changes are an exact diff.** Per changed object the worker
  reads the object's stored edge ids once (`LinkIds`, one range), removes
  the ids that fell out of a replaced scope, and writes the ids that
  appeared (`diffLinks`). An edit that leaves a record's edges unchanged
  costs the read and nothing else — no rewrite, no signal. Structural
  prefix deletes (object deleted, an owner gone from the row, definition
  retired) apply to the link collection as they apply to text docs.
- The cursor row carries `links`, the sink's layout version, stamped by
  the worker after its first landed page or after a backfill — never by
  a plain cursor write, so a failed backfill is retried on the next
  start. An indexed space stamped behind is **backfilled** on its
  worker's advance goroutine: the collection is dropped, every object
  the feed knows is re-extracted once through the chunkers and only the
  edges are kept, landed page by page — text docs untouched, nothing
  re-embeds; an unreadable object is skipped and its edges arrive with
  its next change. Reported as `index.links_backfill.<spaceId>` on the
  process view (`22-processes.md`).
- After a page changed edges the indexer reports the affected target
  keys (`Options.OnLinks` — only targets an edge appeared for or
  vanished from); the server publishes them as one device-scope bus
  event `links.updated` (`21-events.md`), capped at 200 targets with
  `truncated: true` past that.

### Reads

`Indexer.Backlinks` seeks `targetObject` for a whole-object target
(every edge to the object, its records and its values — the reply
splits them on whether the target is a part) and `targetKey` for a
record, value, identity or file target; `Indexer.Links` walks the source
prefix (object / dataset / record); `BacklinksAll` runs the seek over
every indexed space — the device holds only spaces this account is a
member of, so the account-wide read is access-filtered by construction.
Every HTTP read is capped at 500 edges per space and says so
(`truncated`); there is no continuation.

## Removal semantics

All removals are applySeq-consistent: discovered through the same
`ChangedSince` window and applied in the same page transaction.

| What happened | Who detects it | Index operation |
|---|---|---|
| record deleted / value cleared (streaming chunker) | the chunker (streams the tombstoned record / empty value) | entry with `Data == ""` → range delete `[objectId:dataset:recordId, +" ")` (the record's every chunk) |
| record shrank to fewer chunks | the indexer (`planDocs` diffs the new chunk set against the stored hashes) | delete the trailing chunk ids, upsert the changed ones |
| any change to a reconciled storage collection (editor) | the `MultiReconciler` + hash diff, per storage collection | delete vanished window ids, upsert changed/new ones, leave unchanged ones |
| an owner left the row (retype, collection detached) | the indexer: a static chunker's `TypeId()` is no longer a member; module and runtime storage collections via `EvictDatasets` (no owner among the members) | prefix delete `objectId:dataset:` |
| part or runtime dataset definition removed | the module / schema chunker (the storage collection left the catalog → per-space retired set, held for the process lifetime) | prefix delete `objectId:dataset:` on each object's next dirty tick |
| object deleted (`Objects().Delete`) | the indexer (`ObjectChange.Deleted` in the change feed) | prefix delete `objectId:` |

Every prefix operation runs on the link collection too (§ Links), so an
object's or a dataset's edges leave with its text docs.

Object deletion leaves **no tombstone**: the SDK purges the shared
`objects` row and every per-object storage collection, and announces the
deletion once through the change feed as `ObjectChange{Deleted: true}`,
at an applySeq strictly greater than the object's last content change.
That flag is the only eviction signal for the object's docs — nothing
re-streams for a purged object. Record-level tombstones (a deleted chat
message, a cleared value) survive with `_deletedAt` set; chunkers opt in
via `Projection({IncludeDeleted: true})` and stream them as
`Data == ""`. Removing what was never indexed is a no-op, so every
operation applies unconditionally.

After an owner leaves and returns, streaming chunkers (chat, runtime
records) do not resurrect records below the cursor — those index on
their next write. Reconciled storage collections and property values
re-index on the membership write itself (it writes the object's row).

**Definition-removal eviction is lazy and process-scoped**: an object
never dirtied after the removal keeps its stale docs, and a restart
forgets the retired set (the SDK wipes the removed definition's name).

## ApplySeq semantics

`_applySeq` is the SDK's **per-space, peer-local, monotonic apply
counter**, advanced by every apply that mutates the space's records —
synced changes, the account mirror and device-local writes alike. It is
the record-level twin of `ObjectChange.ApplySeq`, so the chunker window
and the change-feed cursor share one ordering axis. Treat it as an
opaque cursor: compare and persist it, never ship it to another peer.

`RecordsSince` chains `Projection({IncludeDeleted: true})` → a typed
`_applySeq > since` filter → `Sort "_applySeq"` → `Iter`, so every
chunker streams ascending past the cursor. Rows never stamped with
`_applySeq` sort below any `since >= 0` and are excluded — the same
contract as `Changes().ChangedSince`. Filters are built with the typed
`any-store/v2/query` package; static pieces are built once.

## Phase 2 — the indexer (`internal/indexer`)

The consumer of the chunker feed: a background service started with the
account engine when `index.enabled` (default true), holding one
any-store database at `<data-dir>/index/index.db` (per account,
`02-server.md` § Data dir layout), and serving
`POST /v1/spaces/:spaceId/search` / `any search`.

**Observability**: long-running work is reported on the process view
(`GET /v1/processes`, `22-processes.md` § Internal producers) as
device-scope processes — `index.fts.<spaceId>` (advance pass, done =
changes, total unknown), `index.embed.<spaceId>` (vector drain,
done/total docs), `index.links_backfill.<spaceId>` (objects) and
`index.model_download` (bytes). The fts, embed and backfill processes
announce only past 3 s of elapsed work (`Options.AnnounceAfter`), so
routine per-edit indexing never appears (`Options.OnProcess`, bridged in
`internal/server/engine.go`).

### Store layout

- **One collection per space** (named by spaceId). Doc shape:
  `{id, scope, objectId, dataset, recordId, chunk?, data, title, hash,
  applySeq, vector?, pending?}` where `id` is `objectId:dataset:recordId`
  for a record's first chunk and that base + `U+001F` + `n` for chunk
  `n > 0` (`chunk` is stored only when non-zero). Every removal is a
  primary-key range: `objectId:` (object deleted), `objectId:dataset:`
  (an owner left the row), `[base, base+" ")` (record deleted — the base doc and
  every chunk suffix). Ids stay unique although recordIds repeat across
  objects (propIds do). The separator is a control byte, so every byte a
  real id can continue `base` with sorts at or above `0x20` and the
  record range is exact. A runtime dataset's id pattern is
  client-supplied, so the indexer enforces this: a record id or dataset
  name carrying a byte below `0x20` is skipped with a warning. Prefix
  ranges use bytewise bounds `[P, P[:len-1]+";")` (`;` = `:`+1) and
  drive the primary btree directly; per-doc deletion cleans FTS and
  vector entries in the same transaction.
- Indexes per collection: BM25 **full-text** on `data` (plus `title`
  when `index.search.titleWeight > 0`); a range index on `objectId` (the
  search filter's residual seeks it — § Filtering by object; structural
  deletes never use it, they are primary-key ranges; an existing
  collection backfills it in one write transaction on its first open
  after the upgrade, ~0.2 s per 100k docs, and every chunk upsert
  maintains it from then on); sparse range on `pending`
  (the embed queue); and — once at least one embedded doc exists — a
  **cosine vector index** on `vector`, created lazily
  (`Store.EnsureVectorIndex`) because IVF trains from existing docs. The
  strategy (`index.vector.mode`, `Store.vectorIndexParams`) defaults to
  **IVF-SQ**: cheap near-flat incremental ingest and physical deletes,
  ~3–4 recall@10 below exact (`search/README.md` § Index mode).
  Alternatives: `btree` / `hnsw` (recall ≈ exact, serial super-linear
  ingest, tombstone-rebuild deletes), `hybrid` (HNSW + RAM cache),
  `bruteforce` / `exact` (exact, O(N) per query). Existing indexes keep
  their mode until rebuilt.
- A `cursors` collection holds one `{id: spaceId, seq, gen, links}` row
  per space — `gen` is the SDK's per-space `Changes().Generation()`, the
  epoch the cursor belongs to; `links` the link-sink layout stamp.
  Cursor writes MERGE: advancing with no epoch in hand keeps the stamped
  one, because erasing it would silently disable rebuild detection. A
  `_meta` row pins the **schema version** (`indexSchemaVersion` in
  `store.go`), the **vector dimension** and the **chunk target**. A
  mismatch is refused with `ErrIndexRebuildRequired` naming the
  directory to remove — at open, or for the dimension when an embedder
  first answers with a different one. There is no migration: the index
  is derived state and rebuilds from cursor 0.

### Re-index triggers — per-space worker boot

Before the loops start, `alignIndex` checks that the persisted index
still describes the SDK store it was built from, and drops the space's
docs and restarts at cursor 0 when it does not:

- **`Generation()` changed** — the SDK store was rebuilt and the
  applySeq axis restarted at zero.
- **cursor > `MaxApplySeq()`** — an older `sdk.db` was restored under a
  cursor that ran ahead of it.

Either way the cursor names a position the feed will never report
again: `ChangedSince` returns nothing, with no error. A failed read
leaves the cursor alone (freezing the index over a transient error is
worse than the drift) and is re-checked on the next boot; the merge rule
above keeps the stamp on record meanwhile.

### Advance loop (FTS path) — per-space worker

The single operation is `advance`: page through
`Changes().ChangedSince(cursor, 256)`, and per page:

1. **Deleted ⇒ evict.** Every change with `Deleted: true` prefix-deletes
   `objectId:` and skips the chunkers. Collected page-wide before
   chunking, so a same-page content change can't upsert past the
   eviction.
2. For each live object, read the shared objects row once
   (`IncludeDeleted`; a tombstoned row catches a delete racing the
   read). Its members are read from it once (`index.Members`), and a
   definition object invalidates the chunkers' catalogs. Then per
   registered chunker: a `DynamicChunker` names the storage collections
   to prefix-evict; a static chunker whose `TypeId()` is not a member
   prefix-evicts `objectId:<dataset>:`; otherwise the chunker runs —
   `ReconcileAll` / `Reconcile` (hash diff) or `ChunksSince(cursor)`
   (`Data == ""` → delete, else upsert).
3. **One write transaction per page** — prefix deletes → record deletes
   → upserts → link ops — then persist the cursor (the page's max
   `ApplySeq`) and loop. A removal wins over an upsert of the same doc
   in one page: an object found tombstoned mid-collect evicts entries
   earlier chunkers already queued. Re-applying a page is idempotent.
   Text-bearing upserts land marked `pending` — **FTS is searchable
   immediately**, never waiting on the embedder — except `props`-scope
   docs and short `prop` docs, which are never marked.

Hot path: `Changes().Subscribe` does a non-blocking send into a cap-1
dirty channel (the callback runs on the SDK apply path); the worker
debounces 250 ms and runs `advance`. Lost signals are harmless — advance
is cursor-driven. A failed advance retries after 5 s. Space discovery:
`Spaces().List` at start plus `Spaces().Subscribe` — an added or updated
space with status active/unknown spawns a worker; a removed space, or
one updated to deleted, stops it and drops its index. A failed
`Spaces().Get` at spawn retries with backoff (2 s doubling, 1 min cap).

### Embed loop (vector path) — parallel, batched

A second per-space goroutine drains `pending` docs: `EmbedDocs` in
batches of `index.embedBatch` (default 64), `index.embedConcurrency`
batches in parallel (default 1; 4 for `openai` / `auto`) → `SetVectors`
(one write transaction, update-only — docs deleted meanwhile are
skipped) → `EnsureVectorIndex`. Advance nudges it after each page with
new text; a 1-minute ticker retries after embedder failures. A rewritten
record goes back to `pending`. `index.embedder: none` ⇒ the loop doesn't
run and the index is FTS-only.

Embedders (`indexer.Embedder`), selected by `index.embedder`:

- `auto` — **default**: an OpenAI-compatible primary with the `local`
  embedder as fallback. Both MUST serve the same model (one vector space,
  one dimension); the default pairing is Qwen3-Embedding-0.6B online and
  locally (`index.openai.*` names the primary, required). A circuit
  breaker skips the primary for 30 s after 3 consecutive failures. A
  query gives the primary half of the remaining query budget, so the
  fallback still has time to decode.
- `local` — llama.cpp in a child process (§ The embedder child process),
  no external service. yzma purego bindings (no CGO) load the prebuilt
  llama.cpp shared libs from `index.local.libDir` (else `$YZMA_LIB`,
  else `llamacpp/` next to the binary — populated by `make llamacpp`,
  which `make build` runs failure-tolerant; § GPU offload). Default
  model: **Qwen3-Embedding-0.6B Q8_0** (1024-dim, last-token pooling,
  L2-normalized; queries carry the Qwen retrieval instruction, docs embed
  bare). The GGUF (sha256-pinned) is downloaded into the shared model
  cache `<root>/models/` (a copy already in the account's `index/models/`
  is used in place); the download is resumable, logs progress, reports
  `index.model_download`, and never blocks boot — until it completes the
  embedder reports unavailable (outage semantics below).
  Air-gapped: set `index.local.modelPath` (no download). `index.local.dim`
  truncates output vectors (Matryoshka) to shrink the index. Compute
  threads default to `NumCPU()-1` (`index.local.threads`); past the
  physical core count can regress on hyperthreaded CPUs. Loaded cost ≈
  640 MB mmap + ~200 MB context; nothing loads before the first embed
  call. Linux needs a system `libffi.so.8` (NixOS: `nix develop`).
- `ollama` — local `/api/embed`, default `embeddinggemma`, doc/query
  task prompts.
- `openai` — any OpenAI-compatible `/embeddings` API (`index.openai.model`
  required).

**An unavailable embedder never breaks the pipeline.** There is no
boot-time probe: whenever an embedder is configured, text-bearing docs
are marked `pending` regardless of its reachability, so an outage — at
boot or mid-run — only freezes the vector side while FTS indexes and
answers normally. When the embedder comes back, the next embed round
drains the queue. The vector dimension is learned from the first
successful batch (or pinned via `index.vector.dim`) and persisted in
`_meta`, so a later model or dimension change against a populated index
is refused (`ErrIndexRebuildRequired`), not silent corruption. A batch
that fails midway lands the vectors embedded so far.

**Batching costs context.** The local child sends a batch one decode
group per frame — up to `index.local.batchDocs` docs (**default 1**) per
`llama_decode`. The unified KV cache partitions `contextSize` across the
packed sequences, so `batchDocs: N` caps each text at `contextSize/N`
tokens (rounded down to a 256-token block, at least 256). Texts are
truncated to that bound, so a wider batch never fails a decode — it
embeds less of each text; the server logs a warning when the bound falls
below `contextSize`. On a mixed record stream width buys nothing once
the bound is held equal (GTX 1080 / Vulkan: 10.1 docs/s at
`batchDocs: 1` vs 9.2 at 4; 32-core CPU: 2.16 vs 2.14). Batched and
single decodes produce identical vectors
(`TestLocal_BatchedMatchesSingle`).

### The embedder child process

The `local` embedder does not decode in the server process. It re-execs
this binary as the hidden `any run embedder` and talks to it over
stdin/stdout with a magic-prefixed, length-delimited frame protocol
(JSON header + binary block; vectors as little-endian float32). One
child, spawned on the first embed call and shared by every space worker.
The stream carries one request at a time, shared through a one-slot
semaphore with two classes: `EmbedDocs` sends one decode group per frame
and re-takes the slot for every frame, and a search query takes the
slot ahead of any waiting doc frame. After 8 consecutive query turns a
doc frame runs anyway, so a saturating query stream still leaves
indexing a frame per burst. Once the child is up, a query waits for at
most the decode in flight (~1–2 s worst case for a 2048-token doc on
CPU), never for a 64-doc batch. A cold spawn or a wedged child holds the
slot longer; the query budget in § Search covers that. Acquisition is
context-aware: a caller that gives up leaves the queue.

**Why a child.** llama.cpp faults are not recoverable in Go: a Vulkan
device-lost throws a C++ exception through a purego frame with no
handler (`std::terminate`), and `GGML_ASSERT` calls `abort()`. In the
child they kill only the child: the round fails, its docs stay
`pending`, and the next tick retries.

**The child only embeds.** It gets a model path and decode parameters
and answers with vectors. It never opens the index db, the data dir or
any-store; the cursor, `pending` marking, `SetVectors`, the vector index,
model discovery and the download all stay in the server.

**Failure handling.** A dead child, a desynchronized stream, or a frame
that outlives `index.local.requestTimeout` (default 3 min) kills the
child and fails the round; the next round respawns behind an exponential
backoff (1 s → 1 min). The timeout exists because a wedged GPU stops
answering rather than failing; it counts as a fault and demotes the GPU
(below). An error *frame* is not a fault — the child stays. The child's
stderr is logged, and its tail is quoted when it dies. A caller that
goes away is not a fault either: a request already on the wire is read
to completion so the stream stays usable, the child is kept, and the
next caller finds it ready.

**Priority.** The child runs below the server: `index.local.niceness`
(default 10, 0 disables) nices it on Unix and lowers its priority class
on Windows, applied before llama.cpp loads so its decode threads
inherit it.

**Library search path.** On Windows the child adds the lib dir to the
DLL search path before loading llama.cpp: Windows resolves a DLL's own
imports (`ggml.dll` → `ggml-base.dll` → `libomp.dll`) by module name and
never looks in the importing DLL's folder. Linux and macOS libs find
their siblings through their rpath.

**Hardware.** The child reports what llama.cpp initialized on — OS/arch,
registered backends and the shared object each came from, devices (GPU
name and driver), the pinned llama.cpp release, CPU count and thread
budget, `llama_print_system_info()` — in its `ready` frame. The server
logs it on every spawn ("local embedder child started", CPU features at
DEBUG) and keeps the last report behind `Indexer.EmbedHardware()`.

**Threads.** `index.local.threads` is the child's CPU budget — lower it
to keep background indexing off the user's cores. `Indexer.SetEmbedThreads`
changes it at runtime (the next spawn uses it; an idle child is retired);
no endpoint exposes it.

### GPU offload (local embedder)

The shipped llama.cpp bundles are **GPU-capable with automatic CPU
fallback**: macOS arm64 carries Metal; Linux and Windows carry
**Vulkan** (NVIDIA / AMD / Intel) alongside every `libggml-cpu-*`
variant. A backend whose driver or loader is missing doesn't register,
and inference lands on the best CPU variant — no config, no error.
llama.cpp offloads all layers when a usable GPU exists (full offload of
the default model costs ~2 GB, dominated by compute buffers that scale
with `contextSize`). `index.local.gpuLayers: 0` forces CPU-only
inference: it also turns llama.cpp's op offload off, since `n_gpu_layers`
only places the weights and a registered GPU would otherwise still run
the matmuls. Measured on a GTX 1080 (478 editor windows, ~330 tokens
each, `batchDocs: 16`): CPU ≈ 2.0 docs/s at ~14 cores vs Vulkan ≈ 8.5 docs/s with the CPU
idle. An integrated GPU can be slower than the CPU (§ Tuning).

**A GPU that dies mid-run does not take the server down.** It kills the
child, and the server demotes itself to CPU for the rest of the process:
every later spawn passes `--gpu-layers 0`, one WARN names the reason and
quotes the child's stderr, and the affected docs re-embed on CPU from
the `pending` queue. The demotion is in-memory — the next start tries
the GPU again. On a machine whose GPU reliably hangs under embedding
load (an integrated GPU that also drives the display is the known case),
set `index.local.gpuLayers: 0`.

CUDA and ROCm builds are not bundled; point `index.local.libDir` at a
custom llama.cpp build to use them.

### Build tags — `fts` and `vector` (selecting the legs at compile time)

The two search legs are selected at build time by positive build tags:

| Build | Tags | FTS | Vector / embeds |
|---|---|---|---|
| Desktop / server (`make build`) | `fts vector` | on | on |
| Darwin `-sandbox` tarball | `fts vector ffi_no_embed` | on | on (incl. `local`) |
| FTS-only | `fts` | on | off |
| Vector-only | `vector` | off | on |
| Plain `go build` | *(none)* | off | off |
| Android bind (`makefiles/android.mk`) | `gomobile fts` | on | **off (forced)** |
| iOS c-archive (`scripts/build-xcframework.sh`) | `mobile fts` | on | **off (forced)** |

- **The vector leg is always off on mobile.** `capVector` is
  `vector && !gomobile && !mobile`. On Android the embedder
  implementations are not linked at all (`NewEmbedder` has a no-op
  variant under `!vector || gomobile`): the `local` embedder's libffi
  bindings resolve `ffi_prep_cif` at package load, which Android doesn't
  provide, so a runtime toggle could not prevent the crash. No embedder
  is constructed and no model is downloaded on either platform.
- **`fts` is a plain opt-in**, available everywhere including mobile.
- **`ffi_no_embed` is a packaging flag.** It compiles nothing out: the
  `local` embedder, the ANN index and every mode keep working. It makes
  jupiterrider/ffi use the system `/usr/lib/libffi.dylib` instead of a
  copy extracted into the user Caches dir, which macOS library
  validation refuses to load. Mechanism and consumer contract:
  `18-ci.md` § The darwin `-sandbox` variants.

Mechanics (`internal/indexer`): `capFTS` (`fts`) and `capVector` are
build-tagged constants (`caps_fts_*.go`, `caps_vector_*.go`). When a cap
is false the store creates no index for that leg (`spaceColl`) and the
leg's search short-circuits to no hits; `capVector` false also stops
docs being marked `pending`, so the embed loop never runs.
`NewEmbedder` has the real switch in `embed_factory_vector.go` and the
no-op in `embed_factory_novector.go`.

Tags decide what is compiled; `index.enabled` / `index.embedder` decide
what runs on top (there is no per-leg runtime flag). A server built with
neither leg logs a warning at startup (`indexer.CompiledCaps`) — its
`/search` returns no hits. A request carrying `require` / `exclude` on a
build without the `fts` leg is refused (`409 index.terms_unsupported`):
the terms could not be enforced. Tests covering a leg carry its tags
(`make test` runs the `fts vector` suite).

On the embedded path (`internal/embedded.Start`) the compiled `fts` cap
is the whole gate, and `index.embedder` is forced to `"none"`.

A host that can't open its index gets a distinguishable failure:
`ErrIndexRebuildRequired` (schema version, vector dimension or chunk
target mismatch, `internal/indexer/errors.go`) reaches the iOS shim as
start code `4`, so the app can offer "reset local data". The index is a
derived cache, so deleting it is always the whole fix.

### Search

`POST /v1/spaces/:spaceId/search` `{query, scopes?, limit?, mode?,
require?, exclude?, maxData?, passages?, filter?}` →
`{hits: [{scope, objectId, dataset, recordId, chunk?, data, dataOffset?,
dataTotal, score, passages?}], mode, vectorStatus, truncated?}`.

Modes: `fts` (BM25), `vector` (cosine ANN; requires an embedder; hits
with similarity ≤ 0 — or ≤ `minVectorSim` — are dropped), `hybrid`
(default — both legs fused by reciprocal rank, k = 60; degrades to `fts`
when no embedder is configured or the query embedding fails or exceeds
its budget — `mode` in the reply is the mode that ran). The query
embedding is bounded by `index.search.queryEmbedTimeout` (default 5 s,
every embedder): a cold model load, a wedged child or a slow API
degrades the search instead of holding it, and a caller that disconnects
leaves the embedder's queue at once. Scores are comparable only within
one response. `limit` defaults to 10 and is clamped to 100. CLI:
`any search <spaceId> <query> [--scopes a,b] [--limit N] [--mode …]
[--require T …] [--exclude T …] [--max-data N] [--passages N]`.

**`limit` counts records.** A hit is one `(objectId, dataset,
recordId)`, shown through its best-ranked chunk; the record's other
chunks that ranked within the search window come back as `passages`
(`passages: N`, max 10, best first, same window fields). Each leg reads
at least `fetch = clamp(3·limit, 30, 100)` chunks and continues until it
covers enough distinct records — `2·limit` for the lexical leg, `limit`
for the vector leg — capped at 1000 chunks (`maxLegFetch`). The lexical
leg is one any-store cursor opened without `Limit` and pulled to that
rule (`Store.openFTS` / `Indexer.ftsLeg`): any-store ranks every match
before the first row whatever the `Limit` and materializes only what is
pulled, so the deeper read costs ~1 µs per row and an early `Close` is
free (§ Tuning). The vector leg has no cursor — `$knn` computes its `K`
nearest up front — so it re-queries with `K` ×4 until covered, or until
the index returns fewer than `K` candidates: an IVF search reaches only
the probed cells (~4√N docs at nprobe 16). Under a scope filter
any-store sizes its candidate beam from `K`, so a short round ends the
leg only once a wider `K` stopped adding rows (`vectorStop`); rows
dropped by the similarity floor end it at once. Fusion is per chunk
(`fuseRRF`, keyed by chunk doc id, so a long record never gains rank
mass from chunk count), then `groupHits` collapses chunks into records
scored by their best chunk — max, never sum. Two consequences: the fused
order is reciprocal rank over bounded windows (a record deep in both
legs can outrank one shallow in one leg), and when one record dominates
a whole window the reply can hold fewer than `limit` records although
the index has more. The reply is bounded by `limit × (1 + passages) ×
maxData` runes of text (the chunk size, ~2000 runes, in place of
`maxData` when it is -1). Work order in `Indexer.Search`: query
embedding, then the vector leg, then the lexical cursor — no read
transaction is held across the embed wait or another store call (an open
cursor pins a reader slot, a page cache and a WAL read-mark until
`Close`).

**Hit `data` is a window, not the record.** Each hit's `data` is at most
`maxData` runes (default 512; `-1` = the whole chunk text), placed around
the most selective query / `require` term that occurs in the chunk (terms
tried longest first, stop words excluded, token-boundary matches
preferred) — the head when none occurs (a vector-only hit) — and snapped
to word boundaries. `dataOffset` is the window's rune offset into the
chunk's indexed text and `dataTotal` that text's rune length, so a
client can tell a clipped preview from the full text. `chunk` (omitted
when 0) names the chunk (§ Chunking long records). The full record is
one dataset query away.

**FTS query operators.** `query` understands `"quoted phrases"` (matched
by adjacency) and trailing-`*` prefixes (`zepp*`). `require` / `exclude`
are arrays of must / must-not terms; each may itself be a phrase or
prefix. A bare term alongside a `require` is an optional boost, not a
filter. Phrases and prefixes in `query` shape the FTS leg only;
`require` / `exclude` bind every hit in every mode: the vector leg is
post-filtered against the FTS index (`Store.FilterTerms`, one `$text`
query restricted to the leg's hit ids) before fusion — the contract is
"must contain / must not contain", not "the lexical leg agreed".
Stop-word stripping is skipped when `query` contains a `"`, so phrases
survive intact.

**Ranking knobs (`index.search.*`, `05-config.md`).** Measured in
`search/README.md`:

- **Stop-word stripping** (`stopWords`, default **on**) — a small English
  stop list (`stopwords.go`) is removed from the FTS-leg query only; the
  vector leg gets the full query. An all-stop-words query is left
  unchanged.
- **Weighted RRF** (`ftsWeight` / `vectorWeight`, default 1 / 1) — scales
  each leg's fusion contribution.
- **Adaptive leg weighting** (`adaptiveWeights`, default **off**) —
  down-weights the FTS leg per query by its score concentration, so a
  flat BM25 distribution (paraphrastic queries) can't drag hybrid below
  the dense leg. FTS only — cosine is uncalibrated. Wins on weak-lexical
  corpora, small cost on lexical-friendly ones (`search/README.md`
  § Per-corpus leg weighting).
- **Default operator** (`defaultOperator`, default `or`) — `and` makes
  bare FTS terms all-required. AND over natural-language queries
  collapses recall (BEIR nDCG@10: SciFact 0.66 → 0.02, FiQA 0.23 → 0.03);
  use it only for short keyword input the client controls. Phrase /
  `require` / `exclude` give precision without that cliff.
- **Vector similarity floor** (`minVectorSim`, default 0 = similarity
  must be > 0) — drops vector hits at or below the cutoff before fusion.
  For the default local model a static floor filters signal along with
  noise: gibberish queries score ~0.6 cosine, on par with on-topic
  queries (`live_probe_test.go`). Keep 0 for that model.
- **BM25 parameters** (`bm25B`, `bm25K1`, `titleWeight`, default 0 =
  engine defaults, no title field). Set at index creation: changing `b` /
  `k1`, or `titleWeight` from 0 to non-zero, needs a rebuild; a change
  between non-zero title weights applies at query time.

`vectorStatus` (`used` / `unavailable` / `disabled` / `skipped`) tells
the consumer whether semantic recall took part and why not —
`unavailable` (the configured embedder did not answer in time; a retry
may differ) versus `disabled` (this server never runs vector search).
Value table: `03-api.md` § POST /v1/spaces/:spaceId/search.

The endpoint is consumer-side: no SDK method sits behind it.

Errors:

| code | status | when |
|---|---|---|
| `request.missing_field` | 400 | no `query` |
| `request.bad` | 400 | `limit` < 0 |
| `request.invalid_field` | 400 | `maxData` < -1, `passages` outside 0..10 |
| `search.bad_mode` / `search.bad_scope` | 400 | unknown mode / scope not a slug |
| `filter.invalid` / `filter.unknown_operator` | 400 | a bad `filter` — the `/objects/query` grammar's own codes |
| `index.no_embedder` | 400 | `mode=vector` with no embedder configured |
| `index.disabled` | 409 | `index.enabled: false` |
| `index.terms_unsupported` | 409 | `require` / `exclude` on a build without the `fts` leg |
| `index.embedder_unavailable` | 503 | `mode=vector` while the embedder is unreachable or over the query budget — retryable; hybrid degrades instead |

### Filtering by object

`filter` is a condition over the hit's HOST OBJECT row — the per-space
`objects` collection, in the `/objects/query` grammar verbatim
(`any.type`, `any.collections`, `<ownerId>.<propId>`, `modifiedAt`, `id`, …). A hit is kept
only if its object's row matches, in every mode, like `require` /
`exclude`; `limit` still counts matching records; the row is read live,
so a property write (a bin move) is honored by the next search without
a re-index. An object with no row never matches. Record fields of the
hit's own dataset (a chat message's `creator`, a block's `type`) are not
filterable — a row per hit is one read, a record per hit is another.

The rows live in the SDK's store, the hits in the index store, and
neither side can be estimated cheaply from the other — the store's
`$text` planner reads per-term document frequencies only once a bounded
residual makes a probe plan possible, and an unindexed object predicate
costs a scan to count. So the filter is probed on the objects side and
the request takes one of two paths (`internal/indexer/host_filter.go`):

1. **Probe.** Iterate the filter with early exit after `filterIdsMax`
   (256) ids, collecting them — an unbounded iterator closed early, not
   a `Limit`, because the SDK applies a limit before it skips the
   collection's tombstones. An equality or range
   on an indexed field (`any.type`, `any.collections`, `modifiedAt`,
   `id`) exits in microseconds; a dense predicate the index cannot bound
   (`any.collections $nin [bin]` — the everyday shape) exits after the
   first few hundred rows; only a narrow unindexed predicate (a property
   value held by a few objects) scans the collection, ~4 ms per 6k
   objects, which is what resolving its ids costs anyway.
2. **Small set — residual, probe forced on the vector leg.** A set the
   probe resolved whole rides both legs as `objectId $in ids`. On the
   lexical leg any-store's cost-based `$text` planner chooses per
   query between the driver plan (walk the posting lists, residual
   after the fetch) and the probe plan (seek the `objectId` index,
   verify each candidate against the text index by point-gets —
   order-identical); measured to fire up to ~100 objects, where it
   turns a 20–50 ms scan that came back short into a complete page in
   0.1–2 ms, and to cost a tie up to ~400, where the planner keeps the
   driver. 256 sits in that band: every set below it gets a complete
   page for at most the price of the unfiltered leg, and the store
   drains until `limit` is met. On the vector leg the same set FORCES
   the probe (`IndexHint` on the `objectId` index): the ANN beam is
   blind to a few vectors among many — 103 objects' ~145 vectors among
   66k returned nothing at every K — while probing them costs one
   distance per doc; the store's own cost model only picks the probe
   for a handful of docs. The residual path's one cost is that it has
   no read budget: a residual anti-correlated with a broad query drains
   the whole posting list before the first row, as an unfiltered search
   of that query would. A set the probe found EMPTY answers at once —
   no query embedding, no leg.
3. **Large set.** The vector leg first resolves a lazy set up to
   `filterResidualMax` (9 999 — the `$in` size any-store still derives
   index bounds from) and rides it as a residual: every widening round
   is a full ANN pass (~70 ms at 66k vectors), so one round with the
   residual (any-store widens its candidate beam inside it) beats
   re-running the ANN per round through lookups — measured 80 vs
   250–290 ms per hybrid request. The lexical leg then takes the same
   residual; fts-only, where no vector leg resolves, it stays lazy
   (the residual costs the resolve, ~8 ms per 6k objects, where the
   lookups cost ~0.1 ms). A set past the residual bound post-filters
   on both legs: rows are judged through one primary-key `$in` query
   on the objects collection per batch — `filterBatch` (64) rows for
   the lexical cursor, a widening round for the vector leg — with
   verdicts cached per object for the request and shared by both legs.
   A lexical page still short after `filterScanRows` (5000) rows means
   the filter is anti-correlated with the ranking (the matching objects
   sit deep — a binned import searched for its own content): the set is
   materialized once, up to `filterMaterializeMax` (50 000) ids, and the
   same cursor continues with in-process membership; a set larger than
   that stays lazy. Past `filterScanRowsMax` (100 000) rows — or the
   vector leg's K ceiling — a page still short of `limit` matching
   records carries `truncated: true`. The lexical cursor is a read
   transaction on `index.db` held across those lookups (a different
   store, so no reader slot is shared and nothing can deadlock); its
   hold grows from ~1 ms to the lookups' sum, at most a few hundred
   milliseconds, and the store's reader slots are per process, so
   that many concurrent filtered searches queue behind each other.

### Tuning (measured — `internal/indexer/bench_test.go`)

Defaults in `indexer.Options`, picked from file-backed benchmarks:

| Dial | Default | Why |
|---|---|---|
| `BatchLimit` (ChangedSince page = write tx) | 256 | FTS insert throughput plateaus at 256 docs/tx (~65k docs/s vs ~52k at 16); one page ≈ 4 ms. Bigger pages add latency, not throughput. |
| `EmbedBatch` (texts per EmbedDocs call) | 64 | Ollama embeddinggemma: 54 / 72 / 76.5 / 78.6 texts/s at 8 / 32 / 64 / 128 — ≥97% of max at 64, half the per-call latency of 128. The embedder is the bottleneck by ~3 orders of magnitude (vs ~30k vecs/s IVF-SQ insert), which is why embedding lives off the advance path. |
| `Debounce` (dirty → advance) | 250 ms | A no-op advance is sub-ms; the dial only coalesces write bursts into one page. |
| `RetryBackoff` / `PendingEvery` | 5 s / 1 min | Failure paths: advance retry, embed catch-up tick. |

Search at 10k docs (dim 768): FTS ≈ 1.9 ms, vector ≈ 1.0 ms per query.

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

A `Limit` buys nothing on the lexical leg — BM25 accumulation and the
full sort are paid before the first row either way (the same 7.5 MB/op
at 100k) — and each extra row pulled costs ~1 µs over the first thousand,
~2 µs deep into a drain. The vector leg's reach is the probed IVF cells
(K=1000 returns 600 rows at 10k docs); an explicit `ef` of 10000 changes
nothing, and an early `Close` saves only the per-row fetch. An open
iterator pins ~0.7 MiB (`$text`) / 0.2 MiB (`$knn`) and releases it all
on `Close` (`TestIteratorEarlyCloseNoLeak`). End to end on the same
corpus plus five 17-chunk records and one short record sharing a rare
term, `limit 10` answers the six matching records in 0.19 ms (fts) and
ten records in 0.42 ms (hybrid, 10k) / 0.51 ms (100k). Timings drift 2–3×
on a busy machine; re-measure idle with
`ANY_CUTOFF_BENCH_SIZES=10000,100000 go test -tags 'fts vector' -run '^$'
-bench BenchmarkCutoff -benchmem -benchtime 100x -count 3
./internal/indexer`.

Query embedding while the local child is saturated (three workers
looping 2000-rune frames — `TestWorkerEmbedder_RealChild_QueryLatency`,
Qwen3-Embedding-0.6B Q8, 20 jittered queries): the wait is half a doc
decode on average, never more than one.

| Hardware / mode | doc decode under load | query p50 / p90 / max |
|---|---|---|
| Ryzen 9 9950X, CPU 16 threads | 297 ms (3.3 frames/s) | 214 / 318 / 352 ms |
| Ryzen 9 9950X, iGPU (RADV, Vulkan) | 755 ms (0.9/s) — slower than its CPU | 710 / 825 / 836 ms |
| Ryzen 9 3900X, CPU 23 threads (default) | 383 ms (1.2/s) | 345 / 496 / 600 ms |
| Ryzen 9 3900X, CPU 12 threads | 470 ms (1.1/s) | 479 / 720 / 753 ms |
| GeForce GTX 1080, Vulkan | 181 ms (5.6/s) | 107 / 186 / 199 ms |

End to end on the 9950X CPU (server + 360-chunk backlog draining, 110
hybrid `/search` calls over HTTP): p50 212 ms, p90 306 ms, max 446 ms,
every reply `mode=hybrid` / `vectorStatus=used`. Re-measure the
`Options` dials with `go test -tags 'fts vector' ./internal/indexer
-bench . -benchtime 30x` (`ANY_BENCH_OLLAMA=1` adds the real-embedder
run).

### Known limits

- Removing `<data-dir>/index/` rebuilds every space from cursor 0 — FTS
  catches up quickly, every text doc re-embeds.
- Embedder latency only delays the vector leg: fresh writes are
  FTS-searchable immediately and gain vector recall once embedded. At
  query time the local child serves a search ahead of doc frames (wait ≤
  one decode) and the embedding is capped
  (`index.search.queryEmbedTimeout`, default 5 s), past which hybrid
  answers lexical-only.
- **Embedder input is clamped** per sequence to
  `index.local.contextSize / index.local.batchDocs` tokens (default
  2048 / 1 = 2048, EOS preserved for last-token pooling). The 2000-rune
  chunk target keeps prose inside it; a chunk of dense CJK or code can
  exceed the clamp and embed head-only — FTS covers its full text.
- **`require` / `exclude` bind the hit, i.e. the chunk**: a term that
  appears only in another chunk of the same record does not satisfy a
  `require` for this one, and a passage is always a chunk that satisfied
  them itself.
- **Passages are the window's, not the record's.** `passages` lists a
  record's other chunks that ranked within the legs' windows (≤ 1000
  chunks deep); a record whose chunks all match shows only the ones the
  legs reached.
- **`filter` sees the object, not the record.** It binds hits to the
  host object's row (§ Filtering by object); a dataset record's own
  fields are out of reach, and a narrow filter on an unindexed
  property costs a scan of the objects collection per search until
  the SDK indexes property values. The vector leg under a filter past
  the residual bound is bounded by the ANN's reach like an unfiltered
  one, and a set whose docs have no vectors yet returns nothing on that
  leg whatever its size.

## Tests

- `internal/index/stream_test.go` — `RecordsSince` chains the
  IncludeDeleted projection + `_applySeq` window + sort, parses the seq,
  stops and closes on a yield error; `IsDeleted`.
- `internal/index/prop_test.go`, `internal/index/schema_test.go` — the
  prop and schema chunkers.
- `internal/indexer/host_filter_test.go` — the search filter's paths:
  residual, batched post-filter with one lookup per object, the
  materialize-and-continue rescue, truncation, hybrid sharing one set,
  the empty set; `internal/indexer/store_test.go::TestStore_ObjectIdResidual`
  — the `objectId` index and a residual on the lexical cursor;
  `internal/server/handlers_search_test.go::TestSearch_Filter` — over
  HTTP in every mode, bin in / out, a tombstoned objects row inside the
  probe window, the 400s.
- `internal/editor/chunker_test.go`, `internal/chat/chunker_test.go` —
  text extraction including the tombstone case.
- `anyuri/links_test.go`, `internal/index/links_test.go`,
  `internal/editor/links_test.go`, `internal/chat/links_test.go` — the
  link scanner, canonical targets, marker resolution, the card / embed
  rules, chat attachments; `internal/indexer/links_store_test.go` — the
  link sink (replace / rewrite / eviction / backfill stamp), untagged;
  `internal/server/handlers_links_test.go` — the index end to end over
  HTTP; `internal/e2e/multipeer_links_test.go` — a joiner's index sees
  the owner's block link and its deletion.
- `internal/server/handlers_index_test.go::TestIndexChunkers_FullFlow` —
  the chunkers in-process against a live SDK: creation, cursor advance,
  tombstones.
- `internal/server/handlers_indexer_test.go` — the indexer end to end:
  cold sync, tail catch-up, owner-loss and object-delete eviction,
  editor coalescing and embed reuse, embedder outage, realtime updates,
  type definitions excluded, default-on props;
  `handlers_index_schema_test.go` — the schema chunker.
- `internal/indexer` unit tests — store round-trips (FTS + vector +
  pending lifecycle + purge/drop, in-memory any-store), chunking
  (`chunk_test.go`), RRF fusion and record grouping (`rrf_test.go`),
  snippets, generation re-index, spawn retry, the HTTP embedder clients
  against `httptest` servers, the `auto` fallback breaker.
- `internal/indexer/search_terms_test.go` — `require` / `exclude` in
  every mode, chunk windows + `maxData`, and
  `TestIndexer_SearchLimitCountsRecords`: a 17-chunk record whose every
  chunk outranks a short exact match, `limit 10` in fts / hybrid /
  vector → both records, passages on the long one.
- `internal/indexer/cutoff_leak_test.go` — an any-store `$text` (with and
  without `Limit`) and `$knn` iterator closed after a few rows, 500
  times: no goroutine, no retained heap, reader slots released, a write
  and the DB close go through afterwards.
- `internal/indexer/embed_local_test.go` — local embedder factory and
  pre-ready errors (no libs/model needed), token truncation (EOS
  preservation), `l2Normalize`, Matryoshka dim; plus a gated integration
  test (`ANY_TEST_LOCAL_EMBEDDER=1` + `ANY_INDEX_LOCAL_MODEL_PATH`)
  running the real model: dims, unit norms, relevance ordering,
  truncation, concurrency under `-race`.
- `internal/indexer/embed_local_download_test.go` — the download manager
  against `httptest`: happy path, sha256 mismatch, Range resume,
  progress strings.
- `internal/indexer/embed_worker_test.go` — the child supervisor against
  a helper process (the test binary re-exec'd, speaking the frame
  protocol in place of a model): per-frame batching, a query jumping the
  doc queue, abandoned callers keeping the child, crash → CPU demotion,
  request timeout, spawn backoff, `SetThreads` idle/busy, `Close`
  mid-request and mid-spawn; plus a gated real-child comparison against
  the in-process model.
- `internal/indexer/search_budget_test.go` — the query-embedding budget
  in `Search`: hybrid degrades to fts / `unavailable`, vector fails as
  embedder-unavailable, a cancelled caller gets its own cancellation.
- `internal/server/handlers_search_test.go` — in-process SDK + in-memory
  store + deterministic fake embedder: all three modes end to end, scope
  filtering, deletion purge, degraded / disabled errors, and the
  asynchronous worker path (`Start` + poll).
