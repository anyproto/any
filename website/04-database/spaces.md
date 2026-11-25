---
title: Spaces
description: Create, list, update and delete spaces — the encrypted, shareable containers every object lives in.
order: 10
---
# Spaces

A space is the unit of sharing and encryption: one read key, one member list, one set of objects. Everything you store belongs to exactly one space.

## Create a space

```bash
curl -X POST http://127.0.0.1:2046/v1/spaces \
  -H 'Content-Type: application/json' \
  -d '{"name": "Project Phoenix", "description": "shared notes + chat"}'
```

The body is `{name?, description?, iconCid?, spaceType?, anySpaceType?}`. The reply is a `201` with the space's `SpaceInfo`. `spaceType` is pinned at create and cannot be patched later. `anySpaceType` says what the account uses the space for: a lowercase slug of at most 64 characters (`[a-z0-9][a-z0-9._-]*`, else `400 request.invalid_field`), given at create; no route changes it afterwards.

## The `SpaceInfo` shape

Every single-space response (`POST /v1/spaces`, `GET /v1/spaces/:spaceId`) returns the same object:

| Field | Meaning |
|-------|---------|
| `id` | The space id (base58). |
| `spaceType` | App-level classification: `any.space` for a created space, `any.onetoone` for a direct space. |
| `anySpaceType` | What the account uses the space for, such as `bao` for its agent space. The account's own: synced across its devices with the space list, readable before the space loads, never visible to other members. Omitted on a space the account gave no use. |
| `author` | The owner's account identity, resolved from the ACL (omitted when not loadable). |
| `name`, `description`, `iconCid` | Member-replicated metadata. |
| `status` | `active`, `deleted`, `joining`, `one_to_one_pending`, `one_to_one_declined`, `invite_pending`, `invite_declined`, `guest_revoked`. |
| `ownRole` | Your role: `owner` / `admin` / `writer` / `reader` / `guest` / `none`. `none` also means "not mirrored yet" — `GET …/members/me` is the authoritative read. |
| `createdAt` | RFC 3339 added-to-account time (create for the author, join for a joiner). The zero time means unknown. |
| `spaceIndexObjectId` | The deterministic id of the in-space object that owns the metadata — subscribe to it for live name/icon updates. |
| `settings` | Your account-private per-space settings (omitted when never written). |
| `derived` | `true` for permanent account-derived spaces (see [Derived spaces](../collaboration/derived-spaces.html)). |
| `push` | Push key material `{spaceKey, encKey, encKeyId}` (see [Push](../notifications/push.html)). |

## List spaces

```bash
curl http://127.0.0.1:2046/v1/spaces               # active spaces only
curl 'http://127.0.0.1:2046/v1/spaces?status=all'  # every row, tombstones included
curl 'http://127.0.0.1:2046/v1/spaces?status=invite_pending'
```

`GET /v1/spaces` returns the mapped `SpaceInfo` rows and hides non-active spaces by default. Pending invites, incoming direct spaces and deleted tombstones are the same list filtered on `status`.

For a filterable, sortable or live view use the query primitive over the raw space rows — `POST /v1/spaces/query` and `POST /v1/spaces/query/subscribe` take the standard [query body](reading-data.html). Rows there carry `createdAt` as an instant, so `{"sort": ["-createdAt", "-id"]}` is newest-first. See [Space list](../realtime/space-list.html).

```bash
any space query --sort -createdAt,-id --limit 10
any space subscribe
```

## The account's agent space

The account's agent space is a regular space whose row carries `anySpaceType: "bao"`. Find it in the space list: the active row with that value; with more than one, the row with the earliest `createdAt` (the lower `id` on a tie).

```bash
curl -s http://127.0.0.1:2046/v1/spaces | jq '[.spaces[] | select(.anySpaceType == "bao")]'
```

An account with no such row creates it:

```bash
curl -X POST http://127.0.0.1:2046/v1/spaces \
  -H 'Content-Type: application/json' \
  -d '{"name": "Bao", "anySpaceType": "bao"}'
```

