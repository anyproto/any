# The object model for clients — types, usecases, properties

What a client needs to know to render a space and write into it, in the
order it needs it. Governing detail lives in the linked docs; this page
is the model plus the calls that follow from it.

## The model in one page

A **space** holds objects. An **object** is a row in the space's
`objects` collection: an id, the types it carries (`any.types`), and one
property-value group per type it carries.

A **type** is properties plus **parts**. A part is a display unit a
client renders; each part owns datasets served by a **module** —
`records` (a declared schema), `editor` (document blocks), `chat`
(messages). Content lives in those datasets, one record per block or
message, not in the object row.

    object ──carries──▶ type ──has──▶ part ──owns──▶ dataset (module)
      │                  │
      └─ property values └─ property definitions

A **usecase** is a set of well-known bundles the server installs on
request. A **bundle** is one root object under a permanent id
(`system:person/v1`) that declares a type, a miniapp, or both. Installing
is idempotent and convergent: every device that asks lands on the same
root and the same property ids.

Three ids, never interchangeable:

| id | what it is | where it comes from |
|---|---|---|
| `typeId` | content-addressed type id | `GET …/types`, catalog setup |
| `xKey` | the type's or property's stable handle (`person`, `email`) | declared; resolved client-side |
| `propId` | content-addressed property id — **the write key** | `GET …/types/:typeId/properties` |

**Writes key by `propId`.** `xKey` is the label you resolve it by; it
never reaches storage. Keying a value by `xKey` is `property.not_found`.

## Startup: what a client does when it opens a space

1. `GET /v1/spaces/:spaceId/bundles` — the installed registry. It answers
   after the convergence wait and carries `synced`; `synced: true` plus no
   row means definitively not installed.
2. `GET /v1/spaces/:spaceId/types?includeHidden=true` — every type, with
   `xKey`, `weight`, `layout` and the `hidden` / `builtIn` flags. The
   flags are omitted when false, so read an absent key as false.
3. `POST /v1/spaces/:spaceId/objects/query` with
   `{"filter": {"any.types": "miniapp"}}` — the installed apps. Each row
   carries `miniapp.bundle`, the bundle id that installed it. This one
   query does **not** take the `__type__` exclusion below: a miniapp row
   is a bundle root, and the roots that also declare a type (wiki,
   contacts) are exactly the ones the exclusion would drop.

Do not install anything at startup. Set a usecase up when the user asks
for the feature (§ Usecases).

## Usecases and the catalog

`GET /v1/catalog` lists the usecases; `POST /v1/catalog/:usecaseId/setup`
with `{"spaceId": …}` installs one. Setup resolves the usecase's
`requires` closure in dependency order and adopt-or-installs each bundle:

    POST /v1/catalog/crm/setup
    → people (person, organization) → contact → contacts → crm

The reply lists every bundle with `installed` (`true` = this call created
the root, `false` = adopted an existing one), the `typeId`, and
`properties` — the **xKey → propId map**, which is what you cache. Setup
is idempotent: run it again and everything adopts. Run it on every device
that needs the feature; a second device adopts the first device's roots
with byte-identical property ids.

Evolution is additive. A catalog release that adds a property or a choice
option heals it onto an existing install on the next setup; it never
removes or renames what a space already has.

Full contract: [`28-well-known-bundles.md`](28-well-known-bundles.md).
Clients register their own bundles the same way through
`POST /v1/spaces/:spaceId/bundles` — favourites is the worked example
([`25-favorites.md`](25-favorites.md)).

## Types

### Built-in hidden types

`page`, `miniapp`, `bin` and `dataview` are registered in every space,
resolve by their literal id, and are `hidden` — absent from `GET …/types`
unless you pass `?includeHidden=true`. An object opts into them; nothing
stamps them.

- **`page`** — one part `body` owning the shared `editor_blocks`
  collection. Carry it to make an object a document.
- **`miniapp`** — one property `bundle`. Marks an object as an installed
  app's entry point.
- **`bin`** — `movedAt` + `movedBy`. Move and restore are
  `POST …/properties/:objectId/attach/bin` and `…/detach/bin`; one change
  carries the type and both stamps, so a row is never half-marked.
- **`dataview`** — saved views ([`24-data-views.md`](24-data-views.md)).

`any`, `spaceIndex` and `type` also come back from `GET …/types`. They
are the universal property group, the space-metadata type and the
meta-type — never offer them in a type picker.

### Which type a client renders

An object carries several types. The one with the highest `weight` is the
**primary** type. `any` and the built-ins carry no weight and never win;
ties break on type id. So a person who is also a contact (weight 10 vs 5)
renders the person profile.

Render the primary type's `layout` **with the parts of every carried
type**, ordered by `pos` — a shared collection appears once however many
types share it. Rendering only the primary type's parts is the common
mistake: a person object that also carries `page` has a document body,
and it disappears from the UI.

`layout` is opaque to the server — `{"type": "<slug>", "config": {…}}` in
the descriptor shape, your vocabulary.

### Creating a type

`POST /v1/spaces/:spaceId/types` requires a non-empty `xKey` (slug the
name; it must survive renames). A collision with an existing type's
`xKey` **or** id is `409 type.xkey_conflict`. Built-in types are
registered, not created here, and every write on them is
`400 type.registered`.

Prefer a catalog usecase over minting your own type for anything other
clients also model — that is what makes two clients converge on one
`person` instead of two.

