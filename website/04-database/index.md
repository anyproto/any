---
title: Database
description: How data is organised in any — spaces, objects, types, datasets — and the one query primitive that reads all of it.
order: 0
---
# Database

any is a document database that lives on your device, syncs through an end-to-end-encrypted network, and merges concurrent edits with CRDTs. This section covers the data model and the HTTP surface for reading and writing it.

## The model in one picture

```
account
 └── space                      encrypted, shared with members
      ├── objects               one row per object: its property values
      │     └── object          one type in any.type, N collections in any.collections
      │           ├── properties     record[ownerId][propId]
      │           └── datasets       chat_messages, editor_blocks, <runtime>…
      ├── types                 property definitions + parts + layout
      └── collections           property definitions only
```

- A **space** is the unit of sharing and encryption. Every member holds the same data; every write is a change in a per-object DAG that syncs to everyone.
- An **object** is a document. It has exactly one **type** in `any.type`, any number of **collections** in `any.collections`, property values keyed by `<ownerId>.<propId>` — the owner being the type or one of the collections — and any number of per-object **datasets** (records that belong to that object — chat messages, editor blocks, or a runtime dataset you declare).
- A **type** is what an object is: property definitions (kind, descriptor, scope), a **layout**, and **parts** — display units owning datasets a module serves: `records` (a runtime schema), `editor` (a block body), `chat` (a conversation). The hidden built-in types `page` and `dataview` exist in every space; your own document types are registered as bundles, and the space's chat is installed by the server's catalog.
- A **collection** is what an object is filed under: property definitions and nothing else. `miniapp` (the sidebar) and `bin` are built in; the wiki and the contact facets come from the catalog.

## One read path, many write paths

Reads always go through the windowed query primitive: `POST /v1/spaces/:spaceId/objects/query` for the cross-object `objects` storage collection and `POST /v1/spaces/:spaceId/query` for one object's dataset. Each has a `/subscribe` twin that returns the same snapshot plus a live stream of changes (see [Subscribe](../realtime/subscribe.html)). Writes go through purpose-built endpoints — property set, generic `/modify`, and the modules' own handlers — and each returns the same `{versionId, changeId, recordIds}` receipt; object create answers with the new `objectId`.

> **Why it matters.** There is no server between you and your data. Queries run against a local any-store database, so a read is a local disk read, a write is immediately visible, and both work offline. Sync and merge happen underneath — the query you ran a second ago keeps answering while peers catch up.

## Where to start

1. [Spaces](spaces.html) — create one, read its metadata, understand its lifecycle.
2. [Objects](objects.html) — create objects with a type, file them under collections, delete them.
3. [Types and properties](types-and-properties.html) — define the shape of your data.
4. [Collections](collections.html) — file objects under facets that add columns.
5. [Reading data](reading-data.html) — the filter grammar, sort, paging.
6. [Writing data](writing-data.html) — property writes, `/modify` ops, the write receipt.

<div class="cards">
<a href="data-model.html"><strong>Data model</strong><span>One type, N collections and N datasets per object; schemas and scopes.</span></a>
<a href="spaces.html"><strong>Spaces</strong><span>Create, list, update and delete the encrypted containers your data lives in.</span></a>
<a href="objects.html"><strong>Objects</strong><span>Documents with one type, any number of collections, properties and per-object datasets.</span></a>
<a href="types-and-properties.html"><strong>Types and properties</strong><span>Declare the one type an object is, with its layout, parts and property definitions.</span></a>
<a href="collections.html"><strong>Collections</strong><span>File objects under facets that add columns — the wiki, the sidebar, the bin, your own.</span></a>
<a href="property-lifecycle.html"><strong>Property lifecycle</strong><span>What is pinned, what patches, what removal does.</span></a>
<a href="data-types.html"><strong>Data types</strong><span>Kinds, the xFormat descriptor, the `{"$date": …}` instant, and the synced / local / account scopes.</span></a>
<a href="reading-data.html"><strong>Reading data</strong><span>Mongo-style filters, sort, limit/offset and cursor paging through /query.</span></a>
<a href="writing-data.html"><strong>Writing data</strong><span>Property writes, /modify ops, local-scope writes and the ModifyResult receipt.</span></a>
<a href="indexes.html"><strong>Indexes</strong><span>What is indexed, what is a scan, and how to keep hot queries cheap.</span></a>
<a href="runtime-datasets.html"><strong>Runtime datasets</strong><span>Declare a dataset schema on a type at runtime and have every peer enforce it.</span></a>
<a href="upsert.html"><strong>Upsert</strong><span>Idempotent batch ingest keyed by caller-supplied record ids.</span></a>
<a href="aggregation.html"><strong>Aggregation</strong><span>MongoDB-style pipelines over objects or a dataset.</span></a>
<a href="version-history.html"><strong>Version history</strong><span>List changes, diff versions and view an object as it was.</span></a>
<a href="markdown-import-export.html"><strong>Markdown import/export</strong><span>Render blocks to markdown and import markdown back, losslessly.</span></a>
<a href="system-fields.html"><strong>System fields</strong><span>The derived row-root stamps and the `_ver` / `_deletedAt` metadata on every record.</span></a>
<a href="derived-objects.html"><strong>Derived objects</strong><span>Deterministic object ids that every device computes offline.</span></a>
</div>
