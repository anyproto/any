# Events / Subscriptions

**Shipped in v1 over Server-Sent Events.** Live reads in v1 go through
the **windowed query/subscribe** primitive: one POST opens a stream
that delivers an initial materialised snapshot plus per-change windowed
deltas (`added`/`updated`/`removed`). The full wire format is
documented in `03-api.md` § "Subscribe (Server-Sent Events)"; this
file records the design rationale and the contract clients must
respect.

The three windowed endpoints — cross-object
(`…/objects/query/subscribe`), per-object (`…/query/subscribe`), and the
**account's space list** (`POST /v1/spaces/query/subscribe`) — all wrap
the SDK's `Query.Snapshot` / `Query.Subscribe` 1:1 — same chained
builder, same `QueryOpts`, same `SubscriptionEvent` shape on the wire.
The space-list one is bound to `Service.Query(SpaceIndexObjectId(),
"spaces")` instead of a regular space, but the frame set and `closed`
reasons are identical; a space joined on another device or head-synced
in surfaces as an `added` change. (`GET /v1/spaces` stays the mapped
`SpaceInfo` snapshot convenience — the subscribe stream carries the raw
tech-index rows.) The raw per-apply `Space.Subscribe` /
`Space.SubscribeProperties` endpoints were retired alongside the SDK;
their use cases collapse into "windowed query with no filter".

## Why SSE (not WebSocket)

We considered WebSocket, NDJSON, and SSE. SSE won on the v1 axis:

- One-way is enough — the server pushes; the client never sends
  control frames into the open subscription.
- Plain HTTP/1.1 — works through any HTTP middleware, debuggable with
  `curl -N`, no upgrade handshake or framing to write.
- WebSocket's main wins are bidirectional control frames (we don't
  need them in v1) and multiplexing many subscriptions on one
  connection (SSE-per-target is fine while we have a handful of
  consumers).

