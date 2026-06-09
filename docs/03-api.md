# HTTP API

## Conventions

- **Framework**: `github.com/labstack/echo` (v4).
- **Base path**: `/v1/`. All endpoints are versioned from day one.
- **Media type**: `application/json; charset=utf-8` — requests with a body
  and every response. No other content types in v1.
- **IDs in the path**: `{spaceId}`, `{objectId}`, `{typeId}`, `{propId}`
  are URL-safe strings (base58). Path segments are URL-encoded.
- **Success**: `200 OK` for reads, `201 Created` for creates,
  `204 No Content` for side-effect-only endpoints (e.g.
  `PUT /v1/account/metadata`, `PATCH /v1/spaces/:spaceId`,
  `DELETE /v1/spaces/:spaceId/objects/:objectId`).
- **Errors**: see `06-errors.md`. Always JSON, always the same shape.
- **Binding**: use `echo.Context.Bind` for request bodies. Share the
  request/response types between server and CLI via `internal/api/`.
- **Dataset reads go through `/query` and `/query/subscribe`.**
  Built-in types (chat, editor) keep bespoke handlers for *writes*
  only — POST/PATCH/DELETE and reactions. Reads always go through
  the per-object query primitive with the matching `dataset` value
  (`chat_messages`, `editor_blocks`, etc.). One read path for every
  dataset, one wire shape for every snapshot. The lone exception is
  `GET /editor/markdown`, which renders blocks to markdown bytes —
  a transform, not a dataset read.

### Write responses

Every dataset write — `Space.Modify` / `Space.Delete` and the bespoke
chat (send / edit / delete / react) and editor (create / patch /
delete) handlers — returns the same shape, `api.ModifyResult`:

```json
{ "versionId": "<change VersionId>",
  "changeId":  "<changeId>",
  "recordIds": ["<id>"],
  "rejections": [] }
```

- `versionId` is the **change's** VersionId. Clients running the
  subscribe-then-query-then-apply recipe stamp `_ver.<op.path> =
  versionId` on the touched paths to pre-seed dedup against the matching
  live event, and use it to order their own writes against remote ones.
  (Distinct from `_ver.id`, the per-record creation marker — a stable
  id, not a per-edit version.)
- `recordIds` mirrors the input record order. On creates it carries the
  server-derived id (`recordIds[0]`); on edit / delete / react it echoes
  the target id.
- `rejections` is omitted unless a handler dropped an op (partial
  success). The bespoke chat/editor handlers turn any rejection into a
  4xx instead, so it's always empty there.

Writes never return the record body — read it back through `/query` (or
live via `/query/subscribe`). One write shape across the whole API.

## Endpoint catalog

### Meta

| Method | Path            | Purpose                                |
|--------|-----------------|----------------------------------------|
| GET    | `/v1/health`    | server health, version, account id     |
| POST   | `/v1/shutdown`  | graceful shutdown                      |

### Account

| Method | Path                         | Purpose                                |
|--------|------------------------------|----------------------------------------|
| GET    | `/v1/account`                | own id + metadata                      |
| PUT    | `/v1/account/metadata`       | `Account.UpdateMetadata`               |

```json
// PUT /v1/account/metadata
{ "name":"Alice",
  "description":"writer, reader, occasional debugger",
  "iconCid":"bafy..." }
// → 204
```

The SDK persists the bytes to the local tech-space and pushes them to
identityRepo, so the new profile becomes visible in every space the
caller is a member of without further per-space writes (read it back
via `GET /v1/spaces/:id/members/me`). At least one of `name` /
`description` / `iconCid` must be set; an all-empty body returns
`400 request.missing_field`.

### Spaces

| Method | Path                            | Purpose                             |
|--------|---------------------------------|-------------------------------------|
| POST   | `/v1/spaces`                    | `Service.Create`                    |
| GET    | `/v1/spaces`                    | `Service.List` → `[]SpaceInfo`      |
| POST   | `/v1/spaces/query`              | `Service.Query` (spaces dataset) snapshot |
| POST   | `/v1/spaces/query/subscribe`    | `Service.Query` (spaces dataset) subscribe (SSE) |
| GET    | `/v1/spaces/:spaceId`           | `Space.Info`                        |
| PATCH  | `/v1/spaces/:spaceId`           | `Space.SetMetadata`                 |
| POST   | `/v1/spaces/:spaceId/sync`      | `Space.SyncHeads`                   |
| DELETE | `/v1/spaces/:spaceId`           | `Service.Delete`                    |
| POST   | `/v1/spaces/join`               | `Service.Join`                      |
| POST   | `/v1/spaces/derive`             | `Service.Derive`                    |
| POST   | `/v1/spaces/one-to-one`         | `Service.OneToOne`                  |

`SpaceInfo` carries a `spaceIndexObjectId` field: the deterministic id
of the in-space `spaceIndex` derived object that owns this space's
metadata. Stable across peers and across SDK reboots — clients
attach a `POST /v1/spaces/:id/objects/query/subscribe` stream filtered
on this id to live-update name / description / icon. Single-space responses
(`POST /v1/spaces`, `GET /v1/spaces/:id`, `PATCH /v1/spaces/:id`)
always populate the field. `GET /v1/spaces` fills it on a best-effort
basis; rows whose Space handle the SDK can't resolve (e.g. tombstoned
entries) omit it.

#### Query / subscribe the space list

`GET /v1/spaces` (`Service.List`) stays the mapped convenience — it
returns the public `SpaceInfo` shape (status / ownRole projected from the
raw tech-index rows). For a **filterable / sortable / live** view, the
generic windowed primitive reads the tech-space `spaces` dataset
directly:

```
POST /v1/spaces/query              snapshot   → { records, total?, hasNext? }
POST /v1/spaces/query/subscribe    SSE        → ready → snapshot → changes → closed
```

