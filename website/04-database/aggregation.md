---
title: Aggregation
description: MongoDB-style pipelines — $match, $group, $unwind, $sort and friends — computed in one snapshot read over objects or a per-object dataset.
order: 110
---
# Aggregation

An aggregation is an ordered pipeline of stages that filters, reshapes, unwinds, groups and sorts records inside one snapshot read. Reach for it when a single [query](reading-data.html) isn't enough — counts per group, top-N rollups, tag distributions — instead of pulling every record over HTTP and reducing on the client.

Aggregation is snapshot-only. There is no `/aggregate/subscribe`; re-run the pipeline to refresh, and keep [`/query/subscribe`](../realtime/subscribe.html) for live windows over raw records.

## Endpoints

```
POST /v1/spaces/:spaceId/objects/aggregate    cross-object — the space's objects collection
POST /v1/spaces/:spaceId/aggregate            per-object dataset (objectId + dataset required)
```

The same two scopes as `/query`. Request body:

```json
{
  "objectId": "obj_abc",
  "dataset":  "chat_messages",
  "pipeline": [ { "$match": {} }, { "$group": {} } ],
  "groupLimit":       50000,
  "accumArrayLimit":  10000,
  "memoryLimitBytes": 268435456,
  "explain": false
}
```

`objectId` and `dataset` apply to the per-object variant only; `pipeline` is required; the three limits and `explain` are optional. The response is `{"records": [...]}` — pipeline **result documents**, not dataset rows. With `explain: true` you get `{"plan": "..."}` instead (diagnostic, not a stable format).

Deleted records are excluded server-side: a `_deletedAt`-missing `$match` is prepended to every pipeline, so aggregates always agree with `/query`.

## Examples

Messages per author in a chat:

```sh
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/aggregate -d '{
  "objectId": "'$OBJ'", "dataset": "chat_messages",
  "pipeline": [
    {"$group": {"_id": "$creator", "n": {"$count": {}}}},
    {"$sort":  {"n": -1}}
  ]}'
# → {"records": [{"id": "A6Wd…", "n": 42}, {"id": "A9kQ…", "n": 17}]}

any aggregate $SPACE $OBJ --dataset chat_messages \
  --pipeline '[{"$group":{"_id":"$creator","n":{"$count":{}}}},{"$sort":{"n":-1}}]'
```

Movie count per year over objects (property values live at `<typeId>.<propId>`, see [Reading data](reading-data.html)):

```sh
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/aggregate -d '{
  "pipeline": [
    {"$match": {"'$TYPE'.'$YEAR'": {"$gte": 1980}}},
    {"$group": {"_id": "$'$TYPE'.'$YEAR'", "n": {"$count": {}}}},
    {"$sort":  {"id": 1}}
  ]}'
```

Tag distribution — unwind an array property, collect titles per tag:

```sh
any aggregate $SPACE --properties --pipeline '[
  {"$unwind": "$'$TYPE'.'$TAGS'"},
  {"$group": {"_id": "$'$TYPE'.'$TAGS'", "n": {"$count": {}},
              "titles": {"$addToSet": "$'$TYPE'.'$TITLE'"}}},
  {"$sort": {"n": -1}}, {"$limit": 10}
]'
```

Objects per calendar month of their last edit — the date operators compute on the instants `any` stores:

```sh
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/aggregate -d '{
  "pipeline": [
    {"$group": {"_id": {"$dateTrunc": {"date": "$modifiedAt", "unit": "month"}},
                "n": {"$count": {}}}},
    {"$sort": {"id": 1}}
  ]}'
```

Just a count: `any aggregate $SPACE --properties --pipeline '[{"$count": "objects"}]'` → `{"records": [{"objects": 128}]}`.

## Stages and operators

Stages: `$match` (the full `/query` filter language plus `$expr`), `$sort`, `$skip`, `$limit`, `$count`, `$project`, `$addFields`/`$set`, `$unwind`, `$group`, `$facet`, `$lookup` (self-join only — `from` must name the aggregated collection or be omitted).

Accumulators in `$group`: `$sum`, `$avg`, `$min`, `$max`, `$count`, `$first`, `$last`, `$push`, `$addToSet`.

Expressions take field references (`"$a.b.c"`, including the search virtuals `"$_score"` / `"$_distance"`), literals (`{"$literal": …}` escapes a `$`-leading string), nested documents/arrays, and this closed operator set:

