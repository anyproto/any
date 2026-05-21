# Events / Subscriptions

**Shipped in v1 over Server-Sent Events.** Two endpoints, mirroring the
SDK's `Space.Subscribe(objectId, dataset)` and `Space.SubscribeProperties()`
1:1. The full wire format is documented in `03-api.md` § "Subscribe
(Server-Sent Events)"; this file records the design rationale and the
contract clients must respect.

## Why SSE (not WebSocket)

We considered WebSocket, NDJSON, and SSE. SSE won on the v1 axis:

- One-way is enough — `Event{SpaceId, ObjectId, Dataset, VersionId, Records}`
  flows server → client; the client never pushes back into the
  subscription.
- Plain HTTP/1.1 — works through any HTTP middleware, debuggable with
  `curl -N`, no upgrade handshake or framing to write.
- Auto-reconnect comes built-in to browser EventSource, which lines up
  with the SDK's "re-Query on (re)connect for cold state" contract.
- WebSocket's main wins are bidirectional control frames (we don't
  need them in v1) and multiplexing many subscriptions on one
  connection (SSE-per-target is fine while we have a handful of
  consumers).

If multiplexing or client-side control frames become load-bearing
later, `/v2/subscribe` over WebSocket is still on the table — additive
to the SSE endpoints, not a replacement.

## Contract that clients must respect

1. **Events deliver from registration onward.** There is no replay.
   On (re)connect, query for cold state separately if you need it.
2. **`lagged` means re-Query.** When the SDK drops events for a slow
   consumer (per-subscriber mailbox cap = 64 events, drop-on-overflow
   policy), the server emits `event: lagged` before the next
   `changes` frame. The dropped events are gone. Treat `lagged` as a
   prompt to re-Query the affected dataset; in-stream events are no
   longer a full picture.
3. **Wait for `ready` before treating the stream as live.** Errors
   that happen before the SDK Subscribe call returns surface as a
   regular JSON error envelope, not SSE.
4. **`closed` is terminal.** Reconnect after `closed`; treat
   `closed{server_shutdown}` and `closed{sdk_closed}` as transient.
   A bare EOF without a `closed` frame is a transport problem (also
   reconnect, but log it).
5. **Events carry a projected delta.** Each `Event` is `(spaceId,
   objectId, dataset, versionId, records[])`. `versionId` is the
   per-change DAG order — clients dedup buffered events against the
   `_ver` stamps in their queried snapshot. `records[]` is the
   post-apply effect of the change projected to a flat list of
   `$set` / `$unset` ops per record (the SDK has already merged
   with full CRDT semantics, so callers without a CRDT engine apply
   `records[].ops` directly to a JSON-shaped local copy). A record
   with `deleted: true` means drop the id; ops is empty. The SDK's
   internal `AddSeq` (local-receive counter) is deliberately not on
   the wire — versionId is the cross-peer primitive.

## Client recipe: subscribe → collect → query → apply

This is the **only correct order** to build a live view on top of a
`(spaceId, objectId, dataset)` triple — or the per-space firehose, or the
properties subscription. Subscribing after the snapshot leaves a gap
between the read and the first event; querying after `ready` and
discarding events that arrive in between has the same gap going the
other way. The recipe closes both.

### Why this works

Each event carries `versionId` — the per-change DAG order (a lexid)
that the SDK stamps the change with. On apply, the SDK stamps each
touched **field path** on the record with that versionId, inside the
record's `_ver` map:

- A `$set` / `$unset` at path P writes `_ver.<P> = versionId`.
- A delete (tombstone) writes the change's versionId to `_ver.*` (the
  default key — applies to every path not explicitly enumerated).
- `_ver.id` is the **creation marker** — set once when the record is
  created, only ever *lowered* if a tombstone arrives with a strictly
  smaller versionId. It does **not** move on edits and cannot be used
  to dedup them.

