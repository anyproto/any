# Local store — device-local, non-CRDT collections

Plain any-store collections that never sync, served at `/v1/local`.
They give a client or agent the full any-store surface — filters,
sort, indexes, modifiers, aggregation pipelines — for state that must
NOT replicate: scratch and working sets during a run, ingest staging
before an `/upsert`, per-device caches derived from synced data,
telemetry, tool results awaiting review.

A local collection is **not a dataset**: no type, no schema, no
handler, no `_ver`, no tombstones, no DAG. It is not a version domain
and it is not subscribe-able. It is a document store that happens to
speak the same query language as the synced data and to live in the
same file.

Consumer-side surface, like `/search` and `/v1/events`: the SDK's only
involvement is handing out its DB handle (`SDK.Store()`). Nothing here
goes through `Space.Modify` / `Query`; nothing here is visible to
peers.

## Where it lives, and why there

Local collections are any-store collections **inside the SDK's own
`<account-dir>/sdk/sdk.db`**, fenced by a name tag:

| scope | storage name |
|---|---|
| `account` | `l_a_<name>` |
| `space` | `l_s_<spaceId>_<name>` |

One file, not a sidecar, because a shared snapshot across local and
synced collections exists only inside one DB: any-store's `$lookup`
runs on a DB-wide read transaction, and `$out` / `$merge` write within
one DB. Neither crosses to synced collections: the SDK's aggregate is
read-only, and any-store's `$lookup` joins only the aggregated
collection itself. What works is local↔local — `$out`/`$merge` into a
local collection, `$lookup` on the aggregated one.

The tag is what keeps the two worlds apart. The SDK's boot-time orphan
sweep classifies a collection by the segment before its first `_`
(space id → space-owned, cid → object-owned, anything else → fixed,
never swept); `l` is neither, so local collections are structurally
exempt — the same exemption `_meta` and `files_*` enjoy. Offload only
drops `<spaceId>_*`. Re-index rebuilds CRDT collections from the DAG
and leaves everything else alone.

### What sharing the file costs

- **Not rebuildable.** `sdk.db` holds the SDK's replay cache of the
  DAGs AND the only copy of local data. The SDK's own
  rebuild paths (generation bump, handler version bump) are safe —
  they touch CRDT collections only — but deleting the `sdk/` directory
  loses local collections. There is no automatic backup; § Export and
  import is the manual one.
- **Not independently deletable.** Local state cannot be wiped
  without wiping the account's replay cache with it. Drop
  collections through the API instead.
- **One global writer.** any-store serializes writes per DB, so a
  long local transaction stalls CRDT applies. Every write here is
  chunked (256 docs per transaction); see Limits.

## The fence

Every collection name that reaches any-store through this surface
passes one chokepoint — `localstore.ParseRef` — which applies the tag
and refuses everything else. That includes the raw names a client
writes INSIDE a pipeline: `$out "<name>"`, `$merge {into}`,
`$lookup {from}` are scanned before any-store parses and must name a
local collection by its storage name (`400 local.bad_sink_target`).

Two rules follow, and `any` keeps both structurally, not by
convention:

- **`any` never writes an SDK collection.** A direct write bypasses
  the DAG and is reverted by the next re-index; the fence makes it
  unreachable.
- **No transaction ever spans a local and an SDK collection.** Local
  rows have no DAG, so a local-vs-synced atomic write cannot hold
  across peers; the fence never hands out an SDK handle to span with.

## Model

- **Collection** = `{scope, spaceId?, name}` on the wire; the storage
  name is reported (`storageName`) because pipelines need it.
  `name` matches `^[a-z0-9][a-z0-9_-]{0,63}$`; `_` is legal inside it —
  the fixed tag segments keep the storage-name split exact.
- **Space scope** binds a collection to a space id and pre-flights
  the space on every **write** — ensure, insert, upsert, update,
  indexes, a pipeline's sink target (`404 space.not_found` for an
  unknown space, `409 space.deleted` for a tombstoned one — a dead
  space cannot be resurrected as a namespace). Reads (get, query,
  aggregate), delete, drop, list, export and import never pre-flight:
  a gone space's collections stay readable and deletable, and an
  imported store's collections (§ Export and import) are readable on
  a server that has never seen their space.
  **Nothing drops a space-scoped collection when its space goes
  away**; it outlives the space and `drop` is the cleanup path. The
  sharp case is a 1-1 space: delete is local-only and the id is
  re-derivable from both account keys, so re-deriving the same 1-1
  inherits the stale rows. Derived spaces can't hit this (undeletable).
  Clients that care version or namespace their collection names.
