---
title: Property lifecycle
description: How a property definition is born, what is pinned on first write, what can be patched, how values relate to definitions, and what removal does — all under CRDT convergence.
order: 35
---
# Property lifecycle

A property definition is a small synced record inside its type. Its id is content-addressed, its structural facts are pinned by the first write, its display facts merge per path, and its removal is a tombstone that leaves values untouched. Knowing which is which is what keeps concurrent schema edits from fighting.

## 1. Define

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$MOVIE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Year", "xKey": "year", "kind": "number", "scope": "synced",
       "meta": {"index": "props", "pos": "a1", "icon": "calendar"}}'
# → 201 {"propId": "EwyHGrtTdxB"}
any type property add $SPACE $MOVIE --name Year --xkey year --kind number
```

The `propId` is derived from the change that created the record (`base58(xxh3-64(changeId))`, up to 11 chars) — the same id on every peer, and the field key under which values are stored. Built-in properties on `any` use readable ids (`name`, `description`, `icon`, `tags`) that an 11-char base58 string can never collide with.

`xKey` is your stable code-side handle: clients resolve `xKey → propId` from `GET …/properties` and write by `propId`. It is metadata — not unique, not enforced, never seen by storage — and `GET …/types` requires a type-level `xKey` for the same reason (types resolve by handle, not by display name).

## 2. What the first write pins

| Field | Mutability | Why |
|---|---|---|
| `id` | immutable | it is the record id and the storage key |
| `kind` (`string` / `number` / `boolean` / `array` / `object` / `datetime`) | first-write-wins | values are validated against it on every peer |
| `scope` (`synced` / `account` / `local`) | first-write-wins | it selects the write route and version domain; a route change would strand values |
| `format.type` (`links` / `date` / `datetime` / `select` / `multiselect`) | first-write-wins, sub-path granular | it constrains `kind`; a broad replace of `format` is dropped at apply so a type change can't be smuggled in |
| `items`, `properties` (nested shapes) | client-soft: additions only | narrowing would invalidate stored values |
| `name`, `description`, `xKey`, `xKind` | freely mutable | labels |
| `meta.<k>`, `format.ui`, `format.filter`, `format.meta.<k>` | freely mutable | consumer conventions |
| `format.options.<key>.{name,color,pos,meta.<k>}` | freely mutable per leaf; the key itself immutable | the key IS the stored value |

Pins are enforced at apply time on every peer, convergently: an op that tries to change `kind` or `format.type` is dropped, not merged. Changing a pinned fact means defining a new property with a new id — the old one keeps its values.

`kind` may be omitted when `format` is given; it defaults from the format (`links`/`multiselect` ⇒ `array`, `select` ⇒ `string`, `date`/`datetime` ⇒ `datetime`). Passing `"kind": "string"` with a date format opts into the ISO-string convention instead of instants — and that choice is pinned too. Value conventions per kind and format: [Data types](data-types.html).

## 3. Values

Values live on objects at `{typeId}.{propId}`, written through the typed set route, which auto-routes by the declared scope:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/set/$MOVIE \
  -H 'Content-Type: application/json' \
  -d '{"EwyHGrtTdxB": 1995}'
```

Three rules connect values to definitions:

- **The server validates shape, not membership.** A number for a `number` kind, a well-formed `{"$date": …}` for an instant, a plain `any://<objectId>` for a link (`400 property.format_violation` otherwise). A `select` value is *not* checked against `format.options` — options are dangling-tolerant by design.
- **Read tolerance.** A value that violates the current definition, or sits under an unknown propId, is returned as-is. There is no `valid` flag and no re-validation cascade; clients decide how to render out-of-spec data.
- **Same-name properties are not a conflict.** Two peers concurrently adding "Rating" produce two ids, both fully real. Consolidating is a user or agent decision, never a merge rule.

> **Why it matters.** Hosted schemas migrate tables in one transaction the server controls. Here two devices can edit a schema offline and both edits must converge without a coordinator — so the design moves every "breaking" fact into an immutable pin and everything else into per-path LWW. Nothing ever needs a migration, and nothing that merged is ever rolled back.

## 4. Patch

`PATCH …/properties/:propId` is one CRDT change with per-path `set` / `unset`. It covers rename, reorder, icon, index hints and the whole select-option lifecycle:

```bash
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SPACE/types/$MOVIE/properties/$GENRE \
  -H 'Content-Type: application/json' \
  -d '{"set":   {"name": "Genre",
                 "format.options.noir.name": "Noir",
                 "format.options.noir.color": "gray",
                 "format.options.noir.pos": "a2"},
       "unset": ["format.options.western"]}'
# → 204
any type property option set $SPACE $MOVIE $GENRE noir --name Noir --color gray
```

- `set` must target a scalar leaf; a bare container (`meta`, `format.options`, `format.options.<key>`) is rejected on `set` but allowed on `unset` — unsetting an option key deletes the option.
- Pinned paths answer `400 property.immutable`; unknown paths `400 request.invalid_field`; a bad `format.ui` vocabulary or an unparseable `format.filter` `400 property.format_invalid`.
- Deleting then re-adding the same option key works: it is a field unset, not a tombstone.
- Definitions on registered built-in types are frozen: `400 type.registered`.

Because each leaf merges independently, two members renaming different options at once both win; two renaming the same option converge on the later write.

## 5. Remove

```bash
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/types/$MOVIE/properties/$PROP   # → 204
any type property remove $SPACE $MOVIE $PROP
```

Removal tombstones the definition record. Stored values are **not** cleaned up — they stay in the object rows as orphan data, read-tolerant — and later writes to the removed id are dropped op by op. The same holds one level up: dropping a type from an object's `any.types` orphans that namespace's values rather than deleting them.

There is deliberately no "rename or delete a value across N objects" operation: values store option keys, not labels, so a rename is one definition write and zero object writes, and a delete leaves keys dangling by design.

## 6. Observe

Definitions are synced records, so a schema change is visible live to every member the same way data is — subscribe to the type object's `properties` dataset, or re-read `GET …/types/:typeId/properties`. Search indexing of values follows `meta.index` (`props` by default, a named scope, or `none`) — [Indexing](../search/indexing.html).

## Related

- [Types and properties](types-and-properties.html) — the endpoint walkthrough.
- [Data model](data-model.html) — where definitions and values sit.
- [Runtime datasets](runtime-datasets.html) — the same pin/patch discipline applied to dataset fields.
