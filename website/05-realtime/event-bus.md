---
title: Event bus
description: An ephemeral, at-most-once event bus for transient signals — publish with one POST, receive over filtered SSE, across this device, the account's devices, or a space's members.
order: 40
---
# Event bus

Not everything belongs in the database. A "open this document" directive, a progress tick, a cursor position — these are signals, not data: they should reach whoever is listening right now and vanish otherwise. The event bus carries them: a publisher `POST`s an envelope, the server fans it out to every SSE subscriber whose filter matches, and nothing is stored.

## Envelope

```json
{
  "type":    "ui.open_object",
  "scope":   "device",
  "target":  "obj_…",
  "data":    { "spaceId": "spc_…", "objectId": "obj_…", "source": "cli" },
  "sender":  { "identity": "A5k…", "self": true }
}
```

| Field | Rules |
|---|---|
| `type` | dotted lowercase slugs `[a-z0-9_]+(\.[a-z0-9_]+)*`, ≤ 128 chars. An open set — new kinds need no server change; subscribers ignore what they do not handle |
| `scope` | `device` / `account` / `space` |
| `spaceId` | required iff `scope` is `space`; rejected otherwise |
| `target` | optional subject token `[A-Za-z0-9._-]{1,128}` — an object id, run id, process id — filterable on subscribe |
| `data` | free-form JSON, ≤ 64 KiB marshaled |
| `sender` | stamped by the server: `identity` is the publishing account, `self` is true for any device of this account. Supplying it in a publish body is `400 request.unknown_field` |

## Scopes

| Scope | Reaches | Transport |
|---|---|---|
| `device` | subscribers in this server process only | in-memory fan-out; never leaves the machine |
| `account` | every device of this account | the account's tech space pub/sub |
| `space` | every member of the named space | that space's pub/sub |

Network scopes ride any-sync's ephemeral pub/sub: read-key-encrypted, per-message signed, relayed through the responsible sync nodes and directly between LAN peers. `sender.identity` is taken from the message signature — a payload's claims about its sender are discarded, so presence-style signals cannot be spoofed. Members who are offline simply miss the event.

Account and space events are encrypted under the corresponding space read key and signed by the account key. Relays forward ciphertext; local-network peers can exchange events without an online relay.

## Publish

```bash
curl -X POST http://127.0.0.1:7001/v1/events \
  -H 'Content-Type: application/json' \
  -d '{"type":"ui.open_object","scope":"device",
       "data":{"spaceId":"SPACE","objectId":"OBJ","source":"cli"}}'
# → {"subscribers": 1}

any events publish --type ui.open_object --data '{"spaceId":"SPACE","objectId":"OBJ"}'
any events publish --type presence.typing --scope space --space SPACE --target CHAT --data '{}'
```

`subscribers` is the number of **local** subscribers the event matched. `0` means nobody was listening — the publish still succeeds. It is the only delivery signal; there is no ack.

| Error | Cause |
|---|---|
| `400 request.missing_field` | empty `type` or `scope`, or `scope: space` without `spaceId` |
| `400 request.invalid_field` | bad `type` / `target` grammar, unknown scope, `spaceId` on a non-space scope, or a type + target exceeding the topic budget (256 bytes / 16 segments — enforced on every scope so a producer keeps working when it switches scope) |
| `400 request.unknown_field` | unknown top-level key, including `sender` |
| `400 events.payload_too_large` | `data` over 64 KiB |
| `409 events.no_read_key` | network scope without full membership (guest / public access) |
| `403 events.topic_not_owned` | the type maps into another account's self-owned topic namespace |
| `404` / `409 space.*` | space resolution errors for `scope: space` |

## Subscribe

```
GET /v1/events/subscribe?scope=device&type=ui.*&type=process.progress&target=run1
```

Filters are repeatable query parameters — **AND across dimensions, OR within one**; no parameters means everything:

| Param | Match |
|---|---|
| `scope` | exact |
| `spaceId` | exact |
| `type` | exact (`process.progress`) or prefix with a trailing `.*` (`process.*` matches `process` and everything under it) |
| `target` | exact |

