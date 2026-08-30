---
title: Objects
description: Create objects with types and initial properties, place them in the navigation tree, find what links to them, and delete them.
order: 20
---
# Objects

An object is a document in a space: a set of attached types, property values keyed by type and property id, a position in the space's tree, and any number of per-object datasets.

## Create an object

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{
    "types": ["page"],
    "initialProperties": { "any": { "name": "Dune" } }
  }'
# → 201 {"objectId": "bafy…"}
```

The create body has exactly three keys:

| Key | Meaning |
|-----|---------|
| `types` | Type ids attached at create. `nav` is appended server-side when absent. |
| `initialProperties` | Starting values, keyed by type id then property id: `{"<typeId>": {"<propId>": value}}`. The only home for property values — a top-level `name` is rejected. |
| `nav` | Optional tree placement override: `{type, parentId, pos}`. |

Any other top-level key answers `400 request.unknown_field` naming the accepted set; a wrong shape (a string where an object is expected) is `400 request.schema`. Nothing is silently dropped. Values under `initialProperties` are checked against each property's declared format (`400 property.format_violation`).

Bind types at create time. Runtime attach/detach (`POST …/properties/:objectId/attach/:typeId`) is registered but returns `501 sdk.not_implemented`, so an object that will hold chat messages or editor blocks needs `chat` / `editor` in `types` up front — a dataset write to an object missing the matching type is rejected with `400 dataset.validation`.

## The object row

Every object is one row in the space's `objects` collection. Read it back through the cross-object query:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"id": "'$OBJ'"}}'
```

```json
{ "records": [ {
  "id": "bafy…",
  "any": { "types": ["page", "nav"], "name": "Dune" },
  "nav": { "type": 1, "parentId": "", "pos": "PPQY" },
  "author": "A5…", "spaceId": "bafy…",
  "createdAt":  { "$date": "2026-08-05T17:00:00.000Z" },
  "modifiedAt": { "$date": "2026-08-05T17:00:00.000Z" },
  "modifiedBy": "A5…",
  "_ver": { "…": "…" }
} ] }
```

`author`, `spaceId`, `createdAt`, `modifiedAt` and `modifiedBy` are derived by the SDK and read-only; `modifiedAt` bumps on every synced write to the object and converges across peers on DAG order, and `modifiedBy` names the identity that signed that same change (`author` stays the creator). `modifiedAt` is the author's clock — good for `{"sort": ["-modifiedAt"]}`, never a fencing token. Full list in [System fields](system-fields.html).

`GET /v1/spaces/:spaceId/properties/:objectId` returns the same row as `{"record": {…}}` when you already hold the id.

## The navigation tree (`nav`)

Every new object is stamped with three `nav` values so clients can render a tree without a dedicated endpoint:

| Path | Meaning |
|------|---------|
| `nav.type` | `1` = item, `2` = folder. |
| `nav.parentId` | Id of the parent folder; `""` is the root. |
| `nav.pos` | A lexid ordering key among siblings. Defaults to the next position after the current maximum in the target folder. |

List a folder's children in order:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"nav.parentId": "'$FOLDER'"}, "sort": ["nav.pos"]}'
```

`nav` is a virtual built-in type: it appears in `GET …/types` with `builtIn: true`, its paths are literal strings (`nav.parentId`, not a content-addressed id), and it has no dataset of its own.

### Moving objects

A drag-and-drop move is one property write on the `nav` type — both fields land in a single change:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/set/nav \
  -H 'Content-Type: application/json' \
  -d '{"patch": {"parentId": "'$NEW_PARENT'", "pos": "PPQZ"}}'
```

Clients compute drop positions with a lexid allocator (alphabet `CharsAllNoEscape`, block size 4, step 100) so a move needs no server round-trip to pick a `pos`.

## Backlinks

Object references are property values with `format.type: "links"` — arrays of `any://<objectId>` URIs. The reverse lookup answers "which objects reference X?":

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/backlinks
# → {"backlinks": [{"objectId": "…", "typeId": "…", "propId": "…"}]}
```

One entry per (referencing object, property) pair; only values under a currently attached type count. An unknown or unreferenced id returns an empty array, not a 404. Link values carry no index, so this is a scan over the objects collection. `nav.parentId` is not a links property — query children directly as shown above.

## Delete an object

```bash
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ   # → 204
```

The row receives a record-level tombstone, disappears from every `objects/query`, and a `removed` entry reaches subscribers of the cross-object stream. Per-object reads of a deleted id (`/query` with `objectId`, editor, history routes) answer `404 object.not_found`; the cross-object query has no such failure — a filter on a dead id returns zero rows.

> **Note.** Search hits can briefly outlive their object because the index evicts asynchronously. A client following a hit into `/query` treats a 404 as "stale hit", not an error.

## Related

- [Types and properties](types-and-properties.html) — what `types` and `initialProperties` refer to.
- [Reading data](reading-data.html) — the filter grammar used above.
- [Page](../types/page.html), [Chat](../types/chat.html), [Editor](../types/editor.html) — the built-in types an object can carry.
- [Links](../types/links.html) — the `any://` grammar behind `links` properties and backlinks.
