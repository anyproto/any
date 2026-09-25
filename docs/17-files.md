# Files (files v2)

The `any` wrap of the SDK's files-v2 subsystem: file payloads stored
as **space data** — same sync, same ACL, same encryption as every
other record. This doc covers the model; the endpoint catalog
lives in [`03-api.md` § Files](03-api.md#files-files-v2), the SSE
frames in [`04-events.md`](04-events.md), the CLI in
[`01-cli.md`](01-cli.md), config in [`05-config.md`](05-config.md),
and the client recipe in [`08-clients.md`](08-clients.md).

## Model

- **Files bind to objects.** There are no standalone file objects: a
  file is attached to an existing object (`POST
  /objects/:objectId/files`), and the SDK lazily derives a per-object
  **payloads child object** on the first attach. One `payloads` row
  per file.
- **The payloads row is split cleartext / sealed.** Cleartext,
  node-readable fields — `rootCid`, `size`, `networkSign`, `author`,
  `objectId` — are what the network needs for custody/quota/GC. The
  member-only secrets — file key, `name`, `mime`, sha256, inline bytes
  — live in one sealed field encrypted under the space read key.
  Consequence for readers: the typed views (`GET /files`,
  `GET /files/:fileId`) unseal name/mime; the payload-row query does
  not (and cannot, for a keyless reader).
- **Two storage tiers, invisible to callers.** Files **< 4096 bytes**
  ride inline in the sealed row (`inline: true`, no `rootCid`, durable
  by construction, no network backup needed). Larger files are
  encrypted, chunked into a UnixFS DAG, stored locally as one CARv2
  per `rootCid` under `<account-dir>/files/` (SDK-owned, next to `sdk/`), and
  backed up to the network's fileV2 broker in the background.
- **Deduplication is per-space** by content address: attaching the
  same bytes twice in one space shares the CARv2. No cross-space dedup
  — that's the accepted cost of files being space-local.
- **fileId ≠ rootCid.** `fileId` is the payloads row id (derived from
  the creating change, unique per attach); `rootCid` is the content
  address. Two attaches of the same bytes yield two fileIds sharing
  one rootCid.

## Durability states

`GET /files/:fileId/status`, the `status` SSE frames, and
`GET /files/stats` all speak the same three-state vocabulary:

| State | Meaning |
|-------|---------|
| `durable` | a verified network-custody receipt (`networkSign`) is recorded on the row — or the file is inline |
| `inflight` | registered in the CRDT; backup queued, running, or being driven by another device |
| `limited` | the network refused backup (storage limit); retried on a slow cadence and on `POST …/retry` |

Attach is **offline-first** and never waits on the network: the
request registers the file in the CRDT, stores the bytes locally,
queues the backup and returns. Attach latency is local work only —
spooling, encryption, the local CAR. The reply says `durable: true`
only for an inline file or one whose content is already backed up in
the space (dedup); otherwise `durable: false`. The backup ("durable")
phase runs on a persistent background queue that starts the upload at
once and retries with backoff when the broker is unreachable or
refuses. Jobs survive a restart and their backoff does not: a file
attached offline backs up as soon as the server next starts online.
No fileV2 nodes in the nodeconf (or no connectivity) means files sit
`inflight` until the network appears; a storage-limit refusal is
`limited`. Several backups run at once, files that share content share
one upload, and deleting a file cancels its upload. From the moment
attach returns, local-network peers can fetch the file from this device
(§ Reads) — before and regardless of the backup.

`GET /files/subscribe` streams `FileStatus` on **local** transitions
only — attach, backup progress/failure, pin completion, manual
retries. **How a receiver learns a remote file became fetchable**: the
custody receipt (`networkSign`) is a synced cleartext field on the
payload row, so the durable flip arrives as an ordinary row-update
event on `…/files/query/subscribe` (or as `durable: true` on a
re-GET) — not on the status stream. There is no synced file event
feed and no space-wide live rows feed: clients re-list, subscribe per
object (`files/query/subscribe`), or ride the status stream.

## Reads: which endpoint when

1. **Typed member view** — `GET /files[?objectId=]`,
   `GET /files/:fileId`: unsealed `FileInfo` incl. name/mime. The
   normal client path. The unfiltered listing walks every file in the
   space — fine at human scale; page with `?limit=` for huge spaces.
2. **Windowed query/subscribe** —
   `POST /objects/:objectId/files/query[/subscribe]`: the generic
   snapshot+SSE primitive over one object's payload rows (cleartext
   fields only; `404 file.not_found` until the object's first attach).
   Use it for live per-object file lists.
3. **Durability liveness** — `GET /files/subscribe` (status stream)
   and `GET /files/:fileId/status` / `GET /files/stats` (reads).

## Download semantics

The stored mime is resolved once, at attach: an explicit
`Content-Type` wins, else the content's magic numbers over the first
4096 bytes for binary formats, while for text the name's extension
decides (text has no signatures, only heuristics). Unplaceable
content stays unset. Full precedence in `docs/03-api.md` § Files.

`GET /files/:fileId/content` serves the verified plaintext as a
regular HTTP resource — stored mime, `Content-Disposition`,
`Content-Length`, `Range`/206 (the SDK reader is seekable; seeks map
to DAG offsets, so a video scrub does not download the prefix).
Content not yet local streams in on demand and every fetched block
persists — reads accrete toward a complete local copy. The SDK fetches
from a local-network peer that holds the complete file (`p2p.enabled`,
on by default) and otherwise from the network's public read base,
which serves durable files only. When
neither can serve the bytes — no peer holds them and the file isn't
durable yet, or the network advertises no public read base — the
endpoint returns `409 file.not_available`; retry once the row gains
`networkSign`. Downloads are plain HTTP responses (not SSE): one in
flight when the server shuts down is cut by the 10s drain deadline;
the client retries with a `Range` from where it stopped.

## Cache, offload, pin

Local bytes (CARv2s) are a cache once a file is durable:

- `POST /files/:fileId/offload` drops one file's local bytes; a later
  download refetches transparently. Refused with `409
  file.not_durable` while the local bytes are the only copy. Inline
  files are a no-op. Content shared via dedup loses its bytes for all
  sharing files (each stays refetchable).
- `POST /files/:fileId/pin` schedules a full background fetch into the
  local store (survives restarts) — for "keep this available offline".
- Account-wide: `GET /v1/files/cache` (size), `POST
  /v1/files/cache/free` (LRU reclaim of safe-to-drop content, returns
  bytes actually freed), `POST /v1/files/cache/sweep` (one safety
  pass: prune deleted-file refs, delete unreferenced content past
  grace, drop stale partials).
- **No background GC by default.** The sweep runs periodically only
  when `files.gcInterval` is set (`05-config.md`); otherwise
  reclamation is entirely caller-driven. Deleting a space offloads all
  its local state including file bytes (`03-api.md` § Spaces).

## Delete

`DELETE /files/:fileId` removes the file for **every member**: the
payload row is deleted in one synced change, so it disappears from
`GET /files`, `/files/query` snapshots, and live `/files/query/subscribe`
windows (as a `removed`) on every device once the deletion syncs.
Deleting an original **cascades to its variants** — they are
unresolvable without it. Locally, pending background work (backup,
pin) is cancelled and the content ref released; the bytes themselves
are reclaimed by cache GC (the safety sweep, once the CAR is
unreferenced past grace — deletion never races a settling sync).
Content shared with a surviving file via dedup keeps its bytes.

Unknown or already-deleted ids → `404 file.not_found` (delete is not
idempotent over the wire). The **network copy is not reclaimed** —
fileprotov2 has no delete RPC; the broker's row-driven accounting
stops counting the rows once the deletion syncs.

## Variants

A **variant** is an alternate representation of a file — e.g. a
thumbnail the client rendered — attached as an ordinary sibling file
on the same object with `?variant=<tag>&variantOf=<originalFileId>`
(both or neither; the original must live on the same object). It has
its own fileId, tier, durability and lifecycle.
`GET /files/:fileId/content?variant=<tag>` resolves the sibling from
the original's id. The variant tag vocabulary is open — clients agree
on tags like `thumb` out of band.

## What is deliberately not wrapped

The SDK's files-v2 work also ships broker/embedder primitives —
`Space.Payloads()` (keyless payload view), `Space.TreeHeads()`,
`Service.Track/Evict`, `Headless` mode, `Sync.TreeTypes` selective
sync. They exist for the filenode-v2 broker embedding, which links the
SDK directly and doesn't go through `any`'s HTTP server; for a
key-holding `any` client the keyless payload view is strictly less
information than `GET /files`.

## Known limits

- **No synced file event feed / space-wide rows feed** (§ Durability
  states).
- **Search**: file content and file names are not indexed (no chunker
  for the payloads dataset). References to a file (`any://f/…`) in
  text and attachments are link-index edges (`13-index.md` § Links).
