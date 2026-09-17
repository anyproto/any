# The object model for clients — types, collections, properties

What a client needs to know to render a space and write into it, in the
order it needs it. Governing detail lives in the linked docs; this page
is the model plus the calls that follow from it.

## The model in one page

### Terms

- A **type** is what an object **is**. One per object, in `any.type`. It
  carries the layout, the parts and a set of property definitions.
- A **collection** is what an object is **filed under**. Any number per
  object, in `any.collections`. It carries property definitions and
  nothing else — no layout, no parts.
- A **storage collection** is where a dataset's records live: the
  `collection` field on a dataset definition, and the `dataset` value on
  reads and writes — `editor_blocks`, `chat_messages`, `<typeId>_<key>`.
  A place in the store, not something an object is filed under.

A **space** holds objects. An **object** is a row in the space's
`objects` storage collection: an id, its one type, the collections it is
filed under, and one property-value group per **owner** — the type or a
collection — under `<ownerId>.<propId>`.

A **part** is a display unit a client renders, and parts belong to types
only. Each part owns datasets served by a **module** — `records` (a
declared schema), `editor` (document blocks), `chat` (messages). Content
lives in those datasets, one record per block or message, not in the
object row.

    object ──is a─────────▶ type ──has──▶ part ──owns──▶ dataset (module)
       └──filed under──▶ collection

    type and collections both define properties; the object holds one
    value group per owner.

A **usecase** is a set of well-known bundles the server installs on
request. A **bundle** is one root object under a permanent id
(`system:person/v1`) that can be the sidebar entry, a definition and the
host of its own records — up to all three at once (§ Reading data).
Installing is idempotent and convergent: every device that asks lands on
the same root and the same property ids.

Three ids, never interchangeable:

| id | what it is | where it comes from |
|---|---|---|
| `typeId` / `collectionId` | content-addressed definition id | `GET …/types`, `GET …/collections`, catalog setup |
| `xKey` | the definition's or property's stable handle (`person`, `email`) | declared; resolved client-side |
| `propId` | content-addressed property id — **the write key** | `GET …/types/:typeId/properties`, `GET …/collections/:collectionId/properties`, catalog setup |

**Writes key by `propId`.** `xKey` is the label you resolve it by; it
never reaches storage. Keying a value by `xKey` is `property.not_found`.

## Startup: what a client does when it opens a space

1. `GET /v1/spaces/:spaceId/bundles` — the installed registry. It answers
   after the convergence wait and carries `synced`; `synced: true` plus no
   row means definitively not installed.
2. `GET /v1/spaces/:spaceId/types?includeHidden=true` and
   `GET /v1/spaces/:spaceId/collections?includeHidden=true` — every
   definition, with `xKey`, the `hidden` / `builtIn` flags, and `layout`
   on the types. The flags are omitted when false, so read an absent key
   as false.
3. `POST /v1/spaces/:spaceId/objects/query/subscribe` with
   `{"filter": {"$and": [{"any.collections": "miniapp"},
   {"any.collections": {"$nin": ["bin"]}}, {"miniapp.hidden": {"$ne": true}}]},
   "sort": ["miniapp.pos"]}` — the sidebar. It lists exactly the members
   of the `miniapp` collection: a row with `miniapp.bundle` is an
   installed app (the wiki, the general chat, contacts, …) and the bundle
   id says what to run; a row without one is an object the user pinned,
   rendered as the object it is. An app root that also declares a type or
   a collection is a member like any other — its definition marker sits
   in `any.type`, not in `any.collections`. An empty sidebar is a valid
   state (a joined space, a space another client made): render it and
   offer the catalog.

Do not install anything on open. The client that **creates** a space
sets its default usecases up right after `POST /v1/spaces`; every other
usecase is set up when the user asks for the feature (§ Usecases and
the catalog).

## Usecases and the catalog

`GET /v1/catalog` lists the usecases; `POST /v1/catalog/:usecaseId/setup`
with `{"spaceId": …}` installs one. Setup resolves the usecase's
`requires` closure in dependency order and adopt-or-installs each bundle:

    POST /v1/catalog/crm/setup
    → people (person, organization) → contact → contacts → crm

