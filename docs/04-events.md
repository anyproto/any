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
that the SDK stamps the change with. The same versionId lands in
`_ver.id` on every record the change touched (or in `_ver.<field>` for
field-level versions, depending on the dataset). So:

- An event with `versionId = V` reports the state of the change at V.
- A snapshot row with `_ver.id = V'` reflects every change up to and
  including V'.
- An event is **already covered** by the snapshot iff `event.versionId ≤
  record._ver.id` (lexid string compare).

Subscribing first ensures no change between query-time and live-mode
goes missing. Deduping by versionId ensures we don't double-apply a
change that the snapshot already absorbed.

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
   `POST …/query` for a per-object dataset, etc. Record each row's
   `_ver.id` (or `_ver.<field>` if you key per-field) — that's your
   high-water mark for dedup.
4. **Replay the buffer.** For each event collected while the query was
   in flight, for each record in `event.records`:
   - If the record id is in the snapshot and
     `event.versionId ≤ snapshot[id]._ver.id`, drop the op — the
     snapshot already covers it.
   - Otherwise apply `record.ops` to the snapshot (or set
     `deleted:true` ⇒ remove). Update the local `_ver.id` to
     `event.versionId`.
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
naively to a JSON-shaped local copy:

- `$set` with `path` = dotted-segment field path, `payload` = the
  post-apply JSON value at that path — assign it.
- `$set` with empty `path` and an object payload — the payload is a
  multi-field set: each key is itself a dot-separated path,
  each value is what to assign there.
- `$unset` — delete the key at `path`.
- Record with `deleted: true` — drop the id from local state; `ops`
  is empty.

`$inc` / `$addToSet` / `$pull` / `$incGated` are **not** emitted to
subscribers — the SDK collapses them to the equivalent `$set` of the
merged result before delivery. The wire is intentionally narrow so
non-Go clients don't reimplement CRDT.

### Pseudo-code

```
events = []        // buffered until snapshot lands
state  = {}        // id -> {record fields, _ver: {id, ...}}

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
    state[row.id] = row            // _ver.id already on the row

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
to dedup against the snapshot's `_ver` markers. After the buffer is
drained, the per-record `_ver.id` tracked in `state` keeps the dedup
honest if a duplicate ever does show up (it shouldn't post-`ready`,
but the comparison is cheap).

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
  several ids per change — dedup per record.
- **Field-level dedup is optional.** Most clients can key on
  `_ver.id`. If your dataset surfaces `_ver.<field>` and you want
  finer-grained replay, compare per field — same lexid rule.
- **The snapshot's `_ver.id` may move while you read.** Snapshots
  aren't transactional across rows. The buffer-replay step handles
  this: any change that landed mid-query shows up on the stream and
  gets deduped against whichever row already saw it.

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
