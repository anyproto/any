---
title: The record CRDT
description: How the SDK turns encrypted changes into rows — datasets, records and per-path ops, the `_ver` gate, content-addressed record ids, handlers that run on every peer, tombstones and delivery counters — with a worked merge.
order: 17
---
# The record CRDT

any-sync delivers signed, encrypted changes; any-store holds documents. The record CRDT is the layer between them: it defines a change as a batch of per-record operations on a named dataset and applies it to rows through a per-path version gate, so that every peer holding the same changes — in any order — ends with identical rows.

## Datasets, records, ops

```
object (one any-sync tree)
└── dataset  "chat_messages"          named storage collection; one handler
    └── record  { id, …fields, _ver } anyenc document
        └── op   $set / $unset / $addToSet / $pull / $inc / $incGated / delete
```

One change belongs to exactly one dataset and may touch many records. Each record change carries an `id`, an `upsert` flag and an ordered list of ops. Ops address dotted paths; `$set` and `$unset` also have a multi-field form whose payload is an object of `path: value` entries, each applied as its own gated write.

There is no `insert`. A record is created by an `upsert: true` change whose ops populate it; a strict (default) modify on an absent id is a no-op, so a typo cannot conjure a record. Reserved names — a top-level `id` or `_`-prefixed field — are rejected at validation, before apply ([Writing data](../database/writing-data.html)).

## Versions and `_ver`

Every peer's any-sync assigns each change it holds a **versionId**: an opaque string that sorts lexicographically in DAG order for that peer's view of that tree. It is peer-local — two peers may spell the same change differently — but the relative order of any two changes converges once both peers hold both.

Each record carries `_ver`, a map from field path to the versionId of the last write that landed there:

```json
{ "id": "m1", "text": "hello", "edited": true,
  "_ver": { "id": "!A0", "text": "!C4", "edited": "!C4" } }
```

The apply rule is one comparison per path:

```
if incoming.versionId > record._ver[path]:  write the value, _ver[path] = incoming.versionId
else:                                       skip this path
```

`_ver` is a tree. A write to `a.b` stamps exactly that path; a whole-object `$set` of `a` collapses the subtree to one string, and a `*` entry holds the default for keys a broad write claimed without enumerating. A broad write over finer entries merges per leaf — newer leaves survive, older ones are replaced — so "replace the whole object" and "edit one key" race to the same result in every delivery order.

`_ver.id` is special: it is the **creation marker**, the versionId of the earliest upsert that targeted the id, updated only downward (`min`). That is what makes `sort: ["-_ver.id"]` a stable creation order on every peer ([System fields](../database/system-fields.html)).

## Content-addressed record ids

A record change with an empty `id` gets one derived from the enclosing change:

```
DeriveRecordId(changeId) = base58(xxh3-64(changeId))     ≈ 11 chars
```

The first empty-id record in a batch takes that value; later ones append `:1`, `:2`, …. Because `changeId` is a CID, the id is globally unique with no coordination, and it is the same on every peer. It is why `recordIds[0]` in a write reply is the id of the message or block you just created, and why property ids (`propId`) look the way they do.

## Handlers

Every dataset has a **handler** — a compiled-in one (the `chat` and `editor` module handlers, the `objects` row, spaceIndex, …) or the generic schema handler compiled from a declaration: a [runtime dataset](../database/runtime-datasets.html), or a built-in one with no code of its own such as the `dataview` type's views. Hooks run inside the apply transaction on every peer, identically, for every synced change:

| Hook | Runs | Typical rule |
|------|------|--------------|
| `BeforeCreate` | before a record's first materialization | required fields, id rules, stamp `creator` / `createdAt` from the change envelope |
| `BeforeModify` | per op on an existing record | field pinned, author-only edit (`ctx.Before.creator == ctx.Change.Creator`), value shape, bump `modifiedAt` |
| `BeforeDelete` | per record delete | author-only delete |

A handler writes through a **sink**: `Derive` queues a same-record op that lands with the change's own versionId (this is how server-stamped fields exist without a server), `Project` queues a sibling write to another dataset on the same object. Hooks may read only what converges — the change envelope, the record's pre-op state, and the create-time fields of records in the change's causal past — never replica-local data, so their verdicts are the same everywhere.

