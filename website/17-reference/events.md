---
title: SSE streams
description: Every Server-Sent Events stream the server exposes, its frame set, and the shared set of terminal close reasons.
order: 30
---
# SSE streams

Every live surface in any is a plain HTTP Server-Sent Events response: one request opens the stream, the server pushes frames, and a terminal `closed` frame tells the client to reconnect. All streams share one envelope and one reason vocabulary, so a client needs a single state machine.

## The envelope

```
event: ready
data: {}

event: <stream-specific>
data: { … }

: keepalive                      # comment frame every ~25s while idle

event: closed
data: {"reason": "server_shutdown"}
```

Rules that hold for every stream:

1. Wait for `ready` before treating the stream as live. Errors that happen before the stream opens arrive as a normal JSON error envelope on the response, not as SSE.
2. `closed` is terminal — reconnect with a fresh request. There is no replay and no resume cursor; the new stream's snapshot reflects current state.
3. Windowed streams use POST (the filter body does not fit a query string), so a browser reads them with `fetch` + a streaming body rather than `EventSource`.

## Close reasons

| Reason | Meaning | Emitted by |
|---|---|---|
| `server_shutdown` | the server is exiting (signal or `POST /v1/shutdown`); in-flight streams get up to 10 s to emit it | every stream |
| `deauthorized` | the account behind the stream was torn down in place (`DELETE /v1/auth`, or a `POST /v1/auth` switch) while the server stays up; re-read `GET /v1/auth` before resubscribing | every stream |
| `sdk_closed` | the engine released the underlying subscription (space or SDK closed) | query/subscribe |
| `overflow` | the subscriber's mailbox filled before it drained (query/subscribe: capacity `mailboxCapacity`, default 256, min 16; event bus: 16-deep buffer) | query/subscribe, event bus |
| `drifted` | more than `driftBudgetPercent` (default 30) of the held window left without replacement; the engine refuses to re-query on the hot path | query/subscribe |

`overflow` and `drifted` are split only so clients can log and back off sensibly — recovery is identical.

## Windowed query/subscribe

Endpoints: `POST /v1/spaces/:id/objects/query/subscribe` (cross-object), `POST /v1/spaces/:id/query/subscribe` (per-object dataset), `POST /v1/spaces/query/subscribe` (the account's space list), `POST /v1/spaces/:id/objects/:objectId/files/query/subscribe` (one object's payload rows), `POST /v1/devices/query/subscribe`.

| Frame | Payload | Notes |
|---|---|---|
| `ready` | `{}` | once |
| `snapshot` | `{records, total?, hasNext?}` | once, right after `ready`; `total`/`hasNext` only with `includeTotal` |
| `changes` | `[{versionId, added, updated, removed}]` | zero or more windowed events, coalesced per write |
| `closed` | `{reason}` | terminal |

`added` and `updated` entries are `{id, doc, ops}` — the full post-apply record plus the `$set`/`$unset` ops that triggered the transition (`$inc`/`$addToSet`/`$pull` are collapsed to the resulting `$set` before delivery). `removed` entries are `{id, reason}` with `reason` one of `deleted` (tombstoned — drop for good), `filtered-out` (still exists, no longer matches) or `displaced` (still matches, pushed past `limit`). `versionId` is per-change DAG order local to this peer — never compare it across devices.

```bash
curl -N -X POST http://127.0.0.1:7001/v1/spaces/$SP/query/subscribe \
  -d '{"objectId":"'$CHAT'","dataset":"chat_messages","sort":["-_ver.id"],"limit":50}'
# any query-subscribe $SP $CHAT --dataset chat_messages --sort=-_ver.id --limit 50
```

Contract and client recipe: [Subscribe](../realtime/subscribe.html).

## Sync status

`GET /v1/sync-status/subscribe` (every space, account-scoped) and `GET /v1/spaces/:id/sync-status/objects/:objectId/subscribe`.

| Frame | Payload |
|---|---|
| `ready` | `{}` |
| `status` | the same body as the matching GET — `state` ∈ `unknown`, `offline`, `syncing`, `synced`, `error` |
| `lagged` | `{total}` — the 16-deep forwarder dropped events |
| `closed` | `{reason}` |

State transitions are sparse, so `lagged` is rare. See [Sync status](../realtime/sync-status.html).

## Members

`GET /v1/spaces/:id/members/subscribe` — `any members subscribe <spaceId>`.

| Frame | Payload |
|---|---|
| `member` | `{kind: "added"\|"changed"\|"removed", member: Member, previous: Member \| null}` |
| `lagged` | `{total}` |

`member` is the full post-event shape; `previous` is null on `added`.

## Identities directory

`GET /v1/identities/subscribe` — `any identities subscribe`.

| Frame | Payload |
|---|---|
| `identities` | `{added: [IdentityInfo], updated: [IdentityInfo], removed: [identity]}` |
| `lagged` | `{total}` |

A contact whose profile key arrives later surfaces first in `added` with an empty `name`, then in `updated` once resolved.

## File status

`GET /v1/spaces/:id/files/subscribe` — `any file subscribe <spaceId>`. Local transitions only.

| Frame | Payload |
|---|---|
| `status` | `{fileId, objectId, state: "durable"\|"inflight"\|"limited", cached, attempts?, lastErr?}` |
| `lagged` | `{total}` |

## Event bus

`GET /v1/events/subscribe?scope=&spaceId=&type=&target=` — `any events subscribe`. Not an SDK subscription: the source is the in-process hub behind `POST /v1/events`. At-most-once, no snapshot; only events published after connect are delivered.

| Frame | Payload |
|---|---|
| `event` | `{type, scope, spaceId?, target?, data?, sender: {identity, self}}` |
| `closed` | `{reason: "server_shutdown" \| "overflow"}` |

Filters are repeatable query params — AND across dimensions, OR within one; `type` takes an exact slug or an `x.*` prefix. Process frames (`process.started` / `progress` / `done` / `failed` / `cancelled` / `cancel`) ride this stream: `?type=process.*`. See [Event bus](../realtime/event-bus.html).

## Streams in the CLI

Every streaming subcommand prints one JSON object per frame on stdout:

```
{"event": "ready",   "data": {}}
{"event": "status",  "data": {"spaceId":"…","state":"syncing"}}
{"event": "closed",  "data": {"reason": "server_shutdown"}}
```

> **Note.** The request body's `projection` field shapes snapshot rows and every `added` / `updated` record in `changes`, per-field ops included. Without one, records ship their full form: `_ver` and, when present, `_traces` / `_deletedAt`.
