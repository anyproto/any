---
title: Chat & documents
description: The CRDT modules any ships with — chat and editor blocks — the type parts that declare them, the built-in hidden types, and the any:// link grammar that ties records together.
order: 0
---
# Chat and documents

Any includes **chat** and **document editing** as built-in data modules. They provide record shapes, validation and CRDT merge rules, so your client can use the same query and subscription API for messages and document blocks.

## Choose the content you need

| Build | Start with | Read the details |
|---|---|---|
| A note or document | An object with `type: "page"`. | [Pages](page.html), [editor operations](editor.html). |
| A shared conversation | The space’s general chat, installed through the usecase catalog. | [Chat](chat.html), [catalog setup](../collaboration/bundles.html#the-general-chat). |
| A custom structured record | Your own type with a declared records dataset. | [Runtime datasets](../database/runtime-datasets.html). |
| Links between these records | An `any://` URI for the target object, record, property or file. | [Links and backlinks](links.html). |

## Types, parts and modules

A **type** defines properties, a layout and **parts**. A part is a unit of content your client renders, such as a document body. Its datasets are served by a module.

A custom type declares a document body with a part whose dataset names the editor module. Chat is reserved to the catalog’s general-chat root; clients cannot declare their own chat types. The record shape, validation and merge rules of that storage collection are the module's, enforced on every peer; the server keeps bespoke endpoints for **writes** only — send a message, patch a block — because those operations validate who may edit and which fields get stamped.

**Read records through the generic query API**: `POST /v1/spaces/:spaceId/query` with the storage collection name, and `…/query/subscribe` for live updates.

Parts are a **type's** alone. A collection — what an object is filed under — carries property definitions and nothing else, so it never brings a body, a chat or a records dataset with it ([Collections](../database/collections.html)).

```bash
# a document type: one part sharing the editor module
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types/$DOC/parts \
  -H 'Content-Type: application/json' \
  -d '{"key": "body", "datasets": [{"module": "editor", "shared": true}]}'
```

| Module | Storage collection | Writes via | Reads via |
|------|---------|-----------|-----------|
| `chat` | `chat_messages` (shared) | `…/objects/:objectId/chat/messages[…]` | `/query` with `"dataset": "chat_messages"` |
| `editor` | `editor_blocks` (shared), or `<typeId>_<key>` for a part with its own editor | `…/objects/:objectId/editor/:collection/blocks[…]` and `…/editor/:collection/markdown` | `/query` with `"dataset": "<collection>"` |
| `records` | `<typeId>_<key>` | generic `/modify` and `/upsert` | `/query` with `"dataset": "<collection>"` — [Runtime datasets](../database/runtime-datasets.html) |

A **shared** dataset is the module's canonical storage collection: every type that shares the editor writes the same body, so retyping an object from one document type to another keeps its body instead of stranding it. A **namespaced** dataset (`<typeId>_<key>`) belongs to one type — a meeting's `summary` editor next to its shared notes. Chat is shared-only. An object holds a storage collection only while its type declares the part that owns it: a write without it is `400 dataset.not_declared`, and no write sets a type for you.

The `any://` link grammar is not a type, but it is the glue between them: a mention in a chat message, an image in a document and a citation of a property value are all `any://` URIs in markdown link destinations.

## One read path, one wire shape

Because reads are generic, everything you know about [reading data](../database/reading-data.html) applies to built-in types unchanged: filters, sorts, paging, `includeTotal`, and the windowed [subscribe](../realtime/subscribe.html) stream with its `snapshot` → `changes` frames. A chat client is "a subscription sorted by `-_ver.id` with a limit"; a document view is "a subscription sorted by `nav.pos`".

Every write endpoint returns the same small result instead of the record body:

```json
{ "versionId": "…", "changeId": "…", "recordIds": ["<record id>"] }
```

`recordIds[0]` is the id the server derived for a newly created record. Read the record back through the query path — or, better, let your open subscription deliver it.

## Where the objects come from

Modules reach ordinary objects through their one type. A document is an object whose type is a document type — the built-in `page`, or your own user type with an editor part — and blocks in `editor_blocks`. Chat is reserved to the server: a space has exactly one chat, the catalog's general chat ([the general chat](../collaboration/bundles.html#the-general-chat)) — a derived root that declares its own type with a chat part and hosts its own messages. Every device and member computes the same root, so the conversation can never fork. A client part, dataset or bundle naming `chat` is `400 dataset.module_reserved`, and putting the general chat's type on any other object is `400 type.reserved_carrier`. There is no built-in `editor` or `chat` type: a client's own document type is registered as a bundle, so every device converges on one definition instead of each minting its own.

## Saved views, apps and the bin

`page` has one registered sibling type and two registered **collections**. All three are hidden (`GET …/types` and `GET …/collections` list them only with `?includeHidden=true`), static, and never stamped onto an object for you — an object opts in:

- **`dataview`** (type) holds saved views over a host object — a type or collection definition, or any other object. A dataview is its own object: `POST …/objects` with `{"type": "dataview", "initialProperties": {"dataview": {"host": "<hostId>"}}}`, and its `dataviews` dataset has one record per table on the host, its `views` dataset one record per view (`dataview`, `name`, `pos`, `layout`, an opaque `query` and column settings). Both are plain records datasets written through `/modify` and read through `/query`; find a host's views with `{"any.type": "dataview", "dataview.host": "<hostId>"}`.
- **`miniapp`** (collection) represents the space's app list and pinned objects: `bundle` names the installed bundle an entry runs (absent on an object the user pinned), `pos` sets the list order, and `hidden` hides the entry without uninstalling it. See [Apps](../tutorial/apps.html).
- **`bin`** (collection) is move-to-bin: `POST …/properties/:objectId/collections/bin` stamps `bin.movedAt` / `bin.movedBy` in the same change, the matching `DELETE` restores and clears them. See [Collections](../database/collections.html).

<div class="cards">
<a href="chat.html"><strong>Chat</strong><span>A complete messenger on one CRDT dataset: send, edit, react, mentions, private read tracking, unread counters.</span></a>
<a href="editor.html"><strong>Editor</strong><span>Block-structured documents: atomic block writes plus a lossless markdown bridge for imports, exports and LLM edits.</span></a>
<a href="page.html"><strong>Page</strong><span>The built-in `page` and how your own document type is declared — a type with an editor part — and the fields a page is made of.</span></a>
<a href="links.html"><strong>Links</strong><span>The canonical any:// grammar for objects, records, mentions, spaces, property values and files, and the link index behind backlinks.</span></a>
</div>
