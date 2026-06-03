# 09 — Querying (any-store)

The query primitive is the one read path for everything in a space. It is a
thin wrapper over **any-store** (a MongoDB-flavoured embedded document store),
so the filter/sort language is mongo-style. This is power the old anytypeHelper
never exposed — full operator filters, array matching, multi-key sort, and live
windowed subscriptions — so it's worth learning.

There are two endpoints; both POST (the filter/sort body doesn't fit a query
string) and both have a `/subscribe` SSE twin (see `docs/04-events.md`).

| Endpoint | Scope | Reads |
|----------|-------|-------|
| `POST /v1/spaces/:spaceId/objects/query` | **cross-object** | the per-space `objects` collection — one row per object, its computed property values keyed `<typeId>.<propId>` plus `any.*` / `nav.*` |
| `POST /v1/spaces/:spaceId/query` | **per-object** | one of a single object's datasets (`editor_blocks`, `chat_messages`, `program_source`, `mini_app`, …); needs `objectId` + `dataset` |

## Request body

```json
{
  "objectId": "<oid>",        // per-object only (required there)
  "dataset":  "<name>",       // per-object only (required there)
  "filter":   { ... },        // mongo-style; omit/empty = match all
  "sort":     ["nav.pos", "-_ver.id"],
  "limit":    50,
  "offset":   0,
  "includeTotal": false       // see the caveat below
}
```

Snapshot reply: `{ "records": [ ... ], "total": <int|omitted> }`. Records are the
raw stored documents (per-object datasets) or computed property rows
(cross-object). `/subscribe` adds the live frames documented in `docs/04-events.md`.

## Filter operators

any-store (v0.4.6) supports the mongo set. Confirmed working through the server:

- Comparison: `$eq` (or a bare value), `$ne`, `$gt`, `$gte`, `$lt`, `$lte`
- Sets: `$in`, `$nin`, `$all`
- Existence / shape: `$exists`, `$size`, `$type`
- Logical: `$and`, `$or`, `$nor`, `$not`
- Strings: `$regex`

Multiple keys in one filter object are AND-ed. Examples:

```json
{ "<typeId>.<propId>": "Casablanca" }                       // equality
{ "<typeId>.year":     { "$gte": 1940, "$lt": 1950 } }      // range (AND)
{ "<typeId>.title":    { "$regex": "^The " } }              // prefix
{ "$or": [ { "<t>.a": 1 }, { "<t>.b": "x" } ] }             // disjunction
```

### Arrays — the headline

When a property is an **array**, the filter compares against its *elements*:

- a **scalar** value means **contains**: `{ "<t>.tags": "food" }` → every object
  whose `tags` array includes `"food"`.
- **`$in`** means **intersects**: `{ "<t>.tags": { "$in": ["a","b"] } }`.
- **`$all`** means **superset**: `{ "<t>.tags": { "$all": ["a","b"] } }`.

This is how amemory does category filtering server-side (categories live in a
bare `tags` array; `getObjects("Agent Memory", {filter:{"agent_memory.tags":
{$in:cats}}})` prunes candidates before they cross the wire — the `typeKey`
arg accepts the name, but the dotted filter key uses the type **xKey**).

### Two gotchas

1. **Negation matches field-absent rows.** `$ne`, `$nin`, `$not`, and
   `$exists:false` also match objects that simply don't have the field. On the
   cross-object `objects` collection — which holds *every* object including type
   definitions — `{"<t>.n":{"$ne":2}}` returns piles of unrelated objects.
   Always scope cross-object queries by type (`{"any.types":"<typeId>"}`);
   `anyHelper.getObjects(typeKey, …)` does this for you.

2. **`includeTotal` is page-bounded in v0.0.4.** It is *intended* to be the
   unbounded match count, but the SDK applies `limit`/`offset` to the count too,
   so with `limit:50` you get `total ≤ 50`. For an exact total, query without a
   limit (full scan) — or omit `includeTotal` and don't rely on it. (SDK gap to
   fix.)

## Sort

`sort` is an array of dotted field paths; prefix with `-` for descending.
Multi-key sorts apply left-to-right: `["nav.parentId", "nav.pos"]`. A
`/subscribe` with `limit > 0` requires a `sort`.

## Paths

On the wire, property paths are `<typeId>.<propId>` — both are CID ids — plus
the builtin literals `any.types`, `any.name`, `nav.parentId`, `nav.pos`,
`_ver.id`. Through **anyHelper** you use dotted **xKey** paths instead
(`"agent_memory.tags"`, `"movie.title"`) — the *type xKey* (a stable snake_case
slug of the name, returned by `createType` as `type.xKey`; builtins use their
id) plus the *property xKey*. anyHelper resolves these to the server's
`<typeId>.<propId>` on the way in and reverse-maps records to readable nested
form (keyed by type xKey) on the way out. The xKey is stable across display-name
renames; builtin paths (`any.types`, `nav.parentId`, `program.name`) pass
through unchanged.

## Paging

- **offset/limit** is simple but floats: row 50 becomes row 51 the moment a
  record lands ahead of it. Fine for one-shot reads of stable data.
- **cursor** paging is stable for mutable collections: filter on an indexed,
  monotonic field. Chat pages backward with
  `{ "_ver.id": { "$lt": "<oldestSeen>" } }` sorted `["-_ver.id"]`. See
  `docs/08-clients.md` for the full pattern.

## Indexes & cost

Built-in datasets declare indexes for their hot paths:

- `editor_blocks` → `(nav.parentId, nav.pos)` (tree listing + MaxPos)
- `chat_messages` → `(_ver.id)` (chronological paging)

The per-space `objects` collection has **no per-property indexes**, so filtering
or sorting cross-object queries on a `<typeId>.<propId>` is a scan proportional
to the space size. It's fine at prototype scale; for hot, large-space queries
prefer an indexed builtin field or a dedicated per-object dataset. There is no
"create index" API yet.

## anyHelper surface

| Method | Maps to |
|--------|---------|
| `getObjects(typeKey, {filter, sort, limit, offset, space})` | cross-object query, type-scoped, readable dotted filter keys, returns normalized records |
| `getRecord(objId, dataset, recordId?, {space})` | per-object query → one record |
| `queryRecords(objId, dataset, {filter, sort, limit, offset, includeTotal}, {space})` | per-object query → `{ok, records, total?}` |

For dataset writes use `setRecord` (atomic per-field `$set` upsert); for property
writes use `createObject`/`updateObject` with dotted `"Type.prop"` keys.