Both wrap `Service.Query(SpaceIndexObjectId(), "spaces")` and take the
same body as the per-object `…/query` endpoints (`filter` / `sort` /
`limit` / `offset` / `includeTotal` / `mailboxCapacity` /
`driftBudgetPercent`), plus an optional `dataset` override (defaults to
`spaces`; `profile` is the other system dataset). `objectId` is fixed
server-side to the tech-space index object. Records are the **raw**
tech-index rows (not the mapped `SpaceInfo`) — use `GET /v1/spaces` when
you want the projected status/role. The subscribe frame set and `closed`
reasons are identical to the per-object `…/query/subscribe` (see the Data
plane § Subscribe and `docs/04-events.md`); a space joined on another
device or head-synced in arrives as an `added` change.

#### Dataset schema discovery

```
GET /v1/spaces/:spaceId/datasets   → { datasets: [ { name, schema } ] }   Space.Datasets
GET /v1/datasets                   → { datasets: [ { name, schema } ] }   Service.Datasets
```

`schema` is a standard **JSON Schema** object per dataset
(`{type:"object", properties:{…}, additionalProperties:<dynamic>}`). Each
property carries an `x-scope` extension keyword classifying the field:

- `synced` — user/DAG-written, synced across the account's devices;
- `derived` — handler-computed, read-only to writers (e.g. chat
  `creator` / `createdAt`);
- `local` — device-local, never synced.

`additionalProperties:true` marks a dynamic dataset (free-form keys
allowed, defaulting to synced — e.g. the per-type `objects` namespace and
the chat/editor datasets, which declare their known fields while staying
open). The per-space form lists every dataset the space hosts (`objects`,
`chat_messages`, `editor_blocks`, …); the account-wide form lists the
tech-space system datasets (`spaces`, `profile`) behind the space-list
query/subscribe above.

#### Update space metadata

`PATCH /v1/spaces/:spaceId`

```json
{ "name":        "Project Phoenix",
  "description": "shared notes + chat",
  "iconCid":     "bafy..." }
// → 204
```

All three fields are optional but at least one must be present —
an all-empty body returns `400 request.missing_field`. Field semantics
mirror the SDK's pointer-to-string contract: a key absent from the JSON
body leaves the field unchanged; a key present with an empty string
clears the field. `spaceType` is intentionally not patchable; it's
pinned by the initial Create.

The write lands on the in-space `spaceIndex` object's `properties`
dataset and CRDT-replicates to every member. Each peer's indexer hook
mirrors the converged state into its own local tech-space row.
Because the mirror runs asynchronously (subscription delivery, not
in-line with the local write), an immediate follow-up `GET
/v1/spaces/:id` may briefly return the pre-patch values. Callers that
need the converged state poll, or attach a `…/objects/query/subscribe`
stream filtered on `spaceIndexObjectId`.

#### Force a head-sync round (sync now)

`POST /v1/spaces/:spaceId/sync`

```
// → 204 (no body)
```

Wraps `Space.SyncHeads`: forces an immediate head-sync (diff) round
against the space's responsible nodes instead of waiting for the
periodic headsync timer. The call **blocks** server-side until the
round completes, then returns `204`. Normal operation never needs this
— periodic + reactive sync keep a space current on their own — it
exists for on-demand convergence: a manual "sync now" button, or
collapsing the multi-peer convergence wait in tests from "next periodic
headsync (~30s)" to "as fast as the diff round settles." A single round
exchanges heads with the node; for a writer→reader handoff, sync the
writer first (push to the node) then the reader (pull back).

### Objects

| Method | Path                                                      | Purpose                            |
|--------|-----------------------------------------------------------|------------------------------------|
| POST   | `/v1/spaces/:spaceId/objects`                             | `Objects.Create`                   |
| POST   | `/v1/spaces/:spaceId/objects/query`                       | `Space.QueryObjects.Snapshot`      |
| POST   | `/v1/spaces/:spaceId/objects/query/subscribe`             | `Space.QueryObjects.Subscribe` (SSE) |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId`                   | `Objects.Delete`                   |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/editor/markdown`              | render blocks as markdown |
| PUT    | `/v1/spaces/:spaceId/objects/:objectId/editor/markdown`              | bulk parse markdown → blocks |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/editor/markdown/append`       | append markdown at tail (no read/diff) |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/editor/blocks`                | create one block         |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`       | $set / $unset one block  |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`       | tombstone one block      |

Object bodies are stored as a tree of atomic blocks on a per-object
`editor_blocks` dataset (one record per block) and exposed through the
`…/editor/**` route namespace. The atomic surface is the four
`…/editor/blocks` endpoints; the two `…/editor/markdown` routes are
a lossless import/export layer over the same dataset for LLM tools,
"Export as .md" / "Import .md" flows, and programmatic API users that
don't want to walk the block tree. Liveness goes through the
per-object query/subscribe endpoint with `dataset=editor_blocks`.

The `editor/markdown` routes are aggregating endpoints (each one
bundles several SDK calls) and are a deliberate exception to the
"endpoints map 1:1 onto SDK methods" rule. `GET` reads every
top-level block, renders each to its canonical markdown bytes, and
joins with `\n\n`. `PUT` parses the incoming markdown, diffs against
the current block tree by (type + position + text), and emits
per-block create / update / delete ops through the same write path a
PATCH /editor/blocks call would, so the same `editor_blocks` SSE events
fire under the hood. `PUT` replies with `{"inserted": [...],
"updated": [...], "deleted": [...], "unchanged": N}` where the slices
contain block ids.

`POST …/editor/markdown/append` is the append-only fast path. It
parses the supplied `{"content": "..."}`, looks up only the tail
position (one indexed `-nav.pos` query, never the existing block
bodies), allocates lexids past the last block, and creates every
parsed block in a single ModifyBatch. Cost is O(appended content),
independent of how large the document already is — unlike `PUT`, which
renders and diffs the whole document on every call. The reply uses the
same shape as `PUT` with only `inserted` populated (`updated` and
`deleted` are always empty). Trade-offs the caller accepts: it is
purely additive (no update/delete, and it will create a block
identical to an existing one), and it inserts no leading separator —
`content` is appended structurally after the current last block.
Empty/blank content is a 200 no-op. Use this for grow-by-append pages
(e.g. agent debug logs that append every turn); a run of N appends is
O(N) here versus O(N²) through `PUT`.

