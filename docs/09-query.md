# 09 — Querying (any-store)

The query primitive is the one read path for everything in a space. It is a
thin wrapper over **any-store** (a MongoDB-flavoured embedded document store),
so the filter/sort language is mongo-style. This is power the old anytypeHelper
never exposed — full operator filters, array matching, multi-key sort, and live
windowed subscriptions — so it's worth learning.

There are two endpoints; both POST (the filter/sort body doesn't fit a query
string) and both have a `/subscribe` SSE twin (see `docs/04-events.md`).
When one query isn't enough — counts per group, rollups, tag
distributions — both scopes also take MongoDB-style aggregation
pipelines at the sibling `…/aggregate` endpoints (snapshot-only); see
[`docs/14-aggregation.md`](14-aggregation.md).

| Endpoint | Scope | Reads |
|----------|-------|-------|
| `POST /v1/spaces/:spaceId/objects/query` | **cross-object** | the per-space `objects` collection — one row per object, its computed property values keyed `<typeId>.<propId>` plus `any.*` / `nav.*` |
| `POST /v1/spaces/:spaceId/query` | **per-object** | one of a single object's datasets (`editor_blocks`, `chat_messages`, runtime datasets such as `program_source` / `mini_app`, …); needs `objectId` + `dataset` |

## Request body

```json
{
  "objectId": "<oid>",        // per-object only (required there)
  "dataset":  "<name>",       // per-object only (required there)
  "filter":   { ... },        // mongo-style; omit/empty = match all
  "sort":     ["nav.pos", "-_ver.id"],
  "limit":    50,
  "offset":   0,
  "includeTotal": false,      // see the caveat below
  "projection": { "any": 1, "nav": 1 }
}
```

The body vocabulary is closed (these fields plus the subscribe-only
`mailboxCapacity` / `driftBudgetPercent`).
An unknown key — `"filters"`, say — is `400 request.unknown_field`
naming the accepted set, never a silently unfiltered full-space query.

Snapshot reply: `{ "records": [ ... ], "total": <int|omitted> }`. Records are the
raw stored documents (per-object datasets) or computed property rows
(cross-object). `/subscribe` adds the live frames documented in `docs/04-events.md`.

## Projection

`projection` shapes the records that come back. It is mongo's grammar:
a flat object mapping dotted field paths to `1` (include) or `-1`
(exclude). `true` / `false` / `0` are accepted spellings of the same
two marks.

```json
{"projection": {"any": 1, "nav": 1}}      // nothing but those subtrees
{"projection": {"_ver": -1}}              // every user field, no version map
{"projection": {"nav": 1, "nav.pos": -1}} // all of nav except one leaf
{"projection": {"any.name": 1}}           // one leaf out of a group
```

**Mode is inferred, deepest mark wins.** One or more `1` on a user
field is *include* mode: start from nothing and add. Only exclusions is
*exclude* mode: start from the whole record and carve. Where a path and
one of its ancestors are both marked, the deeper mark decides — that is
what makes "this subtree except one leaf" expressible.

Projection shapes output only. It never changes which records match, or
the order they arrive in; `filter` and `sort` still run over the whole
stored record. And it applies to `/query/subscribe` exactly as it does
to `/query` — snapshot frames, `changes` docs, and the per-field ops
inside them — so a projected subscription cannot widen after the first
update.

### The three rules a client needs

1. **`id` always rides along**, listed or not. It is the record
   identity every windowed cache and every `changes` frame keys on, so
   `{"id": -1}` is `400 request.invalid_field` rather than a footgun.
2. **`_ver` follows the projection automatically.** Never name a `_ver`
   path — narrow the fields you want and the version map narrows with
   them. `{"_ver": -1}` drops it entirely, which is worth doing: even
   narrowed it is around a third of a projected record.
3. **A projected field the record does not have stays absent.** Nothing
   is null-filled, so "not selected" and "not set" never collapse.

### Protocol fields

`_`-prefixed fields sit outside mode inference and carry their own
defaults, so `{"_ver": -1}` alone still means "every user field":

| field | default under a projection | to change it |
|---|---|---|
| `_ver` | included, narrowed to the projection | `{"_ver": -1}` to drop |
| `_addSeq`, `_applySeq` | dropped | `{"_addSeq": 1}` to keep |

The delivery counters are peer-local and SDK-internal — consumers
reason with `versionId` — so a projecting client does not pay for them
by default. **A request with no `projection` at all is unchanged**, byte
for byte, counters included: this is opt-in.

### How `_ver` narrowing stays correct

`_ver` mirrors the record, except that a node may be a bare version
string (collapsed — it applies at and below that point) and an object
node may carry `*`, the version for any sibling not enumerated there.
Narrowing keeps `*` at every level it descends into and copies matched
subtrees verbatim, which buys an exact contract:

