---
title: CRDTs and consistency
description: Writes are changes in a per-object DAG; fields merge by per-path last-writer-wins on DAG order, deletes are sticky, and there is no total order — an honest comparison with transactional databases.
order: 30
---
# CRDTs and consistency

any never rejects a write because someone else wrote first, and never shows two members different final states. It gets there with a specific, narrow CRDT: per-path last-writer-wins over a causal DAG. Knowing exactly what that guarantees — and what it does not — is the difference between a client that feels instant and one that quietly loses edits.

## The unit of write: a change

Every write (`/modify`, a chat send, a block patch, a property set) becomes one **change** appended to the target object's DAG. A change carries:

- `prevIds` — the heads it was built on (its causal parents);
- a batch of per-record ops: `$set`, `$unset`, `$inc`, `$addToSet`, `$pull`, `delete`, keyed by dotted paths;
- the author's signature and the read-key id.

The change's CID is the `changeId` every write returns. It is the same on every peer, which is why it doubles as a **version** for history reads ([Version history](../database/version-history.html)).

## Ordering: DAG order, not clocks

Each peer's any-sync assigns every change it has seen a lexicographically sortable `versionId`. That order is:

- **causal** — a change always sorts after its `prevIds`;
- **deterministic** for concurrent branches — every peer resolves the same tie the same way once it has both branches;
- **peer-local in its string form** — two peers may spell the "same" change with different `versionId` strings. Compare versionIds only within one peer, within one object.

There is **no total order across peers in real time**. Two members editing while apart each hold a linear history that is correct for what they have seen; the merge point is when both branches arrive.

## Merging: per-path LWW

Each record stores a `_ver` map: the versionId of the last change that wrote each path. Applying an op is a gate:

```
if op.versionId > record._ver[path]:   apply, and _ver[path] = op.versionId
else:                                  drop (an older write for this path)
```

Because the rule is per *path*, not per record, concurrent edits to different fields of the same record both survive:

```
peer A: $set title = "Draft 2"          peer B: $set tags = ["idea"]
                 └──────────── merge ────────────┘
             { title: "Draft 2", tags: ["idea"] }      on both peers
```

Concurrent edits to the **same** path pick one winner by DAG order — the other value is gone, on every peer, with no error surfaced to either author. Three ops escape LWW because they compose instead:

| Op | Merge rule |
|----|------------|
| `$inc` | Commutative counter — both increments apply. |
| `$addToSet` / `$pull` | Set semantics — concurrent adds all land. Concurrent add + pull of the same element has no per-element guarantee. |
| `delete` | **Delete wins absolutely.** A tombstone is sticky; every later modify on that record id is dropped and reported to the local writer as a rejection. |

Creates are upserts: two concurrent creates of the same id merge per field, and the record's creation marker `_ver.id` converges to the causally earliest one (the `min` rule), so a chat sorted by `-_ver.id` orders identically everywhere.

## Versus transactions

| Property | Transactional DB (Postgres, a hosted backend) | any |
|----------|-----------------------------------------------|-----|
| A write can fail because of another writer | Yes — serialization failure, OCC version mismatch, unique violation | No. Writes always commit locally. Only *validation* (schema, handler rules, a collection none of the object's types declare, tombstone) rejects. |
| Read-your-writes | Yes | Yes, on the writing device. |
| Cross-record atomicity | Yes — a transaction spans rows and tables | One change spans the records of one object's dataset batch and applies atomically on each peer. Nothing spans objects. |
| Invariants like "balance ≥ 0" | Enforced by the DB under contention | Cannot be enforced across concurrent writers. Model it as a counter (`$inc`) if it must converge, or accept that two offline decrements both apply. |
| Conflict visibility | Loser retries | Loser is silently overwritten per path; deleted records reject later writes. |
| "The" current state | The server's | Each device's, converging as changes arrive. |

> **Why it matters.** Transactions buy you invariants by making writers wait for one another, which needs a single authority that is online. any's model buys you writes that never wait and never need a server, at the price of invariants that must be expressible as commutative operations or per-path overwrites. Design your records so that the fields people edit concurrently are *different* fields, keep counters as counters, and let deletes be final.

## What this means for a client

- **Write narrowly.** `$set` the path you changed (`style.checked`), not the whole record; a whole-record `$set` overwrites every sibling field a peer touched. The editor's markdown `PATCH` exists for exactly this reason ([Editor](../types/editor.html)).
- **Stamp your own writes.** A write returns `versionId`; stamp it on the paths you touched so the matching subscribe delta is recognisable as your own ([Subscriptions](../realtime/subscribe.html)).
- **Never fence on timestamps.** `modifiedAt` and chat `createdAt` are the author's clock. They converge, but two authors' clocks are not comparable. Order by `_ver.id` for creation order, by `-modifiedAt` for display recency only.
- **Treat a tombstone rejection as final.** A write against a deleted record id returns it in `rejections`; recreate under a new id if you need the data back.
- **Local scope is outside the CRDT.** Fields declared `local` mint their own version ids, never enter the DAG, and never sync — they annotate synced records on this device only ([System fields](../database/system-fields.html)).

> **Note.** Validation is not merge. A dataset handler (chat's "author-only edit", a runtime schema's `required`) rejects a change *before* it enters the DAG on the writer, and drops the offending op *after* delivery on readers. Both keep the DAG applying; neither is a conflict — a conflict never surfaces to any caller.
