---
title: Collections
description: A collection is what an object is filed under — a group of property definitions and nothing else; create one, give it columns, file objects under it and query its members.
order: 32
---
# Collections

An object **is** one type and is **filed under** any number of collections. A collection carries property definitions and nothing else — no parts, no layout, no datasets — so filing an object changes the columns it holds, never how it renders.

A person who is also a contact and an investor is **one** object: `type: person`, `collections: [contact, investor]`. It renders with the person layout and carries three property groups — the person's, the contact's and the investor's — each in its own namespace on the row.

> **Note.** "Collection" on this site always means this object kind. The place records live in — `editor_blocks`, `chat_messages`, `<typeId>_<key>` — is a **storage collection**, the `dataset` name on reads and writes. See [Runtime datasets](runtime-datasets.html).

## Type or collection?

| Make it a **type** when | Make it a **collection** when |
|---|---|
| the thing needs a layout or a part of its own — Profile, Task, Meeting | it only adds columns to objects that keep their own type — People, Deals, Reading list, Contact |
| it answers "what is this?" | it answers "where does this belong?" |
| an object can have exactly one | an object can have any number |

A type is a format. "It needs different properties" is never the reason for one: that is a collection. Reading list is a collection: the things in it are pages, profiles and links that keep being pages, profiles and links. Profile is a type: it has a profile layout and a notes body. People is a collection of profiles, and Deals a collection of pages.

A collection names the type of its rows in `meta.defaultType` — the xKey of a type. Create an object inside a collection with that type; when the key is absent, or names a handle no type in the space carries, use `page`. The server does not enforce it.

## Create a collection

`$SPACE` is a writable space; the reply's `collectionId` is `$COLL` below, and `$OBJ` is any existing object in the space.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/collections \
  -H 'Content-Type: application/json' \
  -d '{"name": "Reading list", "xKey": "reading_list"}'
# → 201 {"collectionId": "bafy…"}
any collection create $SPACE --name "Reading list" --xkey reading_list
```

Body: `{name?, description?, iconCid?, xKey, hidden?, meta?}` — the type body minus `layout`. **`xKey` is required**: the stable handle the collection resolves by, a slug of the name that survives renames. An empty one is `400 type.xkey_required`.

Types and collections share **one handle namespace**. An `xKey` (or id) a type already holds cannot be taken by a collection and the other way round — `409 type.xkey_conflict`, whose `details` name `existingTypeId` or `existingCollectionId`.

Property definitions are not part of the create body; a `properties` key, like any unknown key, is `400 request.unknown_field`. Create the collection, then add each column.

## List and read

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/collections
any collection list $SPACE
```

`{"collections": [{id, name?, description?, iconCid?, xKey?, builtIn?, hidden?, meta?}]}` — the synthetic meta row `collection` (the shape of collection objects themselves; a picker skips it), then the registered built-ins, then your own. Flags are omitted when false. Hidden collections appear only with `?includeHidden=true`; `GET …/collections/:collectionId` resolves them always.

`PATCH …/collections/:collectionId` takes `{name?, description?, iconCid?, hidden?, meta?}`, at least one, and answers `204`. A built-in refuses it with `400 collection.registered`. Deleting a collection is not implemented (`501 sdk.not_implemented`) — hide it instead.

Passing a user **type**'s id to any of these routes is `400 collection.not_a_collection`; an id the space has no collection for — including a built-in type such as `page` — is `404 collection.not_found`.

## Columns

Property definitions are one surface: the four routes under `…/collections/:collectionId/properties` take the same bodies, the same `{set, unset}` patch grammar and answer the same codes as their `…/types/:typeId/properties` twins ([Types and properties](types-and-properties.html)).

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/collections/$COLL/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Finished", "xKey": "finished", "kind": "boolean",
       "xFormat": {"type": "checkbox"}}'
