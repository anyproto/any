---
title: Data model
description: An object has one type, any number of collections and any number of datasets — one shared objects row for its property values plus its own record storage collections, each with a discoverable JSON Schema and per-field scope.
order: 5
---
# Data model

An object is one **type** (what it is), any number of **collections** (what it is filed under) and any number of **datasets** it owns: one row in the space-wide `objects` storage collection for its property values, plus its own record storage collections (`chat_messages`, `editor_blocks`, your own runtime datasets…). Each dataset has its own schema, its own write path and the same read path.

## One picture

```
Space
 ├─ types                      user types + built-ins (any, type, collection, and the hidden page / dataview)
 ├─ collections                user collections + the built-in miniapp / bin
 ├─ objects  (one storage collection)   one row per object: values keyed <ownerId>.<propId>
 └─ Object
     ├─ any.type        = movieTypeId           exactly one — the layout and the parts
     ├─ any.collections = [wikiCollectionId, …] any number — columns only
     └─ datasets                                 N storage collections
          ├─ editor_blocks         ← the `editor` module, declared by a body part of the type
          ├─ <movieTypeId>_reviews ← a runtime dataset (the `records` module) on the type `movie`
          └─ payloads              ← files attached to this object (on a derived child object)
```

Three consequences fall out of this shape:

- **One type, no contest.** The object renders with its type's layout and holds that type's parts. Setting a type replaces the previous one; there is no inheritance and no primary-type rule to resolve.
- **Collections stack.** Filing an object under a collection adds that collection's property group and nothing else. A movie in the wiki is one object with one type and one collection, carrying three property namespaces — `any`, the movie's and the wiki's.
- **Datasets are per object.** A document's blocks are records *on that object*, not rows in a space-wide blocks table. The space-wide storage collection is `objects` only — the row per object that holds property values and the system stamps.

> **Why it matters.** In a hosted document database you model "a document with comments" as two tables joined by a foreign key, and the server owns both. Here the object *is* the unit of sync and access: its datasets travel with it, merge as CRDTs with it, and are encrypted with the space it belongs to. There is nothing to join across.

## The `objects` storage collection

Every regular object has exactly one row here, `id` = the object id. Values sit at `{ownerId}.{propId}`, where the owner is the object's type or one of its collections; the human property name lives only on the definition, so renaming a property never touches stored values.

```json
{
  "id": "bafy…obj",
  "any":            { "name": "Heat", "type": "bafy…movie", "collections": ["bafy…wiki"] },
  "bafy…movie":     { "Y9Hxx5xmYmF": ["personA", "personB"], "EwyHGrtTdxB": 1995 },
  "bafy…wiki":      { "Qp3RkT2vLm9": "", "Hs8WnZ4cXb1": "a0", "Fd6JyM7tRe2": false },
  "author":         "A5k…",
  "createdAt":      { "$date": "2026-08-01T10:00:00.000Z" },
  "modifiedAt":     { "$date": "2026-08-05T17:00:00.000Z" },
  "modifiedBy":     "A9t…"
}
```

Cross-object questions ("every movie from 1995", "all pages under this folder") are queries over this storage collection — [Reading data](reading-data.html). Properties are written through the typed set route — [Writing data](writing-data.html). The reserved paths (`any.*`, `_ver`, `_addSeq`, the stamps) are catalogued in [System fields](system-fields.html); the `bafy…wiki` group is the tree — the wiki collection's `parentId` / `pos` / `folder`, ordinary properties like the movie's ([Objects](objects.html)).

## Per-object datasets

A dataset is a Mongo-like record storage collection scoped to one object. Where it comes from decides who defines its shape:

| Storage collection | Contributed by | Schema owner | Write path |
|---|---|---|---|
| `chat_messages` | the space's general-chat root — the `chat` module is reserved to the server's catalog install, so a space has one chat | the server's chat module | `POST …/objects/:o/chat/messages` and friends — [Chat](../types/chat.html) |
| `editor_blocks`, `<typeId>_<key>` | a type's part declaring the `editor` module (shared, or namespaced to the type) | the server's editor module | `POST …/objects/:o/editor/:collection/blocks`, the markdown bridge — [Editor](../types/editor.html) |
| `payloads` | files, on a derived child of the object | the SDK | `POST …/objects/:o/files`; rows read through `POST …/objects/:o/files/query` — [Files](../files/index.html) |
| `<typeId>_<key>` | a runtime dataset declared under a part of a user type (the `records` module) | you, via the declaration | generic `POST …/modify` and `POST …/upsert` — [Runtime datasets](runtime-datasets.html) |

