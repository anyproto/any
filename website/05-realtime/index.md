---
title: Realtime
description: Keep views current with query subscriptions, observe sync progress, and send transient events.
order: 0
---
# Realtime

Use a subscription to keep a view current after its initial load. Any sends events from the local server over **Server-Sent Events (SSE)**: an HTTP response that stays open and delivers named frames as data changes.

## Choose the stream for your task

| I want to… | Stream | Guide |
|---|---|---|
| Keep a list, document, or chat current | query snapshot followed by `added`, `updated`, and `removed` records | [Subscribe](subscribe.html) |
| Watch spaces join, leave, or change | the same query primitive over the account's spaces | [Live space list](space-list.html) |
| Show whether an object or space has synced | state transitions, such as `syncing` and `synced` | [Sync status](sync-status.html) |
| Send navigation, presence, or progress signals | transient events; no stored history | [Event bus](event-bus.html) |

## Four kinds of live stream

| Stream | Endpoint | What it carries |
|---|---|---|
| Windowed query/subscribe | `POST /v1/spaces/:id/objects/query/subscribe`, `POST /v1/spaces/:id/query/subscribe` | snapshot and changes for a filtered, sorted window of records |
| Space list | `POST /v1/spaces/query/subscribe` | snapshot and changes for the account's space list |
| Sync status | `GET /v1/sync-status/subscribe`, `GET /v1/spaces/:id/sync-status/objects/:objectId/subscribe` | sparse state transitions, no records |
| Event bus | `GET /v1/events/subscribe` | signals published through `POST /v1/events` |

Other features expose callback streams: [members](../collaboration/members-and-roles.html) at `GET /v1/spaces/:id/members/subscribe`, [identities](../auth/identities.html) at `GET /v1/identities/subscribe`, and [file status](../files/status-and-durability.html) at `GET /v1/spaces/:id/files/subscribe`.

## One envelope, one reason set

Streams open with `event: ready` and send `: keepalive` comments about every 25 seconds while idle. A terminal `event: closed` explains why the stream ended. A broken connection may end without that frame.

| Reason | Meaning |
|---|---|
| `server_shutdown` | the server is exiting after a signal or managed-server shutdown |
| `deauthorized` | the account was signed out or switched while the server stayed up |
| `sdk_closed` | the space or engine released a query subscription |
| `overflow` | a query subscription or event-bus consumer fell behind and filled its buffer |
| `drifted` | too much of a query's window left without replacement |

Before reconnecting, check `GET /v1/auth` against the account your view belongs to. Stop and clear that view if the account is unauthorized or different. Check after a clean end of stream or HTTP 401 too: the final `deauthorized` frame may never reach the client.

There is no replay or resume cursor. A new **query subscription** supplies a fresh snapshot; replace the previous window with it. **Callback streams** require the matching GET to reload state. **Event-bus messages** missed while disconnected are gone.

Callback streams for sync status, members, identities, and file status use a 16-event forwarder. If it fills, they emit `event: lagged` before the next delivery and keep the stream open. Reload through the corresponding GET.

## Why SSE and not WebSocket

The client opens a stream; all later messages travel from server to client. SSE needs no upgrade handshake and is readable with `curl -N`. Windowed subscriptions use POST, so browser `EventSource` cannot open them. Use streaming `fetch`; the [Subscribe example](subscribe.html#client-example-fetch-streaming-no-eventsource) includes parsing, reconnection, account checks, and cancellation.

## Budget your live surface

Subscribe to what is on screen: one stream per open list, document, or chat. A space-wide object query can observe property changes across many objects; a separate stream for every object is usually unnecessary.

Local writes and remote changes both arrive as ordinary query changes once they are applied on this device. Queries continue to work offline. A durable background consumer, such as an indexer, keeps its own cursor and uses events to wake up; transient events alone cannot establish that it has processed everything.
