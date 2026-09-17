---
title: Objects
description: Create objects with one type and any number of collections, seed their properties, place them in the wiki tree, find what links to them, and delete them.
order: 20
---
# Objects

An object is a document in a space: exactly one **type** — what it is — any number of **collections** it is filed under, property values keyed by owner and property id, and any number of per-object datasets. A place in the space's tree is one of those collections — the wiki, below.

## Create an object

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "page",
    "initialProperties": { "any": { "name": "Dune" } }
  }'
# → 201 {"objectId": "bafy…"}
any object create $SPACE --type page --properties '{"any":{"name":"Dune"}}'
```

The create body has exactly three keys:

| Key | Meaning |
|-----|---------|
| `type` | The object's one type. **Required** — `400 request.missing_field` without it. `page` is the plain document. |
| `collections` | Ids the object is filed under. Optional; the object carries exactly these — nothing is appended server-side. |
| `initialProperties` | Starting values, keyed by **owner** then property id: `{"<ownerId>": {"<propId>": value}}`, the owner being the type or one of the collections. The only home for property values — a top-level `name` is rejected. |

Any other top-level key answers `400 request.unknown_field` naming the accepted set; a wrong shape (a string where an array is expected) is `400 request.schema`. Nothing is silently dropped. Values under `initialProperties` are checked against each property's declared format (`400 property.format_violation`).

A person filed under two facets is one call:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$PERSON'", "collections": ["'$CONTACT'", "'$INVESTOR'"],
       "initialProperties": {"any": {"name": "Ada Lovelace"},
                             "'$CONTACT'": {"'$STATUS'": ["active"]}}}'
```

## Set the type, file under collections

```bash
curl -X POST   http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/type/$TYPE
curl -X POST   http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/collections/$COLL
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/collections/$COLL
any object type set          $SPACE $OBJ $TYPE
any object collection attach $SPACE $OBJ $COLL
any object collection detach $SPACE $OBJ $COLL
```

`POST …/type/:typeId` **sets** the one type, replacing any previous one. There is no unset — every object has a type, and clearing `any.type` through a raw write is `400 membership.type_required`. Retyping is not a delete: the old type's values and its datasets' records stay on the row as orphan data, read-tolerant.

The two POSTs pre-flight their ids: `404 object.not_found`, `404 type.not_found` / `404 collection.not_found`, and the slot check — a user collection's id in the type route is `400 type.not_a_type`, a user type's id in the collections route is `400 collection.not_a_collection`. A registered built-in in the wrong slot has no row there at all: `miniapp` or `bin` as a type is `404 type.not_found`, `page` or `dataview` as a collection is `404 collection.not_found`. The DELETE pre-flights nothing, on purpose: it is the repair path for a bogus id already on the row.

A storage collection — chat messages, editor blocks, a runtime dataset — lives on an object only while its **type** declares the part that owns it ([modules](../types/index.html)); a write without it is `400 dataset.not_declared`, and no write sets a type for you. A type whose part declares a reserved module — the general chat's — is carried only by its own root; naming it as `type` is `400 type.reserved_carrier`.

## The object row

Every object is one row in the space's `objects` storage collection. Read it back through the cross-object query:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"id": "'$OBJ'"}}'
```

```json
{ "records": [ {
  "id": "bafy…",
  "any": { "type": "page", "collections": ["bafy…wiki"], "name": "Dune" },
  "author": "A5…", "spaceId": "bafy…",
  "createdAt":  { "$date": "2026-08-05T17:00:00.000Z" },
  "modifiedAt": { "$date": "2026-08-05T17:00:00.000Z" },
  "modifiedBy": "A5…",
  "_ver": { "…": "…" }
} ] }
```

`author`, `spaceId`, `createdAt`, `modifiedAt` and `modifiedBy` are derived by the SDK and read-only; `modifiedAt` bumps on every synced write to the object and converges across peers on DAG order, and `modifiedBy` names the identity that signed that same change (`author` stays the creator). `modifiedAt` is the author's clock — good for `{"sort": ["-modifiedAt"]}`, never a fencing token. Full list in [System fields](system-fields.html).

`GET /v1/spaces/:spaceId/objects/:objectId` returns the same row as `{"objectId": "…", "record": {…}}` when you already hold the id — `404 object.not_found` for an id the space never had, `410 object.deleted` for a deleted one.

## The wiki tree

The space's tree is the catalog usecase `wiki`: a hidden **collection** whose three columns place any object filed under it. There is no built-in tree type, nothing is stamped on create, and there is no tree endpoint. Set it up once per space and keep the reply — every client, device and member lands on the same ids:

```bash
curl -X POST http://127.0.0.1:7001/v1/catalog/wiki/setup \
  -H 'Content-Type: application/json' \
  -d '{"spaceId": "'$SPACE'"}'
