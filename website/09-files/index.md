---
title: Files
description: Attach files to objects, read them locally or from peers, and manage network backup and disk space.
order: 0
---
# Files

Attach files to existing objects and read them through ordinary HTTP URLs. File registration syncs as part of the space's data. The bytes are encrypted before transfer and available only to members with the space key.

## Choose a task

| I want to… | Guide |
|---|---|
| Attach an image, document, or thumbnail | [Uploading](uploading.html) |
| Display, download, or list attachments | [Downloading](downloading.html) |
| Show backup progress or retry a failed backup | [Status and durability](status-and-durability.html) |
| Keep files offline or free disk space | [Cache](cache.html) |
| Remove a file for every member | [Deleting](deleting.html) |

## Availability and durability

**Available** means the bytes can be read now: from this device, a reachable peer, or the network's stored copy. **Durable** means a verified network backup is recorded, or the file is small enough to travel inline with its registration.

A peer can serve a file before its network backup finishes. Attempt a download when its row arrives; do not wait for `durable: true` to display it. The server returns `409 file.not_available` when it has no usable source. Offloading local bytes has a stricter rule: a network backup must exist.

## The model

- **One attachment, one `fileId`.** The first attach creates a derived child object holding `payloads` records. Every file endpoint takes the attachment's `fileId`.
- **Metadata has two parts.** Fields such as `rootCid`, `size`, `networkSign`, and `objectId` are visible to network nodes. The file key, name, mime, SHA-256, and inline bytes are sealed under the space read key. Typed file GETs unseal them; raw payload queries do not.
- **Two storage tiers.** Files under 4096 bytes travel inside the sealed row and have no `rootCid`. Larger files are split into encrypted blocks, stored locally in a CARv2 file (a container for those blocks), and backed up through the network's file broker. Attach attempts backup synchronously; a persistent queue retries failures.
- **Content can be shared within a space.** Attaching identical bytes twice can reuse one local CAR and `rootCid`, but creates two `fileId`s. Deduplication does not cross spaces. `rootCid` identifies encrypted content, not an attachment.

## The surface at a glance

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces/:spaceId/objects/:objectId/files` | raw body upload → 201 `FileInfo` |
| GET | `/v1/spaces/:spaceId/files` | list files (`?objectId=`, `?limit=`) |
| GET | `/v1/spaces/:spaceId/files/:fileId` | one file's info |
| GET | `/v1/spaces/:spaceId/files/:fileId/content` | download bytes (Range / 206) |
| GET | `/v1/spaces/:spaceId/files/:fileId/status` | one file's durability status |
| GET | `/v1/spaces/:spaceId/files/stats` | durability counts |
| GET | `/v1/spaces/:spaceId/files/subscribe` | local durability transitions (SSE) |
| POST | `/v1/spaces/:spaceId/objects/:objectId/files/query[/subscribe]` | a window over one object's payload rows |
| POST | `/v1/spaces/:spaceId/files/:fileId/pin` / `retry` / `offload` | local-copy management → 204 |
| DELETE | `/v1/spaces/:spaceId/files/:fileId` | delete for every member → 204 |
| GET / POST | `/v1/files/cache`, `/v1/files/cache/free`, `/v1/files/cache/sweep` | account-wide cache management |

Upload bodies and download responses contain raw bytes. The other file endpoints use JSON. The `any file …` CLI group mirrors these endpoints: `attach`, `list`, `get`, `download`, `status`, `stats`, `subscribe`, `pin`, `retry`, `offload`, `delete --yes`, `query`, `query-subscribe`, and `cache`.

## Errors

| Code | Status | Meaning |
|------|--------|---------|
| `file.not_found` | 404 | unknown fileId, unknown objectId on attach, or a payload query before an object's first attach |
| `file.not_durable` | 409 | offload refused because no verified network backup is recorded |
| `file.not_available` | 409 | bytes are neither local nor available from a usable peer or network source |
| `file.variant_invalid` | 400 | invalid `variant` / `variantOf` pairing |
