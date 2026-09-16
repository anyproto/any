# Events / Subscriptions

Live reads go over Server-Sent Events. Dataset reads use the
**windowed query/subscribe** primitive: one POST opens a stream that
delivers an initial materialised snapshot plus per-change windowed
deltas (`added`/`updated`/`removed`). Request bodies and frame payloads
are in `03-api.md` § "Subscribe (Server-Sent Events)"; this file is the
contract clients must respect.

Every windowed endpoint wraps the SDK's `Query.Snapshot` /
`Query.Subscribe` 1:1 — same chained builder, same `QueryOpts`, same
`SubscriptionEvent` shape on the wire, same frames and `closed` reasons:

- `POST /v1/spaces/:id/objects/query/subscribe` — cross-object, over
  the per-space `objects` storage collection;
- `POST /v1/spaces/:id/query/subscribe` — one object's dataset;
- `POST /v1/spaces/:id/objects/:objectId/files/query/subscribe` — one
  object's file payload rows;
- `POST /v1/spaces/query/subscribe` — the account's space list, bound
  to `Service.Query(SpaceIndexObjectId(), "spaces")`. A space joined on
  another device or head-synced in surfaces as an `added` change. The
  stream carries the raw tech-index rows; `GET /v1/spaces` is the
  mapped `SpaceInfo` snapshot;
- `POST /v1/devices/query/subscribe` — the account's device registry
  (`23-devices.md`).

## Why SSE (not WebSocket)

- One-way is enough — the server pushes; the client never sends
  control frames into an open subscription.
- Plain HTTP/1.1 — works through any HTTP middleware, debuggable with
  `curl -N`, no upgrade handshake or framing to write.

The windowed subscribe is POST (the filter body doesn't fit a query
string), so the browser's `EventSource` doesn't apply — clients use
`fetch` with a streaming `ReadableStream` and parse SSE frames in
user-space. The CLI (`any query-subscribe`) and Go client
(`client.StreamQuerySubscribe`) do this for you.

## Contract that clients must respect

1. **The bundled snapshot is the only point-in-time read.** Events
   deliver from registration onward. There is no replay across
   reconnects — opening a new POST gives you a fresh snapshot frame.
2. **`closed` is terminal.** Reconnect after `closed`. Reasons:
   - `server_shutdown` — server is exiting (signal or `POST /v1/shutdown`).
   - `deauthorized` — the account behind the stream was torn down in
     place (`DELETE /v1/auth`, or a `POST /v1/auth` switch to another
     account) while the server stays up. Re-read `GET /v1/auth`
     before resubscribing: the server is unauthorized or serving a
     different account (`03-api.md` § Auth).
   - `sdk_closed` — SDK released the underlying subscription (space
     or SDK closed).
   - `overflow` — per-sub mailbox filled before the consumer could
     drain it. The SDK closes the sub rather than dropping events.
   - `drifted` — more than `driftBudgetPercent` of the held window
     left without replacements; the SDK refuses to re-query on the
     hot path. Resubscribe — the new snapshot reflects current state.

   Every reason means "open a fresh POST". `overflow` and `drifted`
   are split so clients can log and back off; recovery is identical.
3. **Wait for `ready` before treating the stream as live.** Errors
   that happen before the SDK Subscribe call returns surface as a
   regular JSON error envelope on the open response, not SSE.
4. **`snapshot` arrives once, right after `ready`.** Don't apply
   `changes` frames until the snapshot is integrated — the windowed
   engine guarantees the snapshot and the first event sit at adjacent
   versionIds with nothing missed between them.
5. **Events carry a windowed delta.** Each `changes` array element is
   `{versionId, added, updated, removed}`. `added` and `updated`
   carry the full post-apply `doc` plus the per-field `$set`/`$unset`
   ops the change ran; `removed` is a list of `{id, reason}` objects.
   `versionId` is the change's local DAG order — each peer assigns its
   own, so never compare it across peers. `_ver` travels only inside a
   record's `doc`; the batch and its ops carry none.
