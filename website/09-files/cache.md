---
title: Cache
description: Local file bytes as a cache once a file is durable — pin for offline, offload to reclaim space, and the account-wide free and sweep controls.
order: 40
---
# Cache

Once a file is backed up to the network, its local bytes become a cache: droppable and refetchable. Until then they are the only copy the network can vouch for — a peer that happens to hold them does not count — and the server refuses to throw them away. Nothing reclaims space on its own unless you configure it to.

## Pin

`POST /v1/spaces/:spaceId/files/:fileId/pin` schedules a full background fetch of the file into the local store (→ 204). The request survives restarts. Use it for "keep this available offline" — a file that was only ever streamed on demand may be partially local; pinning completes it. Completion shows up as a `status` frame on the [status stream](status-and-durability.html) and as `cached: true` on the file's info.

```bash
curl -X POST "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/pin"
any file pin $SP $FILE
```

## Offload

`POST /v1/spaces/:spaceId/files/:fileId/offload` drops one file's local bytes while keeping the file (→ 204). A later download refetches transparently, block by block, and accretes toward a full copy again.

```bash
curl -X POST "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/offload"
any file offload $SP $FILE
```

- Refused with **`409 file.not_durable`** while the local bytes are the only copy — the file has not been backed up yet. A peer copy alone does not satisfy this check.
- A no-op on inline files (their bytes are the CRDT row).
- Content shared through per-space deduplication loses its bytes for every file sharing that `rootCid`; each stays refetchable.

> **Why it matters.** Because the network copy is ciphertext under the space key, offloading is safe by construction: the bytes you drop can only ever be reconstituted by a member holding the key. Space management on a phone becomes a local decision with no privacy cost.

## Account-wide cache controls

The three `/v1/files/cache*` routes operate across **all spaces** on this device, so they sit outside the space group:

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/files/cache` | `{"size": <bytes>}` — bytes held by file content, complete and partial copies |
| POST | `/v1/files/cache/free` | `{"bytes": N}` → `{"freed": M}` — LRU reclaim of safe-to-drop content |
| POST | `/v1/files/cache/sweep` | one safety pass → 204 |

```bash
curl "http://127.0.0.1:7001/v1/files/cache"
curl -X POST "http://127.0.0.1:7001/v1/files/cache/free" \
  -H 'Content-Type: application/json' -d '{"bytes": 500000000}'
# → { "freed": 312044544 }
curl -X POST "http://127.0.0.1:7001/v1/files/cache/sweep"
```

```bash
any file cache size
any file cache free 500000000
any file cache sweep
```

`free` drops least-recently-used content that is **safe to drop** — backed up or unreferenced, never the only copy — and returns the bytes actually freed, which is less than requested when nothing else is safely evictable. `sweep` is the safety pass: it prunes references of deleted files, deletes unreferenced content past its grace period, and drops stale partial fetches.

## No background GC by default

The sweep runs periodically only when `files.gcInterval` is set in the [configuration](../operations/configuration.html); with a zero interval, reclamation is entirely caller-driven. Two consequences:

- Bytes of a [deleted](deleting.html) file are not reclaimed synchronously — they go when a sweep finds the CAR unreferenced past grace.
- Deleting a whole space still offloads all of its state, file bytes included, through the space-delete path.

> **Note.** `free` and `offload` are about the local copy only. Neither touches the network backup, and neither changes the file's durability state — a durable file stays durable with zero local bytes.
