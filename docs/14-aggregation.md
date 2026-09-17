# 14 — Aggregation pipelines

MongoDB-style aggregation over any dataset: an ordered pipeline of stages
that filters, reshapes, unwinds, groups and sorts records inside one
snapshot read. Wraps the SDK's `Space.Aggregate` /
`Space.AggregateObjects` (any-store's aggregation framework underneath).
Use it when one `/query` isn't enough — counts per group, top-N rollups,
tag distributions — instead of pulling every record over HTTP and
reducing client-side.

**Snapshot-only.** There is no `/aggregate/subscribe`; re-run the
pipeline to refresh. For live windows over raw records use
`/query/subscribe` (`docs/04-events.md`).

## Endpoints

```
POST /v1/spaces/:spaceId/objects/aggregate    cross-object — the per-space `objects` storage collection
POST /v1/spaces/:spaceId/aggregate            per-object dataset (objectId + dataset required)
```

Same two scopes as `/query` (`docs/03-api.md` § Data plane). Request
body:

```json
{
  "objectId": "obj_abc",            // per-object variant only
  "dataset":  "chat_messages",      // per-object variant only
  "pipeline": [ { "$match": {} }, { "$group": {} } ],   // required, array of stages
  "groupLimit":       50000,        // optional — max unique $group keys
  "accumArrayLimit":  10000,        // optional — max $push/$addToSet length
  "memoryLimitBytes": 268435456,    // optional — blocking-stage budget
  "explain": false                  // optional — return the plan instead of results
}
```

Response: `{ "records": [ ... ] }` — pipeline **result documents**, not
dataset rows. A `$group` doc carries the group key as `id`, a `$count`
doc is `{"<name>": N}` with no id at all. With `explain: true` the
response is `{ "plan": "..." }` instead — diagnostic only, not a stable
format.

Deleted records are excluded: the SDK prepends a `_deletedAt`-missing
`$match` to every pipeline, so aggregates agree with what `/query`
returns and the skip stays in the index-planned prefix.

## Stages

`$match` (the full `/query` filter language — `docs/09-query.md` — plus
`$expr` for expression predicates), `$sort`, `$skip`, `$limit`, `$count`,
`$project`, `$addFields` / `$set`, `$unwind`, `$group`, `$facet`,
`$lookup` (**self-join only**: omit `from`; `foreignField` is `id`, the
primary key of the aggregated storage collection).

`$out` and `$merge` are rejected with `aggregate.bad_pipeline` —
`/aggregate` is a read surface; writes go through the CRDT.

Accumulators in `$group`: `$sum`, `$avg`, `$min`, `$max`, `$count`,
`$first`, `$last`, `$push`, `$addToSet`.

Expressions (in `$project` / `$addFields` values, `$group` keys,
accumulator arguments and `$expr`): field references (`"$a.b.c"`),
literals (`{"$literal": ...}` escapes a `$`-leading string), nested
document / array expressions, and compute operators:

| family | operators |
|---|---|
| arithmetic | `$add`, `$subtract`, `$multiply`, `$divide`, `$abs`, `$round` |
| strings | `$concat`, `$split`, `$replaceOne`, `$replaceAll`, `$trim`, `$ltrim`, `$rtrim`, `$strLenBytes`, `$strLenCP` |
| arrays | `$size` |
| conditional | `$cond`, `$switch`, `$ifNull` |
| comparison | `$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$cmp` |
| dates | `$dateAdd`, `$dateDiff`, `$dateTrunc`, `$year`, `$week` |

The list is closed — any other operator is rejected, so there are no type
conversions (`$toDate`, `$toInt`, …) and no `$map` / `$filter` /
`$reduce`. An operand of the wrong type yields `null` rather than an
error.

**The date operators compute on instants.** Every timestamp `any` stores
is one: the objects-row `createdAt` / `modifiedAt`, chat `createdAt` /
`modifiedAt`, runtime-dataset `createTime` / `modifyTime`, and every
property of kind `datetime` (the `date` / `datetime` slugs). On the wire
an instant is `{"$date": "2026-08-05T00:00:00.000Z"}` — in pipeline
output too, so a `$dateTrunc` result reads back in the same shape as the
field it came from. `unit`, `timezone`, `startOfWeek` and `binSize` must
be literals; `timezone` defaults to UTC. A date operator given anything
that is not an instant — an ISO-8601 string included — returns `null`.

```sh
# Objects per calendar month of their last edit.
curl -s localhost:7001/v1/spaces/$S/objects/aggregate -d '{
  "pipeline": [
    {"$group": {"_id": {"$dateTrunc": {"date": "$modifiedAt", "unit": "month"}},
                "n": {"$count": {}}}},
    {"$sort": {"id": 1}}
  ]}'
```

## Examples

Messages per author in a chat (per-object dataset):

```sh
curl -s localhost:7001/v1/spaces/$S/aggregate -d '{
  "objectId": "'$OBJ'", "dataset": "chat_messages",
  "pipeline": [
    {"$group": {"_id": "$creator", "n": {"$count": {}}}},
    {"$sort":  {"n": -1}}
  ]}'
# → {"records": [{"id": "A6Wd…", "n": 42}, {"id": "A9kQ…", "n": 17}]}

any aggregate $S $OBJ --dataset chat_messages \
  --pipeline '[{"$group":{"_id":"$creator","n":{"$count":{}}}},{"$sort":{"n":-1}}]'
```

Movie count per year (cross-object, property values at
`<typeId>.<propId>` — `docs/09-query.md` § Paths):

```sh
curl -s localhost:7001/v1/spaces/$S/objects/aggregate -d '{
  "pipeline": [
    {"$match": {"'$TYPE'.'$YEAR'": {"$gte": 1980}}},
    {"$group": {"_id": "$'$TYPE'.'$YEAR'", "n": {"$count": {}}}},
    {"$sort":  {"id": 1}}
  ]}'
# → {"records": [{"id": 1985, "n": 1}, {"id": 1986, "n": 2}]}
```

Tag distribution — unwind an array property, collect titles per tag:

```sh
any aggregate $S --properties --pipeline '[
  {"$unwind": "$'$TYPE'.'$TAGS'"},
  {"$group": {"_id": "$'$TYPE'.'$TAGS'",
              "n": {"$count": {}},
              "titles": {"$addToSet": "$'$TYPE'.'$TITLE'"}}},
  {"$sort": {"n": -1}}, {"$limit": 10}
]'
```

Just a count:

```sh
any aggregate $S --properties --pipeline '[{"$count": "objects"}]'
# → {"records": [{"objects": 128}]}
```

Several rollups over one scan with `$facet`:

```json
[
  {"$match": {"any.type": "<typeId>"}},
  {"$facet": {
    "total":  [{"$count": "n"}],
    "recent": [{"$sort": {"modifiedAt": -1}}, {"$limit": 5}]
  }}
]
```

## Pushdown — put `$match` first

The longest pushable prefix — a `$match` chain, then at most one `$sort`,
`$skip`, `$limit` **in that order** — compiles into a regular query and
runs through the access planner (secondary indexes, the cost-based
optimizer). Everything after the prefix streams in Go. So:

- Filter early. `[{"$match": …}, {"$group": …}]` can use an index;
  `[{"$group": …}, {"$match": …}]` scans the whole dataset.
- `$expr` never becomes index bounds: in a leading `$match` the ordinary
  keys push down and the expression runs as a residual filter.
- An in-pipeline `$sort` directly followed by `$skip` / `$limit` keeps
  only the top `skip+limit` rows (O(K) memory).
- `$text` and `$knn` clauses need a full-text / vector index, and no
  space storage collection carries one: they answer `aggregate.bad_pipeline`.
  Full-text and semantic search is `POST …/search` (`docs/13-index.md`).

`"explain": true` shows the split (`Pushdown: filter=… sort limit=…` plus
the in-pipeline stage list).

## Limits

Blocking stages (`$group`, in-pipeline `$sort`, `$facet` result buffers)
are bounded; exceeding a bound aborts with `400 aggregate.limit_exceeded`
(`details.limit` = `group` / `accumArray` / `memory`):

| Bound | Default | Body field |
|---|---|---|
| Unique `$group` keys | 50 000 | `groupLimit` |
| `$push` / `$addToSet` length | 10 000 | `accumArrayLimit` |
| Retained bytes (all blocking stages) | 256 MiB | `memoryLimitBytes` |

Negative values mean unlimited and pass through verbatim — the server is
localhost-only and the caller is trusted. There is no spill-to-disk: a
pipeline that needs more filters earlier or raises the limit.

## Where we drift from MongoDB

The pipeline language is a subset; what exists matches Mongo semantics
unless listed here. The two most common surprises first:

| | MongoDB | here |
|---|---|---|
| `$group` output key | `_id` | **`id`** — accepts `_id` or `id` on input, always emits `id` |
| `$count` result | `{"<name>": N}` | same — it arrives inside `records`, as the only document |
| Compute operators | full library | **the closed set listed under Stages** — no type conversions, no `$map` / `$filter` / `$reduce` |
| Runtime type errors | query error | `null` (a non-numeric arithmetic operand, a non-string string operand, division by zero, a non-instant date operand) |
| Date operator parameters | may be expressions | `unit` / `timezone` / `startOfWeek` / `binSize` must be literals |
| `$lookup` | joins any storage collection | **self-join only** — omit `from`; `foreignField` must be `id` |
| `$bucket` / `$bucketAuto` / `$replaceRoot` / `$sortByCount` / `$unionWith` | yes | not supported |
| `$out` / `$merge` | write the result into a storage collection | rejected (`aggregate.bad_pipeline`) — `/aggregate` is read-only |
| `$project` | implicit `_id`, exclusion mode (`{"a": 0}`) | **strictly explicit** — only listed fields appear, `id` included only if listed; exclusion not supported |
| Numbers | int / long / double / decimal | **IEEE 754 float64 only** — `$sum` / `$avg` are float arithmetic, integer precision ends at 2^53 |
| `$group` key equality | type-aware, field-order-insensitive documents | **byte equality** of the canonical encoding — object keys are field-order-sensitive |
| `$min` / `$max`, expression comparisons | BSON cross-type order | anyenc value order (`null < number < string < false < true < array < object < … < dateTime`); `$min` / `$max` ignore null / missing |
| `$sort` stability | not guaranteed | stable |
| `$group` output order | unspecified | first-seen scan order — still add `$sort` |
| Dotted output names (`{"a.b": …}`) | allowed | rejected |
| `$text` | anywhere | unavailable — no space storage collection has a full-text index |
| Memory | spills to disk with `allowDiskUse` | hard budget, `aggregate.limit_exceeded` — no spill |

## Errors

| code | status | meaning |
|---|---|---|
| `request.bad_json` | 400 | missing, unreadable or invalid JSON body |
| `request.missing_field` | 400 | no `pipeline` (or missing `objectId` / `dataset` on the per-object variant) |
| `request.schema` | 400 | `pipeline` is not a JSON array |
| `request.invalid_field` | 400 | per-object aggregate on the tech space's index object over a dataset other than `profile` / `bundles` |
| `aggregate.bad_pipeline` | 400 | unparseable pipeline, unknown stage / accumulator / operator, `$text` / `$knn`, `$out` / `$merge` |
| `aggregate.limit_exceeded` | 400 | a blocking-stage bound blew — `details.limit` says which |

Standard envelope, see `docs/06-errors.md`.

## CLI

```
any aggregate <spaceId> <objectId> --dataset NAME --pipeline '<json>'   # per-object dataset
any aggregate <spaceId> --properties --pipeline '<json>'               # objects storage collection
```

`--pipeline` takes inline JSON, `@FILE`, or `-` for stdin. Optional:
`--group-limit`, `--accum-limit`, `--memory-limit`, `--explain`.
