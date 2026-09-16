# 09 — Querying (any-store)

The query primitive is the one read path for everything in a space. It is a
thin wrapper over **any-store** (a MongoDB-flavoured embedded document store),
so the filter / sort language is mongo-style: operator filters, array
matching, multi-key sort, and live windowed subscriptions.

There are two endpoints; both POST (the filter / sort body doesn't fit a query
string) and both have a `/subscribe` SSE twin (`docs/04-events.md`). For
counts per group, rollups and tag distributions, both scopes also take
MongoDB-style aggregation pipelines at the sibling `…/aggregate` endpoints
(snapshot-only) — [`docs/14-aggregation.md`](14-aggregation.md).

| Endpoint | Scope | Reads |
|----------|-------|-------|
| `POST /v1/spaces/:spaceId/objects/query` | **cross-object** | the per-space `objects` storage collection — one row per object: its property values keyed `<ownerId>.<propId>`, `any.*` (its one type and the collections it is filed under), and the row-root stamps |
| `POST /v1/spaces/:spaceId/query` | **per-object** | one storage collection of a single object (`editor_blocks`, `chat_messages`, a namespaced `<typeId>_<key>` storage collection, …); needs `objectId` + `dataset` |

## Request body

```json
{
  "objectId": "<oid>",        // per-object only (required there)
  "dataset":  "<name>",       // per-object only (required there)
  "filter":   { ... },        // mongo-style; omit/empty = match all
  "sort":     ["-_ver.id"],
  "limit":    50,             // 0 / absent = unbounded
  "offset":   0,
  "includeTotal": false,      // adds total + hasNext to the snapshot reply
  "includeDeleted": false,    // per-object snapshot only — see § Tombstones
  "projection": { "any": 1, "<typeId>": 1 }
}
```

The body vocabulary is closed: these fields plus the subscribe-only
`mailboxCapacity` / `driftBudgetPercent`. An unknown key — `"filters"`, say
— is `400 request.unknown_field` naming the accepted set, never a silently
unfiltered query. A filter that doesn't parse is `400 filter.invalid`
(`details.path` locates it).

On `/subscribe`, **`limit` requires `sort`** — a live window has to be
ordered for the engine to know which records fall inside it, so a limit with
no sort is `400 request.invalid_field`. A snapshot takes an unordered limit:
there it is an arbitrary page.

Snapshot reply: `{ "records": [ ... ], "total"?: <int>, "hasNext"?: <bool> }`.
Records are the stored documents (per-object storage collections) or object rows
(cross-object). With `includeTotal`, `total` is the full match count,
independent of `limit` / `offset`, and `hasNext` is `offset + len(records)
< total`. `/subscribe` frames are documented in `docs/04-events.md`.

## Projection

`projection` shapes the records that come back. It is mongo's grammar: a
flat object mapping dotted field paths to `1` (include) or `-1` (exclude).
`true` / `false` / `0` are accepted spellings of the same two marks.

```json
{"projection": {"any": 1, "<typeId>": 1}} // nothing but those subtrees
{"projection": {"_ver": -1}}              // every user field, no version map
{"projection": {"any": 1, "any.tags": -1}} // all of any except one leaf
{"projection": {"any.name": 1}}           // one leaf out of a group
```

**Mode is inferred, deepest mark wins.** One or more `1` on a user field is
*include* mode: start from nothing and add. Only exclusions is *exclude*
mode: start from the whole record and carve. Where a path and one of its
ancestors are both marked, the deeper mark decides — that is what makes
"this subtree except one leaf" expressible.

Projection shapes output only. It never changes which records match or the
order they arrive in; `filter` and `sort` run over the whole stored record.
It applies to `/query/subscribe` exactly as to `/query` — snapshot frames,
`changes` docs, and the per-field ops inside them — so a projected
subscription cannot widen after the first update.

### The three rules a client needs

1. **`id` always rides along**, listed or not. It is the record identity
   every windowed cache and every `changes` frame keys on, so `{"id": -1}`
   is `400 request.invalid_field`.
