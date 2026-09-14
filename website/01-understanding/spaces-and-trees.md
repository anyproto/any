---
title: Spaces and trees
description: A space is one ACL plus many object trees; an object is a DAG of signed, encrypted changes with heads; how a change is authored and applied, how peers head-sync, what derived ids are, and what the coordinator does versus the sync nodes.
order: 16
---
# Spaces and trees

A space is the unit of membership and sync: one access control list and every object shared under it. An object is an any-sync **tree** — a content-addressed DAG of changes, each signed by its author and encrypted with the space's read key. Every guarantee the API makes about sharing, offline work and convergence comes from these two structures.

## Anatomy of a space

```
space  (id = CID of the signed header + "." + replication key)
├── header        identity, timestamp, spaceType, replicationKey, seed   — cleartext
├── ACL list      chain of signed records: root → invite → join → accept → …
├── settings tree object deletions                                       — an object tree
└── object trees  one per object: chat, page, type, spaceIndex, …
    └── change → change → change   (DAG; tips are the heads)
```

- The **header** is signed at creation and hashed into the space id. It carries the `spaceType` (`any.space`, `any.techspace`, `any.onetoone`) and the `replicationKey` that decides which sync nodes are responsible for the space. The coordinator rejects headers with unknown types, and because the header is content-addressed it can never change.
- The **ACL** is an append-only chain of records — `AclRoot`, `AclAccountInvite`, `AclAccountRequestJoin` / `AclAccountInviteJoin`, `AclAccountRequestAccept` / `Decline` / `Cancel`, `AclAccountsAdd`, `AclAccountPermissionChanges`, `AclAccountRemove`, `AclReadKeyChange`, `AclOwnershipChange`. It names member public keys and their permission (`none`, `reader`, `guest`, `writer`, `admin`, `owner`) and carries the read keys, encrypted to each member ([ACL](../collaboration/acl.html)).
- The **settings tree** is an ordinary object tree with a fixed job: it records object deletions, so a delete propagates like any other change.
- **Object trees** hold the data. Every object you create (`POST /v1/spaces/:id/objects`) is one tree.

## An object is a DAG of changes

Each change in a tree has this shape:

```
Change {
  id            CID of the serialized change          ← the changeId every write returns
  previousIds   the heads this change was built on     ← DAG edges
  aclHeadId     the ACL record in force when it was written
  readKeyId     which read key encrypts data
  identity      author's public key
  signature     over the change, by the author's account key
  data          encrypted payload (the record CRDT batch)
  isSnapshot    marks a snapshot node
  timestamp     author's clock
}
```

The root change is a snapshot. Every later change points at the heads it saw; a change written while another device also wrote creates a fork, and the next change that sees both heads references both, merging the branches. Reading the tree from any head and following `previousIds` yields the full history; where several orders are possible, a topological sort on the change hashes fixes one, the same on every peer.

Changes can arrive out of order. A tree keeps unattached changes on a wait list until their parents arrive, then attaches them — so a receiver never applies a change before its causal ancestors, which is what makes "modify before create" impossible at the protocol level ([CRDTs and consistency](crdt-and-consistency.html)).

## The life of a change

1. **Author.** A write (`/modify`, a chat send, a block patch) is validated locally and encoded as one CRDT batch — one dataset, one or more records, per-path ops.
2. **Sign and encrypt.** The SDK encrypts the batch with the current read key, wraps it in a change referencing the current heads and ACL head, and signs it with the account key. The CID of the result is the `changeId`.
3. **Append and apply.** The change is appended to the local tree and applied to any-store rows in one transaction; the reply returns `{versionId, changeId, recordIds}`. Subscriptions fire after the commit.
4. **Deliver.** The change is streamed at once as a head update to connected sync nodes and LAN peers; head-sync rounds catch up anyone who missed it. Receivers verify the signature against the ACL, attach the change, decrypt it with their copy of the read key, and apply the same batch through the same handlers ([Record CRDT](record-crdt.html)).

Steps 1–3 are local and synchronous. Step 4 happens whenever a peer is reachable.

## Heads and head-sync

The **heads** of a tree are the changes nothing points to yet. Two peers whose head sets match for every tree hold the same data — so peers compare heads, not content.

