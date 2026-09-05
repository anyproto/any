# Saved views (built-in `dataview` type)

A **view** is a saved way of looking at a set of objects: a name, an
icon, a layout, a filter/sort/groupBy, and column settings. Views give
that a home in the space so a view saved in one client is the same view
in the next — instead of each client keeping its own per-device copy in
browser storage.

Views come in two levels. A **dataview** is a named, ordered table on
the host object — "Tasks", "Reading list" — and every view belongs to
one dataview. An object carries as many dataviews as it needs, each
with its own views, which is what lets a page hold a task board and a
reading list side by side without their views bleeding into each other.

`dataview` is a **registered built-in type**, not a type a client
creates. Two clients (or two devices of one client) that each
check-then-create a "views" type both pass their local check and then
merge, leaving the space with parallel type definitions — the
proliferation a client-registered bundle solves for document types
(`03-api.md` § Bundles). A registered type exists in every space by
construction. It is `hidden`: a capability an object opts into, not a
class a user picks (`GET …/types` lists it only with
`?includeHidden=true`).

## Data model

The type attaches to a **host object** and owns two records datasets
under one part (`views`): `dataviews`, one record per dataview on the
host, and `views`, one record per view. The host is whatever the views
are "of":

| Views of…            | Host object                          |
|----------------------|--------------------------------------|
| a type's objects     | the **type object** (`typeId` is an object id) |
| a document / board   | that object                          |

Attaching to a type object is deliberate — `AttachType` has no
meta-type guard, so "views on a type" needs no special mechanism.

A dataview record:

```json
{
  "id": "default",
  "name": "Tasks",
  "icon": "✅",
  "pos": "a0",
  "creator": "<identity>",
  "createdAt": { "$date": "2026-08-20T17:06:40Z" },
  "modifiedAt": { "$date": "2026-08-20T17:06:40Z" }
}
```

A view record — `dataview` names the dataview it belongs to:

```json
{
  "id": "default",
  "dataview": "default",
  "name": "All",
  "icon": "📋",
  "pos": "a0",
  "layout": "table",
  "query": {
    "type": "plain",
    "filter": { "<typeId>.<propId>": { "$in": ["urgent"] } },
    "sort": ["-modifiedAt"],
    "groupBy": { "propId": "<propId>" }
  },
  "layoutSettings": {
    "visible": ["name", "<propId>"],
    "order":   ["name", "<propId>"],
    "widths":  { "name": 320, "<propId>": 160 }
  },
  "localSettings": { "widths": { "name": 480 } },
  "creator": "<identity>",
  "createdAt": { "$date": "2026-08-20T17:06:40Z" },
  "modifiedAt": { "$date": "2026-08-20T17:06:40Z" }
}
```

`filter` and `sort` are the `/query` body shapes **verbatim**, so a view
feeds straight into `…/objects/query[/subscribe]` with no translation.

### What the server does and does not know

`query`, `layoutSettings` and `localSettings` are **opaque**: the server
checks only that each is a JSON object. Clients own the vocabulary
inside them, including what to do with a rule naming a property that was
since deleted. Validating property references server-side would turn a
deleted property into a *write failure* instead of a rule the client
marks invalid — the view must stay editable precisely when it is broken.

The same rule applies one level up. A view's `dataview` is required but
**not validated** against the `dataviews` collection, and deleting a
dataview does **not** cascade: its views stay as orphans, still
readable through `{"dataview": "<id>"}` and still writable. The client
deletes them with the dataview, or re-parents them with one path write
(`$set dataview`). Making the reference a server rule would let a
dataview deleted on one device turn every view write on another into a
failure.

`query.type` is `"plain"` today. The discriminator exists so an
aggregation-backed view (chart, rollup) can land later without
migrating existing records.

### Field rules

`dataviews`:

| Field | Scope | Rule |
|-------|-------|------|
| `name`, `pos` | synced | required on create |
| `icon` | synced | free to rewrite |
| `creator`, `createdAt`, `modifiedAt` | derived | server-stamped, client writes rejected |

`views`:

