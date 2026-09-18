---
title: Status and durability
description: Show network backup state, distinguish it from file availability, and observe local and remote changes.
order: 30
---
# Status and durability

A successful attach means the file is registered locally. **Durability** tells you whether a verified network backup exists; **availability** tells you whether its bytes can be read now. A file can be available from a peer while its backup is still pending.

Use durability for a backup badge and for deciding whether local bytes may be offloaded. Use the [content endpoint](downloading.html) to open a file.

## States

| State | Meaning |
|-------|---------|
| `durable` | the row records a verified network-custody receipt (`networkSign`), or the file is inline |
| `inflight` | the file is registered; backup is queued, running, or being driven by another device |
| `limited` | the network refused backup because of a storage limit; retried on a slow cadence or through `POST …/retry` |

Inline files (under 4096 bytes) are born `durable`. Larger files start `inflight` and flip to `durable` on a persistent background queue that starts the upload as soon as attach returns, retries with backoff and survives restarts. The one exception is content already backed up in the space: an attach that deduplicates against an already-durable file is born `durable`.

## Reading status

**Before you start:** `$SP` is a space you can read and `$FILE` is an attachment's `fileId`. The local server must be running with the appropriate account.

```bash
curl "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/status"
```

```json
{ "fileId": "…", "objectId": "…", "state": "inflight", "cached": true,
  "attempts": 2, "lastErr": "…" }
```

`cached` reports a complete local copy. `attempts` counts failed background attempts since the last success or enqueue; `lastErr` describes the latest failure. Both appear only while work is pending.

Use the space rollup for a backup badge:

```bash
curl "http://127.0.0.1:7001/v1/spaces/$SP/files/stats"
# → { "total": 12, "durable": 10, "inflight": 2, "limited": 0 }
```

CLI: `any file status $SP $FILE`, `any file stats $SP`.

## The status stream

`GET /v1/spaces/:spaceId/files/subscribe` reports **local** transitions: attach, backup progress and failure, pin completion, and manual retries.

```text
event: ready
data: {}

event: status
data: { "fileId": "…", "objectId": "…", "state": "durable", "cached": true }

event: lagged
data: { "total": 3 }

event: closed
data: { "reason": "server_shutdown" }
```

`any file subscribe $SP` prints one JSON line per frame. A `status` payload has the same shape as the status GET. Refresh a space badge from `stats` on each status frame instead of incrementing and decrementing counts yourself.

`lagged` means the forwarder dropped events: reload `stats` or the file statuses. After `closed` or an interrupted connection, check the expected account, reopen the stream, and reload state. Callback streams have no replacement snapshot; see [Realtime](../realtime/index.html).

Another device or member finishing a backup does not emit a local status event here. Read its status or watch its payload row instead.

## How a receiver learns a file is fetchable

Use the payload stream for attachment discovery and remote backup updates. Fetchability also depends on local bytes and peers, so there is no single backup event that proves every change in availability.

1. Subscribe to the object's rows: `POST …/objects/:objectId/files/query/subscribe`. The sender's file arrives as an `added` row — usually without `networkSign` yet, because the sender's backup runs after its attach returns.
2. The moment it becomes fetchable is an `updated` frame on the same row when `networkSign` lands (or `durable: true` on a re-GET of `…/files/:fileId`).
3. Then `GET …/files/:fileId/content`. Downloading before that point returns `409 file.not_available` — a retry-later state to wait out, not an error to surface.

The receipt is a synced cleartext field, so remote backup completion reaches every member with the row. Display metadata remains sealed and comes from the typed file GET. The raw rows cannot supply names or mime.

## Retry

`POST /v1/spaces/:spaceId/files/:fileId/retry` makes pending background work due now and returns 204. After a storage limit is raised or space is freed, use it to retry a limited backup immediately:

```bash
any file retry $SP $FILE
```

## What attach latency includes

Local work only: spooling the body, encrypting it and writing the local CAR. Attach never waits on the network — it queues the backup and returns `durable: false`, whether the broker is reachable or not, and the queue uploads in the background. The registration is already durable in the CRDT; the state only tells you whether the *network* has a copy yet — which is what decides whether [offload](cache.html) is allowed and whether other members can fetch.
