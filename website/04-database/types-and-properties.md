---
title: Types and properties
description: Every object has exactly one type — its layout, its parts and a property namespace; define types and collections with a stable xKey and patch their property definitions in place.
order: 30
---
# Types and properties

A type is what an object **is**: the layout a client renders, the parts that hold its content, and a namespace of property definitions. Every object has exactly one, in `any.type`.

A **collection** is what an object is **filed under**: property definitions and nothing else. Both are owners of property definitions, so everything below about defining, patching and removing a property holds verbatim for a collection's columns — only the owner segment of the route differs. See [Collections](collections.html).

## Create a type

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types \
  -H 'Content-Type: application/json' \
  -d '{"name": "Book", "xKey": "book", "description": "things I read"}'
# → 201 {"typeId": "bafy…"}
any type create $SPACE --name Book --xkey book
```

Body: `{name?, description?, iconCid?, xKey, layout?, hidden?, meta?}`. **`xKey` is required** — it is the stable programmatic handle a type resolves by; the display `name` is not a resolution key and may be renamed freely. Clients derive it as a slug of the name. An empty xKey is `400 type.xkey_required`; one that collides with an existing **type's or collection's** xKey *or id* in the same space is `409 type.xkey_conflict` (`details: {xKey, existingTypeId}`, or `existingCollectionId` when a collection holds the handle — the two surfaces share one namespace).

Property definitions are not part of the create body — a `properties` key, like any unknown key, is `400 request.unknown_field`. Create the type, then add properties one by one.

Create a type when the thing needs a layout, parts or a body of its own; create a collection when it is a facet that only adds columns to objects keeping their own type ([Collections](collections.html)).

Well-known types — person, organization, meeting, journal, deal — are not created here: the server's usecase catalog installs them (`POST /v1/catalog/:usecaseId/setup`) so every client and member lands on one definition ([Bundles](../collaboration/bundles.html)). A type you create under a handle the catalog later installs blocks that install with `409 type.xkey_conflict`.

## List and read types

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/types
any type list $SPACE
```

Each row is `{id, name?, description?, iconCid?, xKey, builtIn?, layout?, hidden?, meta?}`; flags are omitted when false, so read an absent `builtIn` or `hidden` as false. The list starts with four synthetic built-ins — `any` (the universal group: `any.name`, `any.description`, `any.icon`, `any.tags`, `any.type`, `any.collections`), `spaceIndex`, `type` (the meta-type: `xkey`, `layout`, `hidden`, `meta`) and `collection` (the shape of collection objects) — then every registered built-in type (the hidden `page` and `dataview`), then user types; hidden types appear only with `includeHidden=true`. Built-ins report `builtIn: true` with `xKey` equal to their id, which reserves those ids against user definitions. The four synthetic ids are never set on an object; a type picker skips them.

`GET …/types/:typeId` and `GET …/types/:typeId/properties` answer `404 type.not_found` for an unknown id, and `400 type.not_a_type` when the id names a user collection — read that one through `…/collections/:collectionId`. A built-in collection (`miniapp`, `bin`) has no type row and answers `404 type.not_found`. A `200 []` from the properties list always means "exists, no properties yet".

### Parts and layout

A type is more than its columns. Its **parts** are the display units a client renders for an object of the type — a body, a transcript, a task list — each owning storage collections a module serves (`POST …/types/:typeId/parts`; see [Modules](../types/index.html) and [Runtime datasets](runtime-datasets.html)). Its **layout** is the descriptor a client renders the object with: `{"type": "<slug>", "config": {…}}`, your vocabulary, opaque to the server. Both are the type's alone — a collection has neither.

Because an object has exactly one type, its layout and its parts are that type's, with no primary-type contest to resolve. Set the layout at create or through `PATCH …/types/:typeId` (`{name?, description?, iconCid?, layout?, hidden?, meta?}` — absent keeps, `"layout": null` clears; `400 type.registered` on a built-in).

A document type is the built-in `page` (hidden, one editor part, no properties) or a user type with an editor part, registered as a bundle so every device agrees on one. See [Page](../types/page.html).

Two more flags live on the type: `hidden` keeps it out of `GET …/types` (pass `includeHidden=true` to see it; `GET …/types/:typeId` always resolves it) — a bundle root is hidden when its install asks for it — and `meta` is an open bag of consumer flags, one string, bool or number per key, patched per key through `PATCH …/types/:typeId` (`null` unsets) so devices touching different keys merge. The server interprets none of the keys.