A snapshot row's `_ver.<P>` is therefore the last versionId the
snapshot absorbed at path P. So:

- An event op at path P is **already covered** by the snapshot iff
  `event.versionId ≤ snapshot._ver.<P>` (lexid string compare).
- A `deleted: true` event is **already covered** iff the id is absent
  from the snapshot (tombstones aren't typically returned by Query),
  or — if you explicitly include tombstones — `event.versionId ≤
  snapshot._ver.*`.

Subscribing first ensures no change between query-time and live-mode
goes missing. Per-op-path deduping by versionId ensures we don't
double-apply a change that the snapshot already absorbed.

Resolving `_ver.<P>` walks the `_ver` tree segment by segment, falling
back to the closest `*` (default) key when a segment is missing. The
SDK uses the same algorithm internally — see
`internal/crdt/versions.go::GetRecordVersion` in any-sync-sdk — and
clients can mirror it in a few lines of map walking.

### Steps

1. **Subscribe.** Open `GET …/subscribe?dataset=…`. Start an in-memory
   buffer of `changes` events.
2. **Wait for `ready`.** Before this, errors arrive as a JSON envelope
   on the open response — handle that path. After this, the SDK has
   registered the subscription; every committed change from here on
   will be delivered (or counted in `Subscription.Dropped` and surfaced
   as `lagged`).
3. **Snapshot.** Query for cold state: `GET …/editor/blocks` for a
   block tree, `POST …/objects/query` for the per-space firehose,
   `POST …/query` for a per-object dataset, etc. Each row's `_ver`
   map is the per-field high-water — keep it alongside the payload
   fields for dedup.
4. **Replay the buffer.** For each event collected while the query was
   in flight, for each record in `event.records`:
   - If `deleted: true`: drop the op if the id is absent from the
     snapshot (already gone); otherwise remove the id from local
     state. If you explicitly track tombstones, compare against
     `snapshot[id]._ver.*` and drop when `event.versionId ≤` it.
   - Otherwise, for each op in `record.ops`: resolve
     `snapshot[id]._ver.<op.path>` (walk segments, fall back to the
     closest `*` default key). If
     `event.versionId ≤ snapshot[id]._ver.<op.path>`, drop the op —
     the snapshot already covers it. Otherwise apply the op and
     write `_ver.<op.path> = event.versionId` in local state.
5. **Go live.** Apply each subsequent `changes` frame's ops directly to
   local state — same loop as step 4, just running against the live
   feed instead of the buffer.
6. **On `lagged`.** Treat the buffered/streamed events as no longer a
   full picture. Discard the local state for the affected dataset (or
   mark it stale) and restart at step 3.
7. **On `closed`.** Terminal. Reconnect and restart at step 1. The
   reason field (`server_shutdown`, `sdk_closed`) is for logging /
   backoff hints; both are transient.

### Applying ops

`record.ops` is a flat list of `$set` / `$unset` ops the SDK projected
*after* CRDT merge. A thin client without a CRDT engine applies them
naively to a JSON-shaped local copy.

`path` is **always a JSON array** of dotted segments — never `null`.
An empty array `[]` means the record root. So:

- `$set` with `path: ["a","b"]`, `payload: V` — assign `V` to `a.b`.
- `$set` with `path: []` and an object payload — multi-field set at the
  record root: each top-level key in `payload` is itself a
  dot-separated path, each value is what to assign there. (This is the
  shape new-record creates ship as: one op carrying every initial
  field plus the SDK's `_ver.id` stamp.)
- `$unset` with `path: ["a","b"]` — delete the key at `a.b`. `path: []`
  on `$unset` does not occur on the wire — record-level removal arrives
  as `deleted: true` with `ops` empty.
- Record with `deleted: true` — drop the id from local state; `ops`
  is empty.

`$inc` / `$addToSet` / `$pull` / `$incGated` are **not** emitted to
subscribers — the SDK collapses them to the equivalent `$set` of the
merged result before delivery. The wire is intentionally narrow so
non-Go clients don't reimplement CRDT.

Auto-stamped fields ship as ordinary `$set` ops alongside the
caller's payload — handler-derived stamps (chat: `creator` /
`createdAt` / `modifiedAt`; properties: `author` / `createdAt` /
`spaceId`) and the SDK's own `_ver.id` creation marker all arrive in
the same `record.ops` slice on the create event. A viewer
reconstructing a fresh record applies them the same way as user
fields; there is no second-class wire form for auto fields. Per-path
`_ver.<P>` stamps for the user fields themselves are still NOT on
the wire — clients write `_ver.<op.path> = event.versionId` locally
when they apply each op, per the recipe above.

### Pseudo-code

```
events = []        // buffered until snapshot lands
state  = {}        // id -> {record fields, _ver: {id, "<field>": <versionId>, ...}}

stream = open_sse(subscribe_url)
for frame in stream:
    if frame.event == "ready":  break
    if frame.event == "closed": fail("closed before ready")
// arm: buffer everything from now until snapshot returns
async for frame in stream:
    if frame.event == "changes": events.append(frame.data)
    elif frame.event == "lagged": mark_lagged()

snapshot = query(...)             // GET /editor/blocks, POST /query, ...
for row in snapshot.records:
    state[row.id] = row            // _ver map already on the row

for evt in events:                 // flush buffer
    apply(evt, state, dedup=True)
events = []

// live mode
for frame in stream:
    match frame.event:
        case "changes":  apply(frame.data, state, dedup=False)
        case "lagged":   discard(state); goto snapshot
        case "closed":   reconnect(); goto stream
```

`apply` is one function in both phases; the only difference is whether
to dedup against the snapshot's `_ver` map. Dedup is per op path:
resolve `state[id]._ver.<op.path>` and drop the op when
`event.versionId ≤` it. Applied ops write the event's versionId back
into `state[id]._ver.<op.path>`, so the same comparison keeps working
in live mode against late-arriving duplicates.

### Gotchas

- **Don't query first.** Any change committed between your snapshot
  read and the SDK Subscribe call is gone — there's no replay.
- **Don't apply during the snapshot.** Buffer until the snapshot
  lands. Applying live events to a partial local state during the
  query risks ordering paradoxes (a `$unset` for a field the snapshot
  hasn't loaded yet, etc.).
- **`versionId` is a lexid string, not a number.** Compare with byte
  ordering (`==`, `<`, `>`), not numeric.
- **Multi-record events.** The per-space firehose (`dataset=objects`)
  and properties subscription emit events whose `records[]` covers
  several ids per change — dedup per record, then per op-path within
  each record.
- **Dedup is per op path, not per record.** `_ver.id` is the creation
  marker — set once at create, only lowered on delete — so comparing
  `event.versionId ≤ _ver.id` would treat every edit as new even when
  the snapshot already absorbed it. Resolve `_ver.<op.path>` for each
  op instead (the walk falls back to the closest `*` default key when
  a segment is missing).
- **Snapshots aren't transactional across rows.** Different rows may
  reflect changes that landed at different times. The buffer-replay
  step handles this: any change that landed mid-query shows up on the
  stream and gets deduped against whichever row already saw it.

## Lifecycle / shutdown

The server cancels a per-process `shutdownCtx` on graceful teardown
(SIGINT/SIGTERM or `POST /v1/shutdown`); streaming handlers select on
it, emit `event: closed{server_shutdown}`, and drop a `streamsWG`
counter. `server.Run` waits up to `gracefulShutdownDeadline` (10s) for
that counter to drain before calling `e.Shutdown`. A handler wedged on
a slow client write past the deadline gets cut off with the rest of
the listener.

## Capacity / overflow

Per-subscriber mailbox capacity is the SDK default (64). The
dispatcher uses `mb.TryAdd` — non-blocking, drops on overflow,
increments `Subscription.Dropped`. The HTTP handler reads the dropped
counter on every batch and emits `event: lagged{total: <count>}` when
it grows. v1 does not close the stream on lag — the consumer decides
whether to reconnect or just re-Query the dataset.

## Datasets and how clients should consume them

All consumers run the recipe above; the dataset names below are the
`?dataset=` values to pass on the subscribe URL.

- **`editor_blocks`** — per-object block tree. Events project
  per-record `$set` / `$unset` ops over the block fields; apply them
  to a local block tree. The same events fire whether the change
  came from a PATCH /editor/blocks call or a bulk PUT /editor/markdown
  rewrite — markdown PUT becomes "bulk block ops" under the hood.
- **`chat_messages`** — per-object chat stream. One record per
  message; reactions are nested fields.
- **`objects`** — per-space firehose. Each event's `records[]` can
  cover several object ids per change; dedup per record.
- **Properties** — `GET /v1/spaces/:id/properties/subscribe` (the
  dedicated endpoint, no `?dataset=`). Covers property-value writes
  across every object in the space.

### Known non-conforming consumers

The embedded debug UI (`internal/server/web/index.html`) runs a
refetch-on-event stopgap for the tree (subscribes to `objects`,
re-queries the affected folder per event) and chat (subscribes to
`chat_messages`, re-`list`s on every notification). Stale-after-
snapshot in the tree was the immediate trigger for shipping
subscriptions in v1; both will migrate to the recipe. **Don't copy
these as examples** — they predate the recipe and are scheduled to
be replaced.

## Sync-status streams (a separate SSE primitive)

`/v1/sync-status/subscribe` and `/v1/spaces/:id/sync-status/objects/:objectId/subscribe`
are SSE endpoints, but they are **not** the dataset-backed subscribe
primitive documented above. State-flip events are sparse, per-call
single payloads coming off the SDK's `Service.SubscribeStatus` /
`SyncStatusAPI.SubscribeObject` callbacks — no mailbox, no CRDT
records.

Frame set:

```
event: ready
data: {}

event: status
data: { …SpaceSyncStatus or ObjectSyncStatus body… }

event: lagged
data: { "total": <count> }              # only if the forwarder dropped events

event: closed
data: { "reason": "server_shutdown" }   # client-disconnect writes nothing
```

`event: status` body shapes match the GET responses on
`/v1/spaces/:id/sync-status` and `/v1/spaces/:id/sync-status/objects/:id`
respectively — `state` is one of `unknown` / `offline` / `syncing` /
`synced` / `error`. The `closed` reason set is shared with
`/subscribe`, so a client can use one switch for both stream
families.

The per-stream forwarder uses a small buffered channel (16 deep);
overflow drops the event and bumps a counter, surfaced as `lagged`
before the next successful frame. State transitions are sparse
enough that overflow is rare in practice.

Mounting: the account-wide stream lives on `/v1/sync-status/subscribe`
(no `:spaceId`) because the SDK call is account-scoped — one cb sees
every known space's transitions on one stream. Per-object streams
stay under the space group for symmetry with the GET endpoints.

## Open / future

- **Resume from a versionId cursor.** When the SDK supports replay
  from a per-record `versionId`, we can plumb that through as the
  resume primitive — likely as a `?since=<versionId>` query on the
  subscribe URL, with the server replaying changes whose versionId
  sorts after `since` before transitioning to live events.
- **Filtered subscriptions.** Today only `(objectId, dataset)` and the
  per-space firehose. If a UI needs a typed-property filter, layer it
  on top via `mb.WaitCond.WithFilter` server-side rather than rolling
  yet another endpoint shape.
- **WebSocket multiplex.** If a consumer wants many subscriptions per
  connection, `/v2/subscribe` over WS becomes the right answer.
  Additive — SSE endpoints stay.
