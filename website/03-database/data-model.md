---
title: Data model
description: An object implements many types and owns many datasets — one shared objects row for its properties, plus any number of record collections; every dataset has a discoverable JSON Schema with per-field scope.
order: 5
---
# Data model

An object is not one document. It is a set of **types** it implements and a set of **datasets** it owns: one row in the space-wide `objects` collection for its property values, and any number of per-object record collections (`chat_messages`, `editor_blocks`, your own runtime datasets…). Each dataset has its own schema, its own write path and the same read path.

## One picture

```
Space
 ├─ types                      user types + built-ins (any, page, chat, editor, type, …)
 ├─ objects  (one collection)  one row per object: property values keyed <typeId>.<propId>
 └─ Object
     ├─ any.types = [pageTypeId, chatTypeId, movieTypeId, …]   N types, no inheritance
     └─ datasets                                                N collections
          ├─ chat_messages    ← contributed by the built-in `chat` type
          ├─ editor_blocks    ← contributed by the built-in `editor` type
          ├─ reviews          ← a runtime dataset declared on the user type `movie`
          └─ payloads         ← files attached to this object
```

Two consequences fall out of this shape:

- **Types coexist.** An object can be a `page` *and* a `chat` *and* a `movie` at once. Adopting a type appends its id to `any.types`; each type brings its property namespace and, if it declares any, its datasets. There is no inheritance and no "primary" type.
- **Collections are per object.** A chat's messages are a collection *on that chat object*, not rows in a space-wide messages table. The space-wide collection is `objects` only — the row per object that holds property values and the system stamps.

> **Why it matters.** In a hosted document database you model "a document with comments" as two tables joined by a foreign key, and the server owns both. Here the object *is* the unit of sync and access: its datasets travel with it, merge as CRDTs with it, and are encrypted with the space it belongs to. There is nothing to join across.

## The `objects` collection

Every regular object has exactly one row here, `id` = the object id. Values sit at `{typeId}.{propId}`; the human property name lives only on the definition, so renaming a property never touches stored values.

```json
{
  "id": "bafy…obj",
  "any":            { "name": "Heat", "types": ["page", "bafy…movie"] },
  "bafy…movie":     { "Y9Hxx5xmYmF": ["personA", "personB"], "EwyHGrtTdxB": 1995 },
  "bafy…wiki":      { "Qp3RkT2vLm9": "", "Hs8WnZ4cXb1": "a0", "Fd6JyM7tRe2": false },
  "author":         "A5k…",
  "createdAt":      { "$date": "2026-08-01T10:00:00.000Z" },
  "modifiedAt":     { "$date": "2026-08-05T17:00:00.000Z" },
  "modifiedBy":     "A9t…"
}
```

Cross-object questions ("every movie from 1995", "all pages under this folder") are queries over this collection — [Reading data](reading-data.html). Properties are written through the typed set route — [Writing data](writing-data.html). The reserved paths (`any.*`, `_ver`, `_addSeq`, the stamps) are catalogued in [System fields](system-fields.html); the `bafy…wiki` group is the tree — the wiki type's `parentId` / `pos` / `folder`, ordinary properties like the movie's ([Objects](objects.html)).

## Per-object datasets

A dataset is a Mongo-like record collection scoped to one object. Where it comes from decides who defines its shape:

| Dataset | Contributed by | Schema owner | Write path |
|---|---|---|---|
| `chat_messages` | a type's part declaring the `chat` module (shared) | the server's chat module | `POST …/objects/:o/chat/messages` and friends — [Chat](../types/chat.html) |
| `editor_blocks`, `<typeId>_<key>` | a type's part declaring the `editor` module (shared, or namespaced to the type) | the server's editor module | `POST …/objects/:o/editor/:collection/blocks`, the markdown bridge — [Editor](../types/editor.html) |
| `payloads` | files | the SDK | `POST …/objects/:o/files` — [Files](../files/index.html) |
| `<typeId>_<key>` | a runtime dataset declared under a part of a user type (the `records` module) | you, via the declaration | generic `POST …/modify` and `POST …/upsert` — [Runtime datasets](runtime-datasets.html) |

A collection lives on an object only while the object carries a type whose part declares it (`400 dataset.not_declared` otherwise — no write attaches a type), so attach types at create:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"types": ["'$PAGE'", "'$CHAT'", "'$MOVIE'"], "initialProperties": {"any": {"name": "Heat"}}}'
```

That single object now renders as a document, hosts a discussion, and carries `movie` properties — and each concern is a separate collection with separate ordering, indexes and handlers.

## One read path for every dataset

Whatever produced a dataset, it is read the same way: the per-object query with a `dataset` name, and its `/subscribe` twin for liveness.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$OBJ'", "dataset": "chat_messages", "sort": ["-_ver.id"], "limit": 50}'

curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$OBJ'", "dataset": "reviews", "filter": {"score": {"$gte": 8}}}'
```

`any query $SPACE $OBJ chat_messages` is the CLI form. Aggregation pipelines run over a dataset the same way — [Aggregation](aggregation.html).

## Schemas and scopes

Every dataset the space hosts is discoverable with its JSON Schema:

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/datasets
# → { "datasets": [ { "name": "chat_messages", "typeId": "chat", "schema": {…} }, … ] }
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

`additionalProperties: true` marks a dynamic dataset: undeclared keys are allowed and default to `synced`. Runtime datasets add behavioral keywords on top — `required`, `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id`, `x-search` — described in [Runtime datasets](runtime-datasets.html). `typeId` on an entry names the owning type: records exist only on objects carrying it, which is also what the search indexer keys eviction on.

Account-wide, `GET /v1/datasets` lists the tech-space system datasets (`spaces`, `profile`) behind the [space list](../realtime/space-list.html).

> **Note.** Type objects are themselves rows with the `__type__` marker in `any.types`; they carry the `properties` and `datasets` datasets that hold your definitions. You never write those directly — the types API does — but they follow the same model, which is why a definition change is a CRDT write that every member converges on.

## Related

- [Types and properties](types-and-properties.html) — defining the namespaces objects adopt.
- [Property lifecycle](property-lifecycle.html) — what is pinned, what patches, what happens on remove.
- [Runtime datasets](runtime-datasets.html) — declaring your own collections with enforced shape.
- [Derived objects](derived-objects.html) — objects whose ids are computed rather than minted.
