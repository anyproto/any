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

## Used by

- **Tree UI** — left sidebar subscribes via `?dataset=objects` (the
  per-space firehose) and re-queries the affected folder on each
  `changes` frame. Stale-after-snapshot was the immediate trigger for
  shipping subscriptions in v1.
- **Chat** — clients open `subscribe?dataset=chat_messages` against a
  chat object and re-`list` when notified. The chat type is a plain
  `handler.Type` — no chat-specific subscribe endpoint, the generic
  primitive is enough.
- **Blocks** — clients open `subscribe?dataset=body_blocks` against
  an object and apply the projected `$set` / `$unset` ops directly
  to a local block tree. The same events fire whether the change
  came from a PATCH /blocks call or a bulk PUT /markdown rewrite —
  markdown PUT becomes "bulk block ops" under the hood.

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
