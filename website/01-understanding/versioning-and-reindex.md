---
title: Versioning and re-index
description: The account-wide CRDT version mark, handler versions and wire data versions, the version-driven re-index that rebuilds rows from the DAG (lazy per object plus a background sweep), what clients see during the window, why no migration is ever written, and how the search index follows the space generation.
order: 19
---
# Versioning and re-index

Rows in any-store are not the source of truth — the object trees are. Every row is the result of replaying a tree through the current dataset handlers, so when handler logic changes, the fix is not a migration script: the SDK wipes the rows and replays the tree. This page explains the versions that trigger that, what happens during a rebuild, and how the search index keeps up.

## Three kinds of version

| Version | Lives | Gates | Bump when |
|---------|-------|-------|-----------|
| **CRDT version** | one account-wide mark on the tech space, stamped by every release on open and never lowered | **releases** — a server whose supported version is below the stored mark refuses to open the account (`409 sdk.crdt_version_newer`), and turns read-only if the raise arrives from another device while it runs | a release writes data the previous release cannot read |
| **DataVersion** | on every change (an opaque label such as `chat_messages-v5` for a compiled-in handler; the `typeId:shortId` schema state for a runtime dataset) | **peers** — a receiver parks a change for a dataset it has no handler for, or for schema state it has not applied yet | never by hand — a compiled-in label is carried but not compared; a runtime dataset's stamp follows its declaration |
| **HandlerVersion** | on the local registration only; persisted per object as `dataset → version` on the object's `_meta` row | **this device's rows** — a stored version that differs from the registered one marks the dataset stale | the *rows on disk* are wrong under the new logic: a derived field with a new shape, a stamp computed differently, a date stored as an instant instead of a number |

The distinction matters. The CRDT version mark is what keeps an older release from misreading newer data — it fences the whole account, visibly: `GET /v1/health` reports `crdtVersion.newer` as the client's "upgrade required" signal. Bumping HandlerVersion never leaves the device — it only says "my rows need rebuilding". A runtime-declared schema also has a **SchemaRev**, which evicts and reloads a resident handler when the declaration changes; because runtime schemas evolve additively, that never requires recomputing stored rows.

## The version-driven re-index

When an object's controller is built, it reads the stored `dataset → version` map and diffs it against the registered handlers. Any dataset whose versions differ is the object's re-index verdict, and its next load rebuilds it:

```
load object
  1. capture local-scope values        (they never entered the DAG; nothing could replay them)
  2. rewind the addSeq watermark to 0  and persist it together with the captured leaves
  3. wipe the object's rows            its row in shared collections, its per-object collections,
                                       its history rows
  4. replay the whole tree             every change, through the CURRENT handlers
  5. restore the local values, stamp the current versions, clear the in-flight mark
```

The compare is per `(object, dataset)`; the wipe and replay are per object, because one tree carries every dataset of that object and a partial replay would double-apply the rest.

Properties of the mechanism:

- **Lazy, per object.** The rebuild rides object load — the cold-restore path that opens the tree anyway — so startup stays flat and objects nobody opens cost nothing until they are opened.
- **Converged by a background sweep.** Lazy alone would leave the `objects` collection mixing rows built by the old handler with rows built by the new one for as long as some object stayed unopened, and a sort or filter over a rebuilt field would read both shapes. When a space store starts, a paced sweep scans `_meta` for stale objects and loads them. Nothing waits on it; a store with nothing stale pays one scan.
- **Crash-safe by construction.** The rewound watermark is durable before the wipe and the stored versions stay stale until the replay stamps them, so an interruption anywhere simply rebuilds again on the next load — or, once versions are stamped, resumes from the persisted watermark.
- **Not a deletion.** The wipe emits no `removed` events and writes no delete marker, since a delete stamp is sticky and would evict the object from every consumer index for good.
- **Read state and history follow.** Read-tracking classification is suppressed during the replay (otherwise every replayed change would mark the object unread), and the per-object history index is wiped with the rows and lazily rebuilt.

Type objects rebuild too. While a type's `shortIds` / `properties` / `datasets` collections are empty mid-replay, an inbound change for that type fails the DataVersion lookup and is parked, then drained once the definitions are back — only a local write in that window sees a transient `type_unknown` rejection.

## What clients see during the window

A subscription is not told about the wipe — the rebuild never touches the subscribe engine. A held window keeps its rows and then sees the replay as one `updated` delta per change in the tree, with intermediate partial documents, converging when the replay ends. Snapshot reads against a space mid-sweep can return both shapes of a rebuilt field at once; a sort over that field groups by type until the sweep finishes.

Two rules keep a client correct across this:

- **Render from the window, not from a second store.** Applying `added` / `updated` / `removed` to the held rows lands on the converged state automatically ([Subscriptions](../realtime/subscribe.html)).
- **Do not fence on shape.** A field that is `{"$date": …}` on one row and still in its old shape on another during the window is not a schema error; it is a rebuild in flight.

## Why no migrations

Every alternative to "wipe and replay" needs migration code per version pair — code that must produce exactly the rows a fresh replay would, on every platform, for every data shape ever written. The DAG already *is* the migration input: it holds every change verbatim, signed, in causal order, and the CRDT's apply is a pure function of it. Replaying is cheaper to write and impossible to get subtly different from a fresh install.

The same reasoning governs the wire. Stored values never move: a property's `kind` and `scope` are pinned at creation, and a change of meaning is a new property with a new content-addressed id. Runtime schemas grow additively — an added field can never be `required`, because fresh devices replay the dataset's own history against the current schema. A stamp is always computed from the change envelope, never from replica state, so a re-run stamps the same value.

> **Note.** The trade is explicit and pre-release: when a handler's stored shape changes, existing rows are rebuilt on every device from history rather than converted in place, and code built against the old shape reads the new one after the sweep. An object that has never applied a change on this device has no stored versions and is never rebuilt.

## Generation and the search index

Consumers that keep their own derived state — the [search index](../search/indexing.html) above all — follow the change feed `Space.Changes()`, keyed by the per-space `applySeq`. A rebuild keeps that axis climbing: re-applied rows surface as changed again with fresh sequence numbers, so an indexer simply re-chunks them incrementally. Only a rebuilt SDK store — a wiped `sdk.db` — restarts the axis at zero, and that mints a new per-space **Generation**. An older backup restored keeps its Generation but falls behind the indexer's cursor.

The indexer persists the Generation next to its cursor and, on every worker start, compares:

```
stored generation ≠ current generation      → drop the space's index docs, cursor = 0, reindex
stored cursor     > MaxApplySeq             → same (an older store restored under a newer cursor)
otherwise                                   → resume from the stored cursor
```

Without that check a cursor could name a position the feed will never report again: `ChangedSince` would return nothing, forever, with no error, and the index would silently freeze. The [search index is derived state](../operations/data-dir.html) either way — deleting `<account-dir>/index/` with the server stopped is safe: the next start finds no cursor and re-indexes every space from the change feed.

> **Why it matters.** A hosted backend migrates a table once, on the server, under a maintenance window. any has as many copies of every row as the account has devices, no server to run a script on, and no window in which nobody writes. The only migration that can be correct on every copy is the one the data model already performs on every apply — so that is the only one any performs.

## Further reading

- [The record CRDT](record-crdt.html) — handlers, the DataVersion gate and parking.
- [Runtime datasets](../database/runtime-datasets.html) — additive-only schema evolution.
- [Data dir](../operations/data-dir.html) — what is source of truth on disk and what is derived.
