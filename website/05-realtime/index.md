---
title: Realtime
description: How any pushes live data to clients — windowed subscriptions, sync state, the live space list, and the ephemeral event bus.
order: 0
---
# Realtime

Everything live in any arrives over plain HTTP Server-Sent Events from the local server. A subscription is a moving window over a query: one request opens a stream that delivers a snapshot followed by per-change deltas, and the CRDT engine keeps that window correct as writes land from this device or from peers.

## Four kinds of live stream

| Stream | Endpoint | What it carries |
|---|---|---|
| Windowed query/subscribe | `POST /v1/spaces/:id/objects/query/subscribe`, `POST /v1/spaces/:id/query/subscribe` | snapshot + `added`/`updated`/`removed` deltas for a filtered, sorted window of records |
| Space list | `POST /v1/spaces/query/subscribe` | the same primitive over the account's own space list |
| Sync status | `GET /v1/sync-status/subscribe`, `GET /v1/spaces/:id/sync-status/objects/:objectId/subscribe` | sparse state transitions (`syncing` → `synced` …), no records |
| Event bus | `GET /v1/events/subscribe` | transient signals published through `POST /v1/events` — navigation, progress, presence |

A few callback-based streams follow the same envelope for other surfaces: members (`GET /v1/spaces/:id/members/subscribe`), the identities directory (`GET /v1/identities/subscribe`) and file status (`GET /v1/spaces/:id/files/subscribe`). They are documented with their features under [Collaboration](../collaboration/members-and-roles.html), [Auth](../auth/identities.html) and [Files](../files/status-and-durability.html).

## One envelope, one reason set

Every stream opens with `event: ready`, emits `: keepalive` comments every ~25 s while idle, and ends with a terminal `event: closed` carrying a `reason`:

| Reason | Meaning |
|---|---|
| `server_shutdown` | the server is exiting (a signal, or `POST /v1/shutdown` on a managed server) |
| `deauthorized` | the account was torn down in place (`DELETE /v1/auth` or an account switch); the server stays up — re-read `GET /v1/auth` first |
| `sdk_closed` | the underlying subscription was released (space or engine closed; query/subscribe only) |
| `overflow` | the subscriber fell behind and its buffer filled (query/subscribe and the event bus) |
| `drifted` | too much of the held window left without replacement (query/subscribe only) |

The callback streams (sync status, members, identities, file status) never drop the subscriber: when their 16-deep forwarder overflows they emit an `event: lagged` frame before the next delivery and keep the stream open — re-read the matching GET to resync.

All reasons mean the same thing for the client: the stream is over — open a fresh request. There is no replay and no resume cursor; the new snapshot already reflects current state, which is cheaper than shipping the gap.

```
client                          any (127.0.0.1:7001)
  │  POST …/query/subscribe        │
  │ ─────────────────────────────▶ │
  │  event: ready                  │
  │  event: snapshot {records}     │
  │  event: changes [{added…}]     │   ◀── local write or remote change applied
  │  event: changes [{updated…}]   │
  │  : keepalive                   │
  │  event: closed {reason}        │
  │ ◀───────────────────────────── │
  │  (reopen)                      │
```

> **Why it matters.** The subscription engine runs next to the data, on your device. A window is served from the local indexed store, so a subscription costs no network round-trip and keeps working offline — remote changes show up as ordinary `changes` frames when sync brings them in.

## Why SSE and not WebSocket

The server pushes; the client never sends control frames into an open subscription. SSE is plain HTTP/1.1 — it works through any proxy, is debuggable with `curl -N`, and needs no upgrade handshake. The one wrinkle is that windowed subscribes are `POST` (the filter body does not fit a query string), so the browser's `EventSource` does not apply to them; clients use `fetch` with a streaming body and parse frames themselves. See [Subscribe](subscribe.html) for a complete example.

## Budget your live surface

Subscriptions are the one thing that scales with breadth rather than with data size. Subscribe to what is on screen — one stream per open list, document or chat — and never one stream per object of a collection. Anything you need to *know* is queryable on demand; a subscription only tells you that it changed. Durable consumers (indexers, notifiers) keep their own cursor and treat events as a wake-up signal.

<div class="cards">
<a href="subscribe.html"><strong>Subscribe</strong><span>The windowed query/subscribe primitive — frames, recovery, tuning, and a fetch-stream client</span></a>
<a href="sync-status.html"><strong>Sync status</strong><span>Per-space and per-object convergence state, and the streams that report transitions</span></a>
<a href="space-list.html"><strong>Live space list</strong><span>Query and subscribe to the account's own spaces as raw rows</span></a>
<a href="event-bus.html"><strong>Event bus</strong><span>Publish and subscribe to ephemeral signals across devices and space members</span></a>
</div>