#### Blocks

One record per block, stored on a per-object `editor_blocks` dataset.
Nest via `nav.parentId`; order siblings via `nav.pos` (lexid). Wire
shape:

```json
{
  "id":    "<auto-derived from changeId>",
  "_ver":  { "id": "<VersionId of last change>", ... },
  "type":  "paragraph" | "heading" | "list_item" |
           "check_list_item" | "code" | "quote" |
           "divider" | "html" | "table" | "image",
  "style": { "level": 1..6,         /* heading */
             "ordered": true|false, /* list_item */
             "checked": true|false, /* check_list_item */
             "lang":    "go" },     /* code */
  "text":  "**bold** inline markdown",
  "nav":   { "parentId": "<blockId>" | "",
             "pos":      "<lexid>" }
}
```

`text` is INLINE markdown only — bold, italic, inline code, links,
strikethrough. Block-level syntax (heading hashes, list bullets,
fences, quote `>` prefixes) lives in `type` + `style` instead so
clients render blocks structurally without re-parsing.

##### Read blocks

The bespoke list endpoint is gone — reads go through the per-object
query primitive with `dataset=editor_blocks`:

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<oid>",
  "dataset":  "editor_blocks",
  "sort":     ["nav.pos"] }
```

Each record carries `id`, `_ver`, `type`, `style`, `text`, `nav`.
Records sort by `nav.pos` ascending (flat order, not DFS — clients
that want DFS reconstruct the tree by grouping children under each
`nav.parentId`). Empty `records` array when the object has no body
blocks yet.

##### Create

`POST /v1/spaces/:spaceId/objects/:objectId/editor/blocks`

```json
{ "type":  "paragraph",
  "style": {"level": 2},
  "text":  "hello",
  "nav":   {"parentId": "<blockId>", "pos": "<lexid>"} }
```

`type` is required (≤ 64 bytes, non-empty). `style` is an open-ended
object — the handler accepts any sub-keys. `text` is inline markdown.
`nav.parentId` defaults to `""` (top-level); `nav.pos` defaults to
the next lexid past the parent's current max (queried server-side at
create time). Returns 201 with the shared write result
`{versionId, changeId, recordIds}` — `recordIds[0]` is the
server-allocated block id. Read the block back via `POST /query` with
`dataset=editor_blocks` (see § Write responses).

##### Patch

`PATCH /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`

```json
{ "set":   { "text": "...", "style.level": 2 },
  "unset": ["style.checked"] }
