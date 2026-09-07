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
  machine.
- `account` — every device of this account, over the SDK pub/sub bound to
  the tech space (whose owner-only ACL means its peers are exactly the
  account's own devices).
- `space` — every member of the space named by `spaceId`, over that
  space's pub/sub.

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
- `request.invalid_field` — bad `type`/`target` grammar, unknown `scope`,
  `spaceId` present on a non-space scope, or `type` + `target` rendering a
  topic over the pub/sub budget (256 bytes / 16 segments — enforced on
  every scope, device included, so a producer doesn't break when it
  switches to a network scope).
- `events.payload_too_large` — `data` exceeds 64 KiB.

Network scopes add (from the SDK pub/sub sentinels): `409
events.no_read_key` (keyless/guest access — network events need full
membership), `403 events.topic_not_owned` (defensive — can't occur with
the server-side topic mapping), and the space-resolution errors
(`404`/`409 space.*`) for `scope: space`.

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

Network-scope subscriptions have two extra rules:

- An **explicit** `scope=space` subscription must name at least one
  `spaceId` filter (`400 request.missing_field`) — pub/sub interest is
  per-space, there is no "all spaces" subscription. A catch-all (no
  `scope` param) doesn't error: it covers device + account, plus any
  space listed in a `spaceId` filter. A `spaceId` filter admits
  space-scope events **only** (device/account events carry no spaceId),
  so combining it with a scope list that excludes `space` is `400
  request.invalid_field`, and its presence skips the account interests
  a catch-all would otherwise acquire.
- Subscribe can answer `409 events.too_many_patterns` when the space's
  pub/sub pattern budget (100) is exhausted — narrow the type filters or
  share them across subscribers (identical filters share one interest).

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

### `process.*` — the process helper (doc 22)

Long-running operations broadcast their lifecycle as `process.started`
/ `process.progress` / `process.done|failed|cancelled`, with
`process.cancel` as the owner-addressed cancellation directive. The
envelope target carries the process id; the `/v1/processes` endpoints
emit the frames and an in-memory registry taps the hub to serve the
live view. Full convention — data shapes, composite key, heartbeat and
staleness rules — in `docs/22-processes.md`.

### `links.updated` — the link index (device scope)

Published by the search indexer after a page of changes landed edges
in the link index (docs/13-index.md § Links): the canonical targets
whose backlinks changed, so a panel showing them re-reads.

```jsonc
// type: "links.updated"
{ "spaceId": "<space id>", "targets": ["any://o/<sp>/<obj>", "any://m/<sp>/<identity>", …] }
```

`targets` name objects (whatever part of the object was linked — a
block link reports the block's object) or, for identities and files,
the target itself; only targets an edge appeared for or vanished from
are named. Capped at 200 entries, `"truncated": true` past that — a
panel showing an unlisted target re-reads too. At-most-once like every
bus event; a missed frame costs one stale panel until the next read.

## Scopes over the network

`account` and `space` events ride the SDK's ephemeral pub/sub
(`SDK.PubSub()` / `Space.PubSub()`): read-key-encrypted, per-message
signed, fanned out via the responsible sync nodes and direct LAN peers.
Same delivery contract as the local hub — fire-and-forget, at-most-once,
no persistence; offline members simply miss events.

### Topic mapping

Events map onto pub/sub topics so a device pulls only what someone
locally subscribed to. Dotted `type` → slash segments under the `ev/`
root, with the target **always** appended as exactly one segment (`-`
when absent):

| Event                              | Topic                            |
|------------------------------------|----------------------------------|
| `process.progress`, target `p1`    | `ev/process/progress/p1`         |
| `process.progress`, no target      | `ev/process/progress/-`          |
| `editor.cursor`, target `o1`, by A | `acc/ev/editor/cursor/o1/<A>`    |

Types designated **self-owned** (registry `selfOwnedEventTypes` in
`internal/server/events_topics.go`; v1: `editor.cursor`) map into
pub/sub's reserved `acc/…/<accountId>` namespace — only that account can
publish there (enforced at publisher, relay and receiver), making
presence-style signals spoof-proof. Anyone may subscribe; the fan-in
pattern covers the target + account tail. Add a type to the registry to
make it self-owned — a coordinated change, since every peer must map the
type the same way.

SSE filters become NATS-style interest patterns: `type=process.*` →
`ev/process/>`, exact type → `ev/…/<target>` or `ev/…/*`, no type filter
→ `ev/>` + `acc/ev/>`. Patterns are a coarse pull filter only — the hub
re-filters every delivery against the full subscription filter.

### Interests and refcounting

The first local subscriber whose filter needs a pattern on a scope
subscribes it with the SDK; the last drops it. Idle spaces cost nothing,
and N identical filters share one SDK subscription. Because the SDK
fires every subscription matching a message, the bridge subscribes only
the maximal (pairwise-disjoint) cover of the wanted pattern set — one
message is never delivered twice (`internal/server/events_bridge.go`).

### Sender, loopback, delivery

`sender.identity` is stamped from the pub/sub message **signature** —
anything a payload claims about its sender is discarded. `sender.self`
is the SDK's loopback flag: true on every device of the publishing
account. A network-scope publish reaches local subscribers through that
loopback (synchronous on publish), not a second local fan-out, so the
reply's `subscribers` count is the hub's current match count, restricted
to subscribers the event can actually reach — for `scope: space`, those
naming the space in a `spaceId` filter (only they hold a pub/sub
interest on it). Approximate by design.

### Constraints

- **Payload ≤ 64 KiB** per message (enforced at publish, `400
  events.payload_too_large`).
- **~30 msg/s per-peer publish budget** (burst 60; any-sync's pub/sub
  rate limit). Coalesce high-frequency producers: token-level agent
  output at ~4 Hz, cursor positions at ≤ 10 Hz are fine.
- **100 interest patterns per space** (shared across the process — `409
  events.too_many_patterns`).
- Relay through sync nodes needs a network whose nodes carry
  `pubsubrelay` (staging does, any-sync-node ≥ v0.13.1); against older
  nodes events still flow between directly connected LAN peers.
- Guest-mode (public access) spaces are unsupported — the transport
  signs as the account identity, which a guest ACL doesn't contain
  (`409 events.no_read_key`).

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
- `internal/server/events_topics.go` — the type↔topic mapping, the
  self-owned registry, filter→pattern derivation and the
  pattern-subsumption cover used by the bridge.
- `internal/server/events_bridge.go` — the refcounted pub/sub bridge for
  the network scopes.
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
- Single account per server ⇒ the hub is genuinely global. If
  multi-account-per-process ever lands, key the hub by account.
- New event kinds = new `type` values; document the contracts here.
  `any`-internal producers publish through `deps.eventsHub()` directly
  — first one wired: the indexer's embed drain, reporting as a
  device-scope process (docs/22-processes.md § Internal producers).
