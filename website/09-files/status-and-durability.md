---
title: Status and durability
description: The three durability states, the per-file and per-space status reads, the local transition stream, and how a receiver learns a remote file is fetchable.
order: 30
---
# Status and durability

A file is *registered* the instant attach returns; it is *durable* once the network holds a verified backup. Every status surface speaks the same three-state vocabulary, and none of it blocks the write path — a device with no connectivity attaches files exactly like a connected one.

## States

| State | Meaning |
|-------|---------|
| `durable` | a verified network-custody receipt (`networkSign`) is recorded on the row — or the file is inline |
| `inflight` | registered in the CRDT; backup queued, running, or being driven by another device |
| `limited` | the network refused backup (storage limit); retried on a slow cadence and on `POST …/retry` |

Inline files (under 4096 bytes) are born `durable`. Larger files start `inflight` and flip to `durable` on a persistent background queue that starts the upload as soon as attach returns, retries with backoff and survives restarts. The one exception is content already backed up in the space: a deduplicated attach is born `durable`.

## Reading status

```bash
curl "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/status"
```

```json
{ "fileId": "…", "objectId": "…", "state": "inflight", "cached": true,
  "attempts": 2, "lastErr": "…" }
```

`attempts` counts failed background attempts since the last success or enqueue, and `lastErr` the last failure; both appear only while work is pending. The per-space rollup is cheap and safe to hold for a "not backed up" badge:

```bash
curl "http://127.0.0.1:7001/v1/spaces/$SP/files/stats"
# → { "total": 12, "durable": 10, "inflight": 2, "limited": 0 }
```

CLI: `any file status $SP $FILE`, `any file stats $SP`.

## The status stream

`GET /v1/spaces/:spaceId/files/subscribe` streams durability transitions over SSE — attach, backup progress and failure, pin completion, manual retries:

```
event: ready
data: {}

event: status
data: { "fileId": "…", "objectId": "…", "state": "durable", "cached": true }

event: lagged
data: { "total": 3 }

event: closed
data: { "reason": "server_shutdown" }
```

`any file subscribe $SP` prints one JSON line per frame. The frame payload is the same shape as the status GET. `lagged` means the forwarder overflowed and frames were dropped — re-read `stats` or the affected files' status. Refresh a badge from `stats` on every `status` frame rather than tracking counts yourself.

> **Note.** The stream reports **local transitions only**. Another device of yours finishing a backup, or another member's file becoming fetchable, does not appear here — those are visible through GET reads and through the payload row itself, below.

## How a receiver learns a file is fetchable

There is no notification channel to build: the file *is* space data. When another member attaches a file, its payload row syncs to you like any record. The broker's custody receipt, `networkSign`, is a synced **cleartext field on that row**, so the durable flip arrives as an ordinary row update:

1. Subscribe to the object's rows: `POST …/objects/:objectId/files/query/subscribe`. The sender's file arrives as an `added` row — usually without `networkSign` yet, because the sender's backup runs after its attach returns.
2. The moment it becomes fetchable is an `updated` frame on the same row when `networkSign` lands (or `durable: true` on a re-GET of `…/files/:fileId`).
3. Then `GET …/files/:fileId/content`. Downloading before that point returns `409 file.not_available` — a retry-later state to wait out, not an error to surface.

Names and mime for rendering come from `GET …/files/:fileId`; the payload rows carry only the cleartext fields.

> **Why it matters.** Durability is a property of the data, replicated with it, rather than a server-side job table you poll. Any member on any device sees the same `networkSign` the moment it syncs — including a device that was offline when the backup completed.

## Retry

`POST /v1/spaces/:spaceId/files/:fileId/retry` makes pending background work due now (→ 204). It is the right response to `limited`: once the user has freed network storage, offer a retry instead of waiting for the slow cadence. `any file retry $SP $FILE`.

## What attach latency includes

Local work only: spooling the body, encrypting it and writing the local CAR. Attach never waits on the network — it queues the backup and returns `durable: false`, whether the broker is reachable or not, and the queue uploads in the background. The registration is already durable in the CRDT; the state only tells you whether the *network* has a copy yet — which is what decides whether [offload](cache.html) is allowed and whether other members can fetch.
