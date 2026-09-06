---
title: Types and properties
description: Define types with a stable xKey, add property definitions with kinds, descriptors, choice options and scopes, and patch them in place.
order: 30
---
# Types and properties

A type is a named schema an object can carry. It declares property definitions — each with a kind, an optional descriptor, and a sync scope — and it is the namespace under which the object stores those values.

## Create a type

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types \
  -H 'Content-Type: application/json' \
  -d '{"name": "Book", "xKey": "book", "description": "things I read"}'
# → 201 {"typeId": "bafy…"}
any type create $SPACE --name Book --xkey book
```

Body: `{name?, description?, iconCid?, xKey}`. **`xKey` is required** — it is the stable programmatic handle a type resolves by; the display `name` is not a resolution key and may be renamed freely. Clients derive it as a slug of the name. An empty xKey is `400 type.xkey_required`; one that collides with another type's xKey *or id* in the same space is `409 type.xkey_conflict` (`details: {xKey, existingTypeId}`).

Property definitions are not part of the create body. Create the type, then add properties one by one.

## List and read types

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/types
any type list $SPACE
```

Each row is `{id, name, description?, iconCid?, xKey, builtIn, weight?, layout?, hidden?, meta?}`. The list starts with three synthetic built-ins — `any` (the universal type: `any.name`, `any.description`, `any.icon`, `any.tags`, `any.types`), `spaceIndex` and `type` (the meta-type: `xkey`, `weight`, `layout`, `hidden`, `meta`) — then every registered built-in (`nav`, and the hidden `dataview` / `page` / `miniapp` / `bin` — capability types an object opts into), then user types; hidden types appear only with `includeHidden=true`. Built-ins report `builtIn: true` with `xKey` equal to their id, which reserves those ids against user types. The three synthetic ids cannot be attached to an object; a "filter by type" UI skips them.

`GET …/types/:typeId` and `GET …/types/:typeId/properties` answer `404 type.not_found` for an unknown id. A `200 []` from the properties list always means "exists, no properties yet".

### Parts, weight and layout

A type is more than its columns. Its **parts** are the display units a client renders for an object of the type — a body, a transcript, a task list — each owning datasets a module serves (`POST …/types/:typeId/parts`; see [Modules](../types/index.html) and [Runtime datasets](runtime-datasets.html)). Its **weight** decides which of an object's types is primary (the highest wins) and its **layout** is the descriptor a client renders for that primary type; both are set at create or through `PATCH …/types/:typeId` (`{name?, description?, iconCid?, weight?, layout?}`). A document type is the built-in `page` (hidden, one editor part, no properties) or a user type with an editor part, registered as a bundle so every device agrees on one. See [Page](../types/page.html). `bin` is move-to-bin: `POST …/properties/:objectId/attach/bin` stamps `bin.movedAt` / `bin.movedBy`, `detach/bin` restores and clears them; lists exclude carriers with `{"any.types": {"$nin": ["bin"]}}`.

Two more flags live on the type: `hidden` keeps it out of `GET …/types` (pass `includeHidden=true` to see it; `GET …/types/:typeId` always resolves it) — a bundle's self-typed root is hidden by construction — and `meta` is an open bag of consumer flags, one string, bool or number per key, patched per key through `PATCH …/types/:typeId` (`null` unsets) so devices touching different keys merge. The server interprets none of the keys.

## Add a property

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Author", "xKey": "author", "kind": "string"}'
# → 201 {"propId": "bafy…"}
any type property add $SPACE $TYPE --name Author --xkey author --kind string
```

| Field | Meaning |
|-------|---------|
| `name`, `description` | Display metadata; mutable. |
| `xKey` | The property's handle — an alias, unique within the type (`409 property.xkey_conflict`), mutable. **The server never keys values by it** — values are stored and written by `propId`. |
| `kind` | `string` / `number` / `boolean` / `array` / `object` / `datetime`. Required and pinned at first write. |
| `xFormat` | The descriptor — slug, icon, order, options, relation targets, config; see [Data types](data-types.html). Every path under it is mutable. |
| `scope` | `synced` (default), `account`, or `local`. Pinned. `derived` is reserved for built-ins. |
| `meta` | `meta.index` only: the search scope, or `"none"`. |

`GET …/types/:typeId/properties` returns `{properties: [{id, name, description?, xKey, kind, scope, meta?, xFormat?}]}` — this is where a client resolves `xKey → propId` before writing. Sort a property list by `xFormat.pos`, then `id`.

> **Why it matters.** Definitions are synced records. A rename, a reorder via `xFormat.pos` or a new choice option written on one device converges on every member's device through the same CRDT as the data, with no schema-migration step.

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

`PATCH …/types/:typeId/properties/:propId` is a generic per-path `{set, unset}`; every op lands in one change and each leaf merges per path.

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

Other failures: unknown path or wrong leaf type → `400 request.invalid_field`; a slug that does not fit the kind, a reserved key or an unparseable `xFormat.relation.filter` → `400 property.format_invalid`; a taken `xKey` → `409 property.xkey_conflict`; a registered built-in type → `400 type.registered`; unknown ids → `404 sdk.not_found`. Returns `204`.

## Remove a property

```bash
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties/$PROP   # → 204
any type property remove $SPACE $TYPE $PROP
```

Tombstones the definition. Existing values are not cleaned up; later writes to the removed id are dropped op by op.

## Where values live

Values sit at `record[typeId][propId]` on the object's row — `{"<typeId>": {"<propId>": value}}`. On the wire and in filters the path is `<typeId>.<propId>`. Keying a write by `xKey` fails with `property.not_found`; resolve it to the `propId` first. See [Writing data](writing-data.html).

## Related

- [Data types](data-types.html) — kinds, the descriptor vocabulary, instants, scopes.
- [Runtime datasets](runtime-datasets.html) — declaring dataset schemas on a type.
- [Property lifecycle](property-lifecycle.html) — pins, patches, removal and read tolerance in depth.
- [Data model](data-model.html) — how types and datasets compose on one object.