A **head-sync round** is a diff exchange: a device and a responsible node compare hashed ranges of `(treeId, heads)`, find trees that are new, changed or missing on either side, and fetch the missing changes for those trees. It runs on a periodic timer (`syncPeriod`, ~30 s) and on demand — local writes travel as streamed head updates, not diff rounds:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/sync     # one round, 204 on completion
any space sync $SPACE
```

A device that lacks a space **pulls** it — header, ACL and settings root first — then fetches the object trees through head-sync; when a node lacks a space, the device sends it a **space push** and a follow-up diff uploads the trees. Per-space and per-object progress is what [`/sync-status`](../realtime/sync-status.html) reports.

> **Note.** Head-sync skips trees that still sit on their root change alone. That is why every freshly minted bundle root gets an `any.name` change right away — a tree with no second change is not part of anyone's diff.

## Coordinator versus sync nodes

| | Coordinator | Sync nodes |
|---|---|---|
| Holds | network configuration, the registry of spaces and their status, invite files, the deletion log | the spaces' ACLs and object trees — ciphertext only |
| Talks to a device for | "which nodes serve space X", space registration, deletion, space-status checks, the 1-1 inbox | head-sync rounds and change streaming |
| Enforces | valid space headers and `spaceType`, owner-only deletion | valid signatures and ACL membership on every change; ACL changes are confirmed by a consensus node first |
| Can read | that a space exists, its type, who owns it | who the members are, DAG shape, timing, sizes — never a field value beyond the cleartext custody fields of file rows |

The consensus nodes sit behind the sync nodes: an ACL is a linked list, and a sync node asks a consensus node to confirm each new record extends the list without conflict before accepting it. ACL changes therefore need the network; everything else does not.

## Derived ids

Some trees and spaces have ids that are computed rather than minted. A **derived object** starts from a root change that carries only `(spaceId, changeType, payload, parentId)` — no identity, signature or timestamp — so its CID, and thus the object id, is a pure function of its inputs: each space's `spaceIndex` object and bundle roots installed with `"derived": true` work this way. A **derived space** — the tech space, one-to-one spaces, the well-known per-account spaces — starts from a deterministic header: still signed, but with no timestamp or random seed. Either way every device evaluates the same function and lands on the same id with zero communication ([Derived objects](../database/derived-objects.html)).

The price is permanence. A derived tree cannot be deleted — deleting and re-deriving would yield the same id with a fresh history — and any-sync refuses a derived object as a parent, so children of a derived root are bound by seed, not by `parentId`.

## Space lifecycle

- **Create** — `POST /v1/spaces`: sign a header, build the ACL root with the creator as owner and the first read key, create the settings tree, write the space's row into the tech space. The coordinator learns about the space when the first head-sync registers it.
- **Join** — an owner or admin mints a request-to-join invite (`POST …/invites`); the joiner submits the token (`POST /v1/spaces/join`), and every device of the joiner's account lists the space as `joining` until an owner or admin accepts (`…/acl/accept`) or declines, or any of those devices withdraws (`…/acl/cancel-join`). A direct add by identity (`…/acl/add`) and a guest key skip the request. The joiner's read keys arrive through the ACL, and only then does its device pull the space's trees ([Invites](../collaboration/invites.html)).
- **Delete** — `DELETE /v1/spaces/:id` is offline-first: the tech-space row is tombstoned (`remoteStatus=deleted`, so the account's other devices follow), all local state is **offloaded** immediately — watchers closed, CRDT collections and the per-space DB file dropped, disk reclaimed — and, for a space the account owns, a background reconciler sends the signed `SpaceDelete` to the coordinator when it can (a joined space is only offloaded). The coordinator schedules the space for removal after a deletion period and the responsible nodes clear it; the reconciler also detects spaces the coordinator reports gone (deleted on another device, or by the owner of a space you joined) and offloads them. The row stays in the tech space as a sticky `status: "deleted"` tombstone ([Spaces](../database/spaces.html)).
- **Derived spaces** never delete; a one-to-one space is offloaded on every device of the account but never removed from the network, and is re-derivable.

> **Why it matters.** Because a space is an ACL plus signed trees, membership is a property of the data, not of a session. A node cannot forge a change (no key), cannot admit a member (no consensus-validated ACL record), and cannot read a change (no read key). A device can leave the network for a month and rejoin with nothing but heads to compare. Those are not features layered on top of the sync — they are what the sync is.

## Further reading

- [Encryption](encryption.html) — the key hierarchy behind `readKeyId` and the ACL.
- [Local-first](local-first.html) — head-sync from the client's side.
- [Collaboration](../collaboration/index.html) — the endpoint surface for members, invites and ACL operations.