**Datasets come from the type, never from a collection.** A storage collection lives on an object only while its type declares the part that owns it (`400 dataset.not_declared` otherwise — no write sets a type), so pick the type at create:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$MOVIE'", "collections": ["'$WIKI'"], "initialProperties": {"any": {"name": "Heat"}}}'
```

That single object renders with the movie layout, carries the movie's properties and the wiki collection's columns, and holds the movie's reviews — and each concern is a separate storage collection with separate ordering, indexes and handlers. A type whose part declares a reserved module (the general chat) is carried only by its own root: naming it as `type` is `400 type.reserved_carrier`.

## One read path for every dataset

Whatever produced a dataset, it is read the same way: the per-object query with a `dataset` name, and its `/subscribe` twin for liveness. File rows are the one exception — they sit on a derived child whose id clients never see, so they have their own `…/files/query`.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$CHAT'", "dataset": "chat_messages", "sort": ["-_ver.id"], "limit": 50}'

curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$OBJ'", "dataset": "'$MOVIE'_reviews", "filter": {"score": {"$gte": 8}}}'
```

`any query-subscribe $SPACE $CHAT --dataset chat_messages --sort=-_ver.id --limit 50` is the CLI form (its first `snapshot` frame is the read). Aggregation pipelines run over a dataset the same way — [Aggregation](aggregation.html).

## Schemas and scopes

Every dataset the space hosts is discoverable with its JSON Schema:

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/datasets
# → { "datasets": [ { "name": "chat_messages", "module": "chat", "shared": true, "owners": ["<chatRootId>"], "schema": {…} }, … ] }
any datasets $SPACE
```

```json
{
  "type": "object",
  "additionalProperties": true,
  "properties": {
    "text":       { "type": "string",  "x-scope": "synced" },
    "creator":    { "type": "string",  "x-scope": "derived" },
    "createdAt":  { "x-scope": "derived" },
    "unread":     { "type": "boolean", "x-scope": "local" }
  }
}
```

The `x-scope` keyword says who writes a field and how far it travels:

| Scope | Written by | Travels to |
|---|---|---|
| `synced` | clients, through the object's CRDT change | every member of the space |
| `derived` | the handler at apply time (creator, timestamps) — client writes rejected | computed identically on every peer |
| `local` | this device, through the local-scope `modify` route; never enters the DAG | nowhere |
| `account` | this account, through the private tech space | this account's other devices only |

`additionalProperties: true` marks a dynamic dataset: undeclared keys are allowed and default to `synced`. Runtime datasets add behavioral keywords on top — `required`, `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id`, `x-search` — described in [Runtime datasets](runtime-datasets.html). `owners` on an entry lists the types whose parts declare the storage collection — records exist only on objects of one of those types, which is also what the search indexer keys eviction on; `module` names the serving module (`records`, `editor`, `chat`) and `shared` marks a module's canonical storage collection. The SDK's own datasets (`objects`) carry no owners.

Account-wide, `GET /v1/datasets` lists the tech-space system datasets (`spaces`, `profile`, `devices`, …); `spaces` and `profile` are the two the [space list](../realtime/space-list.html) query reads.

> **Note.** A definition is itself an object row: a type carries the marker `__type__` in `any.type`, a collection carries `__collection__`, and each holds the `properties` and `datasets` datasets that store your definitions. You never write those directly — the types and collections APIs do — but they follow the same model, which is why a definition change is a CRDT write every member converges on. A definition also **hosts its own records and values**, with no flag and no self-membership: because the marker sits in `any.type`, a definition row never matches `{"any.type": "<typeId>"}` or `{"any.collections": "<collectionId>"}`, so member queries need no marker exclusion.

## Related

- [Types and properties](types-and-properties.html) — defining the type an object is.
- [Collections](collections.html) — defining what an object is filed under.
- [Property lifecycle](property-lifecycle.html) — what is pinned, what patches, what happens on remove.
- [Runtime datasets](runtime-datasets.html) — declaring your own storage collections with enforced shape.
- [Derived objects](derived-objects.html) — objects whose ids are computed rather than minted.
