---
title: Property lifecycle
description: How a property definition is born, what is pinned on first write, what can be patched, how values relate to definitions, and what removal does — all under CRDT convergence.
order: 35
---
# Property lifecycle

A property definition is a small synced record inside its owner — a type or a collection, the same surface either way. Its id is content-addressed, its structural facts are pinned by the first write, its display facts merge per path, and its removal is a tombstone that leaves values untouched. Knowing which is which is what keeps concurrent schema edits from fighting.

## 1. Define

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$MOVIE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Year", "xKey": "year", "kind": "number", "scope": "synced",
       "meta": {"index": "props"},
       "xFormat": {"type": "number", "pos": "a1", "icon": "calendar"}}'
# → 201 {"propId": "EwyHGrtTdxB"}
any type property add $SPACE $MOVIE --name Year --xkey year --kind number
```

The `propId` is derived from the change that created the record (`base58(xxh3-64(changeId))`, up to 11 chars) — the same id on every peer, and the field key under which values are stored. Built-in properties on `any` use readable ids (`name`, `description`, `icon`, `tags`) that an 11-char base58 string can never collide with. A property a bundle or catalog install declares takes an id derived from the root and its `xKey` instead, so two devices installing apart mint one column, not two.

`xKey` is your stable code-side handle: clients resolve `xKey → propId` from `GET …/properties` and write by `propId`. It is metadata — unique within the owning type or collection by a read-then-create preflight (`409 property.xkey_conflict`), never seen by storage, mutable — and `POST …/types` and `POST …/collections` require a definition-level `xKey` for the same reason (both resolve by handle, not by display name, out of one shared namespace).

## 2. What the first write pins

| Field | Mutability | Why |
|---|---|---|
| `id` | immutable | it is the record id and the storage key |
| `kind` (`string` / `number` / `boolean` / `array` / `object` / `datetime`) | first-write-wins | values are validated against it on every peer |
| `scope` (`synced` / `account` / `local`) | first-write-wins | it selects the write route and version domain; a route change would strand values |
| `items`, `properties` (nested shapes) | pinned | narrowing would invalidate stored values |
| `name`, `description`, `xKey` | freely mutable | labels and the handle |
| `meta.index` | freely mutable | the search flag |
| `xFormat` and every path under it — `type`, `icon`, `pos`, `options.<key>.{name,color,pos,meta.<k>}`, `relation.{targetTypes,filter}`, `config.<k>`, vendor keys | freely mutable per leaf; an option key itself immutable | the descriptor is a hint, not a guarantee; the option key IS the stored value |

Pins are enforced at apply time on every peer, convergently: an op that tries to change `kind` is dropped, not merged. Changing a pinned fact means defining a new property with a new id — the old one keeps its values. The slug (`xFormat.type`) is mutable but the server only lets it move within the pinned kind — `text` to `email`, never `text` to `number`.

`kind` is always explicit; nothing is defaulted from the descriptor. Value conventions per slug: [Data types](data-types.html).

## 3. Values

Values live on objects at `{ownerId}.{propId}` — the owner being the object's type or one of its collections — written through the owner-scoped set route, which auto-routes by the declared scope:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/set/$MOVIE \
  -H 'Content-Type: application/json' \
  -d '{"patch": {"EwyHGrtTdxB": 1995}}'
```

Three rules connect values to definitions:

- **The server validates shape, not membership.** Every peer checks the value's kind against the pinned `kind` (`400 property.kind_mismatch` on a local write). The server checks the rest against the property's *current* slug — a well-formed `{"$date": …}` for a date, a plain `any://<objectId>` for a relation, a `period` / `money` / `geo` compound in its exact shape (`400 property.format_violation` otherwise). A `choice` value is *not* checked against `xFormat.options` — options are dangling-tolerant by design.
- **Read tolerance.** A value that violates the current definition, or sits under an unknown propId, is returned as-is. There is no `valid` flag and no re-validation cascade; clients decide how to render out-of-spec data.
- **Same-name properties are not a conflict.** Two peers concurrently adding "Rating" produce two ids, both fully real. Consolidating is a user or agent decision, never a merge rule.

Separate a property's meaning from its presentation. Its pinned kind and scope define how values behave; mutable descriptors control how clients display them.

## 4. Patch

`PATCH …/properties/:propId` is one CRDT change with per-path `set` / `unset`. It covers rename, the handle, reorder, icon, the index flag and the whole choice-option lifecycle:

```bash
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SPACE/types/$MOVIE/properties/$GENRE \
  -H 'Content-Type: application/json' \
  -d '{"set":   {"name": "Genre",
                 "xFormat.options.noir.name": "Noir",
                 "xFormat.options.noir.color": "gray",
                 "xFormat.options.noir.pos": "a2"},
       "unset": ["xFormat.options.western"]}'
# → 204
any type property option set $SPACE $MOVIE $GENRE noir --name Noir --color gray
```

- `set` targets a leaf and never carries an object; a container (`meta`, `xFormat`, `xFormat.options`, `xFormat.options.<key>`, `xFormat.relation`, `xFormat.config`) is rejected on `set` but allowed on `unset` — unsetting an option key deletes the option. That is what keeps one client from replacing the bag and dropping keys another client added.
- Pinned paths answer `400 property.immutable`; unknown paths or wrong leaf types `400 request.invalid_field`; a slug that does not fit the kind, a reserved key or an unparseable `xFormat.relation.filter` `400 property.format_invalid`; a taken `xKey` `409 property.xkey_conflict`.
- Deleting then re-adding the same option key works: it is a field unset, not a tombstone.
- Definitions on registered built-in types and collections are frozen: `400 type.registered`.

Because each leaf merges independently, two members renaming different options at once both win; two renaming the same option converge on the later write.

## 5. Remove

```bash
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/types/$MOVIE/properties/$PROP   # → 204
any type property remove $SPACE $MOVIE $PROP
```

Removal tombstones the definition record. Stored values are **not** cleaned up — they stay in the object rows as orphan data, read-tolerant — and later writes to the removed id are dropped op by op. The same holds one level up: replacing an object's type, or unfiling it from a collection, orphans that namespace's values rather than deleting them, and setting the owner again brings them back into view.

There is deliberately no "rename or delete a value across N objects" operation: values store option keys, not labels, so a rename is one definition write and zero object writes, and a delete leaves keys dangling by design.

## 6. Observe

Definitions are synced records, so a schema change is visible live to every member the same way data is — subscribe to the definition object's `properties` dataset, or re-read `GET …/types/:typeId/properties` (`…/collections/:collectionId/properties` for a collection's columns). Search indexing of values follows `meta.index` (`props` by default, a named scope, or `none`) — [Indexing](../search/indexing.html).

## Related

- [Types and properties](types-and-properties.html) — the endpoint walkthrough.
- [Collections](collections.html) — the same property surface on the other owner.
- [Data model](data-model.html) — where definitions and values sit.
- [Runtime datasets](runtime-datasets.html) — the same pin/patch discipline applied to dataset fields.
