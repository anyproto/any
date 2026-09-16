---
title: Deleting
description: Delete a file for every member in one synced change — variant cascade, what live views see, and what happens to local and network bytes.
order: 50
---
# Deleting

Deleting a file is a synced write: one change removes the payload row for every member, and every device sees it disappear as the deletion syncs. The bytes are a separate, slower story — local copies go through cache reclamation, and the network copy stays until network-side garbage collection exists.

## Delete

```bash
curl -X DELETE "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE"
# → 204
```

```bash
any file delete $SP $FILE --yes
```

The CLI refuses to run without `--yes`.

## What every member sees

The payload row is deleted in one synced change, so the file disappears from `GET /files`, from `…/files/query` snapshots, and from live `…/files/query/subscribe` windows as a `removed` frame on every device once the deletion syncs. A record that still references the `fileId` will find `GET …/files/:fileId` answering `404 file.not_found`; the reference in your own data is yours to clean up.

**Variants cascade.** Deleting an original also deletes its variants (thumbnails and other alternate representations), since they are unresolvable without it.

> **Why it matters.** Delete is an ordinary CRDT operation, so it works offline and converges like any other write: a member who deletes a file on a plane has the deletion applied on every other device when they land, in causal order with everything else they did. There is no server-side "delete job" that can partially succeed.

## What happens to the bytes

Locally, pending background work for the file — backup, pin — is cancelled and the content reference is released. The bytes themselves are reclaimed by [cache](cache.html) garbage collection: the safety sweep deletes the CAR once it is unreferenced past a grace period, so a deletion never races a sync that is still settling. Content shared with a surviving file through per-space deduplication keeps its bytes.

The **network copy is not reclaimed**. The file protocol has no delete RPC yet; the broker's row-driven accounting stops counting the row once the deletion syncs, and network-side reclamation is a roadmap item.

## Watching a deletion land

A client that holds a live per-object file list sees the deletion the same way it sees any record leaving a window — no extra read needed:

```bash
curl -N -X POST "http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/files/query/subscribe" \
  -H 'Content-Type: application/json' -d '{"sort": ["-_ver.id"], "limit": 50}'
```

```
event: changes
data: [{ "versionId": "…", "removed": [{ "id": "<fileId>", "reason": "deleted" }] }]
```

Only `reason: "deleted"` means the file is gone; `"displaced"` means it merely left the window's limit. Drop the row from your view and, if the deleted file had variants, expect their ids to follow. Without a subscription, a `GET …/files?objectId=` after the deletion syncs simply no longer lists the file, and the per-space `GET …/files/stats` counts drop accordingly.

```bash
any file query-subscribe $SP $OBJ --sort=-_ver.id --limit 50     # one JSON line per frame
any file list $SP --object $OBJ
```

## Idempotency and errors

Delete is not idempotent over the wire: an unknown or already-deleted id answers `404 file.not_found`. A UI retry should treat that 404 as success.

| Code | Status | When |
|------|--------|------|
| `file.not_found` | 404 | unknown fileId, or the file was already deleted |

> **Note.** Deleting the *object* a file is attached to does not go through this endpoint, and deleting a whole space offloads all of the space's state including file bytes — see [spaces](../database/spaces.html).