| Family | Operators |
|---|---|
| arithmetic | `$add`, `$subtract`, `$multiply`, `$divide`, `$abs`, `$round` |
| strings | `$concat`, `$split`, `$replaceOne`, `$replaceAll`, `$trim`, `$ltrim`, `$rtrim` |
| conditional | `$cond`, `$switch`, `$ifNull` |
| comparison | `$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$cmp` |
| dates | `$dateAdd`, `$dateDiff`, `$dateTrunc`, `$year`, `$week` |

Anything absent is rejected: no type conversions (`$toDate`, `$toInt`), no array operators (`$map`, `$filter`, `$reduce`). Date operators work on every instant — system stamps and `date`/`datetime` properties — and return instants in the same `{"$date": …}` wire shape (see [Data types](data-types.html)). An ISO-8601 string in a `string`-kind property is not an instant and returns `null` from every date operator; kind is pinned at first write, so date arithmetic needs a `datetime` property.

## Pushdown — put `$match` first

The longest pushable prefix — a `$match` chain, then at most one `$sort`, `$skip`, `$limit` in that order — compiles into a regular query and runs through the access planner: secondary [indexes](indexes.html), full-text and vector sources all apply. Everything after the prefix streams in memory.

- Filter early. `[$match, $group]` scans an index; `[$group, $match]` scans the whole dataset.
- `$text` and vector clauses are valid only inside the prefix — after `$group`/`$unwind`/`$skip`/`$limit` they fail with `aggregate.bad_pipeline` instead of matching everything.
- An in-pipeline `$sort` directly followed by `$skip`/`$limit` keeps only the top `skip+limit` rows.

`"explain": true` shows the split.

## Limits

Blocking stages (`$group`, in-pipeline `$sort`) are bounded; exceeding a bound aborts with `400 aggregate.limit_exceeded` and `details.limit` naming which one.

| Bound | Default | Body field |
|---|---|---|
| Unique `$group` keys | 50 000 | `groupLimit` |
| `$push`/`$addToSet` length | 10 000 | `accumArrayLimit` |
| Retained bytes across blocking stages | 256 MiB | `memoryLimitBytes` |

Negative values mean unlimited. There is no spill to disk — filter earlier or raise the limit.

> **Why it matters.** The pipeline runs on the replica on your device, over decrypted data that never left it. There is no query planner in a datacenter to send this to — which is also why the budget is a hard in-memory bound rather than a billable spill.

## Where this differs from MongoDB

| | MongoDB | any |
|---|---|---|
| `$group` output key | `_id` | `id` — accepts `_id` or `id` on input, always emits `id` |
| `$count` result | `{"<name>": N}` | same, delivered as the only document in `records` |
| Compute operators | full library | the closed set above |
| `$lookup` | any collection | self-join only |
| `$bucket`, `$bucketAuto`, `$replaceRoot`, `$sortByCount`, `$unionWith` | yes | not supported |
| `$out` / `$merge` | write results | not supported — writes go through the CRDT |
| `$project` | implicit `_id`, exclusion mode | strictly explicit — only listed fields appear, `id` only if listed |
| Numbers | int/long/double/decimal | IEEE 754 float64 only; integer precision ends at 2^53 |
| `$group` key equality | type-aware, field-order-insensitive | byte equality of the canonical encoding — object keys are order-sensitive |
| `$min`/`$max` | type-aware | value order across types; null/missing ignored |
| `$sort` stability | not guaranteed | stable |
| `$group` output order | unspecified | first-seen scan order — still add `$sort` |
| Dotted output names | allowed | rejected |
| `$text` placement | anywhere | pushdown prefix only |
| Memory | `allowDiskUse` spill | hard budget, `aggregate.limit_exceeded` |

## Errors

| Code | Status | Meaning |
|---|---|---|
| `request.missing_field` | 400 | no `pipeline`, or missing `objectId`/`dataset` on the per-object variant |
| `request.schema` | 400 | `pipeline` is not a JSON array |
| `aggregate.bad_pipeline` | 400 | unparseable pipeline, unknown stage/accumulator, `$text`/vector outside the prefix |
| `aggregate.limit_exceeded` | 400 | a blocking-stage bound was exceeded — `details.limit` says which |

## CLI

```
any aggregate <spaceId> <objectId> --dataset NAME --pipeline '<json>'   # per-object dataset
any aggregate <spaceId> --properties --pipeline '<json>'               # objects collection
```

`--pipeline` takes inline JSON, `@FILE`, or `-` for stdin. Optional: `--group-limit`, `--accum-limit`, `--memory-limit`, `--explain`.
