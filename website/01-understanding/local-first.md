---
title: Local-first
description: Every device holds the whole database; writes commit locally, sync happens when a peer is reachable, and convergence is guaranteed by the data model rather than by a server — plus which operations still need a network.
order: 10
---
# Local-first
In `any`, offline is the default. Every read and write is served from the database on your disk. Sync runs in the background, carrying your changes to other devices and members and bringing theirs to you.

A successful write means *this device* has appended the change. It does not mean another device has received it, or that a sync node holds a copy — `/sync-status` below is how you learn that.

## What "local" means here

The `any` server process owns an any-store database under the account's data dir (`<data-dir>/<accountId>/sdk/`). A query is an indexed read against that file. A write validates, appends a change to the object's DAG, applies the CRDT ops to the rows, and returns — all before any network I/O.

```bash
# works with the network cable unplugged
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'content-type: application/json' \
  -d '{"type":"page","initialProperties":{"any":{"name":"Offline note"}}}'
```

The reply is `201` with the new object's id. It will sync later; nothing about the request depends on whether "later" is milliseconds or days away.

## What sync is

A change is a signed, encrypted node in a per-object DAG (a git-like history: each change lists the heads it was built on). Sync is the exchange of DAG changes between peers:

- **Network sync** — the space's responsible any-sync nodes store ciphertext changes and relay them. A device that comes online pulls what it missed and pushes what it wrote.
- **Local-network sync** — devices on one LAN discover each other over mDNS and exchange changes directly for the spaces they share, including while the nodes are unreachable (config `p2p.enabled`, on by default).
- **Global sync** — devices anywhere on the internet connect directly through Anytype's relays, which forward encrypted traffic until the two sides find a direct path. Same guarantee without the same LAN (config `p2p.global`, on by default). See [Networks](../operations/networks.html).

All three deliver the same changes; a space can be fully `synced` with `networkPeers: 0` when everything converged over the LAN or the relays.

## Head-sync and convergence

Peers compare *heads* — the tips of each object's DAG. If two peers have the same heads for every object, they hold the same state. A local write goes out at once, streamed as a head update to the connected nodes and peers. A **head-sync round** is the catch-up for anything a stream missed: it diffs the head sets against the responsible nodes and exchanges the missing changes. It runs on a periodic timer (~30 s); you can force one:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/sync        # → 204 when the round completes
any space sync $SPACE
```

Convergence follows from the CRDT: once two peers have applied the same set of changes, in any order, they hold identical rows ([CRDTs and consistency](crdt-and-consistency.html)). There is no reconciliation step, no conflict dialog, no server copy that wins. The merge is per-path last-writer-wins on DAG order, not a transaction: it does not enforce an invariant across fields, and two concurrent writes to the same field keep one value.

## Observing sync state

Sync state is a first-class read, cheap enough to poll on a render tick:

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/sync-status
```

```json
{ "spaceId": "bafy…", "state": "syncing",
  "synced": 1, "total": 3,
  "networkPeers": 1, "localPeers": 0, "p2p": "notconnected",
  "lastSyncedAt": "2026-08-24T10:12:00Z" }
```

| `state` | Meaning |
|---------|---------|
| `synced` | Every object's heads match the responsible peers. |
| `syncing` | Changes are in flight in one direction or the other. |
| `unknown` | Space not loaded yet on this device (or an object id nobody has seen). |
| `offline` | No responsible node reachable; local reads and writes continue. |
| `error` | The network is incompatible with this build. |

The rollup reports `synced`, `syncing` and `unknown`; `offline` and `error` are reserved in the vocabulary for reachability and network-compatibility signals, so render them rather than failing on them.

Per-object state lives at `…/sync-status/objects/:objectId`, and both levels stream transitions over SSE ([Sync status](../realtime/sync-status.html)).

## Boot catch-up

When the server starts, it binds the listener first and runs the space loading + offline catch-up on a background pass. `GET /v1/health` reports `"bootstrapping": true` while that pass runs; reads against a space that has not had its turn yet serve the pre-offline state. Per-space convergence is what `/sync-status` reports, not the health flag.

> **Why it matters.** A hosted backend answers a query with the truth as the server sees it, and returns an error when it cannot. any answers with the truth as *this device* sees it, always. That is the property that makes an app usable on a plane, a phone in a tunnel, or a laptop whose owner simply never turned sync on — and it is why the API never asks you to handle a "not connected" error on a read or write.

## What still needs a network

| Operation | Offline behavior |
|---|---|
| Query or edit data already on this device | Local state; the write syncs later. |
| Receive changes from another device | Needs a reachable peer: LAN p2p or a sync node. |
| Read a file whose bytes are cached locally | Local. |
| Read an uncached file | Needs a peer or a file node that holds the bytes ([Files](../files/index.html)). |
| Search with `index.embedder: local` | Local once the model and llama.cpp libraries are present. |
| Call an external model or connector from a program | Needs that service; the effect is recorded either way. |

A newly joined space may still be loading: the account's space list and the materialized content of that space are separate states ([space lifecycle](../database/spaces.html)).

## What does not sync

- **Local-scope fields** — dataset fields declared `local` (chat's `unread` flags, for example) are device-only; they never enter the DAG ([System fields](../database/system-fields.html)).
- **Account-scope settings** — per-space `settings` sync across the account's own devices through the tech space, never to other members.
- **The search index** — derived state, rebuilt locally from the change feed ([Indexing](../search/indexing.html)).
- **Run traces** kept by `anyrt` — trace bodies live in the server's device-local store; only a per-run summary syncs ([Traces and replay](../programs/traces-and-replay.html)).

> **Note.** Timestamps such as `modifiedAt` are the *author's* clock. They converge (every peer ends up with the same value), but they are display and sort quality only — never use them as a fence for "has this synced yet". Use `/sync-status` for that.