## Properties

A property definition is `{name, xKey, kind, xFormat?, meta?}`. `kind` is
required and **pinned on first write** — it is the storage contract
(`string`, `array`, `number`, `boolean`, `datetime`, `object`). `xFormat`
is the descriptor: an opaque bag whose interpreted keys are `type`,
`icon`, `pos`, `options`, `relation`, `config`
([`27-descriptors.md`](27-descriptors.md)).

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

`xFormat.relation.targetTypes` holds **xKeys**, not type ids — resolve
them against the space's type list before rendering a picker.

### Editing a definition

`PATCH …/types/:typeId/properties/:propId` takes `{set, unset}` of dotted
paths. One endpoint covers rename and the whole option lifecycle, because
options are leaves under `xFormat.options.<key>.{name,color,pos}`:

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
window has to be ordered. Property paths are `"<typeId>.<propId>"`.

### The one trap: "all objects of type X" includes the type object

Every bundle root that declares a type **carries that type**
(`any.types: ["__type__", "<rootId>", …]`). So the obvious filter returns
the Person type definition next to the actual people. Always exclude the
marker:

```json
{"$and": [{"any.types": "<typeId>"},
          {"any.types": {"$ne": "__type__"}},
          {"any.types": {"$nin": ["bin"]}}]}
```

The third clause is the other half: a binned object keeps its type
membership, so an ordinary list must exclude `bin` carriers too
(§ Content surfaces).

A type created through `POST …/types` carries only `__type__` and does
not self-match — but write the filter this way regardless, so it keeps
working when the type later ships as a bundle.

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

**Object create** takes exactly two keys — any other top-level key is
`400 request.unknown_field`:

```json
{ "types": ["<typeId>"],
  "initialProperties": { "any": {"name": "Ada Lovelace"},
                         "<typeId>": {"<propId>": "ada@example.com"} } }
```

Later property writes go through
`POST …/properties/:objectId/set/:typeId`; arbitrary record writes
through `POST /v1/spaces/:spaceId/modify`; idempotent batch ingest into an
`id: user` dataset through `POST /v1/spaces/:spaceId/upsert`.

**No write attaches a type.** A module collection lives on an object only
while the object carries a type whose part declares it; a write without
one is `400 dataset.not_declared`. Attach deliberately — at create via
`types`, or `POST …/properties/:objectId/attach/:typeId`.

Record writes (modify, upsert, the chat and block endpoints) return
`{versionId, changeId, recordIds}`, not the body — read it back through
query. Object and type creation are the exception: they answer with the
new id under a per-kind key (`objectId`, `typeId`).

## Content surfaces

| surface | how |
|---|---|
| **Document** | carry `page` (or your own type with an editor part). Blocks: `…/objects/:o/editor/:collection/blocks`; whole body: `GET/PUT/PATCH …/editor/:collection/markdown`. Read with `dataset` set to **the same `:collection`**, sorted on `nav.pos` — `editor_blocks` for the shared part `page` uses, `<typeId>_<key>` for a type that declares its own namespaced editor part. |
| **Chat** | one per space, the `general-chat` usecase. Writes: `…/objects/:chatRoot/chat/messages`. Read via `dataset=chat_messages` sorted on `_ver.id`. |
| **Wiki tree** | the `wiki` usecase's three properties on objects that carry it: `parentId` (`""` = top level), `pos` (lexid), `folder`. Children = objects query filtered on the parent property, sorted on the pos property. The server allocates no positions. |
| **Saved views** | the `dataview` type: `dataviews` (tables on a host) and `views` (views of a table). |
| **Favourites** | the client-registered `favorites/v1` bundle on the tech space. |
| **Bin** | attach / detach the `bin` type. |

The markdown bridge round-trips exactly: re-PUTting what GET returned
writes nothing. Import normalises (a tight list becomes one record per
item), so diff against a GET, never against your source text.

### Chat is reserved

The `chat` module is the server's. A client part, part dataset or bundle
naming it is `400 dataset.module_reserved`, and the general-chat type is
carried only by its own root — attaching it elsewhere, or creating an
object that carries it, is `400 type.reserved_carrier`. A space has
exactly one chat, and `POST /v1/catalog/general-chat/setup` returns it.
This holds for 1-1 spaces too: the root is derived, so both participants
compute the same id offline and no install can fork.

## Error and shape conventions

- Every non-2xx is `{"error": {"code", "message", "details?"}}`.
  Unknown body fields are rejected and the message names the accepted
  set — read it, it is usually the whole fix. Unknown **query**
  parameters are ignored.
- Create replies use a per-kind key (`objectId`, `typeId`, `partId`,
  `fileId`); list replies use `id`.
- File **attach** is object-scoped; every file read, download, status and
  delete is space-scoped (`/v1/spaces/:s/files/:fileId/...`).
- A derived object (any bundle root installed with `derived: true`, such
  as the general chat) is permanent: `DELETE …/objects/:id` answers
  `409 object.derived_undeletable`.
- A saved view's stored `query` carries a `type` discriminator. Send
  `query.filter` and `query.sort` to the query endpoints, never `query`
  itself.

Codes: [`06-errors.md`](06-errors.md). Endpoint catalogue:
[`03-api.md`](03-api.md). Call patterns and paging:
[`08-clients.md`](08-clients.md).
