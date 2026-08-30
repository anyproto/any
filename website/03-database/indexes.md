---
title: Indexes
description: Which fields are indexed, which queries are scans, and how to keep hot reads cheap — no create-index API is needed for the built-in paths.
order: 70
---
# Indexes

Every query runs against a local any-store database, so "cost" means local disk work, not a network round-trip. The difference between an indexed read and a scan still matters in a large space — this page says which is which.

## What is indexed

Built-in datasets declare indexes for their hot paths:

| Dataset | Index | Serves |
|---------|-------|--------|
| `editor_blocks` | `(nav.parentId, nav.pos)` | Listing a document's blocks in order; finding the tail position for appends. |
| `chat_messages` | `(_ver.id)` | Chronological paging — `sort: ["-_ver.id"]` with a `_ver.id` cursor. |
| `chat_messages` | `idx_mentions` (sparse, multikey) | `{"mentions": "<identity>"}` filters. |
| `objects` | `modifiedAt` (dense) | `sort: ["-modifiedAt"]` recency lists and range filters on it. |
| `objects` | `any.types` (sparse) | `{"any.types": "<typeId>"}` — the scope every cross-object query should carry. |

The objects collection's other row-root stamps — `author`, `createdAt`, `spaceId`, `modifiedBy` — are unindexed, so a filter on one of them scans.

The objects collection has **no per-property indexes**. A cross-object filter or sort on `<typeId>.<propId>` is a scan proportional to the space size. There is no create-index API for user properties; when a per-property read becomes hot, the options are an indexed built-in field, a dedicated per-object dataset, or a search over the [FTS / vector index](../search/index.html), which is maintained separately from the query engine.

## Cheap patterns

**Page on an indexed cursor, not `offset`.** A cursor filter on an indexed monotonic field returns straight from the index and is stable under concurrent writes:

```json
{ "objectId": "<chat>", "dataset": "chat_messages",
  "filter": { "_ver.id": { "$lt": "<oldestSeen>" } },
  "sort": ["-_ver.id"], "limit": 50 }
```

**List a folder through `nav`.** `{"filter": {"nav.parentId": "<folder>"}, "sort": ["nav.pos"]}` on `editor_blocks` hits the tree index directly.

**Put `$match` first in a pipeline.** [Aggregation](aggregation.html) pushes a leading `$match` down to the index plan; tombstone exclusion folds into the same prefix so it stays index-planned.

**Scope by type.** `{"any.types": "<typeId>"}` does not make a scan indexed, but it stops negation operators (`$ne`, `$nin`, `$exists: false`) from matching every field-less row in the space.

**Always set a `limit`.** A scan that stops after 20 matches is still bounded work; an unbounded one materialises the whole result.

**Ask for the plan.** An aggregation body with `"explain": true` returns `{plan}` instead of records, which shows whether the leading `$match` was pushed to an index or the stage runs as a collection scan:

```bash
any aggregate $SPACE $OBJ --dataset chat_messages --explain \
  --pipeline '[{"$match": {"creator": "A5…"}}, {"$count": "n"}]'
```

## Instants are index-keyable

Datetime values are stored as native instants (unix milliseconds), so a range filter or sort on `createdAt`, `modifiedAt` or a `date` / `datetime` property is memcmp-orderable and can key an index where one exists. Wrap literals in `{"$date": …}` — see [Data types](data-types.html).

## Runtime datasets

A [runtime dataset](runtime-datasets.html) declared on a user type is a per-object collection like `chat_messages`, so its reads are bounded by that object's records rather than the whole space. An `idRule: user` dataset is keyed by the caller-supplied id, which is what [upsert](upsert.html) diffs against.

## The search index is separate

`POST /v1/spaces/:spaceId/search` queries a consumer-side index (BM25 full-text plus an optional vector leg) built from the change feed. It covers editor text, chat text and property values by default, and stays eventually consistent with the query engine — a hit can outlive its object briefly. Use it for "find text anywhere"; use `/query` for exact filters. See [Search](../search/index.html).

> **Why it matters.** Both the query engine and the search index are on-device. Indexing your own data never sends it anywhere: the embedder can run in-process, and the FTS tables live next to the encrypted store in your data directory.

## Related

- [Reading data](reading-data.html) — the filter and sort grammar.
- [Aggregation](aggregation.html) — pushdown and the `explain` plan.
- [Full-text search](../search/full-text.html), [Vector search](../search/vector.html).