The reply lists every bundle with `installed` (`true` = this call created
the root, `false` = adopted an existing one), the `typeId` or the
`collectionId` — whichever kind the bundle declares — and `properties`,
the **xKey → propId map**, which is what you cache. Setup is idempotent:
run it again and everything adopts. Run it on every device that needs the
feature; a second device adopts the first device's roots with
byte-identical property ids.

Evolution is additive. A catalog release that adds a property, a choice
option, a miniapp value or a bundle heals it onto an existing install on
the next setup; it never removes or renames what a space already has.

**Anything your product ships belongs in the catalog, types and
collections included.** A well-known app declares the miniapp root AND
the types, collections, properties and datasets its content uses, so the
journal on a phone and the journal on a desktop are the same type with
the same ids, and an ingest agent writes where the reader reads. Minting
the definition client-side races on `409 type.xkey_conflict` and can end
with one device's entries invisible on the other — and a definition
cannot be deleted.

Full contract: [`28-well-known-bundles.md`](28-well-known-bundles.md).
Clients register their own bundles the same way through
`POST /v1/spaces/:spaceId/bundles` — favourites is the worked example
([`25-favorites.md`](25-favorites.md)).

## Types and collections

### Built-ins

`page` and `dataview` are registered **types**; `miniapp` and `bin` are
registered **collections**. All four resolve by their literal id and are
`hidden` — absent from the default listing unless you pass
`?includeHidden=true`. An object opts into them; nothing stamps them.

- **`page`** (type) — one part `body` owning the shared `editor_blocks`
  storage collection. Set it to make an object a document.
- **`dataview`** (type) — saved views. A dataview is its own object of
  this type, pointed at the host its views are over
  ([`24-data-views.md`](24-data-views.md)).
- **`miniapp`** (collection) — the sidebar: `bundle` (the installed
  app's id, absent on a pinned object), `pos` (client-allocated lexid
  order), `hidden` (out of the sidebar, install kept). Pin = file the
  object under the collection, unpin = remove it.
- **`bin`** (collection) — `movedAt` + `movedBy`. Move and restore are
  `POST …/properties/:objectId/collections/bin` and the matching
  `DELETE`; one change carries the membership and both stamps, so a row
  is never half-marked.

`GET …/types` also lists the meta rows `any`, `spaceIndex`, `type` and
`collection`: the universal property group, the space-metadata type, and
the two meta-types whose namespaces hold a definition's own metadata
(`type.layout`, `collection.xkey`). None of them is a type a user puts on
an object — never offer them in a type picker.

### Rendering an object

**The layout and the parts come from the object's one type.** Render that
type's `layout` with that type's parts, ordered by `pos`.

**The property groups come from the type AND from every collection the
object is filed under** — one group per owner, each rendered from that
owner's property definitions, alongside the universal `any` group. A
person filed under Reading list shows the person columns and the
reading-list columns on the same object; rendering only the type's group
is the common mistake, and the collection's columns then disappear from
the UI.

Every object has a type — `POST …/objects` refuses a body without one
and the type is never cleared — so there is always a layout to render.
`page` is the plain document.

`layout` is opaque to the server — `{"type": "<slug>", "config": {…}}` in
the descriptor shape, your vocabulary. Collections carry none: filing an
object changes its columns, never how it renders.

### Pickers

A **type picker** reads `GET /v1/spaces/:spaceId/types` — hidden types
are out by default, and the meta rows above are skipped. A **collection
picker** reads `GET /v1/spaces/:spaceId/collections`, same rule. The type
picker is single-select and the collection picker is multi-select,
because that is what a row holds.

| action | call |
|---|---|
| set the type (replaces the current one) | `POST …/properties/:objectId/type/:typeId` |
| file under a collection | `POST …/properties/:objectId/collections/:collectionId` |
| unfile | `DELETE …/properties/:objectId/collections/:collectionId` |

All four answer `ModifyResult`. The two POSTs pre-flight the id and refuse
the wrong slot: a collection id in the type route is
`400 type.not_a_type`, a type id in the collection route is
`400 collection.not_a_collection`, an unknown definition is
`404 type.not_found` / `404 collection.not_found`, and an unknown object
is `404 object.not_found`. Filing is idempotent. Neither `DELETE`
pre-flights anything, deliberately: it is the repair path for a bogus id
already on the row.