```

Each key in `set` is a dotted field path applied as one `$set` op.
Each entry in `unset` is a dotted path applied as one `$unset`. Both
fields are optional; an empty patch is a no-op — no change is produced,
so the result carries `recordIds=[blockId]` with an empty `versionId`.
All ops land in a single any-sync change (one VersionId).

Required fields cannot be `$unset`-ed (`type`, `nav.parentId`,
`nav.pos`) — the handler rejects those ops while still applying the
rest of the batch. Per-op rejections do not fail the whole change.

Note on path syntax: dotted-string keys (`"style.level": 2`) are
parsed as one anyenc field path, NOT as nested objects. Use
`"style.level"` to touch a single sub-field; use `"style": {"level":2}`
only when you want to replace the entire `style` object whole-cloth.

Response: the shared write result `{versionId, changeId, recordIds}`
(`recordIds=[blockId]`). Clients running the
subscribe-then-query-then-apply recipe stamp `_ver.<op.path> = versionId`
on the affected paths to pre-seed dedup against the matching live event.

##### Delete

`DELETE /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`
→ 200 with the shared write result `{versionId, changeId, recordIds}`
(`recordIds=[blockId]`). Tombstones the record (sticky — re-creating
the same id is rejected). Children of the deleted block are NOT
cascaded; the client either deletes the descendants explicitly or
rewrites the document via `PUT /editor/markdown`, which diffs the whole
body.

##### Subscribe

Liveness goes through the per-object windowed query/subscribe endpoint
with `dataset=editor_blocks`:

```
POST /v1/spaces/:spaceId/query/subscribe
{ "objectId": "<oid>", "dataset": "editor_blocks", "sort": ["nav.pos"] }
```

`changes` frames carry `added` / `updated` / `removed` for blocks
entering, mutating, or leaving the visible window. `added` records
include the full block as `doc` plus its per-field ops; `updated`
records carry the post-apply doc and the ops that triggered the
change; `removed` carries just the id. The same events fire whether
the change originated from a PATCH /editor/blocks call or a PUT
/editor/markdown bulk rewrite. See `04-events.md`.

#### `nav` auto-stamping on `Objects.Create`

Every new object gets a `nav` row stamped on it server-side: `nav` is
appended to `any.types` and three property values land on the
per-space `objects` collection — `nav.type` (1 = item, 2 = folder),
`nav.parentId` (string id of the parent folder; `""` = root) and
`nav.pos` (lexid for ordering inside a parent). See `internal/nav` for
the constants. The body accepts an optional `"nav"` block to override
defaults:

```json
{
  "types": ["..."],
  "initialProperties": { "...": { "...": "..." } },
  "nav": {
    "type":     2,
    "parentId": "obj_parent_id",
    "pos":      "PPQY"
  }
}
```

`nav.pos` defaults to the next lexid after the current max pos in the
target folder (queried server-side at create time); `Middle()` when
the folder is empty. Mirrors anytype-heart's `LexId.Next(prev)`
pattern. Trees are built by querying the per-space `objects`
collection — no dedicated tree endpoint:

```bash
# children of folder X, in order:
curl -X POST /v1/spaces/$SPID/objects/query -d '{
  "filter": { "nav.parentId": "obj_X" },
  "sort":   [ "nav.pos" ]
}'
```

`nav` is a **virtual built-in type** — surfaced through
`GET /v1/spaces/:spaceId/types` (BuiltIn=true) and
`GET /v1/spaces/:spaceId/types/nav/properties`, but not registered
through the SDK's `handler.Type` machinery (no separate dataset, no
custom validator). Property paths use literal string keys
(`nav.parentId` etc.), not content-addressable propIds.

#### Moves (drag-and-drop)

Tree moves use the existing `setBase` endpoint — no dedicated move
route. To relocate object `oid` under `newParent` at lexid pos `p`:

```
POST /v1/spaces/:spaceId/properties/:oid/base/nav
{ "patch": { "parentId": "<newParent>", "pos": "<p>" } }
```

Both fields land in one DAG change. The web UI ports the lexid
allocator to JavaScript (alphabet `CharsAllNoEscape`, blockSize=4,
stepSize=100 — match the Go side byte-for-byte) so the client can
compute drop-target positions without a server round-trip.

#### Object deletion

`DELETE /v1/spaces/:spaceId/objects/:objectId` is a single
`Objects.Delete` call. The SDK writes a record-level tombstone on the
per-space `objects` row before tearing down the any-sync tree, so the
row disappears from `QueryObjects` and a `deleted: true` event fires
on the per-space firehose (`dataset=objects`) with the change's
`versionId` — the canonical signal subscribers use to drop the id
from local state. See `04-events.md`.

### Data plane

| Method | Path                                                      | Purpose                              |
|--------|-----------------------------------------------------------|--------------------------------------|
| POST   | `/v1/spaces/:spaceId/query`                               | `Space.Query.Snapshot`               |
| POST   | `/v1/spaces/:spaceId/query/subscribe`                     | `Space.Query.Subscribe` (SSE)        |
| POST   | `/v1/spaces/:spaceId/modify`                              | `Space.Modify`                       |
| POST   | `/v1/spaces/:spaceId/delete-records`                      | `Space.Delete`                       |

Two query scopes:

- `POST /v1/spaces/:spaceId/objects/query` (+ `/subscribe`) —
  **cross-object**. Reads the per-space `objects` collection (one row
  per object's computed property values). Use this to find objects by
  property, e.g. `{"filter":{"<typeId>.<propId>":"Casablanca"}}`.
- `POST /v1/spaces/:spaceId/query` (+ `/subscribe`) — **per-object**.
  Reads one of an object's own datasets (`objectId` and `dataset`
  required). Used for a type object's `properties` definitions
  dataset, `editor_blocks`, `chat_messages`, `program_source` /
  `program_description`, `mini_app`, etc.

All four take POST (filter/sort body doesn't fit a query string).
Reads always go through these — the bare `…/query` returns a
point-in-time snapshot; `…/query/subscribe` returns the same
snapshot plus a live SSE stream of windowed transitions. See
`04-events.md` for the subscribe contract.

#### Snapshot request body (shared by both `…/query` and `…/query/subscribe`)

```json
{
  "objectId":   "obj_abc",            // per-object variant only
  "dataset":    "notes",              // per-object variant only
  "filter":     { "tags": "idea" },
  "sort":       ["-_ver.id"],         // required when limit > 0 on subscribe
  "limit":      100,
  "offset":     0,
  "includeTotal":       true,         // populate `total` + `hasNext` in the snapshot
  "mailboxCapacity":    256,          // subscribe only — default 256, min 16
  "driftBudgetPercent": 30,           // subscribe only — default 30
  "projection": { "includeVariants": false, "includeMeta": false }   // NOT IMPLEMENTED
}
```

**`projection` is not implemented yet.** The field is accepted in the
body but the server doesn't thread it to `Query.Projection`, and the
SDK's `Projection(opts)` is itself a no-op in MVP. Every record on
snapshot frames and every `added` / `updated` record in `changes`
events ships its full anyenc form — `_ver` (creation marker + per-
field high-water), and `_traces` / `_deletedAt` if present. Clients
that need a leaner shape strip those fields locally for now. See
`docs/07-roadmap.md` § "Query `Projection`".

Snapshot response (bare `…/query`):

```json
{ "records": [ /* *anyenc.Value rendered as JSON */ ],
  "total":   17,                      // omitted when includeTotal=false
  "hasNext": true }                   // more matches past this page; omitted when includeTotal=false
```

#### Subscribe (Server-Sent Events)

Two endpoints — POST, body as documented above:

```
POST /v1/spaces/:spaceId/objects/query/subscribe       (cross-object)
POST /v1/spaces/:spaceId/query/subscribe               (per-object)
```

Response is `Content-Type: text/event-stream`. Errors before the
stream opens use the canonical JSON envelope (`request.missing_field`,
`space.not_found`, ...). Once the response status is 200, problems
become SSE `event: closed` frames.

Wire format:

```
event: ready
data: {}

event: snapshot
data: {"records":[{"id":"obj_a", "...": "..."}, ...], "total": 17, "hasNext": true}

event: changes
data: [{"versionId":"!!%>",
        "added":  [{"id":"obj_c","doc":{...},
                    "ops":[{"type":"$set","path":[],
                            "payload":{"title":"hello"}}]}],
        "updated":[{"id":"obj_a","doc":{...},
                    "ops":[{"type":"$set","path":["title"],
                            "payload":"renamed"}]}],
        "removed":["obj_b"]}]

: keepalive