```

```json
{ "usecase": "wiki",
  "bundles": [ { "id": "system:wiki/v1", "installed": true,
                 "collectionId": "<wikiCollectionId>",
                 "properties": { "parentId": "<parentIdPropId>", "pos": "<posPropId>", "folder": "<folderPropId>" },
                 "…": "…" } ] }
```

| Column | Kind | Meaning |
|--------|------|---------|
| `parentId` | string | Id of the parent; `""` is the top level. |
| `pos` | string | A lexid ordering key among siblings — allocated by the client. |
| `folder` | boolean | `true` for a folder. |

An object is in the tree only while it is filed under the wiki collection; its placement is ordinary property values at `<wikiCollectionId>.<propId>`. A page in the tree has `page` as its type for the body and the wiki collection for its place; a folder is the same create with `folder` true. What type a tree object has is up to you — the wiki collection only places it.

```bash
# $WIKI, $PARENT_ID, $POS, $FOLDER: collectionId and properties.* from the setup reply
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "page",
    "collections": ["'$WIKI'"],
    "initialProperties": {
      "'$WIKI'": { "'$PARENT_ID'": "", "'$POS'": "a0", "'$FOLDER'": false },
      "any": { "name": "Dune" }
    }
  }'
```

List a node's children in order (`""` as the parent lists the top level):

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"'$WIKI.$PARENT_ID'": "'$NODE'"}, "sort": ["'$WIKI.$POS'"]}'
```

The columns are plain properties: unindexed on the `objects` storage collection (a scan — [Indexes](indexes.html)), excluded from search, and strings rather than relations, so backlinks never report a parent link.

### Moving objects

A drag-and-drop move is one property write in the wiki collection's namespace — both fields land in a single change; a reorder inside the same parent patches `pos` alone:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/set/$WIKI \
  -H 'Content-Type: application/json' \
  -d '{"patch": {"'$PARENT_ID'": "'$NEW_PARENT'", "'$POS'": "a1"}}'
```

The server never allocates a position. Clients compute every `pos` with the lexid allocator the editor's blocks use (alphabet `CharsAllNoEscape`, block size 4, step 100): past the last sibling on create, between two siblings on a drop — no server round-trip.

## Links and backlinks

The server keeps a link index next to its search index: every `any://` reference in editor blocks, chat messages and link-bearing property values becomes an edge with a source place, a kind and a canonical target. Three reads serve a contextual panel:

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/backlinks
# → {"object": [edge…], "parts": [edge…]}
curl http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/links
# → {"links": [edge…]}
curl "http://127.0.0.1:7001/v1/backlinks?target=any://o/$SPACE/$OBJ"
# → {"spaces": [{"spaceId": "…", "object": […], "parts": […]}]}
```

An edge is `{"source": {spaceId, objectId, dataset, recordId, typeId?}, "kind": "mention|link|card|embed|relation", "target": {uri, kind, …ids}}`. `object` holds what points at the object itself, `parts` what points at one of its blocks, messages or property values; `?record=…&dataset=…` or `?prop=…` narrows to one part, `?kind=` filters, and replies cap at 500 edges (`truncated: true`). On a property value the source's `typeId` names the owner that declares it — the object's type or one of its collections. The index follows every write after a short debounce and publishes a device-scope `links.updated` event naming the targets that changed. Values of a type or collection the object no longer has drop their edges. An unknown or unreferenced id returns empty lists, not a 404. The wiki tree's `parentId` is a plain string, not a link — query children as shown above.

## Delete an object

```bash
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ   # → 204
```

An unknown or already-deleted id is `404 sdk.not_found`, a reader or guest gets `403 space.read_only`, and a derived object — a bundle root installed with `derived: true`, such as the general chat — is permanent: `409 object.derived_undeletable`. To move an object out of sight without destroying it, file it under the built-in `bin` collection instead ([Collections](collections.html)).

The row receives a record-level tombstone, disappears from every `objects/query`, and a `removed` entry reaches subscribers of the cross-object stream. Per-object reads of a deleted id (`/query` with `objectId`, editor, history routes) answer `404 object.not_found`; the cross-object query has no such failure — a filter on a dead id returns zero rows.

> **Note.** Search hits can briefly outlive their object because the index evicts asynchronously. A client following a hit into `/query` treats a 404 as "stale hit", not an error.

## Related

- [Types and properties](types-and-properties.html) — what `type` and `initialProperties` refer to.
- [Collections](collections.html) — what `collections` refer to, and the built-in `miniapp` / `bin`.
- [Reading data](reading-data.html) — the filter grammar used above.
- [Page](../types/page.html), [Chat](../types/chat.html), [Editor](../types/editor.html) — the modules a type's parts give an object.
- [Links](../types/links.html) — the `any://` grammar behind `links` properties and backlinks.