> For every path the projection includes, the narrowed `_ver` resolves
> to the same version as the full one.

Paths you *excluded* are outside that contract — a version lookup there
may land on a surviving `*` default instead of its own entry. Read
versions for the fields you asked for, or drop `_ver` and the question
does not arise. Subtrees are never collapsed to their maximum version:
that would over-report a leaf's version, and a client reconciling
optimistic state per field would discard a local edit that is still
newer.

### Divergences from mongo

- **Include and exclude mix.** Mongo rejects a projection carrying
  both; here they compose, deepest mark wins.
- **`id` cannot be excluded** (mongo lets you drop `_id`).
- **Protocol fields are not part of mode inference**, per the table
  above.
- `-1` is accepted as an exclude marker alongside `0` / `false`.

### Bounds

At most 128 entries, at most 8 path segments deep. An empty path, an
empty segment (`"nav..pos"`), the reserved `*` segment, or a value that
is not one of the accepted marks is `400 request.invalid_field`.

### CLI

`--projection` takes the CSV shorthand, `-` prefixing an exclusion the
way `--sort` prefixes a descending key:

```
any query-subscribe SPACE --properties --projection 'any,nav'
any query-subscribe SPACE --properties --projection '-_ver'
```

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

### Dates

Timestamps — the derived `createdAt` / `modifiedAt` stamps and any
property declared with the `date` / `datetime` format — are instants,
written and read as `{"$date": "<RFC 3339>"}`. A filter literal has to
take the same shape:

```json
{ "modifiedAt":        { "$gte": { "$date": "2026-01-01T00:00:00Z" } } }
{ "<typeId>.<propId>": { "$lt":  { "$date": "2026-08-05T00:00:00Z" } } }
```

**A bare number or string does not error — it silently answers empty.**
Ordering comparisons are bracketed by type: a number literal only ever
compares against numbers, a string literal against strings. So
`{"modifiedAt": {"$gte": 1700000000}}` matches no instant at all, and
neither does `$lt`, `$eq` or a bare ISO string. The bracket cuts both
ways, which is the point — a wrapped `$date` filter no longer picks up
a row that still holds a bare epoch number in the same field. Wrap the
literal and the answers are real.

Sorting is chronological (instants are memcmp-orderable and index-keyable),
and `/aggregate` computes on them directly — `$year`, `$dateTrunc`,
`$dateDiff` (docs/14-aggregation.md). A property declared `kind: "string"`
with a date format stays an ISO-8601 string: it compares and sorts
lexicographically, which is chronological for RFC 3339, but every date
operator returns null for it.

### Arrays — the headline

When a property is an **array**, the filter compares against its *elements*:

- a **scalar** value means **contains**: `{ "<t>.tags": "food" }` → every object
  whose `tags` array includes `"food"`.
- **`$in`** means **intersects**: `{ "<t>.tags": { "$in": ["a","b"] } }`.
- **`$all`** means **superset**: `{ "<t>.tags": { "$all": ["a","b"] } }`.

This is how a client does category filtering server-side (categories in a
bare `tags` array; `getObjects("recipe", {filter:{"recipe.tags":{$in:cats}}})`
prunes candidates before they cross the wire — the `typeKey` arg is the type
xKey too, and the dotted filter key uses the xKey).

There is deliberately **no `$contains`** — the scalar spelling above already is
it. Reaching for one gets `400 filter.unknown_operator`, whose message lists the
whole supported set (`details.operator` carries the token you sent).

### Two gotchas

1. **Negation matches field-absent rows.** `$ne`, `$nin`, `$not`, and
   `$exists:false` also match objects that simply don't have the field. On the
   cross-object `objects` collection — which holds *every* object including type
   definitions — `{"<t>.n":{"$ne":2}}` returns piles of unrelated objects.
   Always scope cross-object queries by type
   (`{"any.types":"<typeId>"}`).

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
`_ver.id`, and the row-root derived stamps `author`, `createdAt`,
`modifiedAt`, `modifiedBy`, `spaceId` (objects collection only — see
`03-api.md` § Data plane; `{"sort": ["-modifiedAt"]}` is the recency
ordering, and `modifiedBy` — the signer of that same change — is
unindexed, so a filter on it scans).

Client helpers typically expose dotted **xKey** paths instead
(`"recipe.tags"`, `"movie.title"`) — the *type xKey* (a stable snake_case
slug of the name, returned by type creation as `type.xKey`; builtins use
their id) plus the *property xKey* — and resolve them to the server's
`<typeId>.<propId>` on the way in. The xKey never reaches the server. It
is stable across display-name renames; builtin paths (`any.types`,
`nav.parentId`) pass through unchanged.

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