event: closed
data: {"reason": "overflow"}
```

- **`ready`** — sent once after the SDK `Subscribe` call returns. Wait
  for it before treating the stream as live.
- **`snapshot`** — sent once, right after `ready`. `records` is the
  materialised window (bounded by `limit`/`offset`); `total` is the
  unbounded filter-matching count and `hasNext` reports whether more
  matches exist past this page (`offset+len(records) < total`). Both
  are present only when `includeTotal` was set in the request body.
- **`changes`** — JSON array of zero-or-more windowed events. Each
  event has `versionId` (per-change DAG order, locally-scoped — don't
  compare across peers) plus three buckets:
  - `added` — records that entered the visible window. Each carries
    the full `doc` plus the per-field `$set`/`$unset` ops that
    triggered the entry (a brand-new record's ops collapse to one
    multi-field `$set` at path `[]`).
  - `updated` — records already in the window whose state changed.
    Same `doc`+`ops` shape as `added`.
  - `removed` — array of ids that left the window. The wire does NOT
    distinguish *deleted* / *filter-rejected* / *displaced* (pushed
    past `limit`); all three look the same. From the consumer's view
    the action is the same: drop the id from local state. If you need
    to know which it was, query `…/query` with that id.

  An op's `path` is always a JSON array of dotted segments — never
  `null`. An empty array `[]` means the record root: on `$set`, the
  payload is then an object whose top-level keys are themselves
  dot-separated paths to assign at. `Wait` coalesces every event
  accumulated during the previous write into a single frame, so a
  slow client / network produces fewer, larger frames rather than
  head-of-line stalls.
- **`: keepalive`** — comment frame every 25s during silence; defeats
  idle middlebox timeouts.
- **`closed`** — terminal frame. Reasons:
  - `server_shutdown` — server got a signal or `POST /v1/shutdown`.
  - `sdk_closed` — the SDK released the subscription channel (space
    or SDK closed).
  - `overflow` — the per-subscriber mailbox filled before the consumer
    drained it. The SDK closes the sub rather than dropping events —
    resubscribe to get a fresh snapshot.
  - `drifted` — more than `driftBudgetPercent` of the held window left
    without replacements, and the engine refuses to re-query on the
    hot path. Resubscribe.

  All four reasons mean "the stream is over; if you want live state,
  open a new POST." Recovery is identical for `overflow` and `drifted`
  — the reason is split only so clients can log/backoff sensibly.

Subscriptions deliver events from registration onward only — there is
no replay. The bundled `snapshot` frame is the only point-in-time read.
There is no SSE `id:` — clients fence-and-replay on `versionId` if
they want at-least-once semantics across reconnects.

### Types

| Method | Path                                                          | Purpose                |
|--------|---------------------------------------------------------------|------------------------|
| GET    | `/v1/spaces/:spaceId/types`                                   | `TypesAPI.List`        |
| POST   | `/v1/spaces/:spaceId/types`                                   | `TypesAPI.Create`      |
| GET    | `/v1/spaces/:spaceId/types/:typeId`                           | `TypesAPI.Get`         |
| DELETE | `/v1/spaces/:spaceId/types/:typeId`                           | `TypesAPI.Delete`      |
| GET    | `/v1/spaces/:spaceId/types/:typeId/properties`                | `TypesAPI.Properties`  |
| POST   | `/v1/spaces/:spaceId/types/:typeId/properties`                | `TypesAPI.AddProperty` |
| DELETE | `/v1/spaces/:spaceId/types/:typeId/properties/:propId`        | `TypesAPI.RemoveProperty` |
| PATCH  | `/v1/spaces/:spaceId/types/:typeId/properties/:propId`        | `TypesAPI.UpdatePropertyMeta` |

### Properties (values on objects)

| Method | Path                                                          | Purpose                          |
|--------|---------------------------------------------------------------|----------------------------------|
| GET    | `/v1/spaces/:spaceId/properties/:objectId`                    | `PropertiesAPI.Get`              |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/base/:typeId`       | `PropertiesAPI.SetBase`          |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/account/:typeId`    | `PropertiesAPI.SetAccount`       |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/device/:typeId`     | `PropertiesAPI.SetDevice`        |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/attach/:typeId`     | `PropertiesAPI.AttachType`       |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/detach/:typeId`     | `PropertiesAPI.DetachType`       |

Runtime type binding (`attach` / `detach`) is still `501
sdk.not_implemented` — bind types at object-create time via the `types`
array on `POST /v1/spaces/:spaceId/objects`. See `08-clients.md`
§ "Preflight-validate writes against the bound types".

### Chat (built-in `chat` type)

| Method | Path                                                                     | Purpose                  |
|--------|--------------------------------------------------------------------------|--------------------------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages`                         | send a message           |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId`                  | edit own message text    |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId`                  | delete own message       |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions/:emoji` | toggle own reaction      |

Liveness goes through the per-object query/subscribe endpoint with
`dataset=chat_messages`:

```
POST /v1/spaces/:spaceId/query/subscribe
{ "objectId": "<chatObjectId>",
  "dataset":  "chat_messages",
  "sort":     ["-_ver.id"],
  "limit":    50 }
```

Subscribe descending (`-_ver.id`) with a `limit`: the window holds the
*newest* `limit` messages, so new arrivals enter it (oldest drops out as
`removed`). Ascending would pin the oldest `limit` and new messages would
never appear. Always set a `limit` — an unbounded subscribe risks
overflowing the mailbox.

New incoming messages arrive in `added`; edits in `updated`; deletes
and reactions toggling off in `removed`. `added.doc` carries the full
message body — no follow-up GET needed. See `04-events.md`.

#### Message wire shape (read path)

This is what `POST /query` and `/query/subscribe` return for a
`chat_messages` record. The write endpoints (send / edit / delete /
react) do NOT return this — they return the shared write result
`{versionId, changeId, recordIds}` (see § Write responses); the message
body is always read back through the query path.

```json
{
  "id":               "<change-derived id>",
  "creator":          "<accountId>",
  "createdAt":        1714597200,
  "modifiedAt":       1714597200,
  "replyToMessageId": "<msgId>",
  "fromAgent":        "<opaque identity>",
  "text":             "**hi** _there_",
  "attachments": {
    "a1": { "type": "link",  "link": "any://abc/def" },
    "a2": { "type": "image", "link": "https://example.com/x.png" }
  },
  "reactions":        { "👍": { "<id1>": 1714597200, "<id2>": 1714597205 } }
}
```

