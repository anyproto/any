---
title: Sync status
description: Read whether a space or object has converged with peers, and stream state transitions over SSE.
order: 20
---
# Sync status

Local-first means a write is done the moment it lands in your store; whether it has reached anyone else is a separate question. Sync status answers it per space and per object, with cheap GETs for a render tick and SSE streams for transitions.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/spaces/:spaceId/sync-status` | rolled-up state of one space |
| GET | `/v1/spaces/:spaceId/sync-status/objects/:objectId` | state of one object |
| GET | `/v1/sync-status/subscribe` | account-wide SSE — every known space's transitions on one stream |
| GET | `/v1/spaces/:spaceId/sync-status/objects/:objectId/subscribe` | per-object SSE |
| POST | `/v1/spaces/:spaceId/sync` | force a head-sync round now instead of waiting for the periodic timer |

`/v1/spaces/:spaceId/sync-status/peers` is registered but answers `501 sdk.not_implemented`; the diagnostic `GET /v1/spaces/:spaceId/debug` is the closest equivalent (see [Debugging](../operations/debugging.html)).

## Reading state

```bash
curl http://127.0.0.1:7001/v1/spaces/SPACE/sync-status
any sync-status space SPACE
```

```json
{ "spaceId":      "spc_…",
  "state":        "syncing",
  "synced":       1,
  "total":        3,
  "networkPeers": 0,
  "localPeers":   1,
  "globalPeers":  0,
  "p2p":          "connected",
  "lastSyncedAt": "0001-01-01T00:00:00Z" }
```

| Field | Meaning |
|---|---|
| `state` | `unknown` / `syncing` / `synced`; `offline` and `error` are reserved in the vocabulary and not emitted |
| `synced`, `total` | objects converged vs. objects tracked in the space |
| `networkPeers` | responsible sync nodes with a live connection |
| `localPeers` | LAN peers sharing this space that are connected right now |
| `globalPeers` | internet-wide direct peers — relayed or hole-punched — connected right now |
| `p2p` | direct-layer state, covering both: `unknown` / `notpossible` / `notconnected` / `connected` / `restricted` (OS denied local-network access) |
| `lastSyncedAt` | time of the last completed round; zero value until one has run |

A space can be `synced` with `networkPeers: 0` — it converged entirely over the LAN, or over the global layer.

Per object:

```bash
curl http://127.0.0.1:7001/v1/spaces/SPACE/sync-status/objects/OBJ
any sync-status object SPACE OBJ
```

```json
{ "objectId": "obj_…", "state": "synced", "lastSyncAt": "2026-05-15T12:00:00Z" }
```

Unknown object ids return `{"state": "unknown"}` rather than a 404 — use the object catalog when you need an existence check.

> **Why it matters.** Writes never wait on the network, so a UI needs a separate, honest signal about whether a change has left the device. `synced` is that signal: it comes from the same engine that holds the data, not from a round-trip to a server that may be unreachable — and a space can converge over the LAN with no sync node in sight.

## Streaming transitions

Both `/subscribe` endpoints are SSE, but they are **not** the windowed query primitive: transitions are sparse, per-call payloads with no records, no mailbox window, no `drifted`.

```
event: ready
data: {}

event: status
data: { …the GET body for the space or object… }

event: lagged
data: { "total": 3 }

event: closed
data: { "reason": "server_shutdown" }
```

- `status` carries exactly the shape of the matching GET, one frame per state transition.
- `lagged` appears only if the per-stream forwarder (16 events deep) dropped transitions; it precedes the next delivered frame, and `total` is the cumulative number of drops on this stream. Re-read the GET to resync — the stream stays open.
- `closed` is written when the engine goes away — `server_shutdown` on exit, `deauthorized` when the account is torn down in place — from the [shared reason set](index.html), so one switch handles every stream family.

The account-wide stream is `GET`, so a browser can use `EventSource` directly. Close it on `closed` and on `error`: an `EventSource` reconnects by itself, possibly under another account — re-read `GET /v1/auth` before opening a replacement ([Realtime](index.html)):

```js
const es = new EventSource("http://127.0.0.1:7001/v1/sync-status/subscribe");
es.addEventListener("status", (e) => {
  const s = JSON.parse(e.data);
  badge(s.spaceId, s.state);            // "syncing" → spinner, "synced" → check
});
const closeStream = () => es.close();
es.addEventListener("closed", closeStream);
es.addEventListener("error", closeStream);
// Also call es.close() when the view closes.
```

```bash
curl -N http://127.0.0.1:7001/v1/sync-status/subscribe
any sync-status subscribe                   # account-wide
any sync-status subscribe SPACE OBJ         # one object
```

The account-wide stream lives outside the `/v1/spaces/:spaceId` group because one subscription covers every space the account knows; per-object streams sit under the space for symmetry with the GETs.

## Forcing a round

Head-sync (diff) rounds against responsible nodes run on a periodic timer (about every 30 s). `POST /v1/spaces/:spaceId/sync` runs one immediately, blocks until it completes, and returns `204`:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/SPACE/sync
any space sync SPACE
```

This is what tests use to collapse cross-device convergence waits — sync the writer, then the reader, then read. For ordinary clients the timer plus the `status` stream is enough; call `/sync` when a user explicitly asks to "sync now".

> **Note.** `/v1/health` reports `bootstrapping: true` while the engine's post-start catch-up pass is still running. The server is already serving during that pass; per-space convergence is what `/sync-status` reports, and it becomes meaningful as spaces load. See [Server](../operations/server.html).
