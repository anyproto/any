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

Files under 4096 bytes travel inline with the sealed row and start durable. Larger files start inflight. Attach attempts backup within the request, so a reachable broker may make the response durable already. On failure, a persistent background queue retries, including after a restart. A network with no file nodes leaves the file inflight.

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

1. Subscribe to `POST …/objects/:objectId/files/query/subscribe`, with a sort and limit. The initial snapshot and later `added` rows identify attachments.
2. Read name and mime through `GET …/files/:fileId`, then attempt `GET …/files/:fileId/content`. Local bytes or a reachable peer can satisfy the request even without `networkSign`.
3. A `409 file.not_available` means no usable source can serve the bytes. Retry on connectivity changes, a later open, or a payload update adding `networkSign`; use backoff between attempts.
4. When `networkSign` arrives in an `updated` row, show the network backup as complete. A typed file GET will also report `durable: true`.

The receipt is a synced cleartext field, so remote backup completion reaches every member with the row. Display metadata remains sealed and comes from the typed file GET. The raw rows cannot supply names or mime.

## Retry

`POST /v1/spaces/:spaceId/files/:fileId/retry` makes pending background work due now and returns 204. After a storage limit is raised or space is freed, use it to retry a limited backup immediately:

```bash
any file retry $SP $FILE
```

## What attach latency includes

For a new large file, attach writes the registration and local bytes, then attempts the network backup. With a reachable broker, latency usually includes uploading those bytes. A transport failure or refusal returns a successful attach with `durable: false` and leaves the queue to retry.

Treat a successful attach as complete; update the backup badge separately. A backup permits [offload](cache.html) and network reads. Peers may already be able to serve the file before that backup exists.