6. **`removed` carries a `reason`.** Each removed entry is
   `{"id":…, "reason":…}` where `reason` is one of:
   - `"deleted"` — the record was tombstoned; it no longer exists.
     Drop it from local state for good.
   - `"filtered-out"` — an update changed a field so the query's
     filter no longer matches. The record still exists.
   - `"displaced"` — a higher-priority arrival (or the record's own
     sort-key change) pushed it past `limit`. Still matches the
     filter; just outside the visible window.

   Branch on `reason == "deleted"` to drop the object for good. For
   `"filtered-out"` / `"displaced"` the object still exists — a fresh
   `POST …/query` (or `Snapshot`) would return it — so keep it in any
   model that spans beyond the current window.

   `"deleted"` is the ONE deletion signal on a stream, for both delete
   kinds: a record delete (the CRDT tombstone lands, the row stops
   matching the live filter) and an object delete (the row is purged
   and the SDK emits a synthetic removal with an **empty `versionId`**
   — act on it directly, never drop it through a version fence). The
   tombstone row itself is never streamed — not even with
   `includeDeleted`, which is snapshot-only and refused on
   `/subscribe` (`400 request.invalid_field`). To read tombstones (an
   id allocator finding the highest id ever used), take the snapshot
   flag: `docs/09-query.md` § Tombstones.

## Wire shape recap

```
event: ready
data: {}

event: snapshot
data: {"records":[…], "total":17, "hasNext":true}

event: changes
data: [{"versionId":"…",
        "added":[{"id":"…","doc":{…},"ops":[…]}],
        "updated":[{"id":"…","doc":{…},"ops":[…]}],
        "removed":[{"id":"id1","reason":"deleted"},
                   {"id":"id2","reason":"displaced"}]}]

: keepalive

event: closed
data: {"reason":"overflow"}
```

Wait for `ready`. Integrate `snapshot.records` (`total` / `hasNext`
are present only when the body set `includeTotal`). Apply each
subsequent `changes` batch to the local window: add new records,
update mutated ones, drop ids in `removed` (only `reason:"deleted"`
means the object is gone for good). A `: keepalive` comment arrives
every 25s while idle. On `closed`, reconnect with a fresh POST.

> **Projection applies to the whole stream.** The body's `projection`
> field (mongo grammar — [`docs/09-query.md` § Projection](09-query.md))
> shapes the `snapshot` frame AND every `added` / `updated` record in
> `changes`, per-field ops included, so a projected subscription never
> widens after the first update. `id` always ships and `_ver` narrows
> to match; `{"_ver": -1}` drops it. Under a projection `ops` can come
> back empty — nothing the client holds changed; `doc` stays
> authoritative. Without a `projection` every record ships its full
> anyenc form.

### Why no buffer-then-replay step?

The engine holds the limited window under its own lock and emits the
snapshot atomically with the registration. A windowed consumer's apply
loop is `add/update/remove` with no dedup, no buffering and no `_ver`
walk.

### Applying `added` / `updated` ops

Each `added` or `updated` record carries `doc` (the full post-apply
JSON) plus `ops` (the per-field `$set` / `$unset` ops from the
triggering change). Most consumers take `doc` and overwrite their
local entry. Consumers that merge fields into a richer local model
apply the ops:

- `$set` with `path: ["a","b"]`, `payload: V` — assign `V` to `a.b`.
- `$set` with `path: []` and an object payload — multi-field set: each
  payload key is a dotted path to assign (the shape a record create
  ships as).
- `$unset` with `path: ["a","b"]` — delete the key at `a.b`.
- `$unset` with `path: []` and an object payload — delete each dotted
  key; the values are placeholders. Record-level removal never arrives
  as an op — it is membership in `removed`.

`$inc` / `$addToSet` / `$pull` / `$incGated` are **not** emitted to
subscribers — the SDK collapses them to the equivalent `$set` of the
merged result before delivery, so non-Go clients don't reimplement the
CRDT.

## Datasets and how clients should consume them

All consumers run the same flow above; the dataset names below are the
`dataset` values to pass on the per-object endpoint
(`POST /v1/spaces/:id/query/subscribe`). For cross-object live views
use `POST /v1/spaces/:id/objects/query/subscribe` (no `dataset` —
implicitly the per-space `objects` storage collection).

