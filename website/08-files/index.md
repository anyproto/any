---
title: Files
description: Files as space data — encrypted, synced and access-controlled like every other record, with local-first storage tiers and background network backup.
order: 0
---
# Files

A file in any is space data. It is attached to an object, encrypted under the space's key, replicated to every member through the same sync engine as records, and gated by the same ACL. There is no separate blob service to authenticate against, and no moment where a plaintext byte leaves your device.

## The model

- **Files bind to objects.** There are no standalone file objects: a file is attached to an existing object, and the SDK derives a per-object *payloads* child on the first attach. One `payloads` row per file.
- **Each row is split cleartext / sealed.** The network-visible fields — `rootCid`, `size`, `networkSign`, `objectId` — are what custody, quota and garbage collection need. The member-only part — file key, `name`, `mime`, sha256, inline bytes — lives in one field sealed under the space read key. Typed reads (`GET /files`, `GET /files/:fileId`) unseal it; the raw payload-row query does not.
- **Two storage tiers, invisible to callers.** Files under **4096 bytes** ride inline in the sealed row: no `rootCid`, durable by construction, nothing to back up. Larger files are encrypted, chunked into a content-addressed DAG, kept locally as one CARv2 per `rootCid`, and backed up to the network's file broker in the background.
- **Deduplication is per space** by content address: attaching the same bytes twice shares the local CAR. **`fileId` is not `rootCid`** — `fileId` is the per-attach row id every endpoint takes; two attaches of the same bytes yield two fileIds sharing one rootCid.

> **Why it matters.** A hosted backend stores your files where the operator can read them and serves them through a URL anyone with the token can fetch. Here the file is encrypted before it is content-addressed; the network node holding the backup sees ciphertext, a size and a hash, and only members holding the space key can ever turn it back into a photo.

## The surface at a glance

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces/:spaceId/objects/:objectId/files` | attach (raw body upload) → 201 `FileInfo` |
| GET | `/v1/spaces/:spaceId/files` | list files (`?objectId=`, `?limit=`) |
| GET | `/v1/spaces/:spaceId/files/:fileId` | one file's info |
| GET | `/v1/spaces/:spaceId/files/:fileId/content` | download the bytes (Range / 206) |
| GET | `/v1/spaces/:spaceId/files/:fileId/status` | one file's durability status |
| GET | `/v1/spaces/:spaceId/files/stats` | aggregate durability counts |
| GET | `/v1/spaces/:spaceId/files/subscribe` | durability transitions (SSE) |
| POST | `/v1/spaces/:spaceId/objects/:objectId/files/query[/subscribe]` | windowed query over one object's payload rows |
| POST | `/v1/spaces/:spaceId/files/:fileId/pin` / `retry` / `offload` | local-copy management → 204 |
| DELETE | `/v1/spaces/:spaceId/files/:fileId` | delete for every member → 204 |
| GET / POST | `/v1/files/cache`, `/v1/files/cache/free`, `/v1/files/cache/sweep` | account-wide local cache |

The file **bytes ride plain HTTP** — upload is a raw POST body, download a raw GET response — the two deliberate non-JSON bodies in the API. Everything else is JSON.

## Errors

| Code | Status | Meaning |
|------|--------|---------|
| `file.not_found` | 404 | unknown fileId, unknown objectId on attach, or a payload query against an object with no files yet |
| `file.not_durable` | 409 | offload refused: the local bytes are the only copy |
| `file.not_available` | 409 | content not local and not fetchable yet — retry later |
| `file.variant_invalid` | 400 | broken `variant` / `variantOf` pairing |

CLI: the `any file …` group mirrors every endpoint (`attach`, `list`, `get`, `download`, `status`, `stats`, `subscribe`, `pin`, `retry`, `offload`, `delete --yes`, `query`, `query-subscribe`, `cache`).

<div class="cards">
<a href="uploading.html"><strong>Uploading</strong><span>Attach a raw body to an object; inline vs DAG tiers, names, mime, variants.</span></a>
<a href="downloading.html"><strong>Downloading</strong><span>Stream verified plaintext as a normal HTTP resource with Range support; render by URL.</span></a>
<a href="status-and-durability.html"><strong>Status and durability</strong><span>durable / inflight / limited, the status stream, and how a receiver learns a file is fetchable.</span></a>
<a href="cache.html"><strong>Cache</strong><span>Pin, offload, and reclaim local bytes; the account-wide cache controls.</span></a>
<a href="deleting.html"><strong>Deleting</strong><span>Delete for every member, variant cascade, and what happens to the bytes.</span></a>
</div>
