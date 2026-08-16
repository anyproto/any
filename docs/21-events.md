# 21 — Event bus

An account-wide **ephemeral** event bus: a publisher `POST`s an event; the
server fans it out over SSE to every subscriber whose filter matches.
**Nothing is stored, there is no replay, no ack, delivery is at-most-once.**
It generalizes the retired UI command channel (`/v1/ui/commands`,
doc 15) — UI navigation is now just the `ui.*` corner of the type space.

This is a consumer-side channel, not an SDK dataset — the same kind of
exception as `/search`. An event is a transient signal (a navigation
directive, a progress tick, a presence beat), not shared space data, so it
deliberately does **not** go through the dataset/handler/CRDT machinery.

Scopes:

- `device` — this process only: local in-memory fan-out, never leaves the
  machine. **Implemented.**
- `account` — every device of this account. **`501` until the SDK pub/sub
  bridge lands (SYN-152, needs SYN-150).**
- `space` — every member of the space named by `spaceId`. **`501`, same
  prerequisite.**

## Why in-memory (not a dataset)

A persisted `events` dataset was considered and rejected. The problems it
would create — and that the ephemeral bus simply doesn't have:

- **No replay.** A persisted dataset replays its history to any reconnecting
  subscriber (jump to an hour-old document, re-render stale progress).
  The bus has no snapshot — a subscriber sees only events published *after*
  it connects.
- **At-most-once is correct here.** An event fired while nobody listens is
  dropped. You do not want a queued "open doc" firing minutes later, or a
  backlog of progress ticks replaying on reconnect. Payloads must be
  idempotent or last-write-wins.
- **No growth, no space coupling.** Nothing accumulates, no object hosts it,
  no derived ids to discover. One account per server ⇒ one global bus.

## Envelope

```jsonc
{
  "type":    "process.progress",      // open dotted slug set — new kinds need no server change
  "scope":   "device" | "account" | "space",
  "spaceId": "…",                     // required iff scope == "space"
  "target":  "…",                     // optional subject: objectId, runId, processId…
  "data":    { … },                   // free-form JSON payload
  "sender":  { "identity": "…", "self": true }   // server-stamped, never client-supplied
}
```

- `type` — dotted lowercase slugs (`[a-z0-9_]+(\.[a-z0-9_]+)*`, ≤ 128
  chars). An **open set**: unknown types publish fine and are ignored by
  clients that don't handle them. New event kinds add a new type (plus
  payload fields) with **no server change**.
- `target` — one token of `[A-Za-z0-9._-]{1,128}`, filterable on subscribe.
  The charset keeps the future pub/sub topic mapping (dots → `/` segments,
  target appended as one segment) collision-free.