# → 201 {"propId": "bafy…"}
any collection property add $SPACE $COLL --name Finished --xkey finished --kind boolean
```

`kind` is required and pinned; `xKey` is unique within the collection; values are stored and written by `propId`. A column write on a built-in collection is `400 type.registered`.

## File and unfile

```bash
curl -X POST   http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/collections/$COLL
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/collections/$COLL
any object collection attach $SPACE $OBJ $COLL
any object collection detach $SPACE $OBJ $COLL
```

Both take no body, both are idempotent, and both return the write receipt. Filing appends to `any.collections` and admits writes to that collection's columns; unfiling removes the id. The one route with a side effect is `bin`: a repeated move re-stamps `movedAt` / `movedBy` even though membership does not change.

The POST pre-flights its ids — `404 object.not_found`, `404 collection.not_found`, and `400 collection.not_a_collection` when the id names a user type (a built-in type such as `page` is `404 collection.not_found`) — because `any.collections` is a synced CRDT write with no validation behind it, so a typo would replicate permanently. The DELETE pre-flights nothing on purpose: it is the repair path for a row that already carries a bogus id.

**Unfiling is not a delete.** That collection's values stay on the row as orphan data, read-tolerant, and filing the object again brings them back into view — writes to that group are refused in between. Filter a member list on `any.collections`, never on a property value alone: the orphan values still match.

An object that is new takes its collections in the create body instead — [Objects](objects.html). `$PERSON` is the type, `$CONTACT` the collection, `$STATUS` a choice property declared on it:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$PERSON'", "collections": ["'$CONTACT'"],
       "initialProperties": {"any": {"name": "Ada Lovelace"},
                             "'$CONTACT'": {"'$STATUS'": ["active"]}}}'
```

## Query the members

`any.collections` is an array, so it filters by membership: a scalar matches any element, `$all` demands several, `$nin` excludes.

```json
{"any.collections": "<collectionId>"}                     // filed under it
{"any.collections": {"$all": ["<c1>", "<c2>"]}}           // filed under both
{"any.collections": {"$nin": ["bin"]}}                    // not in the bin
```

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"$and": [{"any.collections": "'$COLL'"},
                           {"any.collections": {"$nin": ["bin"]}}]},
       "sort": ["-modifiedAt"], "limit": 50}'
```

A collection definition's own row carries the marker `__collection__` in `any.type` and its handle at `collection.xkey`, never its own id in either slot — so it never matches a member query and no marker exclusion is needed. The path is indexed (sparse) on the `objects` storage collection; a column value under `<collectionId>.<propId>` is not, so put the membership clause in the filter and let the index narrow the scan ([Indexes](indexes.html)).

## Built-in collections

`miniapp` and `bin` are registered collections, present in every space, hidden from the default listing and static — `400 collection.registered` on a metadata write. An object opts into them; nothing stamps them.

**`miniapp`** is the sidebar. Members carry `bundle` (the id of the installed app, absent on an object the user pinned), `pos` (a client-allocated lexid) and `hidden` (out of the sidebar, install kept). Pin and unpin are the plain filing routes; values go through `POST …/properties/:objectId/set/miniapp`.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"$and": [{"any.collections": "miniapp"},
                           {"any.collections": {"$nin": ["bin"]}},
                           {"miniapp.hidden": {"$ne": true}}]},
       "sort": ["miniapp.pos"]}'
```

**`bin`** is move-to-bin. Filing under it stamps `bin.movedAt` and `bin.movedBy` in the same change as the membership op, so a bin member never lacks its stamps; unfiling clears them. The object keeps its type and its other collections throughout — restore brings it back exactly as it was. Permanent deletion is `DELETE …/objects/:objectId`.

```bash
any object collection attach $SPACE $OBJ bin     # to the bin
any object collection detach $SPACE $OBJ bin     # restore
```

List the bin with `{"any.collections": "bin"}` sorted `["-bin.movedAt"]`; exclude its members from ordinary lists with `{"any.collections": {"$nin": ["bin"]}}`.

## Collections the product ships

A collection your app depends on belongs in a bundle, not in a client-side create: the wiki, and the contact / investor / customer / partner / vendor / cofounder / candidate facets are catalog collections, installed by `POST /v1/catalog/:usecaseId/setup` so every device and member resolves the same id and the same property ids ([Bundles](../collaboration/bundles.html)). Minting one client-side converges only by luck and races on `409 type.xkey_conflict`.

## Related

- [Types and properties](types-and-properties.html) — the other owner of property definitions.
- [Objects](objects.html) — `type` and `collections` at create time.
- [Reading data](reading-data.html) — the filter grammar in full.
- [Bundles](../collaboration/bundles.html) — shipping a collection so every device agrees on it.
