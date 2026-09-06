# 14 — Aggregation pipelines

MongoDB-style aggregation over any dataset: an ordered pipeline of
stages that filters, reshapes, unwinds, groups and sorts records inside
one snapshot read. Wraps the SDK's `Space.Aggregate` /
`Space.AggregateObjects` (any-store's aggregation framework underneath).
Use it when one `/query` isn't enough — counts per group, top-N rollups,
tag distributions — instead of pulling every record over HTTP and
reducing client-side.

**Snapshot-only.** There is no `/aggregate/subscribe`; re-run the
pipeline to refresh. For live windows over raw records, keep using
`/query/subscribe` (`docs/04-events.md`).

## Endpoints

```
POST /v1/spaces/:spaceId/objects/aggregate    cross-object — per-space `objects` collection
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

Deleted records are excluded server-side (a `_deletedAt`-missing
`$match` is prepended to every pipeline), so aggregates always agree
with what `/query` returns.

## Stages

`$match` (full `/query` filter language — `docs/09-query.md` — plus
`$expr`), `$sort`, `$skip`, `$limit`, `$count`, `$project`,
`$addFields`/`$set`, `$unwind`, `$group`, `$facet`, `$lookup`
(**self-join only**: `from` must name the aggregated collection or be
omitted).

Accumulators in `$group`: `$sum`, `$avg`, `$min`, `$max`, `$count`,
`$first`, `$last`, `$push`, `$addToSet`.

Expressions (in `$project`/`$addFields` values, `$group` keys and
accumulator arguments): field references (`"$a.b.c"`, including the
FTS/vector virtuals `"$_score"` / `"$_distance"`), literals
(`{"$literal": ...}` escapes a `$`-leading string), and nested
document/array expressions, and compute operators:

| family | operators |
|---|---|
| arithmetic | `$add`, `$subtract`, `$multiply`, `$divide`, `$abs`, `$round` |
| strings | `$concat`, `$split`, `$replaceOne`, `$replaceAll`, `$trim`, `$ltrim`, `$rtrim` |
| conditional | `$cond`, `$switch`, `$ifNull` |
| comparison | `$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$cmp` |
| dates | `$dateAdd`, `$dateDiff`, `$dateTrunc`, `$year`, `$week` |

That list is exhaustive — anything absent from it is rejected, so
there are no type conversions (`$toDate`, `$toInt`, …) and no array
operators (`$map`, `$filter`, `$reduce`).

**The date operators compute on instants.** Every timestamp `any`
stores is one: system stamps (`createdAt`, `modifiedAt`, chat
`createdAt`, runtime-dataset `createTime`/`modifyTime`) and user
properties declared with the `date` / `datetime` format. On the wire an
instant is `{"$date": "2026-08-05T00:00:00.000Z"}` — in pipeline output
too, so a `$dateTrunc` result reads back in the same shape as the field
it came from.

```sh
# Objects per calendar month of their last edit.
curl -s localhost:7001/v1/spaces/$S/objects/aggregate -d '{
  "pipeline": [
    {"$group": {"_id": {"$dateTrunc": {"date": "$modifiedAt", "unit": "month"}},
                "n": {"$count": {}}}},
    {"$sort": {"id": 1}}
  ]}'