Retyping is not a delete. The old type's values stay on the row as orphan
data, read-tolerant, and so do its datasets' records — which its parts no
longer admit writes to. Unfiling likewise leaves that collection's values
in place.

### Creating a type vs a collection

Create a **type** when the thing needs a layout, parts or a body of its
own — Person, Task, Journal entry. Create a **collection** when it is a
facet or a label that only adds columns to objects that keep their own
type — Reading list, Q3 launch, Archive.

    POST /v1/spaces/:spaceId/types
      {name?, description?, iconCid?, xKey, layout?, hidden?, meta?}
      → 201 {"typeId": "…"}

    POST /v1/spaces/:spaceId/collections
      {name?, description?, iconCid?, xKey, hidden?, meta?}
      → 201 {"collectionId": "…"}

Both require a non-empty `xKey` (slug the name; it must survive renames)
— without one it is `400 type.xkey_required`. The handle is unique across
types **and** collections in the space: a collision with an existing
`xKey` or id is `409 type.xkey_conflict`, whose `details` name
`existingTypeId` or `existingCollectionId`. Neither create takes inline
property definitions (`400 request.unknown_field`) — add each column
afterwards through `POST …/types/:typeId/properties` or
`POST …/collections/:collectionId/properties`.

Built-ins are registered, not created here, and a write on one is
`400 type.registered` / `400 collection.registered`.

Mint a definition here only for what a USER creates in their own space. A
type or collection your app depends on goes in the catalog instead
(§ Usecases and the catalog) — that is what makes two clients converge on
one `person` instead of two.

## Properties

A property definition is `{name, description?, xKey, kind, scope?,
xFormat?, meta?}` — the same body, the same patch grammar and the same
ids on a type and on a collection; only the owner segment of the route
differs. `kind` is required and **pinned on first write** — it
is the storage contract (`string`, `array`, `number`, `boolean`,
`datetime`, `object`). `xFormat` is the descriptor: an opaque bag whose
interpreted keys are `type`, `icon`, `pos`, `options`, `relation`,
`config`, `links` ([`27-descriptors.md`](27-descriptors.md)). A reference
property is `kind: array` with `xFormat.type: "relation"` and values
`["any://<objectId>", …]` — that slug is also what puts its values into
the backlinks index; a property with neither that slug nor an
`xFormat.links` marker indexes no links until it is patched with
`{"set": {"xFormat.type": "relation"}}`.

### Value shapes that bite

[`27-descriptors.md`](27-descriptors.md) governs this table — check it there
before relying on a slug not listed.

| descriptor `type` | kind | value |
|---|---|---|
| `text` / `url` / `email` / `phone` | `string` | a string |
| `date` / `datetime` | `datetime` | `{"$date": "2026-08-05T17:00:00.000Z"}` — `date` must land on midnight UTC |
| `relation` | `array` | `["any://<objectId>"]` — **a bare object id is rejected** |
| `choice` | `array` | **always an array** of option **keys**, not names — a single choice is `["qualified"]`; `config.multiple` controls arity, not kind |
| `number` / `currency` / `percent` | `number` | a number |
| `checkbox` | `boolean` | a bool |

`xFormat.relation.targetTypes` holds **xKeys**, not ids — resolve a
catalog xKey through the bundle registry and a user definition's xKey
against the space's type and collection lists before rendering a picker.

Built-in fields render through the same table: `any.name`,
`chat_messages.createdAt`, a dataview's `name` all come back from
discovery (`description` / `x-format` on the field node) and
`GET …/types/:id/properties` (`description` / `xFormat`) with a
description and, where a slug fits, a descriptor. Where none fits
(markdown text bodies, identities, record ids) the `description` says
what the value is — hardcode nothing a descriptor already tells you.

### Editing a definition

`PATCH …/types/:typeId/properties/:propId` — and its
`…/collections/:collectionId/properties/:propId` twin — takes `{set,
unset}` of dotted paths. One endpoint covers rename and the whole option
lifecycle, because options are leaves under
`xFormat.options.<key>.{name,color,pos}`:

    {"set": {"name": "Labels",
             "xFormat.options.vip.name": "VIP",
             "xFormat.options.vip.pos": "a0"}}

