---
title: Downloading
description: Read file bytes as a regular HTTP resource — mime, filename, Range/206, on-demand network fetch — and list files with names or as live rows.
order: 20
---
# Downloading

The content endpoint serves a file's verified plaintext as an ordinary HTTP resource. Point an `<img>` or `<video>` tag at it and let the browser stream and seek; there is no token exchange, no signed URL, and no client-side decryption step — the server on your device does the unsealing.

## Content

```bash
curl -o photo.jpg "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/content"
curl -o thumb.jpg "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/content?variant=thumb"
```

```bash
any file download $SP $FILE > photo.jpg      # raw bytes on stdout
any file download $SP $FILE -o photo.jpg     # writes the file, prints a small JSON receipt
any file download $SP $FILE --variant thumb
```

The response carries the stored mime as `Content-Type` (octet-stream fallback — the type is resolved at attach, never here), `Content-Disposition: inline; filename=…` from the stored name, `Content-Length`, and full **`Range` / 206** support. The underlying reader is seekable and seeks map to DAG offsets, so scrubbing a video does not download the prefix. Browser tags work directly:

```html
<img   src="http://127.0.0.1:7001/v1/spaces/SP/files/F1/content">
<video src="http://127.0.0.1:7001/v1/spaces/SP/files/F2/content" controls>
```

## When the bytes are not local

Content that is not yet local streams in from the network on demand, and every fetched block persists — repeated reads accrete toward a complete local copy. To fetch a whole file ahead of time, [pin](cache.html) it.

A file whose bytes are neither local nor fetchable yet answers **`409 file.not_available`**. This is a retry-later resource state, not a fault: the file is not durable yet (its sender's backup has not completed), or the network advertises no public read base for fetches. The signal that it became fetchable is the payload row gaining `networkSign` — an `updated` frame on `…/files/query/subscribe`, or `durable: true` on a re-GET of `…/files/:fileId`. Wait for that event rather than polling in a loop; see [status and durability](status-and-durability.html).

Downloads are plain HTTP responses, not SSE: one in flight when the server shuts down is cut by the drain deadline, and the client retries with a `Range` from where it stopped.

> **Why it matters.** The same URL works with the network down, as long as the bytes are local — and once fetched, they stay. A member who opened a photo once on a train keeps it without any "make available offline" step, because the local store is the primary copy, not a cache in front of someone else's server.

## Listing files

Two read paths, for two needs:

**Typed member view** — the normal client path, with the sealed name and mime unsealed:

```bash
curl "http://127.0.0.1:7001/v1/spaces/$SP/files?objectId=$OBJ"
curl "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE"
```

```json
{ "files": [ { "fileId": "…", "objectId": "…", "rootCid": "bafy…", "size": 482113,
               "inline": false, "durable": true, "cached": true,
               "name": "photo.jpg", "mime": "image/jpeg" } ] }
```

`any file list $SP [--object OBJ] [--limit N]` and `any file get $SP $FILE` are the CLI forms. The unfiltered listing walks every file in the space — fine at human scale; page with `?limit=` for huge spaces.

**Windowed payload-row query / subscribe** — the generic snapshot + SSE primitive over one object's rows, for live per-object file lists:

```bash
curl -N -X POST "http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/files/query/subscribe" \
  -H 'Content-Type: application/json' -d '{"sort": ["-_ver.id"], "limit": 50}'
```

Body and frames are identical to every other [query](../database/reading-data.html) and [subscribe](../realtime/subscribe.html): `filter`, `sort`, `limit`, `offset`, `includeTotal`; `ready` → `snapshot` → `changes`. It exists because the `payloads` dataset lives on a derived child object whose id clients don't know, so the generic `/query` cannot reach it. Rows expose the **cleartext fields only** — `id`, `rootCid`, `size`, `networkSign`, `objectId` — never name, mime or key; join names from a `GET /files` pass. It returns `404 file.not_found` until the object's first attach (the backing dataset materializes then) — treat that as an empty list.

> **Note.** A file another member attached shows up as an `added` row on this stream, and the moment it becomes fetchable shows up as an update on the same row when `networkSign` lands. That is the receive-side signal — not the per-space status stream, which reports local transitions only.