- **Document** = a JSON object with a string `id`; a missing `id` is
  minted (16 random bytes, hex). No `_ver`, no `_addSeq`; a delete is a
  delete. No schema — validation is the caller's job.
- **Key local collections by the synced record id** when they mirror
  or annotate synced data: `$lookup`'s `foreignField` is `"id"` (a
  primary-key point read) and `$merge` requires every result doc to
  carry `id`.
- **Indexes** are any-store range indexes (`name?`, `fields`, `unique`,
  `sparse`). FTS and vector indexes are not exposed — search stays
  `/search`'s job and local data is not search-indexed.
- **No subscribe.** any-store has no pub/sub and there is no apply
  path to hook; poll or re-query.
- **No registry.** The space id is in the storage name, so a prefix
  scan (`GET /v1/local/collections?spaceId=`) answers "what belongs to
  this space" exactly.

## Surface

All routes are account-scoped under `/v1/local`, behind the `/v1`
auth guard, outside the `:spaceId` group (a space-scoped collection
is addressed in the body). Full body shapes: docs/03-api.md § Local
store; CLI: docs/01-cli.md § Local store.

```
GET    /v1/local/meta                 stage + accumulator vocabulary (from any-store)
GET    /v1/local/collections          list  ?scope=&spaceId=
PUT    /v1/local/collections          ensure {scope, spaceId?, name, indexes?} → 201 | 200
DELETE /v1/local/collections          drop  ?scope=&spaceId=&name=
POST   /v1/local/insert               {coll, docs}          → {ids}
POST   /v1/local/upsert               {coll, docs}          → {ids}
POST   /v1/local/update               {coll, id, modifier, upsert?} → {modified, record}
POST   /v1/local/delete               {coll, ids | filter}  → {deleted}
POST   /v1/local/get                  {coll, id}            → {record}
POST   /v1/local/query                {coll, filter?, sort?, limit?, offset?, includeTotal?, projection?} → {records, total?, hasNext?}
POST   /v1/local/aggregate            {coll, pipeline, …limits, explain?} → {records} | {plan} | {written}
POST   /v1/local/indexes              {coll, ensure?, drop?} → {indexes}
GET    /v1/local/export               ?scope=&spaceId=&names=a,b → the file (application/gzip)
POST   /v1/local/import               body = the file → {collections}
```

`filter` / `sort` / `modifier` / `pipeline` are the raw any-store
shapes `/query` and `/aggregate` accept (docs/09-query.md,
docs/14-aggregation.md); the stage vocabulary is whatever
`/v1/local/meta` reports — it includes `$facet`, `$lookup`, `$merge`,
`$out`, `$set`.

`projection` is the same grammar too (docs/09-query.md § Projection):
field paths to `1` / `-1`, `id` always present. The protocol-field
rules there are inert here — a local record has no `_ver` and no
delivery counters, so a projection only ever narrows the fields the
caller stored. Use `$project` inside `/aggregate` for the pipeline
equivalent.

### Aggregation sinks

A pipeline ending in `$out` or `$merge` answers `{written: n}` instead
of `records`. The target must be an **existing** local collection —
it passes the same space pre-flight as the request's own collection
and `404 local.collection_not_found` otherwise; a sink never mints a
collection, so a typo'd target cannot become a silent sibling. Sinks
nested in `$facet` are fenced the same way. Constraints any-store
imposes: a sink cannot target the aggregated collection itself, and
`$merge`/`$out` need every result document to carry `id` (both `400
local.bad_pipeline`). `$lookup from` must name the aggregated
collection itself (`400 local.bad_pipeline`).

## Export and import

A collection-level dump: the named collections leave as **one file**
and come back on any server as the same collections — same scope,
space id, name, indexes, documents. The use is diagnostics across
machines: a reporter's device-local data (bao's trace collections,
anybao ADR-023) exported from their app and imported into a
developer's scratch server, where every `/v1/local` read — query,
aggregate, `anyrt trace ls/show` — works on it unchanged. Whole-DB
backup is not this: `sdk.db` is the whole account.

```
GET /v1/local/export?scope=space&spaceId=<id>&names=trace_runs,trace_records,trace_blobs
                                      → 200 application/gzip, Content-Disposition attachment
POST /v1/local/import   (body = the file, exempt from the 1 MiB body cap)
                                      → {collections: [{scope, spaceId?, name, storageName, count, indexes}]}
```