`createdAt` and `modifiedAt` are unix-seconds, server-stamped. They
are equal on a never-edited message — clients detect edits by
comparing them. `text` is markdown; rendering is the client's
problem (`internal/markdown` exists if anyone wants to round-trip).

`fromAgent` is an optional, opaque, create-only tag the sender sets to
mark the message as written by an agent acting on the signer's behalf
(vs typed by the signer directly). It is NOT cryptographically
verified — `creator` is still the change signer; `fromAgent` is a UI
hint. Typical use: an agent subscribed to `chat_messages` ignores its
own messages (`fromAgent` non-empty) and only responds to human ones
(`fromAgent` empty). Omitted from responses when unset.

`attachments` is an optional, create-only map keyed by short opaque
ids (1–64 chars, `[A-Za-z0-9_-]+`); each entry is `{type, link}`.
`type` is an open enum — known values are `"link"` and `"image"`, but
clients should fall back to rendering `link` as a plain anchor for
unknown types rather than dropping the entry. `link` is ≤ 2 KiB. Up
to 32 attachments per message. Immutable post-create — the handler
rejects $set on the attachments path.

`reactions` ships on the wire in the same shape it has in storage:
emoji → `{accountId: <changeTimestamp>}`, where the leaf timestamp is
when that identity added the emoji. This is identical to what `/query`
and `/query/subscribe` return for the record, so a client parses
`reactions` exactly one way regardless of which endpoint produced it
(clients sort by the leaf timestamp themselves if they want arrival
order). Authorization on writes is a single path-segment compare
against `ctx.Change.Creator` in the handler: only the change's signer
can write into `reactions.<emoji>.<their-identity>`. The leaf timestamp
is server-derived (`sink.Derive` overrides whatever the client sent).
See `internal/chat/handler.go`.

#### Send

`POST /v1/spaces/:spaceId/objects/:objectId/chat/messages`

```json
{ "text": "hello", "replyToMessageId": "abc", "fromAgent": "agent-alice" }
```

`text` is required, ≤ 32 KiB. `replyToMessageId` is optional, ≤ 256
bytes, and a soft reference — the server doesn't validate that the
target exists. `fromAgent` is optional, ≤ 256 bytes, non-empty when
present; immutable post-create. Returns 201 with the shared write
result `{versionId, changeId, recordIds}` — `recordIds[0]` is the
server-derived message id. Read the message back via the query path
above.

#### Read

The bespoke list endpoint is gone — reads go through the per-object
query primitive with `dataset=chat_messages`:

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<chatObjectId>",
  "dataset":  "chat_messages",
  "sort":     ["-_ver.id"],
  "limit":    50 }
```

Sort descending (`-_ver.id`) with a `limit` — that loads the *newest*
page first, the usual chat default. `_ver.id` is the record's `VersionId`
at creation — its position in the any-sync DAG (lex-monotonic, per-change
DAG order) — so sorting by it is logical DAG order, not wall-clock time.
It's stamped once at creation and never bumped by edits, so that order is
stable across edits; only the read direction flips.
Page backward into history as a client-side two-step: take the oldest
`_ver.id` from the page you have, then chain a second query with the same
sort and `filter: {"_ver.id": {"$lt": <id>}}` (older messages). Always
set a `limit` so a long history can't produce a huge response; reverse
each page client-side for oldest-at-top display. The bespoke endpoint's
`before` / `after` / `limit` flags moved off the API surface; the recipe
replaces them. See `08-clients.md` for the full read/write recommendations.

Reactions on queried records ship as
`reactions.<emoji>.<accountId> = <timestamp>` (server-derived) — the
same shape the bespoke send / edit / react responses return, so there
is nothing to transpose between the read and write paths.

#### Edit / delete (own only)

`PATCH .../chat/messages/:msgId` body `{ "text": "..." }` replaces the
text and bumps `modifiedAt`. `DELETE .../chat/messages/:msgId` tombstones
the record. Both return `200` with the shared write result
`{versionId, changeId, recordIds}` (`recordIds=[msgId]`), `403
chat.not_author` for non-authors, and `404 chat.not_found` for unknown
ids. The handler enforces the same rules for peer-originated changes.

#### React (toggle)

`POST .../chat/messages/:msgId/reactions/:emoji` (no body) toggles the
caller's reaction. The CRDT op is `$set` (add) or `$unset` (remove)
on the leaf `reactions.<emoji>.<callerId>`; the value on add is the
triggering change's timestamp, server-derived. Because the leaf is
unique per (emoji, identity), two clients toggling at the same time
can't corrupt each other. Returns `200` with the shared write result
`{versionId, changeId, recordIds}` (`recordIds=[msgId]`); read the
updated `reactions` back via the query path.

### Agent data layer (built-in `agent_log` + `agent_memory` types)

Full model, record shapes, and query recipes in
[`docs/11-agent-memory.md`](11-agent-memory.md). Writes only here —
reads + liveness go through `/query` and `/query/subscribe` with
`dataset` ∈ `{agent_turns, agent_chunks, agent_memory_items}`.

| Method | Path | Purpose |
|--------|------|---------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/agent/turns`  | append one immutable turn record |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/agent/chunks` | create one immutable summary chunk |
| GET    | `/v1/spaces/:spaceId/agent/brain`                    | deterministic brain object id |
| POST   | `/v1/spaces/:spaceId/agent/memory`                   | create a memory item |
| PATCH  | `/v1/spaces/:spaceId/agent/memory/:itemId`           | evolve mutable fields (author only) |
| DELETE | `/v1/spaces/:spaceId/agent/memory/:itemId`           | delete a memory item (author only) |

Turns and chunks attach to the chat object itself (the `agent_log`
type is attached on first write, making the object multitype chat +
agent_log); a chunk's `fromSeq`/`toSeq` are explicit pointers to the
raw `agent_turns` range it summarizes. Memory items live on the
per-space brain object — the server resolves it internally on writes;
clients call `GET /agent/brain` once to learn the objectId for reads.
All writes return the shared write result `{versionId, changeId,
recordIds}`. Errors use the `agent.*` code namespace
(`docs/06-errors.md`).

### Members

| Method | Path                                                 | Purpose                            |
|--------|------------------------------------------------------|------------------------------------|
| GET    | `/v1/spaces/:spaceId/members`                        | `MembersAPI.List`                  |
| GET    | `/v1/spaces/:spaceId/members/me`                     | `MembersAPI.Me`                    |
| GET    | `/v1/spaces/:spaceId/members/requests`               | `MembersAPI.JoinRequests`          |
| GET    | `/v1/spaces/:spaceId/members/subscribe`              | `MembersAPI.Subscribe` (SSE)       |
| GET    | `/v1/spaces/:spaceId/members/:identity`              | `MembersAPI.Get`                   |

Static path segments (`/me`, `/requests`, `/subscribe`) are registered
before the `:identity` wildcard so they don't get swallowed. The `Member` wire
shape mirrors `space.Member` 1:1; both `permission` and `status` are
strings (see "Permission / status strings" below). `requestRecordId`
is non-empty only on a pending-request entry — pass it to
`POST /v1/spaces/:id/acl/accept`.

```json
// GET /v1/spaces/:id/members
{
  "members": [
    { "identity":"A6ux…",
      "permission":"owner",
      "status":"active",
      "name":"Alice",
      "iconCid":"…" }
  ]
}
```

### Invites

| Method | Path                                                 | Purpose                            |
|--------|------------------------------------------------------|------------------------------------|
| POST   | `/v1/spaces/:spaceId/invites`                        | `ACL.CreateInvite` — replaces any prior invite |
| GET    | `/v1/spaces/:spaceId/invites`                        | `MembersAPI.Invites`               |
| DELETE | `/v1/spaces/:spaceId/invites`                        | `ACL.RevokeAllInvites`             |
| DELETE | `/v1/spaces/:spaceId/invites/:recordId`              | `ACL.RevokeInvite`                 |
| POST   | `/v1/spaces/join`                                    | `Service.Join` — body carries the share token |

Mint:

```json
// POST /v1/spaces/:id/invites
// → 201
{ "spaceId":"bafyrei…", "inviteToken":"5ZHbdx…" }
```

`inviteToken` is a base58-packed `(spaceId, invitePrivKey)` produced
by `space.EncodeInvite`. Owners share this string out-of-band; joiners
pass it back verbatim:

```json
// POST /v1/spaces/join
{ "inviteToken":"5ZHbdx…",
  "metadata":{ "name":"Bob","iconCid":"…" } }
