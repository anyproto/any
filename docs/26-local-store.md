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

One file, not a sidecar, because two capabilities exist only when local
and synced collections share a DB:

- **cross-collection `$lookup`** — any-store's `$lookup` runs on a
  DB-wide read transaction: one snapshot across both sides. A
  cross-file join has no shared snapshot at all.
- **`$out` / `$merge` rollups** — materialize a synced→local summary
  with one aggregate call.

Both are **gated upstream today** (the SDK's public aggregate is
read-only; any-store's `$lookup` is same-collection) — see
docs/07-roadmap.md. What ships now is local↔local: `$out`/`$merge`
into a local collection, `$lookup` from one.

The tag is what keeps the two worlds apart. The SDK's boot-time orphan
sweep classifies a collection by the segment before its first `_`
(space id → space-owned, cid → object-owned, anything else → fixed,
never swept); `l` is neither, so local collections are structurally
exempt — the same exemption `_meta` and `files_*` enjoy. Offload only
drops `<spaceId>_*`. Re-index rebuilds CRDT collections from the DAG
and leaves everything else alone.

### What sharing the file costs

- **Not rebuildable.** `sdk.db` used to be a pure replay cache of the
  DAGs; it now also holds the only copy of local data. The SDK's own
  rebuild paths (generation bump, handler version bump) are safe —
  they touch CRDT collections only — but a manual `rm sdk/` loses
  local collections. There is no backup story.
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
- **No transaction ever spans a local and an SDK collection.** A
  local-vs-synced atomic write is a footgun (local rows have no DAG;
  agents are already told not to condition shared mutations on local
  values), and the fence never hands out an SDK handle to span with.

## Model

- **Collection** = `{scope, spaceId?, name}` on the wire; the storage
  name is reported (`storageName`) because pipelines need it.
  `name` matches `^[a-z0-9][a-z0-9_-]{0,63}$`; `_` is legal inside it —
  the fixed tag segments keep the storage-name split exact.
- **Space scope** binds a collection to a space id and pre-flights
  the space on every operation except drop (`404 space.not_found` for
  an unknown or deleted space — a dead space cannot be resurrected as
  a namespace). **Nothing drops a space-scoped collection when its
  space goes away**; it outlives the space and `drop` is the cleanup
  path. The sharp case is a 1-1 space: delete is local-only and the id
  is re-derivable from both account keys, so re-deriving the same 1-1
  inherits the stale rows. Derived spaces can't hit this (undeletable).
  Clients that care version or namespace their collection names.
- **Document** = a JSON object with a string `id`; a missing `id` is
  minted (16 random bytes, hex). No `_ver`, no `_addSeq`; a delete is a
  delete. No schema — validation is the caller's job.
- **Key local collections by the synced record id** when they mirror
  or annotate synced data. Both gated features depend on it:
  `$lookup`'s `foreignField` is `"id"` (a primary-key point read) and
  `$merge` requires every result doc to carry `id` with the target's
  primary key being `id`. Establish the discipline now rather than
  retrofit it.
- **Indexes** are any-store range indexes (`fields`, `unique`,
  `sparse`). FTS and vector indexes are not exposed — search stays
  `/search`'s job and local data is not search-indexed.
- **No subscribe.** any-store has no pub/sub and there is no apply
  path to hook; poll or re-query.
- **No registry.** The space id is in the name, so a prefix scan
  answers "what belongs to this space" exactly. A registry earns its
  keep only with createdAt / TTL / last-access — none of which exist
  yet.

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
POST   /v1/local/query                {coll, filter?, sort?, limit?, offset?, includeTotal?} → {records, total?, hasNext?}
POST   /v1/local/aggregate            {coll, pipeline, …limits, explain?} → {records} | {plan} | {written}
POST   /v1/local/indexes              {coll, ensure?, drop?} → {indexes}
```

`filter` / `sort` / `modifier` / `pipeline` are the raw any-store
shapes `/query` and `/aggregate` accept (docs/09-query.md,
docs/14-aggregation.md); the stage vocabulary is whatever
`/v1/local/meta` reports — it includes `$facet`, `$lookup`, `$merge`,
`$out`, `$set`.

### Aggregation sinks

A pipeline ending in `$out` or `$merge` answers `{written: n}` instead
of `records`; the target is created if absent (under the tag — it is a
normal local collection afterwards). Constraints any-store imposes: a
sink cannot target the aggregated collection itself, and `$merge`
needs every result document to carry `id` (both `400
local.bad_pipeline`). `$lookup from` must also be a local collection
and, for now, the aggregated collection itself.

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
`local.collection_not_found` (ensure first), `local.duplicate_id`,
`local.doc_not_found`, `local.unique_violation`, `local.bad_sink_target`,
`local.limit_exceeded`, `local.disabled`.

## What this is NOT

- Not a dataset; not declared on a type; no schema enforcement.
- Not synced, not subscribe-able, not search-indexed.
- Not independently deletable; not covered by any backup story.
- Not cleaned up on space delete — a space-scoped collection outlives
  its space.
- Not the home of SYN-174 scoped datasets (private *records* that are
  still declared, subscribe-able and schema-checked). The two can
  coexist; nothing here designs that.

## Out of scope (v1)

Subscribe / liveness; FTS or vector indexes on local collections;
synced→local `$out`/`$merge` and cross-collection `$lookup` (upstream
prerequisites in docs/07-roadmap.md); auto-cleanup on space delete;
TTL / expiration; a collection registry; backup or export.

## Source

`internal/localstore` (naming, existence, the tag fence — the only
place the tag is applied), `internal/server/handlers_local.go` (wire),
`internal/api/local.go` (bodies), `internal/client/local.go`,
`internal/cli/local.go`. SDK side: `SDK.Store()` and the consumer
contract in the SDK's docs/03-space.md § Space Lifecycle.