The GET stream supports browser `EventSource`. This view fragment assumes the account has already been checked. Close on `closed` or `error` to prevent an automatic reconnect under another account; your lifecycle controller should check `GET /v1/auth` before creating a replacement stream ([Realtime](index.html)).

```js
const es = new EventSource("http://127.0.0.1:7001/v1/events/subscribe?scope=device&type=ui.*");
es.addEventListener("event", (e) => {
  const ev = JSON.parse(e.data);
  if (ev.type === "ui.open_object") router.open(ev.data.spaceId, ev.data.objectId);
});
const closeStream = () => es.close();
es.addEventListener("closed", closeStream);
es.addEventListener("error", closeStream);
// Also call es.close() when the view closes.
```

```bash
curl -N 'http://127.0.0.1:7001/v1/events/subscribe?type=process.*'
any events subscribe --type 'process.*' --scope space --space SPACE
```

Frames:

```
event: ready
data: {}

event: event
data: {"type":"ui.open_object","scope":"device","data":{…},"sender":{"identity":"A5k…","self":true}}

: keepalive

event: closed
data: {"reason":"overflow"}
```

`ready` is emitted once on connect and **no snapshot follows** — there is nothing to snapshot. `closed` reasons are `overflow` (the subscriber's 16-deep buffer filled and it was dropped), `server_shutdown` and `deauthorized`. A stream can also end without a terminal frame. Check the expected account before reconnecting; a new stream has no replay of missed events. Reason strings are shared with every other stream.

Two extra rules for network scopes:

- An explicit `scope=space` subscription must name at least one `spaceId` (`400 request.missing_field`) — interest is per space; there is no "all spaces". With no `scope` and no `spaceId` param a subscription covers device and account events. A `spaceId` filter admits space-scope events only — with no `scope` param it subscribes to exactly those spaces, and pairing it with a scope list that excludes `space` is `400 request.invalid_field`.
- Each space has a budget of 100 pub/sub interest patterns shared across the process; exhausting it answers `409 events.too_many_patterns`. Identical filters share one interest, so narrow or share type filters.

## At-most-once, by design

The bus has no snapshot or replay. An event published while nobody listens is dropped; reconnecting does not recover it. Make payloads idempotent or last-write-wins, and never use the bus to *know* something — query the database for state, use events to learn that it changed. The [process helper](../notifications/processes.html) shows the pattern: every progress frame carries the full descriptor, so a late joiner needs no history.

## Event types in use

| Type | Scope | `data` |
|---|---|---|
| `ui.open_space` | device | `{spaceId, source?}` — `spaceId` is the space to open, independent of the envelope's routing `spaceId` |
| `ui.open_object` | device | `{spaceId, objectId, source?}` |
| `process.started` / `progress` / `done` / `failed` / `cancelled` / `cancel` | any | the [process convention](../notifications/processes.html); envelope `target` is the process id |
| `links.updated` | device | `{spaceId, targets, truncated?}` — canonical `any://` targets whose backlinks changed after the link index landed edges from `spaceId`; match on `targets`, re-read the backlinks. See [Links](../types/links.html) |
| `editor.cursor` | space | presence; a **self-owned** type that maps into the pub/sub namespace only the sender's account can publish to, making it spoof-proof at publisher, relay and receiver |

A UI window mounts one `EventSource('/v1/events/subscribe?scope=device&type=ui.*')` and dispatches into navigation; unknown `ui.*` types are ignored.

## Limits

| Limit | Value |
|---|---|
| payload | 64 KiB per event |
| publish rate (network scopes) | ~30 msg/s per peer, burst 60 — coalesce high-frequency producers (token streams at ~4 Hz, cursors at ≤ 10 Hz are fine) |
| interest patterns | 100 per space |
| subscriber buffer | 16 events; overflow drops the subscriber |

> **Note.** Relay through sync nodes needs a network whose nodes carry the pub/sub relay; against older nodes, account and space events still flow between directly connected LAN peers. Guest-mode (public access) spaces cannot use network scopes — the transport signs as the account identity, which a guest ACL does not contain.