// → 202 {SpaceInfo}      (RequestToJoin: status="joining" until owner accepts)
// → 201 {SpaceInfo}      (AnyoneCanJoin: deferred — never returned in v1)
```

A malformed or unrecognized `inviteToken` returns `400 invite.invalid`.

In the v1 RequestToJoin flow `Service.Join` returns 202: the SDK has
posted the join request, written a `joining` index entry, and the
joiner now polls `GET /v1/spaces/:id/members/me` for the status flip
to `active` after the owner accepts.

Listing returns one entry per active invite record — pass `recordId`
to the DELETE path to revoke a single invite, or DELETE the parent
collection to revoke all in one batch.

### ACL operations

| Method | Path                                                 | Purpose                                    |
|--------|------------------------------------------------------|--------------------------------------------|
| POST   | `/v1/spaces/:spaceId/acl/accept`                     | `ACL.AcceptRequest` — grants permission    |
| POST   | `/v1/spaces/:spaceId/acl/decline`                    | `ACL.DeclineRequest`                       |
| POST   | `/v1/spaces/:spaceId/acl/permissions`                | `ACL.ChangePermissions` — batched          |
| POST   | `/v1/spaces/:spaceId/acl/remove`                     | `ACL.RemoveAccounts` — rotates read key    |
| POST   | `/v1/spaces/:spaceId/acl/add`                        | `ACL.AddAccounts` — server-side flow       |
| POST   | `/v1/spaces/:spaceId/acl/ownership`                  | `ACL.OwnershipChange`                      |
| POST   | `/v1/spaces/:spaceId/acl/self-remove`                | `ACL.RequestSelfRemove`                    |
| POST   | `/v1/spaces/:spaceId/acl/cancel-join`                | `ACL.CancelJoinRequest`                    |
| POST   | `/v1/spaces/:spaceId/acl/stop-sharing`               | `ACL.StopSharing` — drops everyone, rotates|

Bodies (every successful op returns `204 No Content`):

```json
// POST /v1/spaces/:id/acl/accept
{ "requestRecordId":"bafy…", "permission":"writer" }

// POST /v1/spaces/:id/acl/decline
{ "identity":"A6ux…" }

// POST /v1/spaces/:id/acl/permissions
{ "changes":[
  { "identity":"A6ux…", "permission":"reader" },
  { "identity":"BcdE…", "permission":"admin"  }
] }

// POST /v1/spaces/:id/acl/remove
{ "identities":[ "A6ux…", "BcdE…" ] }

// POST /v1/spaces/:id/acl/add
{ "accounts":[
  { "identity":"A6ux…", "permission":"writer",
    "metadata":{"name":"Alice"} }
] }

