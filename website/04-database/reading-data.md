---
title: Reading data
description: The one read path — POST /query over objects or a dataset — with mongo-style filters, array matching, sort, limit/offset and cursor paging.
order: 50
---
# Reading data

All reads go through one primitive: a windowed query over any-store, the embedded MongoDB-flavoured document store under every space. The same body serves a point-in-time snapshot and, on the `/subscribe` twin, a live stream.

## Two scopes

| Endpoint | Reads |
|----------|-------|
| `POST /v1/spaces/:spaceId/objects/query` | **Cross-object.** The space's `objects` collection — one row per object with its property values keyed `<typeId>.<propId>` plus `any.*` and the row-root stamps. |
| `POST /v1/spaces/:spaceId/query` | **Per-object.** One dataset of one object (`editor_blocks`, `chat_messages`, a runtime dataset…). Needs `objectId` + `dataset`. |

Both are POST because a filter does not fit a query string. Both have a `/subscribe` sibling with the same body — see [Subscribe](../realtime/subscribe.html) — and an `/aggregate` sibling for pipelines — see [Aggregation](aggregation.html).

```bash
# every page in the space, newest edits first
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"any.types": "<pageTypeId>"}, "sort": ["-modifiedAt"], "limit": 20}'

# the blocks of one document, in order
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$OBJ'", "dataset": "editor_blocks", "sort": ["nav.pos"]}'
```

## Request body

```json
{
  "objectId":     "<oid>",          // per-object only (required there)
  "dataset":      "<name>",         // per-object only (required there)
  "filter":       { "…": "…" },     // mongo-style; omit = match all
  "sort":         ["-_ver.id"],
  "limit":        50,
  "offset":       0,
  "includeTotal": true              // adds total + hasNext
}
```

The vocabulary is closed. An unknown key — `"filters"`, say — answers `400 request.unknown_field` listing the accepted set rather than silently querying the whole space. A serialised nil `objectId` (`"null"`, `"undefined"`) is `400 object.id_required`. A per-object read of an object the space does not have answers `404 object.not_found`; the cross-object query just returns zero rows.

Response:

```json
{ "records": [ { "id": "…", "…": "…" } ],
  "total": 17,          // with includeTotal
  "hasNext": true }     // with includeTotal
```

Without a `projection`, records ship their full stored form, including `_ver` (creation marker plus per-field high-water version ids) and `_deletedAt` / `_traces` when present. `projection` narrows them: a mongo-style map of field paths to `1` (include) or `-1` (exclude), e.g. `{"any": 1, "<typeId>": 1}`. `id` always ships, `_ver` narrows with the fields you asked for, and `{"_ver": -1}` drops it.

## Filter operators

Multiple keys in one filter object are AND-ed. A bare value means `$eq`.

| Group | Operators |
|-------|-----------|
| Comparison | `$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte` |
| Sets | `$in`, `$nin`, `$all` |
| Existence / shape | `$exists`, `$size`, `$type` |
| Logical | `$and`, `$or`, `$nor`, `$not` |
| Strings | `$regex` |

```json
{ "<typeId>.<propId>": "Casablanca" }
{ "<typeId>.year":     { "$gte": 1940, "$lt": 1950 } }
{ "<typeId>.title":    { "$regex": "^The " } }
{ "$or": [ { "<t>.a": 1 }, { "<t>.b": "x" } ] }
```

An operator outside this set is `400 filter.unknown_operator`, with the token in `details.operator` and the supported list in the message.

### Arrays

When a field is an array, the filter compares against its elements:

- a **scalar** means *contains*: `{"<t>.tags": "food"}`;
- **`$in`** means *intersects*: `{"<t>.tags": {"$in": ["a", "b"]}}`;
- **`$all`** means *superset*: `{"<t>.tags": {"$all": ["a", "b"]}}`.

There is deliberately no `$contains` — the scalar spelling already is it. `{"any.types": "<typeId>"}` is how you filter objects by type.

### Dates

Instants filter as instants. Wrap the literal in `{"$date": …}`:

```json
{ "modifiedAt": { "$gte": { "$date": "2026-01-01T00:00:00Z" } } }
```

> **Note.** A bare number or string here does not error — ordering comparisons are bracketed by type, so a bare literal never matches an instant and the filter comes back empty. See [Data types](data-types.html).

### Negation matches absent fields

`$ne`, `$nin`, `$not` and `$exists: false` also match rows that simply lack the field. The `objects` collection holds *every* object in the space, type definitions included, so `{"<t>.n": {"$ne": 2}}` returns piles of unrelated rows. Always scope a cross-object query by type: `{"any.types": "<typeId>", …}`.

## Sort

`sort` is an array of dotted paths, `-` prefix for descending, applied left to right: `["<wikiTypeId>.<parentIdPropId>", "<wikiTypeId>.<posPropId>"]` (the wiki tree's columns — [Objects](objects.html)). On `/subscribe`, `sort` is required whenever `limit > 0` so the window is well-defined. `{"sort": ["-modifiedAt"]}` is "recently modified first"; `-createdAt` is creation order.

## Paths

| Path | Where |
|------|-------|
| `<typeId>.<propId>` | Property values on the objects collection — both are content-addressed ids, resolved from `GET …/types/:typeId/properties`. |
| `any.types`, `any.name`, `any.description`, `any.tags` | The universal built-in type. |
| `<wikiTypeId>.<propId>` — the wiki type's `parentId` / `pos` / `folder` | Tree placement: ordinary properties of the hidden wiki type, ids from `POST /v1/catalog/wiki/setup` ([Objects](objects.html)). |
| `author`, `createdAt`, `modifiedAt`, `modifiedBy`, `spaceId` | Derived row-root stamps (objects collection only). `modifiedAt` is indexed; the rest, `modifiedBy` included, are scans. |
| `_ver.id` | The record's creation version id — the logical DAG order. |
| `id` | The record id. |

A type's `xKey` is a client-side label, never a server path.

## Paging

**`offset` / `limit`** is fine for a frozen snapshot. It floats on a live collection: row 50 becomes row 51 the moment something lands ahead of it, so paging across writes skips and repeats rows.

**Cursor paging** is stable: filter on an indexed, monotonic field and keep the same sort. Chat pages backward with

```json
{ "objectId": "<chat>", "dataset": "chat_messages",
  "filter": { "_ver.id": { "$lt": "<oldestSeen>" } },
  "sort": ["-_ver.id"], "limit": 50 }
```

`includeTotal` is page-bounded: the SDK applies `limit` to the count, so with `limit: 50` you get `total ≤ 50`. For an exact count query without a limit, or use a `$count` [aggregation](aggregation.html).

> **Why it matters.** Every query runs against a local database, so the cost of a read is disk, not network. Always set a `limit` anyway: an unbounded read builds a huge snapshot, and on a subscription it can overflow the mailbox.

## CLI

`any query-subscribe` is the CLI form of both scopes — it prints one JSON object per SSE frame, so the `snapshot` frame is the one-shot read:

```bash
any query-subscribe $SPACE $OBJ --dataset editor_blocks --sort nav.pos --limit 50
any query-subscribe $SPACE --properties --filter '{"any.types": "'$PAGE'"}' --sort -modifiedAt --limit 20
```

## Related

- [Indexes](indexes.html) — which of these paths are cheap.
- [Subscribe](../realtime/subscribe.html) — the same body, live.
- [Aggregation](aggregation.html) — counts and rollups server-side.
