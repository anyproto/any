---
title: Data model
description: An object has one type, any number of collections and any number of datasets — one shared objects row for its property values plus its own record storage collections, each with a discoverable JSON Schema and per-field scope.
order: 5
---
# Data model

A page, person, or task is an **object**. Every object has exactly one **type**, which defines its properties, layout, and content. It can also belong to any number of **collections**, each adding another group of properties.

An object has two places to store data:

| Data | Where it lives | Example |
|---|---|---|
| Properties that describe the object | its row in the space-wide `objects` storage collection | name, status, due date |
| Records that belong to the object | a per-object dataset | document blocks, chat messages, imported rows |

A **dataset** is a named set of records with a schema. We call the underlying record store a **storage collection** to distinguish it from a collection that groups objects. On the API, the dataset name is the `dataset` field in query and write requests.

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

Use these rules when choosing where data belongs:

- **The type determines content and layout.** A part is a display unit, such as a document body or a table, backed by one or more datasets. Setting another type replaces the type; there is no inheritance.
- **Collections add property groups.** A movie filed under the wiki keeps its movie type. Its row contains the universal `any` group, the movie properties, and the wiki properties. A collection adds no layout or dataset.
- **Datasets are per object.** A document's blocks are records *on that object*, not rows in a space-wide blocks table. The space-wide storage collection is `objects` only — the row per object that holds property values and the system stamps.

Each object has its own history of changes. The sync engine merges concurrent changes using CRDT rules: every peer applying the same changes reaches the same result. The space supplies encryption and access control.

## The `objects` storage collection

Every regular object has one row whose `id` is the object ID. A property value lives at `<ownerId>.<propId>`: the owner's ID selects the type or collection, and `propId` selects its column.

Resolve human-facing handles (`xKey`) through the definition APIs before querying or writing. Display names and handles are not storage keys. Renaming a property changes its definition; existing values stay at the same ID.

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

**Content datasets come from the type.** Collections cannot declare them. Writes require the object's current type to own the dataset; otherwise they fail with `400 dataset.not_declared`. Retyping preserves old records as orphan data but does not keep their write access. Files use their separate payload child.

In this example, `$SPACE` is an existing writable space, `$MOVIE` is a type whose parts declare the reviews dataset, and `$WIKI` is the wiki collection ID from [catalog setup](objects.html#the-wiki-tree). Set both memberships deliberately at create:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$MOVIE'", "collections": ["'$WIKI'"], "initialProperties": {"any": {"name": "Heat"}}}'
```

The movie and wiki properties share the object's row. Reviews live in the movie's per-object dataset, with their own records, ordering, and write rules. A type whose part declares a reserved module (the general chat) is carried only by its own root: naming it as `type` is `400 type.reserved_carrier`.

## One read path for every dataset

Use IDs from the setup above. `$CHAT` is the [general chat root](../types/chat.html#finding-the-chat-object); `$OBJ` is the movie object ID and `$MOVIE` its declaring type ID. The server must be running with read access to `$SPACE`.

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

`additionalProperties: true` marks a dynamic dataset: undeclared keys are allowed and default to `synced`. Runtime datasets add behavioral keywords on top — `required`, `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id`, `x-search` — described in [Runtime datasets](runtime-datasets.html). `owners` on an entry lists the types whose parts declare the storage collection. Writes and search indexing require one of those types, or the definition root itself, which can host its own records; `module` names the serving module (`records`, `editor`, `chat`) and `shared` marks a module's canonical storage collection. The SDK's own datasets (`objects`) carry no owners.

Account-wide, `GET /v1/datasets` lists the tech-space system datasets (`spaces`, `profile`, `devices`, …); `spaces` and `profile` are the two the [space list](../realtime/space-list.html) query reads.

> **Note.** A definition is itself an object row: a type carries the marker `__type__` in `any.type`, a collection carries `__collection__`. Both hold the `properties` and `shortIds` datasets that store your property definitions, and a type also holds `datasets`, its parts and dataset declarations. You never write those directly — the types and collections APIs do — but they follow the same model, which is why a definition change is a CRDT write every member converges on. A definition also **hosts its own records and values**, with no flag and no self-membership: because the marker sits in `any.type`, a definition row never matches `{"any.type": "<typeId>"}` or `{"any.collections": "<collectionId>"}`, so member queries need no marker exclusion.

## Related

- [Types and properties](types-and-properties.html) — defining the type an object is.
- [Collections](collections.html) — defining what an object is filed under.
- [Property lifecycle](property-lifecycle.html) — what is pinned, what patches, what happens on remove.
- [Runtime datasets](runtime-datasets.html) — declaring your own storage collections with enforced shape.
- [Derived objects](derived-objects.html) — objects whose ids are computed rather than minted.