Rejections are **op-granular and never fatal on the apply path**: an op that fails validation is dropped and recorded (a failed `BeforeCreate` or `BeforeDelete` drops that record's change), the rest of the change still commits, and the writer's own pre-check is the only place a whole change is refused. Which fields a peer may write is also declared, as a **scope** on the field or property: `synced` rides the DAG, `derived` is handler-only, `local` never leaves the device, `account` travels through the [tech space](tech-space.html). An inbound DAG op addressing a local or account path is dropped per-op on every peer.

## Merge rules by op

| Op | Gated by `_ver`? | Updates `_ver`? | Behaviour under concurrency |
|----|------------------|-----------------|-----------------------------|
| `$set`, `$unset` | yes | yes | last writer in DAG order wins, per path |
| `$addToSet`, `$pull` | yes (against a newer `$set`) | no | commutative — concurrent adds all land |
| `$inc` | yes (against a newer `$set`) | no | commutative counter |
| `$incGated` | yes | yes | LWW on the post-increment value; not convergent under arbitrary order |
| `delete` | — | collapses to `{id, "*"}` | wins absolutely; the tombstone is sticky |

Two concurrent creates of the same id merge per field. A delete on an absent id writes a tombstone so it still wins against a create it has not seen. Every later modify on a tombstone is dropped — and reported to the local writer as a rejection, never silently.

## Worked example: two concurrent edits

Devices A and B both hold message `m1` with `_ver.text = "!B2"`, then go offline.

```
A:  $set text  = "meet at 10"       → change cA, parents [c0]
B:  $set pinned = true              → change cB, parents [c0]
```

Say DAG order puts `cA` before `cB`. A applies its own change (`!C3`), then receives `cB`, which sorts after it (`!C4`). B applies its own `cB` (`!C3`), then receives `cA` — which sorts *before* `cB`, so B gives it an id below the one it already holds (`!C2z`). Each write touches a different path, so both gates pass on both devices:

```
A: text ← "meet at 10" (!C3)    then pinned ← true (!C4)
B: pinned ← true (!C3)          then text ← "meet at 10" (!C2z)

both:  { "text": "meet at 10", "pinned": true }      ✓ identical rows
```

The `_ver` strings differ between A and B (they are peer-local); the order they encode and the rows do not. Now suppose B had instead written `$set text = "meet at 11"`. Both changes hit the same path; the DAG order — the same on every peer once both changes are present — picks one, and the other is gone everywhere with no error to either author. Design records so that the fields people edit at the same time are *different* fields ([CRDTs and consistency](crdt-and-consistency.html)).

## Tombstones and counters

A deleted record stays as a tombstone `{ id, _deletedAt, _ver: { id, "*" } }` so late writes have something to lose against. Queries and subscriptions skip tombstones; indexers read them through the store's find path (`IncludeDeleted`) to evict documents.

Three peer-local counters ride the apply path, each answering one question:

| Counter | Where | Answers |
|---------|-------|---------|
| `versionId` | `_ver` on each record | which write landed last on this path |
| `addSeq` | `_addSeq` on each record; per-object watermark | is any-store caught up with any-sync (the restore watermark) |
| `applySeq` | `_applySeq`; the `Changes()` feed | is a consumer (the [search indexer](../search/indexing.html)) caught up with any-store — it also counts local and account writes, which never enter the DAG |

All three are local to a peer. None is a timestamp, and none should be compared across devices.

## The DataVersion gate

Every change carries a **DataVersion**: a compiled-in handler stamps an opaque label (`chat_messages-v5`), a runtime dataset stamps the schema state it was written against (`typeId:shortId`). A receiving peer **parks** a change when it has no handler for the change's dataset, or when the change names schema state it has not applied yet — the change is persisted in the tree, kept in a `_detached` storage collection, invisible to queries — and drains it when the registration appears. An opaque label is carried but not compared. For runtime datasets that means "schema first, then data" holds in either arrival order: a record written against a field definition you have not yet received waits for the definition instead of being applied wrongly or dropped.

## Why there is no rollback and no total order

- **No total order.** Concurrent branches have no "before" and "after" until they meet; DAG order gives every peer the same tie-break, not a wall clock. Version history is therefore a walk over a DAG with a cursor, and `timestamp` is display-only ([Version history](../database/version-history.html)).
- **No rollback.** A change that entered the tree is signed and content-addressed; other peers may already have built on it. Correction is another change. Validation drops ops on apply rather than refusing the change, precisely so a tree can always be replayed to the same state.
- **No conflict surface.** Every merge is decided by rule. What a local writer gets back is validation — a handler rejection or a tombstone rejection in `rejections` — never a conflict: a tombstone rejection is just a fact about a record that no longer exists.

> **Why it matters.** The CRDT is deliberately narrow: per-path LWW plus a few commutative ops. That is enough to make "apply this set of changes in any order" a pure function — which is what lets a device be offline for a week, a handler be re-run over history to rebuild rows ([Versioning and re-index](versioning-and-reindex.html)), and a second device restore an account from the DAG alone.
