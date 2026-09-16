---
title: Downloading
description: Display or download files with HTTP, read from local or peer sources, and keep an attachment list current.
order: 20
---
# Downloading

Use a file's `fileId` to download its bytes or display it in a browser. The local Any server verifies and decrypts the content. The response is a regular HTTP resource with content type, filename, length, and range support.

**Before you start:** the server must be running with the account that can read the space. Set `$SP` to the space ID and `$FILE` to an attachment's `fileId`, returned by [upload](uploading.html) or the file listing below. `$OBJ` is the object it is attached to.

## Content

```bash
curl -o photo.jpg "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/content"
curl -o thumb.jpg "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE/content?variant=thumb"
```

The second request needs a `thumb` variant attached to the original file. The [uploading guide](uploading.html#variants) shows how to create one.

```bash
any file download $SP $FILE > photo.jpg      # raw bytes on stdout
any file download $SP $FILE -o photo.jpg     # save bytes; print a JSON receipt
any file download $SP $FILE --variant thumb
```

The response uses the stored mime as `Content-Type`, with an octet-stream fallback, and the stored name in `Content-Disposition: inline; filename=…`. Mime is resolved at attach time. `Content-Length` and **Range / 206** are supported: a video seek reads the required blocks without downloading the prefix.

Replace the IDs in these URLs with your own:

```html
<img   src="http://127.0.0.1:7001/v1/spaces/SP/files/F1/content">
<video src="http://127.0.0.1:7001/v1/spaces/SP/files/F2/content" controls>
```

There is no file token exchange or client-side decryption step. The local server must still be running and authorized for the intended account.

## When the bytes are not local

The SDK reads local blocks first. For missing bytes it prefers a reachable peer holding the complete file, then falls back to the network's public read source. Peer transfer is enabled through the P2P configuration; a public network read requires a recorded backup.

**Try the content GET as soon as the file appears.** An `inflight` file can already be readable from a peer. `networkSign` and `durable: true` report backup, not every possible source of bytes.

A file with no usable source answers **`409 file.not_available`**. Keep the attachment in the UI with an unavailable state. Retry when connectivity or peer availability changes, when the user opens it again, or when its row gains `networkSign`. Back off between retries; a backup receipt is one useful signal, not a requirement for the first attempt.

Every fetched block is stored locally. Repeated reads build toward a complete local copy; [pin](cache.html) schedules a full fetch when you want the whole file available offline. Local data remains readable without a network connection until cache management removes it.

A download is an ordinary HTTP response. If shutdown cuts it off at the drain deadline, resume with a `Range` request from the last byte received.

## Listing files

Choose a typed listing for metadata, or a payload subscription for a live list.

### Typed member view

```bash
curl "http://127.0.0.1:7001/v1/spaces/$SP/files?objectId=$OBJ"
curl "http://127.0.0.1:7001/v1/spaces/$SP/files/$FILE"
```

```json
{ "files": [ { "fileId": "…", "objectId": "…", "rootCid": "bafy…", "size": 482113,
               "inline": false, "durable": true, "cached": true,
               "name": "photo.jpg", "mime": "image/jpeg" } ] }
```

The listing unwraps the sealed metadata for members. CLI equivalents are `any file list $SP [--object OBJ] [--limit N]` and `any file get $SP $FILE`.

Without `objectId`, the listing walks all files in the space. `?limit=N` caps the response; it is not a pagination cursor. For an ordered, paged list on one object, use the payload query below and join its IDs with typed file GETs.

### Windowed payload-row query / subscribe

```bash
curl -N -X POST "http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/files/query/subscribe" \
  -H 'Content-Type: application/json' -d '{"sort": ["-_ver.id"], "limit": 50}'
```

The body and frames follow the [query](../database/reading-data.html) and [subscribe](../realtime/subscribe.html) contracts: `filter`, `sort`, `limit`, `offset`, `includeTotal`, and `ready` → `snapshot` → `changes`. A positive subscription limit requires a sort. Omit `/subscribe` for a one-shot query.

The endpoint resolves the derived child holding `payloads`; clients do not need its ID. Rows expose cleartext fields such as `id`, `rootCid`, `size`, `networkSign`, and `objectId`. They do not expose name, mime, or key; get display metadata from typed file reads.

Until the object's first attach, the query returns `404 file.not_found`: treat that as an empty attachment list. Once subscribed, replace state on each snapshot, apply additions and updates, and remove IDs listed in `removed`. A remote attachment arrives as an `added` row; a later `networkSign` update reports its network backup. The [per-space status stream](status-and-durability.html#the-status-stream) reports local transitions only.