`kind`, `scope`, `items` and `properties` are pinned → `400
property.immutable`. `xFormat.type` is **not** pinned — the slug moves within
what `kind` already allows (`text`→`url`, `number`→`currency`), which is what
bounds the damage ([`27-descriptors.md`](27-descriptors.md) § `type` is
mutable). Option delete is a hard `$unset` and is dangling-tolerant: values
keep the orphan key, so renaming an option is one write and zero object
writes.

## Reading data

`POST /v1/spaces/:spaceId/objects/query` for object rows,
`POST /v1/spaces/:spaceId/query` (with `objectId` + `dataset`) for a
dataset's records. Both take `filter` / `sort` / `limit` / `offset` /
`projection`, and both have a `…/subscribe` twin that streams a snapshot
then live deltas. On a subscribe, `limit` requires `sort` — a live
window has to be ordered. Property paths are `"<ownerId>.<propId>"`,
where the owner is the object's type or one of its collections.

Filter by type with plain equality — `{"any.type": "<typeId>"}`, `$in`
for a set. Filter by collection with membership —
`{"any.collections": "<collectionId>"}` matches any element of the array,
`$nin` excludes, `$all` demands several at once.

### One object, three roles — and what it means for filters

A bundle or app root is a single object, and that one object can be three
things at once:

1. the **sidebar entry** — filed under the `miniapp` collection, with
   `miniapp.bundle` naming the install;
2. a **definition** — a type or a collection. Its row carries the marker
   `__type__` or `__collection__` in `any.type`, and its id is the
   `typeId` / `collectionId` every other object uses;
3. the **host of its own data** — it holds `<rootId>.<propId>` values and
   the records of its own datasets.

All three at once, and with no flag: **a definition hosts itself.** The
contacts root keeps its layouts on itself, the general chat keeps its
messages on itself, a favourites root keeps its entries on itself, and
none of them declares anything to say so — nor is the root a member of
itself.

The marker is what keeps filters simple. It sits in `any.type`, so a
definition row never matches `{"any.type": "<rootId>"}` or
`{"any.collections": "<rootId>"}`: its own id is in neither slot.
**Member queries need no marker exclusion.**

```json
{"$and": [{"any.type": "<typeId>"},
          {"any.collections": {"$nin": ["bin"]}}]}
```

The `bin` clause is the one exclusion an ordinary list still needs: a
binned object keeps its type and its collections (§ Content surfaces).

### Timestamps

Record stamps are ext-JSON instants in both directions:
`{"$date": "…Z"}` (writes also accept `{"$date": <millis>}`). Filter
literals must be wrapped the same way — a bare number or ISO string does
not error, it silently matches nothing, because comparisons are bracketed
by type. `createdAt` / `modifiedAt` / `modifiedBy` on the object row are
server-derived; sort recency with `{"sort": ["-modifiedAt"]}`.

The mapped convenience structs are the exception: `SpaceInfo.createdAt`
is an RFC3339 string and `HistoryChange.timestamp` is unix seconds. Both
are display-only.

Query grammar and index guidance: [`09-query.md`](09-query.md).

## Writing data

**Object create** takes exactly three keys — any other top-level key is
`400 request.unknown_field`, and a wrong shape (`type` not a string,
`collections` not an array, `initialProperties` not an object) is
`400 request.schema`:

```json
{ "type": "<typeId>",
  "collections": ["<collectionId>"],
  "initialProperties": { "any": {"name": "Ada Lovelace"},
                         "<typeId>": {"<propId>": "ada@example.com"},
                         "<collectionId>": {"<propId>": "a0"} } }
```

`initialProperties` is keyed by **any owner the object will have** — its
type or any of its collections.

Later property writes go through
`POST …/properties/:objectId/set/:ownerId`; arbitrary record writes
through `POST /v1/spaces/:spaceId/modify`; idempotent batch ingest into an
`idRule: user` dataset through `POST /v1/spaces/:spaceId/upsert`.