- `names` is comma-separated and needs a scope (`spaceId` implies
  `space`). Without `names`, every collection the `(scope, spaceId)`
  listing would return is exported; nothing there is `404
  local.collection_not_found`. Duplicates export once.
- **No space pre-flight on either side**, like every read: a gone
  space's collections must still export, and the file's spaces need
  not exist on the importing server — reads, aggregation and delete
  work on the imported collections there; only writes that could grow
  them (insert, upsert, update, indexes) answer `404 space.not_found`.
- **One snapshot.** The export runs inside one any-store read
  transaction, so the collections are mutually consistent and every
  section's count is exact. Every name is resolved before the first
  byte — a missing collection is a JSON 404, never a truncated file.
- **Import = ensure + upsert**, 256 documents per transaction like
  every other write here: an existing collection keeps what it has
  and is overlaid (re-importing the same file is a no-op; a newer
  export of the same collections updates the older); an index that
  exists under the same name with a different definition is `400
  local.bad_index`. A failure mid-way leaves the collections before
  it committed. The reply lists the collections as they stand after.
- **The fence holds on import.** Every collection is re-derived from
  its `(scope, spaceId, name)` through `ParseRef`; the recorded
  storage name is checked, never trusted. A file naming `_meta` or
  any untagged collection is `400 local.bad_export`.

### The file

gzip (anyenc carries no checksum; gzip's CRC catches corruption)
around one any-store **anyenc value stream** (`anyenc.NewWriter` /
`anyenc.NewReader`, any-store ≥ v2.1.1): values back to back, each
delimiting itself. The first value is the manifest; then, in manifest
order, exactly `count` documents per collection — no separators, no
trailer. Suggested extension: `.anyenc.gz`.

```
{ format: "any-local-export", version: 1, exportedAt: <unix ms>,
  collections: [ { scope, spaceId?, name, storageName, count,
                   indexes: [{name, fields, unique, sparse}] } ] }
<doc> <doc> …      collections[0], count docs
<doc> …            collections[1], …
```

Only range indexes are recorded (the only kind the store exposes).
`format`/`version` must match exactly; a section shorter than its
count, a non-object document, a document without an `id`, or bytes
past the last section are `400 local.bad_export` (`details.imported`
= collections completed before the failure). Any anyenc reader can
consume the file without a server — the documents are the stored
values as-is.

## Limits

- Request body: the global 1 MiB `BodyLimit`.
- `insert` / `upsert`: ≤ 1000 docs per request
  (`400 local.too_many_docs`), written 256 per transaction. A failure
  mid-way leaves earlier chunks committed.
- `delete` by filter: matching ids are collected under one read, then
  removed 256 per transaction — **not atomic per call**; a concurrent
  writer can interleave. `deleted` reports what was actually removed.
- `query`: `limit` default 100, cap 1000; offset paging.
- `aggregate`: the `/aggregate` blocking-stage defaults
  (`groupLimit` / `accumArrayLimit` / `memoryLimitBytes`).

## Config

```yaml
local:
  enabled: true        # ANY_LOCAL_ENABLED
```

`false` makes every `/v1/local` route answer `409 local.disabled` and
creates nothing. The file is the SDK's and stays open, so existing
local collections sit untouched on disk.

## Errors

`local.*` — docs/06-errors.md. The ones a client should branch on:
`local.collection_not_found` (ensure first — also a sink target),
`local.bad_index`, `local.duplicate_id`,
`local.doc_not_found`, `local.unique_violation`, `local.bad_sink_target`,
`local.limit_exceeded`, `local.disabled`.

## What this is NOT

- Not a dataset; not declared on a type; no schema enforcement.
- Not synced, not subscribe-able, not search-indexed.
- Not independently deletable; not covered by the SDK's backup —
  export is per collection, on request.
- Not cleaned up on space delete — a space-scoped collection outlives
  its space.

## Not supported

Subscribe / liveness; FTS or vector indexes on local collections;
synced→local `$out`/`$merge` and cross-collection `$lookup`;
auto-cleanup on space delete; TTL / expiration; a collection registry;
whole-store backup (export is per named collection).

## Source

`internal/localstore` (naming, existence, the tag fence — the only
place the tag is applied; `export.go` the file format + export/import),
`internal/server/handlers_local.go` (wire),
`internal/api/local.go` (bodies), `internal/client/local.go`,
`internal/cli/local.go`. SDK side: `SDK.Store()` and the consumer
contract in the SDK's
[`docs/03-space.md` § Space Lifecycle](https://github.com/anyproto/any-sync-sdk/blob/main/docs/03-space.md#space-lifecycle).
