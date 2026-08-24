---
title: Local-first
description: Every device holds the whole database; writes commit locally, sync happens when a peer is reachable, and convergence is guaranteed by the data model rather than by a server.
order: 10
---
# Local-first

In any, "offline" is not a degraded mode — it is the normal one. Every read and write is served from the database on your disk. Sync is the background process that carries your changes to other devices and members, and theirs to you.

## What "local" means here

The `any` server process owns an any-store database under the account's data dir (`<data-dir>/<accountId>/sdk/`). A query is an indexed read against that file. A write validates, appends a change to the object's DAG, applies the CRDT ops to the rows, and returns — all before any network I/O.

```bash
# works with the network cable unplugged
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'content-type: application/json' \
  -d '{"types":["page"],"initialProperties":{"any":{"name":"Offline note"}}}'
```

The reply is `201` with the new object's id. It will sync later; nothing about the request depends on whether "later" is milliseconds or days away.

## What sync is

A change is a signed, encrypted node in a per-object DAG (a git-like history: each change lists the heads it was built on). Sync is the exchange of DAG changes between peers:

- **Network sync** — the space's responsible any-sync nodes store ciphertext changes and relay them. A device that comes online pulls what it missed and pushes what it wrote.
- **Local-network sync** — devices of the same account on one LAN discover each other over mDNS and exchange changes directly, including while the nodes are unreachable (config `p2p.enabled`, on by default).

Both paths deliver the same changes; a space can be fully `synced` with `networkPeers: 0` when everything converged over the LAN.

## Head-sync and convergence

Peers compare *heads* — the tips of each object's DAG. If two peers have the same heads for every object, they hold the same state. A **head-sync round** diffs the head sets against the responsible nodes and exchanges the missing changes. It runs on a periodic timer (~30 s) and on every local write; you can force one:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/sync        # → 204 when the round completes
any space sync $SPACE
```

Convergence follows from the CRDT: once two peers have applied the same set of changes, in any order, they hold identical rows ([CRDTs and consistency](crdt-and-consistency.html)). There is no reconciliation step, no conflict dialog, no server copy that wins.

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
| `offline` | No peer reachable; local reads and writes continue. |
| `unknown` | Space not loaded yet on this device (or an object id nobody has seen). |
| `error` | The last round failed; the next timer retries. |

Per-object state lives at `…/sync-status/objects/:objectId`, and both levels stream transitions over SSE ([Sync status](../realtime/sync-status.html)).

## Boot catch-up

When the server starts, it binds the listener first and runs the space loading + offline catch-up on a background pass. `GET /v1/health` reports `"bootstrapping": true` while that pass runs; reads against a space that has not had its turn yet serve the pre-offline state. Per-space convergence is what `/sync-status` reports, not the health flag.

> **Why it matters.** A hosted backend answers a query with the truth as the server sees it, and returns an error when it cannot. any answers with the truth as *this device* sees it, always. That is the property that makes an app usable on a plane, a phone in a tunnel, or a laptop whose owner simply never turned sync on — and it is why the API never asks you to handle a "not connected" error on a read or write.

## What does not sync

- **Local-scope fields** — dataset fields declared `local` (chat's `unread` flags, for example) are device-only; they never enter the DAG ([System fields](../database/system-fields.html)).
- **Account-scope settings** — per-space `settings` sync across the account's own devices through the tech space, never to other members.
- **The search index** — derived state, rebuilt locally from the change feed ([Indexing](../search/indexing.html)).
- **Secrets** stored by anyrt — device-local values, never synced ([Credentials](../programs/credentials.html)).

> **Note.** Timestamps such as `modifiedAt` are the *author's* clock. They converge (every peer ends up with the same value), but they are display and sort quality only — never use them as a fence for "has this synced yet". Use `/sync-status` for that.
