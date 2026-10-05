---
title: Indexes
description: Which fields are indexed, which queries are scans, how a runtime dataset declares its own indexes, and how to keep hot reads cheap.
order: 70
---
# Indexes

Every query runs against a local any-store database, so "cost" means local disk work, not a network round-trip. The difference between an indexed read and a scan still matters in a large space — this page says which is which.

## What is indexed

Built-in datasets declare indexes for their hot paths:

| Storage collection | Index | Serves |
|---------|-------|--------|
| `editor_blocks` | `(nav.parentId, nav.pos)` | Listing a document's blocks in order; finding the tail position for appends. |
| `chat_messages` | `(_ver.id)` | Chronological paging — `sort: ["-_ver.id"]` with a `_ver.id` cursor. |
| `chat_messages` | `idx_mentions` (sparse, multikey) | `{"mentions": "<identity>"}` filters. |
| `chat_messages` | `unread`, `unreadMention`, `unreadReactions` (sparse, each with `_ver.id`) | Unread lists and badges — only currently-flagged messages carry an entry. |
| `dataviews`, `views` | `pos`; on `views` also `(dataview, pos)` | A host's tables and one table's views, in order. |
| `objects` | `modifiedAt` (dense) | `sort: ["-modifiedAt"]` recency lists and range filters on it. |
| `objects` | `any.type` (dense) | `{"any.type": "<typeId>"}` — equality on the object's one type. |
| `objects` | `any.collections` (sparse) | `{"any.collections": "<collectionId>"}` — membership; the sidebar, the bin, the wiki tree's member set. |

Every cross-object query should carry one of those two as its scope.

The `objects` storage collection's other row-root stamps — `author`, `createdAt`, `spaceId`, `modifiedBy` — are unindexed, so a filter on one of them scans.

`objects` has **no per-property indexes**. A cross-object filter or sort on `<ownerId>.<propId>` is a scan proportional to the space size. There is no create-index API for user properties; when a per-property read becomes hot, the options are an indexed built-in field, a [runtime dataset](#runtime-datasets) that declares an index on the field, or a search over the [FTS / vector index](../search/index.html), which is maintained separately from the query engine.

## Cheap patterns

**Page on an indexed cursor, not `offset`.** A cursor filter on an indexed monotonic field returns straight from the index and is stable under concurrent writes:

```json
{ "objectId": "<chat>", "dataset": "chat_messages",
  "filter": { "_ver.id": { "$lt": "<oldestSeen>" } },
  "sort": ["-_ver.id"], "limit": 50 }
```

**List a block's children through the tree index.** `{"filter": {"nav.parentId": "<block>"}, "sort": ["nav.pos"]}` on `editor_blocks` hits `(nav.parentId, nav.pos)` directly. The wiki tree on `objects` has no such index — its `parentId` / `pos` are ordinary columns of the wiki collection and scan ([Objects](objects.html)). Put `{"any.collections": "<wikiCollectionId>"}` in the same filter so the sparse index narrows the scan first.

**Put `$match` first in a pipeline.** [Aggregation](aggregation.html) pushes a leading `$match` down to the index plan; tombstone exclusion folds into the same prefix so it stays index-planned.

**Scope by type or collection.** `{"any.type": "<typeId>"}` and `{"any.collections": "<collectionId>"}` narrow through an index, and they stop negation operators (`$ne`, `$nin`, `$exists: false`) from matching every field-less row in the space. A definition's own row never matches either, so no marker exclusion is needed.

**Always set a `limit`.** A scan that stops after 20 matches is still bounded work; an unbounded one materialises the whole result.

**Ask for the plan.** An aggregation body with `"explain": true` returns `{plan}` instead of records, which shows whether the leading `$match` was pushed to an index or the stage runs as a full scan:

```bash
any aggregate $SPACE $OBJ --dataset chat_messages --explain \
  --pipeline '[{"$match": {"creator": "A5…"}}, {"$count": "n"}]'
```

## Instants are index-keyable

Datetime values are stored as native instants (unix milliseconds), so a range filter or sort on `createdAt`, `modifiedAt` or a `date` / `datetime` property is memcmp-orderable and can key an index where one exists. Wrap literals in `{"$date": …}` — see [Data types](data-types.html).

## Runtime datasets

A [runtime dataset](runtime-datasets.html) is indexed by `id` and by the secondary indexes it declares — `indexes: [{key, fields, sparse?}]` in the declaration, or added later to a dataset that already holds records ([Declared indexes](runtime-datasets.html#declared-indexes)). Here `$DEF` is the definition id of a shared dataset on `$TYPE` with a `datetime` field `start`:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/datasets/$DEF/indexes \
  -H 'Content-Type: application/json' \
  -d '{"key": "by_start", "fields": ["start", "_objectId"]}'
# → 201 {"indexDefId": "…"}

any type part dataset index add $SPACE $TYPE $DEF --index '{"key":"by_start","fields":["start","_objectId"]}'
```

- A filter or sort on a leading run of an index's `fields` is a range read; a field deeper in the index only helps once the fields before it are pinned by equality.
- A per-object dataset's reads are bounded by that object's records rather than the whole space. An `idRule: user` dataset is keyed by the caller-supplied id, which is what [upsert](upsert.html) diffs against.
- In a [shared dataset](runtime-datasets.html#shared-datasets), `id` starts with the object id, so one object's records are a range of `id` — the per-object scope reads exactly that. Reading by a field across all objects needs a declared index led by that field; without one the dataset scope scans every record.
- A count and every aggregation read the matched records, indexed or not.
- An index is built per device. Until a shared dataset's build ends, queries on it scan, and every write on the server waits for it.

An index holds one to four fields, a dataset at most eight indexes, and none is unique. `_objectId` is indexable only on a shared dataset.

## The search index is separate

`POST /v1/spaces/:spaceId/search` queries a consumer-side index (BM25 full-text plus an optional vector leg) built from the change feed. It covers editor text, chat text and property values by default, and stays eventually consistent with the query engine — a hit can outlive its object briefly. Use it for "find text anywhere"; use `/query` for exact filters. See [Search](../search/index.html).

> **Why it matters.** Both the query engine and the search index are on-device. The FTS tables and vectors live next to the encrypted store in your data directory, and with `index.embedder: local` no indexed text leaves the device — the embedder runs as a child process of the server ([Embedders](../search/embedders.html)).

## Related

- [Reading data](reading-data.html) — the filter and sort grammar.
- [Aggregation](aggregation.html) — pushdown and the `explain` plan.
- [Full-text search](../search/full-text.html), [Vector search](../search/vector.html).
