---
title: Types and properties
description: Define types with a stable xKey, add property definitions with kinds, formats, select options and scopes, and patch them in place.
order: 30
---
# Types and properties

A type is a named schema an object can carry. It declares property definitions — each with a kind, an optional format, and a sync scope — and it is the namespace under which the object stores those values.

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

Each row is `{id, name, description?, iconCid?, xKey, builtIn}`. The list starts with three synthetic built-ins — `any` (the universal type: `any.name`, `any.description`, `any.icon`, `any.tags`, `any.types`), `spaceIndex` and `type` (the meta-type, one `xkey` property) — then every registered built-in (`chat`, `editor`, `page`, `nav`), then user types. Built-ins report `builtIn: true` with `xKey` equal to their id, which reserves those ids against user types. The three synthetic ids cannot be attached to an object; a "filter by type" UI skips them.

`GET …/types/:typeId` and `GET …/types/:typeId/properties` answer `404 type.not_found` for an unknown id. A `200 []` from the properties list always means "exists, no properties yet".

### The built-in `page` type

`page` is a pure marker for "this object is a document": no dataset, no properties. Name lives on `any.name`, labels on `any.tags`, body on the `editor` type's `editor_blocks` dataset, tree position on `nav.*`. File a document with `{"types": ["page"]}` and list documents with `{"filter": {"any.types": "page"}}`. Registered types' property definitions are frozen (`400 type.registered`), which is why `page` declares none — per-space columns are a user-type concern. See [Page](../types/page.html).

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
| `xKey` | Client-side stable label. **The server never keys values by it** — values are stored and written by `propId`. |
| `kind` | `string` / `number` / `boolean` / `array` / `object` / `datetime`. Pinned at first write. May be omitted when `format` is set. |
| `format` | Value convention beyond the kind — see [Data types](data-types.html). `format.type` is pinned. |
| `scope` | `synced` (default), `account`, or `local`. Pinned. `derived` is reserved for built-ins. |
| `meta` | Opaque string map, stored verbatim. Conventions: `meta.index` (search scope or `"none"`), `meta.pos` (display order lexid), `meta.icon`. |

`GET …/types/:typeId/properties` returns `{properties: [{id, name, xKey, kind, scope, format?, meta?}]}` — this is where a client resolves `xKey → propId` before writing.

> **Why it matters.** Definitions are synced records. A rename, a reorder via `meta.pos` or a new select option written on one device converges on every member's device through the same CRDT as the data, with no schema-migration step.

## Select and multiselect options

A `select` property stores one option key (kind `string`); `multiselect` stores an array of keys. Options live under `format.options.<key>` as `{name, color, pos, meta?}`, and the key is the value an object stores. Create the property with the format, then manage options through PATCH:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Priority", "xKey": "priority", "format": {"type": "select"}}'

curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties/$PROP \
  -H 'Content-Type: application/json' \
  -d '{"set": {"format.options.high.name": "High",
              "format.options.high.color": "red",
              "format.options.high.pos": "a0"}}'
any type property option set $SPACE $TYPE $PROP high --name High --color red --pos a0
```

Membership is not enforced on value writes: deleting an option leaves existing values pointing at a dangling key. Because values store the key rather than the label, renaming an option is one definition write and zero object writes.

## Patch a definition

`PATCH …/types/:typeId/properties/:propId` is a generic per-path `{set, unset}`; every op lands in one change and each leaf merges per path.

```bash
any type property patch $SPACE $TYPE $PROP --set '{"name": "Priority"}'
any type property patch $SPACE $TYPE $PROP --unset format.options.low
```

| Paths | Behaviour |
|-------|-----------|
| `name`, `description`, `xKey`, `xKind`, `meta.<k>`, `format.ui`, `format.filter`, `format.meta.<k>`, `format.options.<key>.{name,color,pos}`, `format.options.<key>.meta.<k>` | Mutable. Values are strings, except `format.filter` (a condition object). |
| `kind`, `scope`, `items`, `properties`, whole `format`, `format.type` | Pinned → `400 property.immutable`. |
| bare containers (`meta`, `format.options`, `format.options.<key>`) | Rejected on `set`; allowed on `unset` (unsetting an option key deletes the option). |

Other failures: unknown path or non-string value → `400 request.invalid_field`; bad `format.ui` / unparseable `format.filter` → `400 property.format_invalid`; a registered built-in type → `400 type.registered`; unknown ids → `404 sdk.not_found`. Returns `204`.

## Remove a property

```bash
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties/$PROP   # → 204
any type property remove $SPACE $TYPE $PROP
```

Tombstones the definition. Existing values are not cleaned up; later writes to the removed id are dropped op by op.

## Where values live

Values sit at `record[typeId][propId]` on the object's row — `{"<typeId>": {"<propId>": value}}`. On the wire and in filters the path is `<typeId>.<propId>`. Keying a write by `xKey` fails with `property.not_found`; resolve it to the `propId` first. See [Writing data](writing-data.html).

## Related

- [Data types](data-types.html) — kinds, formats, instants, scopes.
- [Runtime datasets](runtime-datasets.html) — declaring dataset schemas on a type.
- [Property lifecycle](property-lifecycle.html) — pins, patches, removal and read tolerance in depth.
- [Data model](data-model.html) — how types and datasets compose on one object.