The create needs no network, and the space's [bundles](../collaboration/bundles.html) work at once. Creating it is an explicit act: the create-account flow, or a user's "set up" action on an account that has none, never a boot step. A device that logged in has synced the space list once, so an absent row there means no device had created one by then, or that this device received the row while its `any` predated the field. On a later start the list is what the device held when it stopped: the account opens without waiting for the network, and a row another device created meanwhile arrives with the tech space's next sync. Offer the create only while the list has no such row and the tech space reads `state: "synced"` (`GET /v1/spaces/<techSpaceId>/sync-status`, with `techSpaceId` from `GET /v1/account`), and keep watching the list: a row from a device that was offline can still arrive. A [derived](../collaboration/derived-spaces.html) `bao` space that an earlier installation created is a different space: its row carries no `anySpaceType`.

## Read one space

```bash
curl http://127.0.0.1:2046/v1/spaces/$SPACE
any space get $SPACE
```

Only active spaces are materialised on read. A pending or deleted row is served straight from the account's index — plain row info, no `spaceIndexObjectId`, nothing downloaded. That is deliberate: reading a pending invite must not pull the space's content before you accept it.

## Update metadata

```bash
curl -X PATCH http://127.0.0.1:2046/v1/spaces/$SPACE \
  -H 'Content-Type: application/json' \
  -d '{"name": "Phoenix", "description": ""}'
any space update $SPACE --name Phoenix --description ""
```

`PATCH /v1/spaces/:spaceId` takes `{name?, description?, iconCid?}` and returns `204`. A key absent from the body leaves the field alone; a key present with `""` clears it. At least one key is required (`400 request.missing_field`).

The write lands in the space and replicates to every member. Each peer mirrors the converged value back into its own space list asynchronously, so an immediate re-`GET` can briefly return the old name. Attach a subscription filtered on `spaceIndexObjectId` when you need the converged state.

## Account-private settings

Settings are yours alone: they sync across your own devices and are invisible to other members.

```bash
curl -X PATCH http://127.0.0.1:2046/v1/spaces/$SPACE/settings \
  -H 'Content-Type: application/json' \
  -d '{"set": {"notifyMode": "mentions"}, "unset": ["oldKey"]}'
any space settings $SPACE --set notifyMode=mentions
```

Keys are single-level strings, values are scalars (string, number, bool). Read them back from `SpaceInfo.settings`. Works on any row the account knows — including a pending direct space you want to mute before accepting.

> **Note.** Metadata (`PATCH /v1/spaces/:spaceId`) is member-visible; settings (`PATCH …/settings`) are account-private. They are separate endpoints on purpose so a write never lands in the wrong audience.

## Sync now

```bash
curl -X POST http://127.0.0.1:2046/v1/spaces/$SPACE/sync   # → 204
any space sync $SPACE
```

Forces an immediate head-sync round against the network instead of waiting for the periodic timer, and blocks until it completes. Normal operation never needs it; use it for a "sync now" button or to collapse convergence waits in tests. Ongoing state lives at [Sync status](../realtime/sync-status.html).

## Delete a space

```bash
curl -X DELETE http://127.0.0.1:2046/v1/spaces/$SPACE   # → 204
any space delete $SPACE --yes
```

Deletion is offline-first. The call returns as soon as the local half is done: a synced `deleted` tombstone is written, all local storage for the space is dropped (disk reclaimed even offline), and a background reconciler sends the signed network delete when it can. Only the owner deletes on the network side; a member deleting a space they joined offloads it locally.

The row stays in the account's list with `status: "deleted"` as a sticky tombstone, which is why the default list filters to active. `GET /v1/spaces/:spaceId` on it still answers `200` with that status, while every write and space-scoped read is `409 space.deleted` — branch on `status`, not on the status code. Derived spaces refuse deletion with `409 space.derived_undeletable`; an unknown id is `404 space.not_found`. On a `joining` row the call withdraws the join request instead: the row reads `deleted` and stays re-joinable.

## Related

- [Members and roles](../collaboration/members-and-roles.html), [Invites](../collaboration/invites.html) — sharing a space.
- [One-to-one](../collaboration/one-to-one.html) — direct spaces derived from two identities.
- [Derived spaces](../collaboration/derived-spaces.html) — well-known per-account spaces.
