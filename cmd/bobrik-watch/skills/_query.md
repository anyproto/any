# Querying objects and datasets

You can query the space with a real filter language (mongo-style, backed by
any-store) — not just "list everything and filter in JS". Reach for it whenever
you'd otherwise load a whole type and loop.

## Find objects by property

`anyHelper.getObjects(type, { filter, sort, limit, offset })` → `{ ok, records,
total?, error }`; iterate `.records`. The `type` arg accepts the type xKey or id
(not the display name); filter & sort keys are dotted **xKey** paths
`"<typeXKey>.<propXKey>"` (the type xKey is a stable slug — `createType` returns
it as `type.xKey`). Each record is nested, keyed by xKey
(`rec["<typeXKey>"].prop`, read with `getProp(rec, "<typeXKey>.prop")`).

```js
// recent noir films, newest first, top 5
anyHelper.getObjects("film", {
  filter: { "film.genre": "noir", "film.year": { "$gte": 1950 } },
  sort: ["-film.year"],
  limit: 5
})
```

Operators: `$eq` (bare value), `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$in`,
`$nin`, `$all`, `$exists`, `$regex`, `$and`, `$or`, `$not`. Multiple keys are
AND-ed.

## Array properties (tags/categories)

When a property is an array, the filter matches its elements:

```js
anyHelper.getObjects("agent_memory", { filter: { "agent_memory.tags": "lesson" } }).records        // contains "lesson"
anyHelper.getObjects("agent_memory", { filter: { "agent_memory.tags": { "$in": ["a","b"] } } }).records // intersects
```

`getObjects` returns `{ ok, records, total?, error }` — iterate `.records`. This
is the efficient way to filter by category — the store does it, you don't pull
everything back. (amemory uses exactly this for `categories`.)

## Read a dataset

Structured content (a mini app's source/state, a program's source) lives in a
dataset, not in properties. One record → `getRecord`; many → `getObjects`
dataset mode (pass `null` type + `objectId`/`dataset`):

```js
anyHelper.getRecord(objId, "mini_app", "main")                                            // one raw record
anyHelper.getObjects(null, { objectId: objId, dataset: "editor_blocks", sort: ["nav.pos"] }).records  // many
```

Dataset field paths are literal (`nav.pos`, `_ver.id`, `text`) — no type prefix.

## Paging

- `limit`/`offset` is fine for stable, one-shot reads.
- For mutable, growing collections page with a **cursor** on a monotonic
  indexed field instead — e.g. chat history backward:
  `getObjects(null, { objectId: chatId, dataset: "chat_messages", filter: { "_ver.id": { "$lt": oldestSeen } }, sort: ["-_ver.id"], limit: 30 }).records`.

## Gotchas

- Don't trust `includeTotal` for an unbounded count yet — it's bounded by
  `limit` in the current SDK. Omit `limit` if you truly need the full count.
- Cross-object property filters scan (the `objects` collection isn't indexed
  per-property) — fine at this scale, but prefer narrow filters and `limit`.