**No property write creates its own owner.** A value under `<ownerId>` is
admitted only while the object has that type or that collection, and a
dataset lives on an object only while its type declares the part that
owns it; a write without either is `400 dataset.not_declared`. Set the
type and file the collections deliberately — at create via `type` /
`collections`, or through the routes in § Pickers.

Record writes (modify, property set, the chat and block endpoints) return
`{versionId, changeId, recordIds}`, not the body — read it back through
query. Upsert returns `{pages: [{versionId, changeId, recordIds}],
created, updated, skipped, rejections?}`. Object and definition creation
answer with the new id under a per-kind key (`objectId`, `typeId`,
`collectionId`).

## Content surfaces

| surface | how |
|---|---|
| **Document** | set a type with an editor part — `page` is the built-in one. Blocks: `…/objects/:o/editor/:collection/blocks`; whole body: `GET/PUT/PATCH …/editor/:collection/markdown`. Read with `dataset` set to **the same `:collection`** — the storage collection — sorted on `nav.pos`: `editor_blocks` for the shared part `page` uses, `<typeId>_<key>` for a type that declares its own namespaced editor part. |
| **Chat** | one per space, the `general-chat` usecase; its root is filed under `miniapp`, so it sits in the sidebar with the other apps. Writes: `…/objects/:chatRoot/chat/messages`. Read via `dataset=chat_messages` sorted on `_ver.id`. |
| **Wiki tree** | the `wiki` usecase's three properties on objects of its type: `parentId` (`""` = top level), `pos` (lexid), `folder`. Children = objects query filtered on the parent property, sorted on the pos property. The server allocates no positions. |
| **Saved views** | one `dataview` object per host, holding `dataviews` (tables on the host) and `views` (views of a table). |
| **Favourites** | the client-registered `favorites/v1` bundle on the tech space. |
| **Bin** | file the object under the `bin` collection, unfile to restore. |
| **Links panel** | what links here and what this links to: `GET …/objects/:o/backlinks` (`{object, parts}`), `GET …/objects/:o/links`, account-wide `GET /v1/backlinks?target=`; refresh on the device bus event `links.updated`. Edges come from editor blocks, chat messages and every property or field whose descriptor carries a link marker (`relation` / `markdown` slug, or `xFormat.links`). Recipe: [`08-clients.md`](08-clients.md) § 15; wire shape: [`03-api.md`](03-api.md) § Links and backlinks. |

The markdown bridge round-trips exactly: re-PUTting what GET returned
writes nothing. Import normalises (a tight list becomes one record per
item), so diff against a GET, never against your source text.

### Chat is reserved

The `chat` module is the server's. A client part, part dataset or bundle
naming it is `400 dataset.module_reserved`, and the general-chat type is
the type of its own root and nothing else — setting it on another object,
or creating an object with it, is `400 type.reserved_carrier`. A space has
exactly one chat, and `POST /v1/catalog/general-chat/setup` returns it.
This holds for 1-1 spaces too: the root is derived, so both participants
compute the same id offline and no install can fork.

## Error and shape conventions

- Every non-2xx is `{"error": {"code", "message", "details?"}}`.
  Unknown body fields are rejected and the message names the accepted
  set — read it, it is usually the whole fix. Unknown **query**
  parameters are ignored.
- Create replies use a per-kind key (`objectId`, `typeId`,
  `collectionId`, `propId`, `partId`, `fileId`); list replies use `id`.
- File **attach** and the payload-row query are object-scoped
  (`/v1/spaces/:s/objects/:o/files[/query]`); every other file read,
  download, status and delete is space-scoped
  (`/v1/spaces/:s/files/:fileId/...`).
- A derived object (any bundle root installed with `derived: true`, such
  as the general chat) is permanent: `DELETE …/objects/:id` answers
  `409 object.derived_undeletable`.
- A saved view's stored `query` carries a `type` discriminator. Send
  `query.filter` and `query.sort` to the query endpoints, never `query`
  itself.

Codes: [`06-errors.md`](06-errors.md). Endpoint catalogue:
[`03-api.md`](03-api.md). Call patterns and paging:
[`08-clients.md`](08-clients.md).