- `data` — ≤ 64 KiB marshaled (matches the pub/sub per-message cap, so a
  device-scope producer doesn't break when it switches scope).
- `sender` — stamped by the server: `identity` is the publishing account
  (signature-verified once network scopes land), `self` is true when the
  event came from this account (any of its devices). A `sender` field in a
  publish body is rejected (`400 request.unknown_field`).
- `sessionId` — **reserved** for a future per-connection identity; not
  implemented.

## Endpoints

Both are account-scoped and sit **outside** the `/v1/spaces/:spaceId` group
(like `/v1/sync-status/subscribe`), behind the standard `/v1` auth guard.

### `POST /v1/events` — publish

Body is the envelope minus `sender`. Response:

```json
{ "subscribers": 1 }
```

`subscribers` is how many **local** subscribers matched the event. `0` means
nobody was listening — the publish still succeeds (fire-and-forget); it's
the only delivery signal, there is no ack.

Validation (`400`):

- `request.bad_json` — body isn't valid JSON.
- `request.unknown_field` — unknown top-level key (including `sender`).
- `request.missing_field` — `type` or `scope` empty, or `spaceId` empty with
  `scope == "space"`.
- `request.invalid_field` — bad `type`/`target` grammar, unknown `scope`, or
  `spaceId` present on a non-space scope.
- `events.payload_too_large` — `data` exceeds 64 KiB.

`scope: account | space` → `501 sdk.not_implemented` until SYN-152.

### `GET /v1/events/subscribe` — subscribe (SSE)

```
GET /v1/events/subscribe?scope=device&type=process.*&type=ui.open_space&target=run1
```

Filter params — all repeatable, **AND across dimensions, OR within one**;
no params = everything:

- `scope` — exact (`device` / `account` / `space`).
- `spaceId` — exact.
- `type` — exact (`process.progress`) or prefix with a trailing `.*`
  (`process.*` matches `process` and everything under `process.`).
- `target` — exact.

Frames:

```
event: ready
data: {}

event: event
data: {"type":"ui.open_space","scope":"device","data":{…},"sender":{…}}

: keepalive

event: closed
data: {"reason":"server_shutdown"}
```

- `ready` — emitted once on connect. **No snapshot follows** (there is none).
- `event` — one per matching published event, the envelope verbatim.
- `: keepalive` — comment heartbeat every ~25s while idle.
- `closed` — terminal frame with a `reason`:
  - `server_shutdown` — the server is exiting.
  - `overflow` — this subscriber fell too far behind (its 16-deep buffer
    filled) and was dropped; reconnect for a fresh stream.

  Reason strings are shared with the other subscribe streams so a client can
  branch on one reason set. A plain client disconnect writes no frame (the
  peer is already gone).

## Event types

### `ui.*` — UI navigation (device scope)

The retired `/v1/ui/commands` vocabulary, now event types. `data` carries
what the command body used to:

```jsonc
// type: "ui.open_space"
{ "spaceId": "<target space id>", "source": "cli" }

// type: "ui.open_object"
{ "spaceId": "<target space id>", "objectId": "<target object id>", "source": "cli" }
```

`data.spaceId` is the **target** space to open — independent of the
envelope's `spaceId` (which is scope routing and absent on device scope) —
so one subscription drives navigation anywhere. `source` is a free-form
publisher hint, UI display only. A UI window mounts one
`EventSource('/v1/events/subscribe?scope=device&type=ui.*')` and dispatches
`event` frames into navigation; unknown `ui.*` types are ignored.

## Server implementation

- `internal/server/events_hub.go` — `eventHub`, a process-global filtered
  broadcaster (mutex + `map[int]eventSub`). `publish` is a **non-blocking**
  fan-out to matching subscribers: one whose buffer is full is dropped
  (channel closed) rather than blocking the publisher. Created lazily via
  `deps.eventsHub()` — no engine/SDK dependency. `any`-internal producers
  (indexer, sync milestones) publish through it directly.
- `internal/server/handlers_events.go` — `eventsPublish` (strict bind →
  validate → stamp sender → route by scope) and `eventsSubscribe` (parse
  filters → the shared `streamStatusSSE` driver: `ready`, keepalive,
  terminal `closed`, registered with `streamsWG` so graceful shutdown waits).
- `internal/api/event.go` — `Event`, `EventSender`, `EventPublishRequest`/
  `Response`, the scope and `ui.*` constants.
- Routes registered in `internal/server/routes.go`.

## CLI

```
any events publish --type T [--scope device|account|space] [--space ID]
                   [--target X] [--data JSON|@FILE|-]
any events subscribe [--scope S]... [--type T]... [--space ID]... [--target X]...
                                          # one JSON line per SSE frame
```

## Limits / future

- Fire-and-forget, at-most-once. No persistence, no replay, no retry, no ack
  beyond the local `subscribers` count. Intentional — see "Why in-memory".
- `data` ≤ 64 KiB. Network scopes will add a ~30 msg/s per-peer publish
  budget (any-sync pub/sub rate limit) — coalesce high-frequency producers.
- Single account per server ⇒ the hub is genuinely global. If
  multi-account-per-process ever lands, key the hub by account.
- `account`/`space` scopes: SYN-152 maps types onto SDK pub/sub topics
  (dotted `type` → slash segments, target appended; designated self-owned
  types into the spoof-proof `acc/…` namespace) with refcounted
  subscribe-side interests. This doc gains a § Scopes over the network
  section when it lands.
- New event kinds = new `type` values; document the contracts here.
