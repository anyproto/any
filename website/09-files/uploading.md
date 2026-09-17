---
title: Uploading
description: Attach a file to an object with a raw-body POST — what gets stored, the inline and DAG tiers, and how variants like thumbnails work.
order: 10
---
# Uploading

Attaching a file is a single POST whose **raw request body is the file** — no JSON envelope, no multipart. Registration is durable in the CRDT the moment the request returns; network backup is a separate concern you never block on.

## Attach

```bash
curl -X POST -T photo.jpg -H 'Content-Type: image/jpeg' \
  "http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/files?name=photo.jpg"
```

```bash
any file attach $SP $OBJ photo.jpg            # name defaults to the basename, mime is resolved by the server (--name, --mime override)
cat photo.jpg | any file attach $SP $OBJ -    # stdin
```

Metadata rides outside the body:

| Where | Stored as |
|-------|-----------|
| `Content-Type` header | the file's `mime`, taken at its word — except `text/plain`, which the name's extension refines (`.md` → `text/markdown`). `application/octet-stream` (the `curl -T` / typeless-`Blob` default) or absent means "not said": the server then resolves the type from the content — magic numbers for binary formats, the name's extension for text (`.md`, `.csv`, `.css`, …) |
| `?name=` | the user-facing `name`. Binary content without an extension gains the one its mime implies (`?name=pasted` + PNG → `pasted.png`); text names come back unchanged |
| `?variant=` + `?variantOf=` | attach as an alternate representation of an existing file on the same object (both or neither) |

The reply is the file's `FileInfo`:

```json
{
  "fileId":   "…",
  "objectId": "OBJ",
  "rootCid":  "bafy…",
  "size":     482113,
  "inline":   false,
  "durable":  false,
  "cached":   true,
  "name":     "photo.jpg",
  "mime":     "image/jpeg"
}
```

| Field | Meaning |
|-------|---------|
| `fileId` | the per-attach handle every other endpoint takes — derived from the creating change, unique per attach |
| `rootCid` | content address of the encrypted bytes; absent for inline files; shared by two attaches of identical bytes |
| `size` | plaintext byte size |
| `inline` | the file rides the CRDT row itself (under 4096 bytes) |
| `durable` | a verified network-custody receipt is recorded, or the file is inline |
| `cached` | a complete local copy exists (always true right after attach) |
| `name`, `mime`, `variant`, `variantOf` | the sealed, member-only metadata |

This is the one route exempt from the global 1 MB request body limit — the body streams straight into the SDK.

## What happens to the bytes

Files **under 4096 bytes** take the inline tier: the bytes live in the sealed part of the payload row, so they sync with the row and are durable by construction (`inline: true`, `durable: true`, no `rootCid`).

Larger files are encrypted, chunked into a content-addressed DAG and stored locally as one CARv2 per `rootCid`. The backup to the network's file broker **never runs inside the attach request**: attach queues it and returns `durable: false`, so attach latency is local work only, online or offline. A persistent background queue — it survives restarts — starts the upload at once and retries with backoff when the broker is unreachable or refuses. A network with no file nodes, or no connectivity at all, simply means files sit `inflight` until the network appears. Treat the 201 as done and don't block the UI on `durable`; see [status and durability](status-and-durability.html) for the badge.

> **Why it matters.** Attach is offline-first. On a plane you can attach a photo to a note, close the laptop, and the registration is already part of the space's history; the upload to the network is bookkeeping that catches up later. A hosted backend's upload either completes now or fails now.

Deduplication is per space by content: attaching the same bytes twice shares one local CAR (and one `rootCid`) under two distinct fileIds. There is no cross-space deduplication — the accepted cost of files being space-local.

## Variants

A **variant** is an alternate representation of a file — typically a thumbnail the client rendered — attached as an ordinary sibling file on the same object:

```bash
curl -X POST -T photo-thumb.jpg -H 'Content-Type: image/jpeg' \
  "http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/files?name=photo-thumb.jpg&variant=thumb&variantOf=$FILE"
```

```bash
any file attach $SP $OBJ photo-thumb.jpg --variant thumb --variant-of $FILE
```

The variant has its own `fileId`, tier, durability and lifecycle, and `GET …/files/:fileId/content?variant=thumb` resolves it from the original's id. The tag vocabulary is open — clients agree on names like `thumb` out of band. `variant` without `variantOf` (or the reverse), or an original living on a different object, is `400 file.variant_invalid`. Deleting the original cascades to its variants (see [deleting](deleting.html)).

## Referencing a file from data

Store the `fileId` in your records — plus whatever you want denormalized for instant rendering; `size`, `mime` and `name` are stable. In markdown text (chat messages, editor blocks) use the [`any://f/` link](../types/links.html):

```
![sunset](any://f/<spaceId>/<fileId>)
```

A chat attachment entry is the same URI: `{"type": "image", "link": "any://f/<spaceId>/<fileId>"}`.

> **Note.** Unknown `objectId` on attach is `404 file.not_found`.
