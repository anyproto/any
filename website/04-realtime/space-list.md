---
title: Live space list
description: Query and subscribe to the account's own space list as raw rows — joins, role changes, settings and push keys arrive as row updates.
order: 30
---
# Live space list

The account's list of spaces is itself data — rows in the `spaces` dataset of the account's private tech space, replicated to every device the account owns. The same windowed query/subscribe primitive that reads any dataset reads that list, so a sidebar can be a live subscription instead of a polling loop.

## Endpoints

```
POST /v1/spaces/query              snapshot   → { records, total?, hasNext? }
POST /v1/spaces/query/subscribe    SSE        → ready → snapshot → changes → closed
```

Both take the standard snapshot body (`filter` / `sort` / `limit` / `offset` / `includeTotal` / `mailboxCapacity` / `driftBudgetPercent` — see [Subscribe](subscribe.html)) plus an optional `dataset`. The object is fixed server-side to the tech-space index object; you never pass an `objectId`.

| `dataset` | Rows |
|---|---|
| `spaces` (default) | one row per space the account knows |
| `profile` | the account's own profile row |

The allowlist is closed: any other name is `400 request.invalid_field`. The same index object hosts the identities directory, whose rows carry a synced decryption key — that dataset is deliberately unreachable here and is read through `GET /v1/identities` instead (see [Identities](../auth/identities.html)).

```bash
curl http://127.0.0.1:7001/v1/spaces/query \
  -H 'Content-Type: application/json' \
  -d '{"filter":{"status":"active"},"sort":["-createdAt"],"limit":50,"includeTotal":true}'

any space query --filter '{"status":"active"}' --sort -createdAt --limit 50 --total
```

## Raw rows, not `SpaceInfo`

`GET /v1/spaces` returns the mapped `SpaceInfo` shape and defaults to active spaces only. The query and subscribe endpoints return the **raw tech-index rows** — every status, no projection, system fields included. Use the GET when you want the convenience shape; use query/subscribe when you want filtering, sorting, or liveness.

Fields worth knowing on a raw row:

| Field | Notes |
|---|---|
| `status` | `active`, `joining`, `one_to_one_pending`, `one_to_one_declined`, `deleted`, … — filter on it; deleted rows stay as sticky tombstones |
| `createdAt` | added-to-account instant, `{"$date": "…"}`; absent on rows stamped before the field existed. Sort `["-createdAt"]`, range-filter with `{"$gte": {"$date": "…"}}` |
| `ownRole` | your own role, mirrored from ACL state: `owner` / `admin` / `writer` / `reader` / `guest` / `none` |
| `settings` | the account-private settings object written via `PATCH /v1/spaces/:id/settings` (e.g. `notifyMode`) |
| `push` | `{spaceKey, encKey, encKeyId}` push key material; absent until the mirror has run for the space |
| `derived` | set on well-known [derived spaces](../collaboration/derived-spaces.html), which cannot be deleted |

## What streams as row updates

Because these are plain row fields, changes to them arrive as ordinary `updated` events on the subscribe stream — no dedicated streams needed:

- **A space joined on another device**, or one that head-synced in, arrives as `added`.
- **A role change** — a grant, promotion or demotion applied by another peer — updates `ownRole` on the row. Role-dependent UI can key off the list subscription instead of fanning out `GET …/members/me` calls per space. Caveats: `"none"` also means "not mirrored yet" (a space this device has not loaded, a pending join, a tombstone), and both participants of a [one-to-one](../collaboration/one-to-one.html) space report `writer`; `GET /v1/spaces/:id/members/me` stays the authoritative per-space read.
- **A read-key rotation** updates `push.encKey` / `push.encKeyId`. Mobile clients keep this stream open while the app is in the foreground so the new key is cached before the first push encrypted under it lands — see [Push notifications](../notifications/push.html).
- **A settings change on another device** (muting a space, say) updates `settings`.
- **A deletion** on any device arrives as a `removed` frame; consumers holding derived per-space state (caches, indexes) purge on it — the server's own search indexer does exactly this.

```bash
curl -N http://127.0.0.1:7001/v1/spaces/query/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"sort":["-createdAt"],"limit":100}'

any space subscribe --sort -createdAt --limit 100 \
  | jq 'select(.event=="changes") | .data[] | .updated[]? | {id, ownRole: .doc.ownRole}'
```

> **Why it matters.** There is no directory service answering "which spaces do I have". The list is CRDT data owned by the account, so every device converges on the same rows — offline joins, renames on a phone, a demotion by a co-owner — without a central lookup, and the list stays readable with no network at all.

## Space metadata is elsewhere

A space's `name` / `description` / `icon` are **member-replicated**, not account-private, and live in a derived in-space `spaceIndex` object rather than on this row. To live-update a space's title, subscribe to that object in the space itself: take `spaceIndexObjectId` from `GET /v1/spaces/:id` and open `POST /v1/spaces/:id/objects/query/subscribe` filtered on that id. Write it with `PATCH /v1/spaces/:id`; write account-private settings with `PATCH /v1/spaces/:id/settings`. See [Spaces](../database/spaces.html).

> **Note.** The frame set and `closed` reasons are identical to every other windowed subscribe. Recover from `overflow` / `drifted` / `server_shutdown` / `sdk_closed` the same way: reopen the POST and take the fresh snapshot.