2. **`_ver` follows the projection automatically.** Never name a `_ver`
   path — narrow the fields you want and the version map narrows with them.
   `{"_ver": -1}` drops it entirely, which is worth doing: even narrowed it
   is around a third of a projected record.
3. **A projected field the record does not have stays absent.** Nothing is
   null-filled, so "not selected" and "not set" never collapse.

### Protocol fields

`_`-prefixed fields sit outside mode inference and carry their own
defaults, so `{"_ver": -1}` alone still means "every user field":

| field | default under a projection | to change it |
|---|---|---|
| `_ver` | included, narrowed to the projection | `{"_ver": -1}` to drop |
| `_traces`, `_deletedAt` | included whole | `{"_traces": -1}` to drop |
| `_addSeq`, `_applySeq` | dropped | `{"_applySeq": 1}` to keep |

`_traces` is the write-correlation map an optimistic client matches its own
echo on (the `traceIds` it sent to `/modify`), so it rides along even when
you list only user fields.

The delivery counters are peer-local and SDK-internal — consumers reason
with `versionId` — so a projecting client does not pay for them by default.
**A request with no `projection` (or an empty one) is unchanged**, byte for
byte, counters included.

### How `_ver` narrowing stays correct

`_ver` mirrors the record, except that a node may be a bare version string
(collapsed — it applies at and below that point) and an object node may
carry `*`, the version for any sibling not enumerated there. Narrowing
follows your **inclusions**, keeping `*` at every level it descends into
and copying matched subtrees verbatim, and then applies your
**exclusions** — dropping a field drops its version with it, where `_ver`
enumerates it (a subtree the CRDT has collapsed to a single version string
keeps covering what is under it). That buys an exact contract:

> For every path the projection includes, the narrowed `_ver` resolves to
> the same version as the full one.

Paths you *excluded* are outside that contract — a version lookup there may
land on a surviving `*` default instead of its own entry. Read versions for
the fields you asked for, or drop `_ver`. Subtrees are never collapsed to
their maximum version: that would over-report a leaf's version, and a client
reconciling optimistic state per field would discard a local edit that is
still newer.

### Divergences from mongo

- **Include and exclude mix.** Mongo rejects a projection carrying both;
  here they compose, deepest mark wins — in both directions. A deeper
  exclude carves an included subtree, and a deeper include narrows one
  (`{"any":1,"any.name":1}` is `any.name`, not all of `any`).
- **`id` cannot be excluded** (mongo lets you drop `_id`).
- **Protocol fields are not part of mode inference**, per the table above.
- `-1` is accepted as an exclude marker alongside `0` / `false`.

### Bounds

At most 128 entries, at most 8 path segments deep. An empty path, an empty
segment (`"any..name"`), the reserved `*` segment, or a value that is not
one of the accepted marks is `400 request.invalid_field`.

Arrays are descended element-wise, mongo-style: `{"tags.name": 1}` over an
array of objects keeps each element's `name`. Every element keeps its slot —
one holding none of the projected paths comes back as `{}` — so a
projection never changes an array's length or shifts its indices.

### CLI

`--projection` takes the CSV shorthand, `-` prefixing an exclusion the way
`--sort` prefixes a descending key:

```
any query-subscribe SPACE --properties --projection 'any,<typeId>'
any query-subscribe SPACE --properties --projection '-_ver'
```

## Filter operators

The operator set is any-store's filter grammar:

- Comparison: `$eq` (or a bare value), `$ne`, `$gt`, `$gte`, `$lt`, `$lte`
- Sets: `$in`, `$nin`, `$all`
- Existence / shape: `$exists`, `$size`, `$type`
- Logical: `$and`, `$or`, `$nor`, `$not`
- Strings: `$regex` (with `$options`)

`$text` and `$knn` also parse, but need a full-text / vector index that no
space storage collection carries; search is `POST …/search` (`docs/13-index.md`).

Multiple keys in one filter object are AND-ed. Examples:

```json
{ "<typeId>.<propId>": "Casablanca" }                       // equality
{ "<typeId>.year":     { "$gte": 1940, "$lt": 1950 } }      // range (AND)
{ "<typeId>.title":    { "$regex": "^The " } }              // prefix
{ "$or": [ { "<t>.a": 1 }, { "<t>.b": "x" } ] }             // disjunction
```