- **`editor_blocks`** (or a namespaced editor storage collection) — per-object
  block tree. Events ship the full post-apply block in `doc`; clients
  update their tree directly. The same events fire whether the change
  came from `…/editor/:collection/blocks` or a
  `PUT …/editor/:collection/markdown` rewrite.
- **`chat_messages`** — per-object chat stream. One message per
  `added`. Reactions live at `reactions.<emoji>.<accountId>` so a
  reaction toggle arrives as a `$set`/`$unset` op inside an `updated`
  event with the parent message id.
- **`objects`** (per-space firehose) — one row per object in the
  space, holding computed property values. Object creation lands as
  `added`, property writes as `updated`, object deletion as `removed`
  with `reason:"deleted"` and an empty `versionId` (contract item 6).

### Known non-conforming consumers

The embedded debug UI (`internal/server/web/index.html`) re-fetches the
whole chat list on every `changes` frame instead of applying it, and
its object tree does not subscribe. **Don't copy the web UI as an
example.**

## Lifecycle / shutdown

Every stream runs inside the live engine's gate and selects on that
engine's context. An engine teardown — process exit (SIGINT/SIGTERM or
`POST /v1/shutdown`), `DELETE /v1/auth`, or a `replace` switch —
cancels the context first; streaming handlers emit their terminal
frame (`closed{server_shutdown}` on exit, `closed{deauthorized}` on a
logout / switch) and leave the gate. The teardown waits up to
`gracefulShutdownDeadline` (10s) for the gate to drain before closing
the SDK; on process exit `e.Shutdown` follows. A handler wedged on a
slow client write past the deadline gets cut off with the rest
(`02-server.md` § Startup, § Shutdown).

## Capacity

Per-subscriber mailbox capacity defaults to 256 (`mailboxCapacity` in
the request body; minimum 16). Drift budget defaults to 30% of the
window (`driftBudgetPercent`). Both close the subscription on exceed
with the matching `closed` reason — query/subscribe streams have no
`lagged` frame.

## Sync-status streams (a separate SSE primitive)

`/v1/sync-status/subscribe` and
`/v1/spaces/:id/sync-status/objects/:objectId/subscribe` are SSE
endpoints but they are **not** the dataset-backed query/subscribe
primitive documented above. State-flip events are sparse single
payloads coming off the SDK's `Service.SubscribeStatus` /
`SyncStatusAPI.SubscribeObject` callbacks — no mailbox window, no
CRDT records.

Frame set:

```
event: ready
data: {}

event: status
data: { …SpaceSyncStatus or ObjectSyncStatus body… }

event: lagged
data: { "total": <count> }              # only if the forwarder dropped events

: keepalive

event: closed
data: { "reason": "server_shutdown" }
```

`event: status` body shapes match the GET responses on
`/v1/spaces/:id/sync-status` and `/v1/spaces/:id/sync-status/objects/:id`
respectively — `state` is one of `unknown` / `offline` / `syncing` /
`synced` / `error`.

The per-stream forwarder uses a 16-deep buffered channel; overflow
drops the event and bumps a counter, surfaced as `lagged` (`total` =
events dropped so far on this stream) before the next successful
frame — re-GET the current state on `lagged`. A `: keepalive` comment
arrives every 25s while idle. `closed` is written only on an engine
teardown, so its reason is `server_shutdown` or `deauthorized` — the
shared reason set, so a client can use one switch for every stream.
The members, identities and file-status streams below run on this same
driver: same `ready` / `lagged` / keepalive / `closed` behaviour.

Mounting: the account-wide stream lives on `/v1/sync-status/subscribe`
(no `:spaceId`) because the SDK call is account-scoped — one callback
sees every known space's transitions on one stream. Per-object streams
stay under the space group for symmetry with the GET endpoints.

## Members stream (callback-based SSE)

`GET /v1/spaces/:id/members/subscribe` streams membership changes —
new members, permission/status flips, and removals — over SSE. The
source is the SDK's `Members().Subscribe` callback (polling the ACL
head every 250 ms, with an immediate kick on every ACL record add,
local or from peers).