| Field | Scope | Rule |
|-------|-------|------|
| `dataview`, `name`, `pos`, `layout` | synced | required on create |
| `icon`, `query`, `layoutSettings` | synced | free to rewrite |
| `localSettings` | **local** | device-only, never synced |
| `creator`, `createdAt`, `modifiedAt` | derived | server-stamped, client writes rejected |

Everything synced is `mutableBy: any` and any writer may delete a record
in either dataset: a shared view is space furniture, and readers/guests
are already fenced by the ACL. Author-only would freeze a departed
member's view forever.

Deleting a record **burns its id permanently** in that dataset — see
[Ensuring the defaults](#ensuring-the-defaults) before relying on a
well-known id.

`pos` is required in both datasets precisely because they are read in
`pos` order: an absent one sorts as `""`, ahead of every positioned
record on every peer.

Read the rules from `GET /v1/spaces/:spaceId/datasets` (`x-scope`,
`x-mutable-by`, `x-stamp`, `x-id`) rather than hardcoding them; the
part and its two collections come back from
`GET …/types/dataview/parts`.

`createdAt` / `modifiedAt` are **instants**, not numbers — they read and
write as `{"$date": "<RFC 3339>"}` (`{"$date": <unix millis>}` for years
outside RFC 3339's range). A numeric decode target silently yields zero,
and a filter literal must carry the same shape or it matches nothing.
Both are the author's clock: sort and display with them, never fence on
them.

### `pos` — order

A client-assigned lexid string, opaque to the server, in both datasets.
Sort with `{"sort": ["pos"]}`; insert between two records by minting a
lexid between their `pos` values. Views are ordered within their
dataview — the documented read `{"filter": {"dataview": "<id>"},
"sort": ["pos"]}` is served by the `(dataview, pos)` index.

### `layoutSettings` vs `localSettings`

Both carry the same vocabulary. The synced one is the shared baseline;
the local one is **this device's override**, and the client renders the
merge (local wins per key). Column-drag autosave belongs in
`localSettings` so dragging a column does not push a change to every
member and every device.

Scope is per top-level field, which is why the override is a sibling
field and not `layoutSettings.widths`.

`localSettings` has no DAG change behind it: it survives restarts but
not a wipe-and-rebuild of local storage. Treat it as a cache of user
preference, not as data.

## Writing and reading

No bespoke endpoints — the record shapes carry no server semantics
worth one.

**Attach the type once** (only needed for an object that already
exists; `POST …/objects` takes a `types` array):

```
POST /v1/spaces/:spaceId/properties/:objectId/attach/dataview
```

**Write** through `POST /v1/spaces/:spaceId/modify` with
`dataset: "dataviews"` or `dataset: "views"`. Ids are
**client-supplied** in both, which is what makes the defaults safe:

```json
{
  "objectId": "<host>",
  "dataset": "dataviews",
  "records": [{
    "id": "default",
    "upsert": true,
    "ops": [{"type": "$set", "path": "", "value": {"name": "Tasks", "pos": "a0"}}]
  }]
}
```

```json
{
  "objectId": "<host>",
  "dataset": "views",
  "records": [{
    "id": "default",
    "upsert": true,
    "ops": [{"type": "$set", "path": "", "value": {"dataview": "default", "name": "All", "layout": "table", "pos": "a0"}}]
  }]
}
```

View ids are one namespace per host, not per dataview: a second
dataview's views need their own ids — the convention is
`<dataviewId>.<key>` (`board.default`), which keeps the deterministic
ensure below working per dataview.

Concurrent creates of the *same* id by *different* members take
arrival-order-dependent creation verdicts; the content still converges
last-write-wins.

#### Ensuring the defaults

Ensure the default dataview and its default view with **fixed ids plus
upsert**, never create-on-open: two devices opening the same object
would otherwise mint two "Tasks" tables with two "All" views.

**A deleted record id is burned permanently.** Ids never reuse, so once
someone deletes the view with id `default`, upserting `default` again
returns **HTTP 200 with a rejection** and creates nothing:

```json
{"versionId": "…", "changeId": "…", "recordIds": ["default"],
 "rejections": [{"recordIndex": 0, "recordId": "default", "opIndex": -1,
                 "reason": "crdt: record is deleted; the id cannot be reused"}]}
```

A client that only checks the status code therefore shows an empty view
list with no error. **Always inspect `rejections`** on an ensure, and
recover by walking a deterministic id sequence — `default`, `default-2`,
`default-3`, … — taking the first id that is not rejected. The same
sequence applies to the dataview (`default`, `default-2`, …) and to the
views under a non-default dataview (`board.default`, `board.default-2`,
…). The sequence is what preserves convergence: every device walks the
same one and lands on the same replacement, instead of each minting a
fresh id and recreating the duplicate problem.

This is the cost of client-supplied ids, and it is why the product rule
"at least one dataview with one view always exists" is a **client**
rule: the server cannot refuse the delete, so the client must not offer
to delete the last remaining one.

Device-local settings go through the same endpoint with
`"scope": "local"` — explicit record id, no upsert (the record must
already exist), no `traceIds`:

```json
{
  "objectId": "<host>", "dataset": "views", "scope": "local",
  "records": [{"id": "default", "ops": [
    {"type": "$set", "path": "localSettings", "value": {"widths": {"name": 480}}}
  ]}]
}
```

**Read** through `POST /v1/spaces/:spaceId/query` (snapshot) or
`…/query/subscribe` (live): the dataview list is `dataset: "dataviews"`,
`sort: ["pos"]`; one dataview's views are `dataset: "views"`,
`filter: {"dataview": "<id>"}`, `sort: ["pos"]`. Both scopes of a view
come back on one record — there is no second read for the local half.

Saved views are **not** search-indexed: a view name is navigation
chrome, not knowledge.

## Building the query

`query.filter` and `query.sort` use the `/query` grammar unchanged —
operators, array semantics and paging are all in `09-query.md`. What
follows is only what is different because the query is **saved and
shared** rather than built fresh per request.

**Key properties by `propId`, never by `xKey`.** The wire path is
`<typeId>.<propId>`, both content-addressed ids. `xKey` is a
client-side convenience that helpers map on the way in — it never
reaches the server, so an xKey path saved in a view resolves for
nobody, including the client that wrote it. Resolve `xKey → propId`
once via `GET …/types/:typeId/properties` and store the propId.
This is also what makes a view survive a property rename: the display
name changes, the propId does not.

**Every view over a type's objects must carry its own type scope.**

```json
{"filter": {"any.types": "<typeId>", "<typeId>.<propId>": {"$in": ["urgent"]}}}
```

The scope is not optional politeness. `objects` is a per-space
collection holding *every* object — type definitions, chat objects,
bundle roots — and the negation operators (`$ne`, `$nin`, `$not`,
`$exists: false`) match field-absent rows, so an unscoped saved filter
like "status is not done" quietly returns the whole space. A filter
built once and replayed for months is exactly where that bites.

**Sort:** dotted paths, `-` prefix for descending, applied
left-to-right. A `/query/subscribe` with `limit > 0` **requires** a
sort — a windowed live view without one is a 400, so a view whose sort
a user cleared still needs a fallback (`["-modifiedAt"]`).

**Dates:** filter literals are instants — `{"createdAt": {"$gte":
{"$date": "2026-01-01T00:00:00Z"}}}`. A bare number or ISO string does
not error, it answers empty: ordering comparisons are bracketed by
type, so a number or string literal never matches an instant.

**Paging:** `limit` / `offset` for a table page. `includeTotal` is
page-bounded — with `limit: 50` you get `total ≤ 50` — so a row count
for the footer needs its own `$count` pipeline, not `includeTotal`.

### Reconciling a stale query

A saved filter outlives the properties it names. Detect it client-side:
diff the propIds the filter references against
`GET …/types/:typeId/properties`. A propId that is gone means the rule
is dangling — **mark the rule invalid and keep the view editable**;
never drop it silently and never block the write. The server will not
help here, deliberately: it treats `query` as opaque so that a deleted
property cannot turn every subsequent write to the view into a failure.

Choice values reference immutable option keys, so the same applies one
level down: an option key missing from the property's current
`xFormat.options` is dangling (options are dangling-tolerant by design —
delete is a hard `$unset` and values keep the orphan key).

## Grouping

`groupBy` is a **render directive**, not a query the server executes:

```json
"groupBy": { "propId": "<propId>" }
```

The server never reads it. Everything below is the client protocol, and
the record shape stays open — extra keys (collapsed set, group order,
show-empty) are yours to add, so treat the single-key shape above as
the minimum rather than the contract.

**The protocol, in one paragraph.** When a client opens a view carrying
a `groupBy`, it first runs a **preflight aggregation** to learn the
property's distinct values and their counts. That answer decides
whether the field is groupable at all — past a column budget, refuse to
group rather than render the result. Only then does it open **one
query/subscribe per group**, and only for the groups actually on
screen. There is no single "grouped query": the server returns rows, and
the grouping is assembled client-side from N windows.

Budget the preflight twice. `groupLimit` (200 above) is the server-side
backstop that turns a runaway grouping into a `400`; the number of
columns a UI can actually show is far smaller, so apply your own cap —
somewhere around a few dozen — and treat exceeding it as "not
groupable" even when the aggregate succeeds. The preflight is one
snapshot request; the cost that matters is the N live windows it
authorises.

**v1 groups by `choice` only.** That is the slug with a bounded, named,
ordered value set — the option catalog gives columns a name, a colour,
an order, and an empty column for an option nothing uses yet. A
free-text or number property has no catalog, so its "columns" would be
whatever values happen to exist; offer grouping on those only once you
have a product answer for how many columns is too many. Dates are out
for a different reason (below).

**1. Ask for the distinct values, with counts.** `/aggregate` is the
only surface that answers "what values does this property take":

```json
POST /v1/spaces/:spaceId/objects/aggregate
{
  "pipeline": [
    {"$match":  {"any.types": "<typeId>", "…": "the view's own filter"}},
    {"$group":  {"_id": "$<typeId>.<propId>", "count": {"$count": {}}}},
    {"$sort":   {"count": -1}}
  ],
  "groupLimit": 200
}
```

`$match` carries the view's own filter, so the counts describe the view
rather than the whole space. Result docs come back with the group key as
**`id`**, not `_id` (see `14-aggregation.md`).

**Always `$unwind` first.** A `choice` value is an array even when the
property holds one value, and `$group` on an array field groups by the
**whole array** — grouping the raw field yields one group per distinct
*combination*: `["urgent","backend"]` and `["urgent"]` land in different
columns and neither counts as "urgent". Unwind to get per-option counts:

```json
"pipeline": [
  {"$match":  {"any.types": "<typeId>"}},
  {"$unwind": "$<typeId>.<propId>"},
  {"$group":  {"_id": "$<typeId>.<propId>", "count": {"$count": {}}}}
]
```

An object with two options (`config.multiple`) is then counted in both
groups — which is what a many-valued board should show, and means the
group counts sum to more than the object count. Say so in the UI rather
than letting the numbers look broken.

**2. Decide whether the property is groupable at all.** Either signal —
more distinct values than your column budget, or a
`400 aggregate.limit_exceeded` when `groupLimit` trips — means "not
groupable". Surface that as a state the user can see and undo (fall
back to the ungrouped list, keep the `groupBy` in the record so the
choice isn't silently lost), rather than rendering hundreds of columns
or opening hundreds of subscriptions.

The **column set comes from the property's option catalog**
(`GET …/types/:typeId/properties`), not from the aggregate: the catalog
is ordered (`xFormat.options.<key>.pos`), named and coloured, and it
gives an option with zero matches its own empty column. Order columns by
the catalog's `pos`, not by count, or columns reshuffle under the user
as data changes. The aggregate then supplies counts and reveals dangling
keys — values pointing at an option that was deleted.

**The "no value" group is separate.** It never appears in the unwound
aggregate — `$unwind` drops documents whose field is missing or empty —
so count that group on its own:

```json
"pipeline": [
  {"$match": {"any.types": "<typeId>", "<typeId>.<propId>": {"$exists": false}}},
  {"$count": "n"}
]
```

Keep the type scope in that `$match`: `$exists: false` matches every
object that simply lacks the field, including type definitions.

**3. Read each group with its own plain query.** One
`…/objects/query/subscribe` window per **visible** group:

```json
{"filter": {"any.types": "<typeId>", "<typeId>.<propId>": "<optionKey>"},
 "sort": ["-modifiedAt"], "limit": 50}
```

Against an array the scalar spelling is **contains**, so an object
carrying that option matches — no `$unwind` on the read path, that stage
exists only for counting. The empty group's window swaps the value
condition for `{"$exists": false}`.

Recompute counts when a change frame arrives. Do not open a window for a
collapsed or off-screen group — one window per visible group is the
budget (`08-clients.md` § 10).

### Keeping the groups fresh

`/aggregate` is snapshot-only — there is no subscribe variant — so a
group that did not exist when the view opened never appears on its own,
and counts drift as soon as anything moves. Two halves, and only one of
them needs polling:

**The column set streams.** A type's property definitions live in the
`properties` dataset on the **type object**, which is an ordinary
per-object dataset:

```json
POST /v1/spaces/:spaceId/query/subscribe
{"objectId": "<typeId>", "dataset": "properties", "sort": ["_ver.id"], "limit": 100}
```

Hold that one stream while a grouped view is open and a new option — or
a rename, a recolour, a reorder — arrives live, so a newly added option
becomes a new empty column with no polling at all. This is the whole
column set for choice grouping.

**Counts do not.** Only the aggregate knows how many rows sit in each
group, and which option keys are dangling. Re-run the preflight:

- on a coalesced timer while the grouped view is **visible** — tens of
  seconds, not seconds; counts are advisory decoration, and each run is
  a full pass over the matching rows;
- immediately when something invalidates it outright: the view's filter
  or `groupBy` changed, the view was reopened, or the catalog stream
  reported a new option;
- opportunistically when a change frame from any group's window shows a
  row whose group property changed — that is the cheap signal that the
  distribution moved.

Stop the timer when the view is hidden or closed. A count that is a
minute stale is invisible to users; a poll loop running behind a
background tab is not.

Grouping on an **open value set** (not offered in v1) has no catalog to
stream, so there the timer is the *only* way a new group is ever
discovered — one more reason v1 stays on `choice`.

### Date grouping is out of this iteration

Grouping by calendar period is **not offered yet**. The date operators
(`$dateTrunc`, `$year`, `$week`) reach through `/aggregate` but are
inert on what `any` stores — system stamps are unix-second numbers,
user date properties are ISO strings, and both return null; there is no
`$toDate` to bridge them. Grouping a raw timestamp gives one group per
distinct second.

Arithmetic bucketing (`{"$round": [{"$divide": ["$modifiedAt", 86400]}, 0]}`)
is reachable, but `$round` is nearest rather than floor, so day
boundaries land at midday — wrong enough to mislead. Real calendar
grouping waits on native datetime values (SYN-136); see
`14-aggregation.md`.

## Scope tiers

Views come in three tiers. **Only the shared tier ships here.**

| Tier | Visible to | Status |
|------|-----------|--------|
| shared | everyone with space access | **live** |
| account-private | this account's devices | needs scoped datasets (SYN-174) |
| device-private | this device | needs scoped datasets + the local sidecar |

The private tiers need scoped **datasets** — records that only exist for
me — not scoped fields. The SDK scopes fields, and its account mirror
covers the `objects` rows only. When they land they are parallel
datasets (`views_account` / `_device`), not a per-record scope
flag: one dataset is one version domain, and mixing DAG, tech-tree and
local-lexid versions in one dataset breaks versionId ordering and
subscribe dedup.

Because ids are namespaced per `(objectId, dataset, recordId)`,
promoting a private view to shared is a dataset move that can preserve
the id, and a link of the form `any://o/<space>/<obj>#view_<viewId>`
resolves across all three tiers.

## Related

- `08-clients.md` § 13 — the client call patterns: ensure, autosave
  debounce, live-surface budget, migration off per-device storage
- `03-api.md` § Types → Built-in `dataview` type, § Properties
- `09-query.md` — the filter/sort grammar a view's `query` embeds
- `14-aggregation.md` — the pipeline surface grouping depends on