An operator outside the set is `400 filter.unknown_operator`; the message
lists the whole supported set and `details.operator` carries the token you
sent.

### Dates

Timestamps — the derived `createdAt` / `modifiedAt` stamps and every
property of kind `datetime` (the `date` / `datetime` slugs) — are instants,
written and read as `{"$date": "<RFC 3339>"}`. A filter literal takes the
same shape:

```json
{ "modifiedAt":        { "$gte": { "$date": "2026-01-01T00:00:00Z" } } }
{ "<typeId>.<propId>": { "$lt":  { "$date": "2026-08-05T00:00:00Z" } } }
```

**A bare number or string does not error — it silently matches nothing.**
Ordering comparisons are bracketed by type: a number literal only compares
against numbers, a string literal against strings. So
`{"modifiedAt": {"$gte": 1700000000}}` matches no instant, and neither does
`$lt`, `$eq` or a bare ISO string. Wrap the literal.

Instants sort chronologically and are index-keyable, and `/aggregate`
computes on them directly — `$year`, `$dateTrunc`, `$dateDiff`
(`docs/14-aggregation.md`). A string field holding an ISO-8601 date is just
a string: it compares and sorts lexicographically (chronological for
RFC 3339 in one offset), and every date operator returns `null` for it.

### Arrays

When a field is an **array**, the filter compares against its *elements*:

- a **scalar** value means **contains**: `{ "<t>.tags": "food" }` → every
  object whose `tags` array includes `"food"`.
- **`$in`** means **intersects**: `{ "<t>.tags": { "$in": ["a","b"] } }`.
- **`$all`** means **superset**: `{ "<t>.tags": { "$all": ["a","b"] } }`.

This is how a client filters by category server-side, pruning candidates
before they cross the wire. There is no `$contains` — the scalar spelling
already is it.

### Type and collection

An object carries **one type** — `any.type`, a scalar string — and **any
number of collections** — `any.collections`, an array of collection ids. The
two filter differently:

```json
{ "any.type":        "<typeId>" }                    // of that type
{ "any.type":        { "$in": ["<t1>", "<t2>"] } }   // of any of those types
{ "any.collections": "<collectionId>" }              // filed under it
{ "any.collections": { "$all": ["<c1>", "<c2>"] } }  // filed under both
{ "any.collections": { "$nin": ["bin"] } }           // not in the bin
```

`any.type` is **plain equality on a scalar** — no array matching, `$in` for a
set of types. `any.collections` is **membership** (§ Arrays): a scalar value
matches any element, `$all` requires several, `$nin` excludes.

Ordinary listings exclude bin members with
`{"any.collections": {"$nin": ["bin"]}}`; the bin itself is
`{"any.collections": "bin"}` sorted `["-bin.movedAt"]`.

**No marker exclusion is ever needed.** A type definition's own row carries
`any.type: "__type__"` and a collection definition's carries
`any.type: "__collection__"` — the marker, never its own id — so a definition
row never matches `{"any.type": "<rootId>"}` or
`{"any.collections": "<rootId>"}`, not even while it hosts its own property
values and its own datasets' records.

### Negation matches absent fields

`$ne`, `$nin`, `$not`, and `$exists: false` also match rows that simply
don't have the field. The cross-object `objects` storage collection holds
*every* object, type and collection definitions included, so
`{"<t>.n": {"$ne": 2}}` returns piles of unrelated objects. Scope
cross-object queries by type (`{"any.type": "<typeId>"}`) or by collection
(`{"any.collections": "<collectionId>"}`).

## Tombstones

A deleted record leaves a **tombstone**: the id is burned (re-upserting it
answers `upsert.record_deleted`), the content is wiped, and the row keeps
`id`, `_deletedAt`, `_ver` and `_traces`. Every read skips tombstones by
default — `/query`, `/subscribe` and `/aggregate` alike.