```

The exception is a date property declared `kind: "string"` — the
ISO-8601 convention the date formats carried before instants existed.
Those still return `null` from every date operator, and no `$toDate`
exists to bridge them; ISO-8601 sorts lexicographically, so predicates
(`$lt` / `$gte` against a literal) and `$split`-based bucketing are what
they support. Kind is pinned at first write, so such a property stays a
string for life — a client that wants date arithmetic on it defines a
new property.

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

`$text` prefix driving a grouped rollup (`_score` flows downstream):

```json
[
  {"$match": {"$text": "zeppelin disaster"}},
  {"$addFields": {"score": "$_score"}},
  {"$sort": {"score": -1}},
  {"$limit": 50},
  {"$group": {"_id": "$author", "n": {"$count": {}}, "best": {"$max": "$score"}}}
]
```

## Pushdown — put `$match` first

The longest pushable prefix — a `$match` chain, then at most one
`$sort`, `$skip`, `$limit` **in that order** — compiles into a regular
query and runs through the access planner: secondary indexes, the
cost-based optimizer, full-text (`$text`) and vector sources all apply.
Everything after the prefix streams in Go. So:

- Filter early. `[{"$match": …}, {"$group": …}]` scans an index;
  `[{"$group": …}, {"$match": …}]` scans the whole dataset.
- `$text` and vector clauses are valid **only inside the prefix** — a
  `$match` containing them after `$group`/`$unwind`/`$skip`/`$limit`
  fails with `aggregate.bad_pipeline` instead of silently matching
  everything.
- An in-pipeline `$sort` directly followed by `$skip`/`$limit` keeps
  only the top `skip+limit` rows (O(K) memory).

`"explain": true` shows the split (`Pushdown: filter=… sort limit=…` +
the in-pipeline stage list).

## Limits

Blocking stages (`$group`, in-pipeline `$sort`) are bounded; exceeding
a bound aborts with `400 aggregate.limit_exceeded`
(`details.limit` = `group` / `accumArray` / `memory`):

| Bound | Default | Body field |
|---|---|---|
| Unique `$group` keys | 50 000 | `groupLimit` |
| `$push`/`$addToSet` length | 10 000 | `accumArrayLimit` |
| Retained bytes (all blocking stages) | 256 MiB | `memoryLimitBytes` |

Negative values mean unlimited and pass through verbatim — the server
is localhost-only and the caller is trusted. There is no spill-to-disk:
a pipeline that needs more should filter earlier or raise the limit.

## Where we drift from MongoDB

The pipeline language is deliberately a subset; what exists matches
Mongo semantics unless listed here. The two most common surprises
first:

| | MongoDB | here |
|---|---|---|
| `$group` output key | `_id` | **`id`** — accepts `_id` or `id` on input, always emits `id` (rows can be re-inserted unchanged) |
| `$count` result | `{"<name>": N}` | same — but note it arrives inside `records`, as the only document |
| Compute operators | full library | **the closed set listed under Stages** — no type conversions, no array operators |
| Date operators | operate on date values | present, but `null` against every timestamp `any` stores — see Stages |
| `$lookup` | joins any collection | **self-join only** — `from` must name the aggregated collection or be omitted |
| `$bucket` / `$bucketAuto` / `$replaceRoot` / `$sortByCount` / `$unionWith` | yes | not supported |
| `$out` / `$merge` | write the result into a collection | **not part of this endpoint** — `/aggregate` is a read surface; writes go through the CRDT |
| `$project` | implicit `_id`, exclusion mode (`{"a": 0}`) | **strictly explicit** — only listed fields appear, `id` included only if listed; exclusion not supported |
| Numbers | int/long/double/decimal | **IEEE 754 float64 only** — `$sum`/`$avg` are float arithmetic, integer precision ends at 2^53 |
| `$group` key equality | type-aware, field-order-insensitive documents | **byte equality** of canonical encoding — object keys are field-order-sensitive |
| `$min`/`$max` | type-aware comparison | anyenc value order across types; null/missing ignored |
| `$sort` stability | not guaranteed | stable |
| `$group` output order | unspecified | first-seen scan order — still add `$sort` |
| Dotted output names (`{"a.b": …}`) | allowed | rejected |
| `$text` placement | anywhere | pushdown prefix only (same for vector clauses) |
| Memory | spills to disk with `allowDiskUse` | hard budget, `aggregate.limit_exceeded` — no spill |

## Errors

| code | status | meaning |
|---|---|---|
| `request.missing_field` | 400 | no `pipeline` (or missing `objectId`/`dataset` on the per-object variant) |
| `request.schema` | 400 | `pipeline` is not a JSON array |
| `aggregate.bad_pipeline` | 400 | unparseable pipeline, unknown stage/accumulator, `$text`/vector outside the prefix |
| `aggregate.limit_exceeded` | 400 | a blocking-stage bound blew — `details.limit` says which |

Standard envelope, see `docs/06-errors.md`.

## CLI

```
any aggregate <spaceId> <objectId> --dataset NAME --pipeline '<json>'   # per-object dataset
any aggregate <spaceId> --properties --pipeline '<json>'               # objects collection
```

`--pipeline` takes inline JSON, `@FILE`, or `-` for stdin. Optional:
`--group-limit`, `--accum-limit`, `--memory-limit`, `--explain`.
