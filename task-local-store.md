# Task: Local store — non-CRDT any-store collections in `sdk.db`

Device-local, never-synced collections with the full any-store query /
modifier / index / aggregation surface, exposed over HTTP under
`/v1/local/…`. Not a dataset, not a version domain, no DAG, no handler —
a plain document store that shares the SDK's any-store DB and speaks
the same query language as the synced data.

Consumer-side feature in the `any` server (same category as `/search`
and `/v1/events`). The SDK's only involvement is handing out its DB
handle (prerequisite #1); nothing here goes through `Space.Modify` /
`Query`, and nothing here is visible to peers.

**Decision:** local collections live in the SDK's `sdk.db`, fenced by a
name tag (`l_`), not in a separate file.

Citations are `path:line` in `any-sync-sdk@v0.2.6` / `any-store/v2@v2.0.1`
(module cache) unless marked any-side.

---

## Motivation

### Why local collections at all

Agents and clients keep accumulating state that must NOT replicate:
scratch/working sets during a run, ingest staging before an `/upsert`,
per-device caches derived from synced data, telemetry, tool results
awaiting review. Today that state either leaks into synced datasets
(polluting every member's replica with device noise and burning DAG
changes for throwaway writes) or lives outside any-store entirely (a
JSON file, sqlite, in-memory maps) and loses filters, sort, indexes and
`$group` pipelines — the exact tools the rest of the data already has.

### Why the same DB

Two capabilities are only possible when local and synced collections
share one any-store file, and both are the point of having a *store*
rather than a scratch file:

- **Cross-collection `$lookup`** — join a local collection against
  `<spaceId>_objects` (or any dataset collection) inside one pipeline.
  any-store's `$lookup` runs on a DB-wide `btree.ReadTx`
  (`aggregate.go:319`, `lookupBtreeTx`): one snapshot across both
  sides. A cross-file join has no shared snapshot at all.
- **`$out` / `$merge` rollups** — materialize a synced→local summary
  (a per-space report, a denormalized index for an agent) with one
  aggregate call.

Both are **gated upstream today** and not promised for v1 — see
prerequisites #2 and #3. v1 ships local↔local `$out`/`$merge` and
`$lookup` within the local tag only.

The arguments for a separate file, each resolved:

| separate-file argument | resolution |
|---|---|
| `rm sdk.db` is a recovery step; local data would die with it | It is no longer a free recovery step — `sdk.db` now holds non-rebuildable rows. Documented as a cost (docs #20/#21). The rebuild contract is the SDK's (`internal/crdt/meta.go:51`, `internal/spaceobjects/reindex.go:36`), and it re-replays CRDT collections only; tagged collections are untouched by a re-index, so the SDK-driven rebuilds (generation bump, handler version) are safe. Only a manual file wipe loses local data. |
| the orphan sweep / offload could drop or miss it | The sweep's owner resolution splits at the first `_` and classifies the prefix; `l` fails `validateSpaceId` (no `.`) and `cid.Decode` ("cid too short") → `ownerNone`, documented as unswept present and future (`internal/spaceimpl/gc.go:290`). Offload only drops `<spaceId>_*` (`internal/spaceimpl/offload.go:183`). Verified empirically against v0.2.6. |
| single-writer contention | Real, and handled by chunking every write ≤256 docs per tx (§ Wire, #12) — the SDK's own `offloadDropChunk` precedent (`offload.go:224`: *"any-store has a single global writer, so one tx for the whole sweep stalls every local write"*). |
| listing is structural in a separate file | A prefix filter over `GetCollectionNames` is exact — the tag is fixed-width and never emitted by the SDK (asserted by test, #15). |
| independent lifetime / wipe / backup | Given up. Local data is not independently deletable and has no backup story; stated in docs/26 "what this is NOT". |
| layering: `sdk/` is the SDK's | The SDK exposes the handle; the `l_` namespace is `any`'s by contract, exactly as `index/` is. |

### Prohibition: no transaction ever spans a local and an SDK collection

One DB makes a `WriteTx` covering `l_s_<spaceId>_x` and
`<spaceId>_objects` *possible*. `any` never opens one, and never writes
an SDK collection at all: a direct write bypasses the DAG and is
silently reverted by the next re-index
(`internal/spaceobjects/reindex.go`). The `ParseRef` fence (#15) is what
makes this structural rather than a convention — every collection name
that reaches any-store through `localstore` carries the tag.

### Relation to SYN-174 (scoped datasets)

Different thing. SYN-174 is a *dataset* whose records are private but
still declared on a type, subscribe-able, schema-checked. This is a raw
store with none of that machinery and no subscribe. Not designed here.

---

## Model

- **DB:** the SDK's `<account-dir>/sdk/sdk.db`, handle obtained from the
  SDK (prerequisite #1). The SDK owns open and close; `localstore` holds
  the handle for the engine's lifetime and has no lifecycle of its own.
- **Naming.** Fixed-segment tagged names:
  - account scope: `l_a_<name>`
  - space scope: `l_s_<spaceId>_<name>`
  - `name`: `^[a-z0-9][a-z0-9_-]{0,63}$` (`_` legal inside — the fixed
    third segment is what makes the split unambiguous, so `ParseRef`
    needs no cid/base36 shape test).
  - Storage-name parsing is a fixed 3-segment split (`l`, `a|s`, rest;
    for `s` the spaceId is the next segment, the remainder is the name).
  - Not `_<name>`: the SDK owns leading `_` (`_meta`, `_detached`,
    `_history_traces`, `_history_meta`, `_read_state`, `_read_unread`).
    Not `<spaceId>_<name>`: that is the CRDT shape, claimed by offload
    and the orphan GC.
- **Collection** = one any-store collection, addressed on the wire by
  `{scope, spaceId?, name}`; the storage name never appears on the wire.
- **Document** = arbitrary JSON object with a string `id` (client-supplied,
  or server-minted `xid` if absent on insert). No `_ver`, no `_addSeq`,
  no tombstones — a delete is a delete.
- **Modelling rule (write into docs/26):** key local collections by the
  synced record id. Both future features depend on it — `$lookup`'s
  `foreignField: "id"` is a primary-key point read, and `$merge` requires
  every result doc to carry `id` with the target's pk being `id`.
  Establish the discipline in v1, not retrofitted.
- **No schema.** Free-form. Validation is the caller's job.
- **Indexes:** any-store `IndexInfo` verbatim (`fields`, `unique`,
  `sparse`) via ensure/drop. FTS/vector are OUT.
- **No subscribe.** any-store has no pub/sub and there is no apply path
  to hook. Poll or re-query.
- **Not search-indexed.** The indexer streams `Changes()`; nothing here
  has one.
- **Space-scoped cleanup is the client's job (v1).** No `DropSpace` hook,
  no boot sweep. Consequence, stated in docs: **a space-scoped
  collection outlives its space.** The sharp case is 1-1 spaces — delete
  is local-only and the id is re-derivable from both account keys, so
  re-deriving the same 1-1 inherits the stale rows (derived spaces can't
  hit this; they're undeletable). Clients version or namespace
  collection names if they care.
- **No registry (v1).** The spaceId is in the name, so a prefix scan
  answers "what belongs to this space" exactly. A registry earns its
  keep only once it carries createdAt / TTL / last-access — add it with
  expiration, not before.
- **Limits:** request body under the global `BodyLimit`; `insert`/`upsert`
  ≤ 1000 docs per request, written in **≤256-doc write txs**; `query`
  `limit` default 100, cap 1000 (offset paging, as `/query`); aggregate
  reuses `/aggregate`'s `groupLimit` / `accumArrayLimit` /
  `memoryLimitBytes` defaults.

---

## Wire contract

All under `/v1/local`, account-scoped (outside the `:spaceId` group,
behind the `/v1` auth guard). Collection addressing by body for POSTs,
query params for GET/DELETE:

```
GET    /v1/local/collections?scope=&spaceId=        list  → [{scope, spaceId?, name, count, indexes}]
PUT    /v1/local/collections                        ensure {scope, spaceId?, name, indexes?} → 200 | 201
DELETE /v1/local/collections?scope=&spaceId=&name=  drop   → 204

POST   /v1/local/insert      {coll, docs: [..]}                         → {ids}
POST   /v1/local/upsert      {coll, docs: [..]}                         → {ids}          (whole-doc UpsertOne)
POST   /v1/local/update      {coll, id, modifier, upsert?}              → {modified, doc} (UpdateId / UpsertId)
POST   /v1/local/delete      {coll, ids?: [..] | filter?}               → {deleted}
POST   /v1/local/query       {coll, filter?, sort?, limit?, offset?, includeTotal?} → {records, total?}
POST   /v1/local/get         {coll, id}                                 → {record}       (FindId)
POST   /v1/local/aggregate   {coll, pipeline, groupLimit?, accumArrayLimit?, memoryLimitBytes?, explain?}
                                                                        → {records} | {plan} | {written}
POST   /v1/local/indexes     {coll, ensure?: [IndexInfo], drop?: [name]} → {indexes}
GET    /v1/local/meta                                                   → {stages, accumulators}
```

`coll` = `{scope, spaceId?, name}`. `filter` / `sort` / `modifier` /
`pipeline` are the raw JSON shapes `/query` and `/aggregate` accept,
parsed at the boundary with `query.ParseCondition` / `ParseSort` /
`ParseModifier` / any-store's pipeline parser (the typed-filter
invariant holds: raw shapes enter only here). `records` are stored docs
verbatim.

- **Listing** = `GetCollectionNames` + tag filter. That is already an
  ordered cursor scan over `coll:` keys (`db.go:882`) which the SDK runs
  every boot; if it ever matters, an upstream
  `GetCollectionNamesWithPrefix` is a seek-and-stop on a sorted keyspace.
- **Stage vocabulary** is advertised from `anystore.AggregateStages()` /
  `AggregateAccumulators()` via `/v1/local/meta` and quoted, not
  hand-listed, in docs — it includes `$facet`, `$lookup`, `$merge`,
  `$out`, `$set`.
- **`$out` / `$merge`** are accepted for local→local. The sink target is
  a client string, so after pipeline parse it is re-validated through
  `ParseRef` in the same chokepoint — an untagged or malformed target is
  `400 local.bad_sink_target`. Upstream constraints to document: a sink
  cannot target the source (`ErrAggregateIntoSource`,
  `aggregate_sink.go:40`), and `$merge` needs every result doc to carry
  `id` with the target's primary key being `id`. `$lookup` `from` must
  also pass `ParseRef` (v1: local only — see prerequisite #3).
- **Write chunking.** `insert`/`upsert` chunk into ≤256-doc txs.
  `delete` by `filter` collects matching ids under a `ReadTx`, then
  deletes in ≤256-id txs — **not atomic per call**; a concurrent writer
  can interleave. The response reports `deleted` actually removed.
  (Alternative if atomicity is wanted later: cap the match count at 256
  and 400 above it.)
- **`local.enabled: false`** means routes answer `409 local.disabled` and
  no collection is created; the file is the SDK's and always open, so
  existing tagged collections sit untouched on disk.

### Errors (`docs/06-errors.md`, namespace `local.*`)

| code | status | when |
|---|---|---|
| `local.disabled` | 409 | `local.enabled: false` |
| `local.collection_not_found` | 404 | any op on an un-ensured collection |
| `local.bad_name` | 400 | name/scope/spaceId fails `ParseRef` |
| `local.bad_sink_target` | 400 | `$out`/`$merge`/`$lookup` names an untagged or invalid collection |
| `local.doc_not_found` | 404 | `get` / `update` without upsert |
| `local.duplicate_id` | 409 | insert on an existing id (`anystore.ErrDocExists`) |
| `local.unique_violation` | 409 | unique index hit |
| `local.bad_filter` / `bad_modifier` / `bad_pipeline` / `bad_sort` | 400 | parse errors (reuse the `/query`/`/aggregate` mapping helpers; `ErrAggregateIntoSource` → `bad_pipeline`) |
| `local.limit_exceeded` | 400 | aggregate blocking-stage bounds (same shape as `aggregate.limit_exceeded`) |
| `local.too_many_docs` | 400 | insert/upsert > 1000 |

PUT ensure is idempotent: 201 created, 200 existed. A space-scoped op on
a spaceId with no active tech-space row → `404 space.not_found`
(`Spaces().Get` pre-flight) so a deleted space can't be resurrected as a
namespace — this does not, by itself, clean anything up.

### Config (`docs/05-config.md`)

```yaml
local:
  enabled: true        # ANY_LOCAL_ENABLED
```

### CLI (`docs/01-cli.md`) — 1:1 with the endpoints

```
any local collections [--scope space|account] [--space ID]
any local ensure   NAME [--space ID] [--index 'a,-b' ...] [--unique]
any local drop     NAME [--space ID] --yes
any local insert   NAME [--space ID] --doc '<json>'|@FILE|-        # array or single
any local upsert   NAME [--space ID] --doc ...
any local update   NAME [--space ID] ID --modifier '<json>' [--upsert]
any local delete   NAME [--space ID] [ID ... | --filter '<json>'] --yes
any local get      NAME [--space ID] ID
any local query    NAME [--space ID] [--filter …] [--sort …] [--limit N] [--offset N] [--total]
any local aggregate NAME [--space ID] --pipeline '<json>'|@FILE|- [--explain]
any local indexes  NAME [--space ID] [--ensure 'a,-b'] [--drop NAME]
any local meta
```

Absent `--space` ⇒ `scope: account`.

---

## Prerequisites

1. **SDK — expose the DB handle (BLOCKS v1).** `SDK.db` is unexported
   with no accessor on `SDK` or `space.Space` (`sdk.go:50`). Add a
   `SDK.Store() anystore.DB` (name TBD) documented as: consumers may
   create/use collections under their own reserved tag only; the SDK's
   orphan sweep never touches `ownerNone` names; the SDK never emits a
   name starting with `l_`. Step 0 below.
2. **SDK — sink-target validator on the public aggregate (unblocks
   synced→local rollups; blocks nothing in v1).** `Space.Aggregate` /
   `AggregateObjects` apply a blanket `.ReadOnly()`
   (`internal/spaceimpl/aggregate.go:132`). The fence exists for a
   reason — without a replacement `$out` could rewrite
   `<spaceId>_objects`. Replacement: a validator hook the consumer
   supplies, which `any` points at `ParseRef` so a synced-source
   pipeline may sink only into a tagged collection.
3. **any-store — `$lookup` across collections (unblocks cross-collection
   joins; blocks nothing in v1).** Resolve `from` to the target
   collection's namespace. Two call sites: the validator's
   `From != q.c.name` rejection (`aggregate.go:197`) and `lookupFunc`'s
   hardcoded `c.ns` (`aggregate.go:340`). No transaction work —
   `lookupBtreeTx` already yields a DB-wide `btree.ReadTx`
   (`aggregate.go:319`), which is exactly why co-location is the
   enabler. `foreignField` stays `"id"`.

---

## Implementation

### 0. SDK accessor PR (prerequisite #1)

Small, separate PR on any-sync-sdk; `any` re-pins. Until it lands, step
1 is testable against a plain `anystore.DB`.

### 1. `internal/localstore` — naming, existence, the tag fence

```go
type Store struct { db anystore.DB }
func New(db anystore.DB) *Store                     // no Open/Close — the SDK owns the handle

type Scope string                                   // "account" | "space"
type Ref struct { Scope Scope; SpaceId, Name string }
func ParseRef(scope, spaceId, name string) (Ref, error)   // ErrBadName
func ParseStorageName(s string) (Ref, bool)               // fixed 3-segment split; false for untagged
func (r Ref) storageName() string                         // "l_a_<name>" | "l_s_<spaceId>_<name>"

func (s *Store) Ensure(ctx, Ref, idx []anystore.IndexInfo) (created bool, err error)
func (s *Store) Drop(ctx, Ref) error
func (s *Store) List(ctx, scope Scope, spaceId string) ([]Info, error)   // GetCollectionNames + tag filter
func (s *Store) Collection(ctx, Ref) (anystore.Collection, error)         // ErrNotFound if absent
func (s *Store) SinkTarget(name string) (Ref, error)                      // $out/$merge/$lookup targets → ParseStorageName
```

`ParseRef` is a **security boundary**: it is the one chokepoint every
path goes through — wire refs, internal `Drop`, `$out`/`$merge`/`$lookup`
targets. Nothing in `localstore` can address an untagged collection; the
tag is applied in `storageName()` and only there.

Tests (`localstore_test.go`, plain `anystore.Open` in-memory):
- naming round-trip incl. names containing `_` and `-`;
- ensure idempotency; list by scope/space;
- **inverse fence test:** create a mix of CRDT-shaped
  (`<cid>_objects`, `_meta`) and tagged collections, enumerate
  `GetCollectionNames`, assert `ParseStorageName` accepts exactly the
  tagged set and `List` never returns an untagged name;
- `SinkTarget` rejects `<cid>_objects`, `_meta`, `l_x_…`, bare names.

### 2. Engine wiring (`internal/server/engine.go`)

- `deps.local = localstore.New(sdk.Store())` after the SDK opens; `nil`
  when `local.enabled: false`. No close.
- No space-lifecycle hook (v1 decision above).

### 3. Handlers (`internal/server/handlers_local.go`, `internal/api/local.go`)

Thin: parse `coll` → `ParseRef` → `Spaces().Get` pre-flight for space
scope → `deps.local.Collection` → op → JSON. Reuse:

- `handlers_query.go`'s filter/sort parsing + error mapping for `query`
  (factor helpers out if inline).
- `runLocalAggregate` over `anystore.AggQuery` (the existing
  `runAggregate` takes `space.Agg`); same fastjson arena path, `explain`
  branch, plus the `{written}` reply for sink pipelines. Sink/lookup
  target validation runs after parse, before execution.
- `query.ParseModifier` for `update`.
- chunked write loop (`writeChunks(ctx, coll, docs, 256, fn)`) shared by
  insert / upsert / delete.

Routes in `routes.go` under `/v1/local` in the account group (next to
`/v1/identities`, `/v1/devices`); `local.disabled` guard when
`deps.local == nil`.

### 4. Client + CLI

`internal/client/local.go`, `internal/cli/local.go` (`any local …`).
Follow `internal/cli/aggregate.go` for `@FILE|-` input and
`internal/cli/types.go` for group layout.

### 5. Docs (same change)

- new `docs/26-local-store.md` — model, motivation (why the same DB, the
  prohibition), wire table, the modelling rule (key by synced record
  id), and "what this is NOT": not a dataset, no subscribe, not
  search-indexed, not synced, **not independently deletable, not
  covered by any backup story, and a space-scoped collection outlives
  its space** (1-1 caveat spelled out).
- `docs/03-api.md` — § Local store.
- `docs/02-server.md` (any-side) — no new dir in the layout. One line:
  `sdk/sdk.db` now holds non-rebuildable data (`l_*` collections), so a
  manual wipe of `sdk/` loses it; the SDK's own re-index paths do not
  touch tagged collections. (Note: `docs/02-server.md:147` documents
  `index/` as safe to remove — that stays true; `sdk.db` was never
  documented as freely wipeable on the any side, the rebuild contract
  is the SDK's: `internal/crdt/meta.go:51`,
  `internal/spaceobjects/reindex.go:36`, `e2e/deletion_feed_test.go:121`.)
- `docs/05-config.md` — `local.enabled`.
- `docs/06-errors.md` — `local.*`.
- `docs/01-cli.md` — `any local`.
- `docs/00-overview.md` + CLAUDE.md invariants — `/v1/local` joins the
  consumer-side exception list next to `/search` and `/v1/events`; add
  the prohibition (any never writes an SDK collection, never spans one
  in a tx).
- `docs/07-roadmap.md` — Done entry; open items: prerequisites #2/#3,
  auto-cleanup, registry+TTL.
- OpenAPI annotations on every handler.

### 6. Tests

- `internal/server/handlers_local_test.go` — httptest with an in-memory
  DB: ensure/list/drop, insert dup → 409, update/upsert, delete by ids
  and by filter (>256 matches, all removed), query
  filter+sort+paging+total, aggregate `$group` + explain, `$out` and
  `$merge` local→local (`{written}`), `$out` to an untagged name → 400
  `bad_sink_target`, `$out` into source → 400, `$lookup` from an
  untagged name → 400, indexes ensure/drop + unique → 409, space scope on
  unknown space → 404, disabled → 409.
- `internal/localstore/localstore_test.go` — as in §1.
- e2e (`internal/e2e`): tagged collections survive a server restart;
  deleting a space leaves its `l_s_<spaceId>_*` collections in place
  (pins the documented consequence); a CRDT re-index (generation bump)
  leaves tagged collections untouched.

---

## Order of work

0. SDK accessor PR (prerequisite #1); `any` re-pin.
1. `internal/localstore` + tests (self-contained against a plain
   `anystore.DB` — does not wait on 0).
2. api types + handlers + routes + handler tests.
3. engine wiring.
4. client + CLI.
5. docs + roadmap.
6. e2e.

Size: ~1.35k lines Go incl. tests (the tag fence, sink validation and
write chunking add ~150 over a bare CRUD surface), one new doc, plus
the step-0 SDK PR.

## Out of scope (say so in the doc)

- Subscribe / liveness.
- FTS / vector indexes on local collections.
- Synced→local `$out`/`$merge` and cross-collection `$lookup` beyond the
  local tag (prerequisites #2, #3).
- Auto-cleanup on space delete; TTL / expiration; the collection
  registry.
- Backup/export; per-collection size caps / eviction.
- Mobile shims get the feature for free via the server; no binding-side
  surface.
