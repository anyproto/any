---
title: Local-first
description: Read and write local data, observe background sync, and understand which operations need a network connection.
order: 10
---
# Local-first

Any reads and writes database state on the device running the server. Sync exchanges changes in the background, so an application can keep working with its local data while disconnected.

A successful local write means this device has recorded the change. It does not mean other devices have received it or that a backup node holds it.

## What "local" means here

The `any` server process owns an any-store database under the account's data dir (`<data-dir>/<accountId>/sdk/`). Queries run against that local database. An ordinary data write validates the operation, appends a change to the object’s history, applies its CRDT operations to local records, and returns. Propagating that change is a separate step.

The following example assumes the server is running and `$SPACE` names a space already available on this device. Use the [quickstart](../quickstart/curl.html) to create one.

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

Both paths deliver the same changes; a space can be fully `synced` with `networkPeers: 0` when everything converged over the LAN.

## Head-sync and convergence

Peers compare *heads* — the tips of each object's DAG. If two peers have the same heads for every object, they hold the same state. A local write goes out at once, streamed as a head update to the connected nodes and peers. A **head-sync round** is the catch-up for anything a stream missed: it diffs the head sets against the responsible nodes and exchanges the missing changes. It runs on a periodic timer (~30 s); you can force one:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/sync        # → 204 when the round completes
any space sync $SPACE
```

Convergence follows from the CRDT: once two peers have applied the same set of changes, in any order, they hold identical rows ([CRDTs and consistency](crdt-and-consistency.html)). Your application does not need to manually reconcile replicas. It still needs to understand the merge rules: deterministic convergence does not enforce every business invariant or preserve both competing values for the same field.

## Observing sync state

Read the space’s current sync state:

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

## What still needs a network

| Operation | Offline behavior |
|---|---|
| Query or edit data already on this device | Uses local state; writes can sync later. |
| Receive changes from another device | Needs a reachable peer. |
| Read a file whose bytes are cached locally | Works locally. |
| Read an uncached file | Needs a peer or backup service that can supply its bytes. |
| Search with local embeddings | Works locally once the model and libraries are available. |
| Call an external model or connector | Depends on the configured service and connection. |

A newly joined space may still be loading. The account’s space list and the materialized content of that space are separate states; see [space lifecycle](../database/spaces.html).

## Data with a narrower sync scope

- **Local-scope fields** — dataset fields declared `local` (chat's `unread` flags, for example) are device-only; they never enter the DAG ([System fields](../database/system-fields.html)).
- **Account-scope settings** — per-space `settings` sync across the account’s own devices through the tech space, rather than to other members.
- **The search index** — derived state, rebuilt locally from the change feed ([Indexing](../search/indexing.html)).
- **Run traces** kept by anyrt — trace bodies live in the server's device-local store; only a per-run summary syncs ([Traces and replay](../programs/traces-and-replay.html)).

> **Note.** Timestamps such as `modifiedAt` are the *author's* clock. They converge (every peer ends up with the same value), but they are display and sort quality only — never use them as a fence for "has this synced yet". Use `/sync-status` for that.