The per-object snapshot takes `"includeDeleted": true` to return them next
to the live rows, `_deletedAt` telling the two apart. The use case is a
writer that assigns its own record ids from a sequence (`id: user`
datasets, `docs/03-api.md` § Runtime dataset schemas): the live maximum is
not the next free id once anything was deleted, so the probe is

```json
{"objectId": "<log>", "dataset": "<collection>",
 "includeDeleted": true, "sort": ["-id"], "limit": 1}
```

— one primary-key read; the answer's `id` is the highest ever used. Sort on
`id`, not on a content field: a tombstone has none. With `includeDeleted`,
`total` counts tombstones too. The flag is snapshot-only
(`400 request.invalid_field` on `/subscribe`) and per-object only
(`400 request.unknown_field` on `objects/query` — a deleted object is
purged wholesale, there is no tombstone row).

Live streams never carry a tombstone row: on `/subscribe` a deletion of
either kind is `removed: [{id, reason: "deleted"}]` (`docs/04-events.md`
§ Contract that clients must respect). The stream says *what just went
away*; the tombstone-inclusive snapshot says *which ids were ever used*.

A `projection` shapes tombstones like any other row, and `_deletedAt` rides
along under one (it is a protocol field, § Projection), so a projected
tombstone-inclusive read can still tell the two apart.

## Sort

`sort` is an array of dotted field paths; prefix with `-` for descending.
Multi-key sorts apply left to right:
`["<wikiCollectionId>.<parentIdPropId>", "<wikiCollectionId>.<posPropId>"]`
(the wiki tree's columns, `03-api.md` § The wiki tree).

## Paths

On the wire, property paths are `<ownerId>.<propId>` — both content ids.
The owner is the object's **type** or one of its **collections**: a value
lives in the namespace of the surface that declares the property, and an
object holds one namespace per surface it carries. Alongside them are the
built-in paths `any.type`, `any.collections`, `any.name`,
`any.description`, `any.tags`, `_ver.id`, and the row-root derived stamps
`author`, `createdAt`, `modifiedAt`, `modifiedBy`, `spaceId` (`objects`
storage collection only — `03-api.md` § Data plane). `{"sort":
["-modifiedAt"]}` is the recency ordering; `modifiedBy` — the signer of
that same change — is unindexed, so a filter on it scans. The wiki tree is
no exception: the wiki collection's `parentId` / `pos` / `folder` are
`<wikiCollectionId>.<propId>` paths, the ids resolved from
`POST /v1/catalog/wiki/setup` (`03-api.md` § The wiki tree).

Client helpers may expose dotted **xKey** paths instead (`"recipe.tags"`,
`"movie.title"`) — the owner's `xKey` (from `GET …/types` or
`GET …/collections`; built-in ones use their id) plus the property's `xKey`
(from `GET …/types/:typeId/properties` or
`GET …/collections/:collectionId/properties`) — and resolve them to
`<ownerId>.<propId>` before sending. The server never sees an xKey path:
keying a filter by xKey matches nothing.

## Paging

- **offset / limit** is simple but floats: row 50 becomes row 51 the moment
  a record lands ahead of it. Fine for one-shot reads of stable data.
- **cursor** paging is stable for mutable storage collections: filter on an
  indexed, monotonic field. Chat pages backward with
  `{ "_ver.id": { "$lt": "<oldestSeen>" } }` sorted `["-_ver.id"]` —
  `docs/08-clients.md` § 4.

## Indexes & cost

Indexed paths:

- `objects` → `any.type` (dense), `any.collections` (sparse), `modifiedAt`
- `editor_blocks` (and every editor storage collection) → `(nav.parentId, nav.pos)`
- `chat_messages` → `(_ver.id)`, plus sparse `(unread, _ver.id)`,
  `(unreadMention, _ver.id)`, `(unreadReactions, _ver.id)` and multikey
  `(mentions, _ver.id)`
- `dataviews` → `(pos)`; `views` → `(dataview, pos)`, `(pos)`

Property values on the `objects` storage collection have **no indexes**:
filtering or sorting on an `<ownerId>.<propId>` path is a scan proportional
to the space's object count — scope by `any.type` or `any.collections` so
the index narrows the scan first. Runtime datasets carry no secondary
indexes, and there is no create-index API.
