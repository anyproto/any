# Saved views (built-in `data_view` type)

A **view** is a saved way of looking at a set of objects: a name, an
icon, a layout, a filter/sort/groupBy, and column settings. SYN-175
gives views a home in the space so a view saved in one client is the
same view in the next — instead of each client keeping its own
per-device copy in browser storage.

`data_view` is a **registered built-in type**, not a type a client
creates. Two clients (or two devices of one client) that each
check-then-create a "views" type both pass their local check and then
merge, leaving the space with parallel type definitions — the same
proliferation the built-in `page` type solved for documents. A
registered type exists in every space by construction.

## Data model

The type attaches to a **host object** and owns one dataset,
`data_views`, holding one record per view. The host is whatever the
views are "of":

| Views of…            | Host object                          |
|----------------------|--------------------------------------|
| a type's objects     | the **type object** (`typeId` is an object id) |
| a document / board   | that object                          |

Attaching to a type object is deliberate — `AttachType` has no
meta-type guard, so "views on a type" needs no special mechanism.

```json
{
  "id": "default",
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
  "createdAt": 1755700000,
  "modifiedAt": 1755700000
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

`query.type` is `"plain"` today. The discriminator exists so an
aggregation-backed view (chart, rollup) can land later without
migrating existing records.

### Field rules

| Field | Scope | Rule |
|-------|-------|------|
| `name`, `layout` | synced | required on create |
| `icon`, `pos`, `query`, `layoutSettings` | synced | free to rewrite |
| `localSettings` | **local** | device-only, never synced |
| `creator`, `createdAt`, `modifiedAt` | derived | server-stamped, client writes rejected |

Everything synced is `mutableBy: any` and any writer may delete a view:
a shared view is space furniture, and readers/guests are already fenced
by the ACL. Author-only would freeze a departed member's view forever.

Read the rules from `GET /v1/spaces/:spaceId/datasets` (`x-scope`,
`x-mutable-by`, `x-stamp`, `x-id`) rather than hardcoding them.

`createdAt` / `modifiedAt` are unix seconds serialized as JSON **floats**
(`1787334898.0`) — decode them into a floating-point or generic number
type, not an integer one. Both are the author's clock: sort and display
with them, never fence on them.

### `pos` — view order

A client-assigned lexid string, opaque to the server. Sort views with
`{"sort": ["pos"]}`; insert between two views by minting a lexid between
their `pos` values.

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

No bespoke endpoints — the record shape carries no server semantics
worth one.

**Attach the type once** (only needed for an object that already
exists; `POST …/objects` takes a `types` array):

```
POST /v1/spaces/:spaceId/properties/:objectId/attach/data_view
```

**Write** through `POST /v1/spaces/:spaceId/modify` with
`dataset: "data_views"`. Ids are **client-supplied**, which is what
makes the default view safe:

```json
{
  "objectId": "<host>",
  "dataset": "data_views",
  "records": [{
    "id": "default",
    "upsert": true,
    "ops": [{"type": "$set", "path": "", "value": {"name": "All", "layout": "table", "pos": "a0"}}]
  }]
}
```

Ensure the default view with a **fixed id plus upsert**, never
create-on-open: two devices opening the same object would otherwise mint
two "All" views. Concurrent creates of the *same* id by *different*
members take arrival-order-dependent creation verdicts; the content
still converges last-write-wins.

Device-local settings go through the same endpoint with
`"scope": "local"` — explicit record id, no upsert (the record must
already exist), no `traceIds`:

```json
{
  "objectId": "<host>", "dataset": "data_views", "scope": "local",
  "records": [{"id": "default", "ops": [
    {"type": "$set", "path": "localSettings", "value": {"widths": {"name": 480}}}
  ]}]
}
```

**Read** through `POST /v1/spaces/:spaceId/query` (snapshot) or
`…/query/subscribe` (live), `dataset: "data_views"`, `sort: ["pos"]`.
Both scopes come back on one record — there is no second read for the
local half.

Saved views are **not** search-indexed: a view name is navigation
chrome, not knowledge.

## Grouping

Grouping is **client-driven**, in three steps. `groupBy` in the record
is a render directive; it is not a query the server executes.

**1. Ask for the distinct values, with counts.** `/aggregate` is the
only surface that answers "what values does this property take":

```json
POST /v1/spaces/:spaceId/objects/aggregate
{
  "pipeline": [
    {"$match":  {"any.types": "<typeId>"}},
    {"$group":  {"_id": "$<typeId>.<propId>", "count": {"$count": {}}}},
    {"$sort":   {"count": -1}}
  ],
  "groupLimit": 200
}
```

`$match` should carry the view's own filter, so the counts describe the
view rather than the whole space. Result docs come back with the group
key as **`id`**, not `_id` (see `14-aggregation.md`).

**2. Decide whether the property is groupable at all.** Too many
distinct values, or a `400 aggregate.limit_exceeded` on `groupLimit`,
means "not groupable" — surface that instead of rendering hundreds of
columns.

For `select` / `multiselect` the **column set comes from the property's
option catalog** (`GET …/types/:typeId/properties`), not from the
aggregate: the catalog is ordered, named and colored, and it gives an
option with zero matches its own empty column. The aggregate then adds
counts and reveals dangling option keys — values pointing at an option
that was deleted (options are dangling-tolerant by design).

**3. Read each group with its own plain query.** One
`…/objects/query/subscribe` window per **visible** group, filtered to
that value:

```json
{"filter": {"<typeId>.<propId>": "<optionKey>"}, "sort": ["-modifiedAt"], "limit": 50}
```

Recompute counts when a change frame arrives. Do not open a window for a
collapsed or off-screen group — one window per visible group is the
budget.

`/aggregate` is snapshot-only: there is no subscribe variant, so counts
refresh by re-running the pipeline, never by streaming.

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
datasets (`data_views_account` / `_device`), not a per-record scope
flag: one dataset is one version domain, and mixing DAG, tech-tree and
local-lexid versions in one dataset breaks versionId ordering and
subscribe dedup.

Because ids are namespaced per `(objectId, dataset, recordId)`,
promoting a private view to shared is a dataset move that can preserve
the id, and a link of the form `any://o/<space>/<obj>#view_<viewId>`
resolves across all three tiers.

## Related

- `03-api.md` § Types → Built-in `data_view` type, § Properties
- `09-query.md` — the filter/sort grammar a view's `query` embeds
- `14-aggregation.md` — the pipeline surface grouping depends on
