---
title: The tech space
description: The account's private, owner-only derived space — what it stores (space list, profile, devices, settings, account-scoped values, identities), how the space list and its live stream are views over it, the mirrors that feed it, and why "my devices" semantics fall out of it.
order: 18
---
# The tech space

Every account has exactly one **tech space**: a space derived from the account keys, with an owner-only ACL, that syncs only between that account's own devices. It is where any keeps everything that is *about the account* rather than about a shared space — starting with the list of spaces itself.

## What it is

- **Derived.** Its id is a pure function of the account key; every device of the account computes the same id and re-creates it on first login. Its header carries `spaceType: any.techspace`.
- **Owner-only.** The ACL has one member and the network refuses any further ACL record. Nothing in it can be shared; there is no such thing as a shared account.
- **Hidden.** It is not a `Space` in the API — no `/v1/spaces/<techId>` — but its datasets are read through dedicated endpoints, and it uses the same [record CRDT](record-crdt.html) as every other space, so writes to it sync, merge and converge like any other data.
- **First.** It loads before regular spaces at boot, because the space list comes from it.

## What it holds

The tech space carries one derived **index object** whose datasets are the account's system data:

| Dataset | One row per | Read through | Written by |
|---------|-------------|--------------|------------|
| `spaces` | space the account participates in | `GET /v1/spaces`, `POST /v1/spaces/query[/subscribe]` | the SDK on create / join / delete, and the mirrors below |
| `profile` | the account | `GET /v1/account`, `POST /v1/spaces/query` with `"dataset": "profile"` | `PUT /v1/account/metadata` |
| `devices` | device (row id = peer id) | `GET /v1/devices`, `POST /v1/devices/query[/subscribe]` | `PUT /v1/devices/me`, `POST /v1/devices/activate`, engine boot |
| `identities` | account identity ever encountered | `GET /v1/identities[/…]` | the SDK's identity-repository fetcher |
| `inboxCursor` | the account | — | the one-to-one inbox notifier |
| `bundles`, `account_values`, … | see below | | |

A `spaces` row is the account's view of a space:

```json
{ "id": "bafy…", "type": "any.space", "name": "Team", "icon": "…",
  "localStatus": "ok", "remoteStatus": "ok",
  "createdAt": { "$date": "2026-08-01T09:00:00.000Z" },
  "ownRole": "writer", "push": { "spaceKey": "…", "encKey": "…", "encKeyId": "…" },
  "settings": { "notifyMode": "mentions" }, "derived": false }
```

Fields mix scopes on purpose: `type`, `remoteStatus`, `createdAt`, `settings`, `derived` are **synced** across the account's devices; `localStatus`, `ownRole` and `push` are **local** — each device derives them from the same converged inputs, so replicating them would only add lag.

## The space list is a view

`GET /v1/spaces` maps `spaces` rows into `SpaceInfo`, filtering to active rows by default; `GET /v1/spaces?status=one_to_one_pending` or `status=deleted` selects the others. `POST /v1/spaces/query` and `POST /v1/spaces/query/subscribe` expose the raw rows through the same windowed primitive every dataset uses:

```bash
curl -N -X POST http://127.0.0.1:7001/v1/spaces/query/subscribe \
  -H 'content-type: application/json' \
  -d '{"sort":["-createdAt"],"limit":50}'
# event: ready → snapshot{records} → changes[{added,updated,removed}] …
```

Because the list is a dataset, everything that changes a row arrives as a row update on that stream: a join completing on another device, a rename, a role change, a push-key rotation, a settings edit ([Live space list](../realtime/space-list.html)). The `dataset` field of these two endpoints accepts only `spaces` and `profile` — the `identities` rows carry each contact's profile-decryption key, and are read through `GET /v1/identities` where that key is stripped.

## Mirrors: how rows stay current

Several row fields are **derived from other places** and mirrored in by SDK watchers on each device:

| Field | Source of truth | Mirror |
|-------|-----------------|--------|
| `name`, `description`, `icon` | the target space's own `spaceIndex` object — encrypted in-space CRDT data, so every member sees a rename | each device's indexer hook copies the converged value into its local row after `PATCH /v1/spaces/:id` or a peer's edit |
| `ownRole` | the space's ACL | the ACL mirror watcher, once at space load and once per applied ACL record |
| `push.{spaceKey,encKey,encKeyId}` | ACL key material | the same watcher; `encKey` rotates with the read key |
| `type` | the space header | backfilled once, set-once, on the first successful load of a row registered before the header was readable |
| `createdAt` | the creating change's timestamp | stamped at row creation: create for the author, join for a joiner |

A mirror is asynchronous, which shows up in one place: an immediate re-read after `PATCH /v1/spaces/:id` can briefly return the old name. Subscribe to the space list or to the `spaceIndex` object if you need the moment it lands.

## Account-scoped values

A property or dataset field declared `scope: account` is **shared across the account's devices but invisible to other members** — a personal "favourite" flag on a shared object, a per-account read marker on a chat. The tech space is its transport: for every target space the account is in, it holds one derived **carrier object** (`account_values` dataset, one record per `(objectId, dataset, recordId)`), and a per-device mirror replays converged carrier values into the target rows at their normal paths, stamped with the tech tree's versions.

The write path is the ordinary one — `POST …/types/:t/properties` with `"scope": "account"`, then a value write — and reads see the value at its usual path. Deleting the object tombstones its carrier records; leaving or deleting the space drops the carrier ([System fields](../database/system-fields.html)). Values of `scope: local` take the shorter route: written straight into the device's row, no DAG, no tech space.

## Devices and the account's own state

The `devices` dataset is what gives the account a notion of "my devices": one row per peer id with name, OS, version, installed apps and active-app claims, all synced through the tech space and nowhere else. The self row is upserted at engine boot; election of the active device for an app slug is computed by readers from the converged claim data, never from version ids ([Devices](../auth/devices.html)).

Per-space `settings` (the `notifyMode` for push, for example) live on the `spaces` row for the same reason — they are the account's preference about a space, not the space's data — and so sync to the account's other devices without ever reaching other members ([Push](../notifications/push.html)).

## Why account-only

Everything here is *about you*: which spaces you are in, what you call your devices, how you want to be notified, what you flagged for yourself. Keeping that in a space only your keys can open gives three properties at once:

- **Multi-device for free.** A second device restored from the mnemonic derives the same tech space, pulls it first, and immediately knows every space to join, every device sibling, and every account-scoped value — before any shared space has loaded.
- **Privacy by construction.** Other members never see your space list, your device names, your read markers or your settings, because those rows never enter a shared tree.
- **One CRDT.** Account state converges by the same rules as everything else; there is no separate "preferences sync" with its own conflict story.

> **Why it matters.** A hosted backend stores your account profile, device list and per-user preferences in tables only the server can join. any stores them in a space only you can decrypt, replicated by the same protocol as your documents — so "which devices are mine" is a query, and answering it needs no server at all.

## Further reading

- [Accounts](../auth/accounts.html) — mnemonic, account id, restoring a device.
- [Spaces](../database/spaces.html) — the `SpaceInfo` shape and status vocabulary.
- [Identities](../auth/identities.html) — the encrypted profile directory.
