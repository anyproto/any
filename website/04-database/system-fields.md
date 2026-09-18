---
title: System fields
description: The fields any stamps for you — `_ver`, `_addSeq`, `_deletedAt`, `createdAt` / `modifiedAt`, `author` / `modifiedBy`, and the built-in `any.*` properties.
order: 140
---
# System fields

Every record carries fields you never write: per-field version stamps, a delivery counter, tombstone markers, creation and modification instants, and — on objects — a handful of built-in properties. This page lists them, says which are synced and which are peer-local, and which ones are safe to sort, page or filter on.

## Record-level bookkeeping

Present on every record returned by [`/query`](reading-data.html) and in subscribe events. Client writes addressing them are rejected.

| Field | Scope | Meaning |
|---|---|---|
| `_ver.id` | peer-local | The record's **creation marker** — the version id of the change that created it. Set once, never bumped by edits. Monotonic on one device (and indexed on `chat_messages`): the standard cursor for stable paging (`{"_ver.id": {"$lt": "<oldestSeen>"}}` sorted `["-_ver.id"]`) and the chronological order chat sorts on. |
| `_ver.<path>` | peer-local | Per-field high-water mark: the version id of the last change that touched that path. Clients running subscribe-then-query-then-apply compare against it to dedupe live events. |
| `_addSeq` | peer-local | The space's monotonic delivery counter on this device — advances on every change to any of an object's datasets. The search indexer's cursor. Compare and persist it; never assume another peer holds the same value for the same change. |
| `_deletedAt` | synced | Present on a record-level tombstone. Tombstones are excluded from `/query`, subscribe and aggregation; they surface only to consumers that opt in (`"includeDeleted": true` on a per-object `/query` snapshot, the search feed, the history record read with `deleted: true`). |
| `_traces` | synced | Trace ids stamped through `traceIds` on the write, when any. |

Version ids are peer-local: two devices holding the same change name it by different `_ver` values. Use them for ordering and paging *on the device that produced them*, never as identifiers you exchange. The exchangeable identifier of a change is its `changeId` — see [Version history](version-history.html).

> **Note.** History diffs never carry `_ver`. Snapshot rows and `added`/`updated` records do — narrowed to your `projection` when you send one, in full otherwise. `{"projection": {"_ver": -1}}` drops it, and `_addSeq`/`_applySeq` drop by default under any projection.

## Row-root stamps on objects

Every row of the per-space `objects` storage collection carries these next to `id`, all derived and read-only:

| Field | Meaning |
|---|---|
| `author` | Identity that created the object (the root-change signer). |
| `createdAt` | Creation instant — the root change's time. |
| `modifiedAt` | Instant of the latest **synced** change that touched the object, whatever dataset it landed on. Any property write bumps it; peers converge on one value (last-writer-wins on DAG order). Local- and account-scope writes deliberately don't bump it. |
| `modifiedBy` | Account identity that signed the change `modifiedAt` points at — the object's last writer. Equal to `author` on an object nobody has edited since it was created. |
| `spaceId` | The hosting space. |

Both instants are the **author's clock** and are written in the `{"$date": …}` wire shape:

```json
{ "id": "bafy…", "author": "A5k…",
  "createdAt":  { "$date": "2026-08-05T17:00:00.000Z" },
  "modifiedAt": { "$date": "2026-08-20T09:12:31.000Z" },
  "modifiedBy": "A9t…" }
```

"Recently modified first" is `{"sort": ["-modifiedAt"]}`. A filter literal must take the same shape — `{"modifiedAt": {"$gte": {"$date": "2026-01-01T00:00:00Z"}}}`. A bare number or string does not error; it silently matches nothing, because ordering comparisons are bracketed by type and a number never compares against an instant. See [Data types](data-types.html).