// POST /v1/spaces/:id/acl/ownership
{ "newOwner":"A6ux…", "oldOwnerPerm":"admin" }
```

`self-remove`, `cancel-join`, and `stop-sharing` take no body.

#### Permission / status strings

| Wire string | `space.Permission` |
|-------------|--------------------|
| `none`      | `PermissionNone`   |
| `reader`    | `PermissionReader` |
| `guest`     | `PermissionGuest`  |
| `writer`    | `PermissionWriter` |
| `admin`     | `PermissionAdmin`  |
| `owner`     | `PermissionOwner`  |

| Wire string | `space.MemberStatus`     |
|-------------|--------------------------|
| `unknown`   | `MemberStatusUnknown`    |
| `joining`   | `MemberStatusJoining`    |
| `active`    | `MemberStatusActive`     |
| `removed`   | `MemberStatusRemoved`    |
| `declined`  | `MemberStatusDeclined`   |
| `removing`  | `MemberStatusRemoving`   |
| `canceled`  | `MemberStatusCanceled`   |

Unknown values on the wire return `400 request.schema`. Member-event
SSE is **not** wired in v1 — clients refresh by re-`GET`-ing the
collection after a write.

### Sync status

| Method | Path                                                              | Purpose                                    |
|--------|-------------------------------------------------------------------|--------------------------------------------|
| GET    | `/v1/spaces/:spaceId/sync-status`                                 | `Space.SyncStatus().Space()`               |
| GET    | `/v1/spaces/:spaceId/sync-status/objects/:objectId`               | `Space.SyncStatus().Object`                |
| GET    | `/v1/spaces/:spaceId/sync-status/objects/:objectId/subscribe`     | per-object SSE (state-flip stream)         |
| GET    | `/v1/sync-status/subscribe`                                       | account-wide SSE — every space's rollup    |
| GET    | `/v1/spaces/:spaceId/sync-status/peers`                           | **501** until the SDK lands per-space peer list (use `/debug` for diagnostic equivalent) |

The two GETs are cheap; safe to call on a render tick. `state` is one
of `unknown` / `offline` / `syncing` / `synced` / `error`. Unknown
object ids return `{state: "unknown"}` rather than 404 — the SDK is
forgiving here, callers that need existence checks should use the
object catalog.

```json
// GET /v1/spaces/:spaceId/sync-status
{ "spaceId":      "spc_…",
  "state":        "syncing",
  "synced":       1,
  "total":        3,
  "networkPeers": 0,
  "lastSyncedAt": "0001-01-01T00:00:00Z" }

// GET /v1/spaces/:spaceId/sync-status/objects/:objectId
{ "objectId":   "obj_…",
  "state":      "synced",
  "lastSyncAt": "2026-05-15T12:00:00Z" }
```

The two `/subscribe` endpoints are SSE streams. Wire shape and
lifecycle are documented in `04-events.md` § Sync-status streams —
short version: `event: ready`, then one `event: status` per state
transition (carrying the GET body), terminating with `event: closed`
on server shutdown. Account-wide subscribe lives outside the space
group because the SDK call is account-scoped — one stream covers
every known space.

### Debug (diagnostic)

| Method | Path                                                 | Purpose                                |
|--------|------------------------------------------------------|----------------------------------------|
| GET    | `/v1/spaces/:spaceId/debug`                          | `Space.Debug().Space()`                |
| GET    | `/v1/spaces/:spaceId/debug/objects/:objectId`        | `Space.Debug().Object`                 |

**Diagnostic only — not a stable interface.** The SDK's `DebugAPI` is
explicitly tagged as "fields and methods may grow or move"; this
mirror inherits the same churn. Production UI should use
`/sync-status` instead (501 until the SDK lands it).

`GET /v1/spaces/:spaceId/debug` returns the per-space outbound
headsync counters since boot (in-memory; resets on every server
restart). `peers` is `[]` until at least one diff round has run
against a responsible node.

```json
{
  "spaceId": "spc_…",
  "peers": [
    { "peerId":     "12D3Koo…",
      "lastSyncAt": "2026-05-15T12:00:00Z",
      "new":        2,
      "changed":    5,
      "lastErr":    "" }
  ]
}
```

`GET /v1/spaces/:spaceId/debug/objects/:objectId` returns a joint-
consistent snapshot of one object's tree + sync state. The read
locks the object tree and walks every change — on a million-change
tree this can block local writes for a noticeable pause. **Not for
high-frequency polling.** First touch of a never-loaded object also
triggers a cold-restore.

```json
{
  "objectId":        "obj_…",
  "syncState":       "syncing",
  "pending":         ["head_a"],
  "lastSyncAt":      "2026-05-15T12:00:00Z",
  "heads":           ["head_a"],
  "headsCount":      1,
  "branchCount":     0,
  "treeLen":         3,
  "snapshots":       1,
  "latestVersionId": "01HX…",
  "maxAddSeq":       17
}
```

`syncState` is one of `unknown` / `offline` / `syncing` / `synced` /
`error`. `latestVersionId` is empty for root-only / transient cold-
restore states and is local to this peer — `VersionIds` are not
comparable across peers. `maxAddSeq` is the controller's
delivery-order watermark, surfaced as a sanity check against tree
length — it is not a cross-peer primitive.

## Body shapes (examples)

Query body / response are documented in § Data plane above.

**POST /v1/spaces/:spaceId/modify**

```json
{
  "objectId": "obj_abc",
  "dataset":  "notes",
  "records": [
    {
      "id":     "",
      "upsert": true,
      "ops": [
        { "type": "$set",       "path": "",     "value": { "title": "x" } },
        { "type": "$addToSet",  "path": "tags", "value": "idea" }
      ]
    }
  ],
  "traceIds": ["demo"]
}
```

Response: the shared write result `{versionId, changeId, recordIds,
rejections?}` — see § Write responses. `recordIds` mirrors the input
record order (`recordIds[0]` is the derived id for the empty-id upsert
above).

## Middleware

Minimal in v1:

- `middleware.Recover` — catch panics, return 500.
- `middleware.RequestID` — generate an id per request for the logs.
- **Logger middleware** wired to `any-sync/app/logger` — one line per
  request at info level (path, status, duration).
- `middleware.BodyLimit("1M")` — reject anything larger; prevents
  accidental uploads before the file API lands.

No CORS, no rate limiting, no auth middleware in v1.

## Pagination

Offset-based, mirroring the SDK. Cursor pagination is a future add.

## Idempotency

POST endpoints are **not** idempotent in v1 — each POST produces a new
DAG change. An `Idempotency-Key` header is a future add.