Frame set:

```
event: ready
data: {}

event: member
data: { "kind": "added"|"changed"|"removed",
        "member": { …Member… },
        "previous": { …Member… } | null }

event: lagged
data: { "total": <count> }

event: closed
data: { "reason": "server_shutdown" }
```

`kind` values:

- **`added`** — identity appeared in the snapshot.
- **`changed`** — same identity, different state.
- **`removed`** — identity dropped from the snapshot.

`member` always carries the full post-event `Member` shape (same as
`GET /v1/spaces/:id/members/:identity`). `previous` is null on
`added`.

CLI: `any members subscribe <spaceId>`.

## Identities directory stream (callback-based SSE)

`GET /v1/identities/subscribe` streams changes to the account-global
identities directory — new contacts, profile resolutions, and removals —
over SSE. Account-scoped (no `:spaceId`). The source is the SDK's
`Identities().Subscribe` callback, which delivers batched
`IdentityListEvent`s.

Frame set:

```
event: ready
data: {}

event: identities
data: { "added":   [ { …IdentityInfo… } ],
        "updated": [ { …IdentityInfo… } ],
        "removed": [ "A5k…" ] }

event: lagged
data: { "total": <count> }

event: closed
data: { "reason": "server_shutdown" }
```

One `identities` frame per directory change batch; any of `added` /
`updated` / `removed` may be absent. `added` / `updated` carry the
full `IdentityInfo` (same shape as `GET /v1/identities/:identity`, no
decryption key); `removed` carries the account ids that left. A contact
whose profile key arrives after the initial sighting surfaces first in
`added` with an empty `name`, then again in `updated` once resolved.

CLI: `any identities subscribe`.

## File status stream (callback-based SSE)

`GET /v1/spaces/:id/files/subscribe` streams file durability
transitions — attach, backup progress/failure, pin completion, manual
retries — over SSE (docs/17-files.md § Durability states). The source
is the SDK's `Files().SubscribeStatus` callback. **Local transitions
only** — a remote device finishing a backup is visible via
`GET /files/:fileId/status` reads, not here.

Frame set:

```
event: ready
data: {}

event: status
data: { "fileId": "…", "objectId": "…",
        "state": "durable" | "inflight" | "limited",
        "cached": true,
        "attempts": 2, "lastErr": "…" }   // attempts/lastErr only while work is pending

event: lagged
data: { "total": <count> }

event: closed
data: { "reason": "server_shutdown" }
```

CLI: `any file subscribe <spaceId>`.

Live per-object file **lists** are a different stream: the windowed
query/subscribe primitive at
`POST /v1/spaces/:id/objects/:objectId/files/query/subscribe` (same
body and frame set as every `/query/subscribe`; rows are the cleartext
payload fields — see 03-api.md § Payload-row query / subscribe).

## Event bus stream (hub-based SSE)

`GET /v1/events/subscribe` streams the account-wide ephemeral event
bus (docs/21-events.md — envelope, scopes, filter grammar, event
types). Not an SDK dataset subscription: the source is the in-process
`eventHub` broadcaster behind `POST /v1/events`, fed for the network
scopes by the SDK pub/sub. **At-most-once, no snapshot** — only events
published after connect are delivered.

Filtering happens server-side via repeatable query params (`scope` /
`spaceId` / `type` exact-or-`x.*`-prefix / `target`) — AND across
dimensions, OR within one; no params = everything.

Frame set:

```
event: ready
data: {}

event: event
data: { "type": "ui.open_space", "scope": "device",
        "target": "…", "data": { … },
        "sender": { "identity": "A5k…", "self": true } }

: keepalive

event: closed
data: { "reason": "server_shutdown" | "deauthorized" | "overflow" }
```

No `lagged` frame: the hub gives each subscriber a 16-deep buffer and
**drops the subscriber on overflow** (terminal `closed{overflow}`,
reconnect for a fresh stream) — same recovery contract as the windowed
query/subscribe, same shared reason set.

Mounting: account-scoped, outside the space group like
`/sync-status/subscribe`. CLI: `any events subscribe`.