**`modifiedAt` and `modifiedBy` are one pair.** Both come from a single change — the object's latest by DAG order, whatever dataset it landed on: a property write, an editor block, a chat message, a runtime-dataset record, a record delete. They carry that change's version, so they move together and never pair one change's time with another's signer. A change that arrives late regresses neither. Concurrent writers are resolved by DAG order, not by clock, so the identity that wins can be the one whose wall clock reads earlier. Deleting the object removes the row outright, stamps included.

`modifiedBy` is an account identity in the same encoding as `author`, as chat `creator`, as `identity` in `GET /v1/spaces/:spaceId/members`, and as `id` from `GET /v1/account` — resolve a name and icon through the members list, and through the account-global [identities directory](../auth/identities.html) (`GET /v1/identities/:identity`) for a past writer who has since left the space. `modifiedAt` is indexed and is the conventional recency ordering; `modifiedBy` is not, so a filter on it scans. A row without `modifiedBy` has either not been rebuilt yet (rebuilds run on each object's first load, plus a background sweep) or its latest change has no known signer — never "nobody modified it".

The `any` type's property listing (`GET …/types/any/properties`) carries the stamps as derived properties — `modifiedBy` appears there as "Modified by" — but their values sit at the row root. Filter and sort by the bare name; `any.modifiedBy` matches nothing.

Runtime datasets get the same trio on demand through `stamp: creator` / `createTime` / `modifyTime` fields ([Runtime datasets](runtime-datasets.html)); chat messages carry `creator` / `createdAt` / `modifiedAt`.

> **Why it matters.** There is no server clock to trust. Every stamp is whichever device wrote the change, and the CRDT converges on *a* value, not the *true* time. Sort and display on these freely; never use them as a fence for "has everything before T arrived" — that is what [sync status](../realtime/sync-status.html) is for.

## Built-in properties on objects

Objects carry a few properties under the universal `any` group. Paths use literal keys, not content-addressed property ids.

| Path | Kind | Meaning |
|---|---|---|
| `any.type` | string | The object's one type — what it is. Required at create, never cleared. Plain equality: `{"any.type": "<typeId>"}`, `$in` for a set. A type definition's row carries the marker `__type__` here and a collection's carries `__collection__`, never its own id, so a member query needs no exclusion. |
| `any.collections` | string array | The collections the object is filed under. A scalar filter is a *contains* test — `{"any.collections": "<collectionId>"}` — `$all` demands several, `$nin` excludes (`{"any.collections": {"$nin": ["bin"]}}` on every ordinary list). |
| `any.name` | string | Display name. |
| `any.description` | string | Description. Indexed with `any.name` under the search scope `basic`. |
| `any.icon` | string | Display icon; the encoding is the client's. |
| `any.tags` | string array | Free-form labels; filter with `{"any.tags": "<label>"}`. |

Tree placement is not a system field. The wiki usecase's `parentId` / `pos` / `folder` are ordinary columns of a hidden collection, at `<wikiCollectionId>.<propId>` on the objects filed under it, and nothing is stamped on create — [Objects](objects.html). The bin's `bin.movedAt` / `bin.movedBy` are the same kind of thing: ordinary synced properties of the built-in `bin` collection, written by the server in the same change as the membership op ([Collections](collections.html)).

## Field scopes

Every dataset field belongs to one scope, visible as `x-scope` in the dataset's [schema](runtime-datasets.html):

| Scope | Behavior |
|---|---|
| `synced` | Written through the DAG, replicated to every member, in history. |
| `derived` | Computed on apply (stamps, chat `creator`), read-only to writers. |
| `local` | Device-local, never synced — chat's `unread` flags. Written through `POST …/modify` with `"scope": "local"`; never bumps `modifiedAt`. |
| `account` | Synced across this account's devices only, invisible to other members (property definitions today). |

Negation operators (`$ne`, `$nin`, `$not`, `$exists: false`) also match rows that lack the field entirely — on the `objects` storage collection, which holds every object including type and collection definitions, always scope by `any.type` or `any.collections` first.