**One caveat**: the windowed subscribe is POST (the filter body
doesn't fit a query string), so the browser's `EventSource` doesn't
apply — clients use `fetch` with a streaming `ReadableStream` and
parse SSE frames in user-space. The CLI (`any query-subscribe`) and
Go client (`client.StreamQuerySubscribe`) do this for you.

If multiplexing or client control becomes load-bearing later,
`/v2/subscribe` over WebSocket is still on the table — additive to
the SSE endpoints, not a replacement.

## Contract that clients must respect

1. **The bundled snapshot is the only point-in-time read.** Events
   deliver from registration onward. There is no replay across
   reconnects — opening a new POST gives you a fresh snapshot frame.
2. **`closed` is terminal.** Reconnect after `closed`. Reasons:
   - `server_shutdown` — server is exiting (signal or `POST /v1/shutdown`).
   - `sdk_closed` — SDK released the underlying subscription (space
     or SDK closed).
   - `overflow` — per-sub mailbox filled before the consumer could
     drain it. The SDK closes the sub rather than dropping events.
   - `drifted` — more than `driftBudgetPercent` of the held window
     left without replacements; the SDK refuses to re-query on the
     hot path. Resubscribe — the new snapshot reflects current state.

   All four mean "open a fresh POST." `overflow` and `drifted` are
   semantically split so clients can log/backoff intelligently;
   recovery is identical.
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
   There is no `_ver` map on the wire — clients derive it from
   `versionId` if they care about fence-and-replay across reconnects.
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

Wait for `ready`. Integrate `snapshot.records` (and stash `total` /
`hasNext` if you asked for it via `includeTotal`). Apply each subsequent `changes` batch to the local
window: add new records, update mutated ones, drop ids in `removed`
(only `reason:"deleted"` means the object is gone for good).
On `closed`, reconnect with a fresh POST.

> **Projection is not implemented yet.** The body's `projection`
> field is parsed but ignored — every record ships its full anyenc
> form including `_ver` (and `_traces` / `_deletedAt` when present).
> Strip those fields client-side if you want a leaner local model.
> Tracked in `docs/07-roadmap.md`.

### Why no buffer-then-replay step?

The previous (now-retired) raw-stream primitive required the client to
subscribe first, buffer events, fetch a snapshot, and dedup the buffer
against per-field `_ver` stamps in the snapshot. That work is now done
inside the SDK — the engine holds the limited window under its own
lock and emits the snapshot atomically with the registration. A
windowed consumer's apply loop is `add/update/remove` with no dedup,
no buffering, no `_ver` walk.

### Applying `added` / `updated` ops

Each `added` or `updated` record carries `doc` (the full post-apply
JSON) plus `ops` (the per-field `$set` / `$unset` ops from the
triggering change). Most consumers just take `doc` and overwrite their
local entry. Consumers that want atomic field merges into a richer
local model (e.g. CRDT-on-top-of-CRDT, or "show what changed since
last frame") apply the ops:

- `$set` with `path: ["a","b"]`, `payload: V` — assign `V` to `a.b`.
- `$set` with `path: []` and an object payload — multi-field set at
  the record root (the wire shape new-record creates ship as).
- `$unset` with `path: ["a","b"]` — delete the key at `a.b`. `$unset`
  with `path: []` does not occur; record-level removal arrives as
  membership in `removed`.

`$inc` / `$addToSet` / `$pull` / `$incGated` are **not** emitted to
subscribers — the SDK collapses them to the equivalent `$set` of the
merged result before delivery. The wire is intentionally narrow so
non-Go clients don't reimplement CRDT.

## Datasets and how clients should consume them

All consumers run the same flow above; the dataset names below are the
`dataset` values to pass on the per-object endpoint
(`POST /v1/spaces/:id/query/subscribe`). For cross-object live views
use `POST /v1/spaces/:id/objects/query/subscribe` (no `dataset` —
implicitly the per-space `objects` collection).

- **`editor_blocks`** — per-object block tree. Events ship the full
  post-apply block in `doc`; clients update their tree directly. Same
  events fire whether the change came from a `PATCH /editor/blocks`
  call or a bulk `PUT /editor/markdown` rewrite.
- **`chat_messages`** — per-object chat stream. One message per
  `added`. Reactions live at `reactions.<emoji>.<accountId>` so a
  reaction toggle arrives as a `$set`/`$unset` op inside an `updated`
  event with the parent message id.
- **`objects`** (per-space firehose) — one row per object in the
  space, holding computed property values. Object creation lands as
  `added`, property writes as `updated`, deletes as `removed`. Object
  deletion writes a CRDT tombstone on the row before tearing down the
  tree, so the firehose's `removed` signal is the canonical
  cross-space delete observability.

### Known non-conforming consumers

The embedded debug UI (`internal/server/web/index.html`) used the
old EventSource-driven raw stream for chat liveness. It's currently
disabled — chat lists render as a static snapshot — pending a
fetch-streaming POST migration. The tree refetch-on-event stopgap is
also being rewritten as a windowed subscribe. **Don't copy the
current state of the web UI as an example.**

## Lifecycle / shutdown

The server cancels a per-process `shutdownCtx` on graceful teardown
(SIGINT/SIGTERM or `POST /v1/shutdown`); streaming handlers select on
it, emit `event: closed{server_shutdown}`, and drop a `streamsWG`
counter. `server.Run` waits up to `gracefulShutdownDeadline` (10s) for
that counter to drain before calling `e.Shutdown`. A handler wedged on
a slow client write past the deadline gets cut off with the rest of
the listener.

## Capacity

Per-subscriber mailbox capacity defaults to 256 (`mailboxCapacity` in
the request body; minimum 16). Drift budget defaults to 30% of the
window (`driftBudgetPercent`). Both close the subscription on exceed
with the matching `closed` reason — no in-band `lagged` signal in
v1, by design.

## Sync-status streams (a separate SSE primitive)

`/v1/sync-status/subscribe` and
`/v1/spaces/:id/sync-status/objects/:objectId/subscribe` are SSE
endpoints but they are **not** the dataset-backed query/subscribe
primitive documented above. State-flip events are sparse, per-call
single payloads coming off the SDK's `Service.SubscribeStatus` /
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

event: closed
data: { "reason": "server_shutdown" }
```

`event: status` body shapes match the GET responses on
`/v1/spaces/:id/sync-status` and `/v1/spaces/:id/sync-status/objects/:id`
respectively — `state` is one of `unknown` / `offline` / `syncing` /
`synced` / `error`. The `closed` reason set is shared with the
query/subscribe streams, so a client can use one switch for both
families.

The per-stream forwarder uses a small buffered channel (16 deep);
overflow drops the event and bumps a counter, surfaced as `lagged`
before the next successful frame. State transitions are sparse
enough that overflow is rare in practice.

Mounting: the account-wide stream lives on `/v1/sync-status/subscribe`
(no `:spaceId`) because the SDK call is account-scoped — one cb sees
every known space's transitions on one stream. Per-object streams
stay under the space group for symmetry with the GET endpoints.

## Members stream (callback-based SSE)

`GET /v1/spaces/:id/members/subscribe` streams membership changes —
new members, permission/status flips, and removals — over SSE. The
source is the SDK's `Members().Subscribe` callback (polling the ACL
head at ~250 ms, with an immediate kick on local ACL record writes).

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
`added`. The forwarder uses the same small buffered channel (16 deep)
and overflow-to-`lagged` pattern as sync-status.

CLI: `any members subscribe <spaceId>`.

## Identities directory stream (callback-based SSE)

`GET /v1/identities/subscribe` streams changes to the account-global
identities directory — new contacts, profile resolutions, and removals —
over SSE. Account-scoped (no `:spaceId`). The source is the SDK's
`Identities().Subscribe` callback, which delivers batched
`IdentityListEvent`s; the handler reuses the same callback→channel bridge
and 16-deep overflow-to-`lagged` forwarder as the members/sync-status
streams.

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
`updated` / `removed` may be empty/absent. `added` / `updated` carry the
full `IdentityInfo` (same shape as `GET /v1/identities/:identity`, no
decryption key); `removed` carries the account ids that left. A contact
whose profile key arrives after the initial sighting surfaces first in
`added` with an empty `name`, then again in `updated` once resolved.

CLI: `any identities subscribe`.

## File status stream (callback-based SSE)

`GET /v1/spaces/:id/files/subscribe` streams file durability
transitions — attach, backup progress/failure, pin completion, manual
retries — over SSE (docs/17-files.md § Durability states). The source
is the SDK's `Files().SubscribeStatus` callback, bridged through the
same 16-deep overflow-to-`lagged` forwarder as the
members/sync-status streams. **Local transitions only** — a remote
device finishing a backup is visible via `GET /files/:fileId/status`
reads, not here.

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

## Open / future

- **Resume from a versionId cursor.** When the SDK supports replay
  from a per-record `versionId`, we can plumb that through as
  `?since=<versionId>` on the subscribe URL, with the server replaying
  changes whose versionId sorts after `since` before transitioning to
  live events.
- **Server-driven lagged signal on query/subscribe.** The windowed
  stream closes on overflow rather than emitting a `lagged` frame; a
  future variant could grow a soft signal for clients that want to
  ride out short bursts without resubscribing.
- **WebSocket multiplex.** If a consumer wants many subscriptions per
  connection, `/v2/subscribe` over WS becomes the right answer.
  Additive — SSE endpoints stay.
