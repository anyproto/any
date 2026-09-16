---
title: Page
description: How a document is declared — a type whose part shares the editor module — what fields a page is made of, and how to list and file pages.
order: 30
---
# Page

A page is an object whose type is a **document type**: a type with one part whose dataset names the `editor` module. Two kinds qualify. The built-in `page` type is the plain document — hidden from the type picker, present in every space, no properties of its own; pass it as `type` at create and the object holds a body. A **user document type** is one you declare yourself, with the same kind of part, and normally register as a [bundle](../collaboration/bundles.html) so every client and device converges on one — that is the type to use when a page needs columns or a layout. Both write the one `editor_blocks` storage collection, so retyping between them keeps the body.

## The built-in `page`

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "page", "initialProperties": {"any": {"name": "Reading list"}}}'
```

That is the whole declaration: `page` is registered, not created, so there is nothing to ensure and nothing that can fork. It is `hidden` (`GET …/types` lists it only with `?includeHidden=true`; `GET …/types/page` resolves it always) and carries no `layout` — a `page` renders by the client's default.

## Declaring your own type

```bash
DOC=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SP/types \
  -H 'Content-Type: application/json' \
  -d '{"name": "Article", "xKey": "article", "layout": {"type": "page"}}' | jq -r .typeId)

curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types/$DOC/parts \
  -H 'Content-Type: application/json' \
  -d '{"key": "body", "name": "Body", "ui": {"type": "document"},
       "datasets": [{"module": "editor", "shared": true}]}'
```

The `xKey` must not collide with another type's or collection's handle, nor with a built-in id — `page` and `dataview` are taken as types, `miniapp` and `bin` as collections (`409 type.xkey_conflict`; the two surfaces share one namespace). `"shared": true` puts the body in the module's canonical storage collection, `editor_blocks` — the one every document type writes, so an object retyped from `page` to `article` keeps the body it had. `layout` is the descriptor a client renders the object with, opaque client vocabulary. The type carries properties like any other — a status, a priority, a relation — which is what the registered built-in cannot.

## What a page is made of

| Concern | Where it lives |
|---------|----------------|
| Display name | `any.name` |
| Labels | the built-in `any.tags` (free-form string array) |
| Body | the `editor_blocks` storage collection the type's part declares — [editor](editor.html) |
| Columns | the type's own properties at `<typeId>.<propId>` (the built-in `page` has none), plus one group per collection the page is filed under |
| Position in the tree | the wiki collection's `parentId` / `pos` / `folder` columns, when the page is filed under it (see [objects](../database/objects.html)) |
| Recency | the derived row-root `modifiedAt` instant, with `modifiedBy` naming who signed that change (see [system fields](../database/system-fields.html)) |

## Creating and listing pages

Create an object with the type:

```bash
OBJ=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$DOC'", "initialProperties": {"any": {"name": "Reading list", "tags": ["books"]}}}' \
  | jq -r .objectId)
```

To place it in the wiki tree, also send `"collections": ["<wikiCollectionId>"]` — the id the wiki's catalog setup returns.

Then write its body through the editor — the object already holds the storage collection because its type declares the part:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/editor_blocks/markdown/append \
  -H 'Content-Type: application/json' \
  -d '{"content": "# Reading list\n\n- [ ] Children of Time"}'
```

List a space's documents with a filter on the type, most recently edited first. A binned object keeps its type, so exclude the `bin` collection:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"$and": [{"any.type": "'$DOC'"}, {"any.collections": {"$nin": ["bin"]}}]},
       "sort": ["-modifiedAt"], "limit": 50}'
```

The same body against `…/objects/query/subscribe` gives a live document list. Filter by label with `{"any.tags": "books"}` — array fields match on any element. "Every object with a body, whatever its type" is a filter on every type that shares the editor: the `owners` of `editor_blocks` in `GET /v1/spaces/:spaceId/datasets` (the built-in `page` is always among them), matched with `{"$and": [{"any.type": {"$in": [...]}}, {"any.collections": {"$nin": ["bin"]}}]}`. A type's own definition row carries the marker in `any.type`, so it never matches and needs no exclusion.

## Built-in or bundle

In a local-first system there is no central moment where "the pages type" gets created. Two members working offline would each create one, and the CRDT would faithfully keep both — the space ends up with parallel "Pages" types. The built-in `page` avoids that by being registered: it exists everywhere, but its definition is frozen — no properties, no layout. Registering your own type as a bundle keeps it a plain user type — properties, layout, parts — while the registry converges every device on one id: `POST …/bundles` with the type declared on the root is adopt-or-install, so whoever runs it second adopts the first one's type. Pick `page` for a plain body, a bundle-registered type for a document that is also a record. Well-known document types — a journal entry, a meeting with its notes, summary and transcript — come ready-made from the server's catalog ([Apps](../tutorial/apps.html)), so a client sets those up rather than declaring its own.

> **Note.** A page's body is not part of the type's row. It is the editor storage collection on the same object, which is why an object can be a page with no blocks yet (an empty `records` array on the `editor_blocks` query) and why the body is searchable through the editor chunker under the `basic` scope (see [search](../search/index.html)).