## Add a property

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Author", "xKey": "author", "kind": "string"}'
# → 201 {"propId": "bafy…"}
any type property add $SPACE $TYPE --name Author --xkey author --kind string
```

The same body on `POST …/collections/$COLL/properties` adds a column to a collection.

| Field | Meaning |
|-------|---------|
| `name`, `description` | Display metadata; mutable. |
| `xKey` | The property's handle — an alias, unique within the owning type or collection (`409 property.xkey_conflict`), mutable. **The server never keys values by it** — values are stored and written by `propId`. |
| `kind` | `string` / `number` / `boolean` / `array` / `object` / `datetime`. Required and pinned at first write. |
| `xFormat` | The descriptor — slug, icon, order, options, relation targets, config; see [Data types](data-types.html). Every path under it is mutable. |
| `scope` | `synced` (default), `account`, or `local`. Pinned. `derived` is reserved for built-ins. |
| `meta` | `meta.index` only: the search scope, or `"none"`. |

`GET …/types/:typeId/properties` returns `{properties: [{id, name, description?, xKey, kind, scope, meta?, xFormat?}]}` — this is where a client resolves `xKey → propId` before writing. Sort a property list by `xFormat.pos`, then `id`.

Definitions are synced records. Renaming a property, changing its display order, or adding a choice option propagates to members without rewriting each object's values.

## Choice options

A `choice` property stores an array of option keys — one element unless `xFormat.config.multiple` is on. Options live under `xFormat.options.<key>` as `{name, color, pos, meta?}`, and the key is the value an object stores. Declare them at create or manage them through PATCH, one leaf at a time:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Priority", "xKey": "priority", "kind": "array",
       "xFormat": {"type": "choice"}}'

curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties/$PROP \
  -H 'Content-Type: application/json' \
  -d '{"set": {"xFormat.options.high.name": "High",
              "xFormat.options.high.color": "red",
              "xFormat.options.high.pos": "a0"}}'
any type property option set $SPACE $TYPE $PROP high --name High --color red --pos a0
```

Membership is not enforced on value writes: deleting an option leaves existing values pointing at a dangling key. Because values store the key rather than the label, renaming an option is one definition write and zero object writes.

## Patch a definition

`PATCH …/types/:typeId/properties/:propId` is a generic per-path `{set, unset}`; every op lands in one change and each leaf merges per path. `PATCH …/collections/:collectionId/properties/:propId` is the same call on a collection's column.

```bash
any type property patch $SPACE $TYPE $PROP --set '{"name": "Priority"}'
any type property patch $SPACE $TYPE $PROP --set '{"xFormat.config.multiple": true}'
any type property patch $SPACE $TYPE $PROP --unset xFormat.options.low
```

| Paths | Behaviour |
|-------|-----------|
| `name`, `description`, `xKey`, `meta.index`, and every path under `xFormat` | Mutable. A `set` targets a leaf — a string, number, boolean or array — never an object. |
| `kind`, `scope`, `items`, `properties` | Pinned → `400 property.immutable`. |
| containers (`meta`, `xFormat`, `xFormat.options`, `xFormat.options.<key>`, `xFormat.relation`, `xFormat.config`) | Rejected on `set`; allowed on `unset` (unsetting an option key deletes the option). |

Other failures: unknown path or wrong leaf type → `400 request.invalid_field`; a slug that does not fit the kind, a reserved key or an unparseable `xFormat.relation.filter` → `400 property.format_invalid`; a taken `xKey` → `409 property.xkey_conflict`; a registered built-in → `400 type.registered`; a user collection's id on a `…/types` route (or a user type's id on a `…/collections` one) → `400 type.not_a_type` / `400 collection.not_a_collection`, while a built-in in the wrong slot is `404 type.not_found` / `404 collection.not_found`; unknown ids → `404 sdk.not_found`. Returns `204`.

## Remove a property

```bash
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties/$PROP   # → 204
any type property remove $SPACE $TYPE $PROP
```

Tombstones the definition. Existing values are not cleaned up; later writes to the removed id are dropped op by op.

## Where values live

Values sit at `record[ownerId][propId]` on the object's row — `{"<ownerId>": {"<propId>": value}}` — where the **owner** is the object's type or one of its collections. On the wire and in filters the path is `<ownerId>.<propId>`. An object filed under two collections holds three groups: the type's and one per collection.

No property write creates its own owner: a value is admitted only while the object has that type or that collection. Set the type and file the collections first — at create, or through the routes in [Objects](objects.html). Keying a write by `xKey` fails with `property.not_found`; resolve it to the `propId` first. See [Writing data](writing-data.html).

## Related

- [Collections](collections.html) — the other owner of property definitions, and what filing an object does.
- [Data types](data-types.html) — kinds, the descriptor vocabulary, instants, scopes.
- [Runtime datasets](runtime-datasets.html) — declaring dataset schemas on a type.
- [Property lifecycle](property-lifecycle.html) — pins, patches, removal and read tolerance in depth.
- [Data model](data-model.html) — how a type, its collections and its datasets compose on one object.
