---
title: Page
description: How a document is declared — a user type whose part shares the editor module — what fields a page is made of, and how to list and file pages.
order: 30
---
# Page

A page is an object carrying a **document type**: a user type with one part whose dataset names the `editor` module. There is no built-in page type — you declare yours, and normally register it as a [bundle](../collaboration/bundles.html) so every client and device converges on one.

## Declaring the type

```bash
PAGE=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SP/types \
  -H 'Content-Type: application/json' \
  -d '{"name": "Page", "xKey": "page", "weight": 10, "layout": {"type": "page"}}' | jq -r .typeId)

curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types/$PAGE/parts \
  -H 'Content-Type: application/json' \
  -d '{"key": "body", "name": "Body", "ui": {"type": "document"},
       "datasets": [{"module": "editor", "shared": true}]}'
```

`"shared": true` puts the body in the module's canonical collection, `editor_blocks` — the one every document type shares, so an object that is both a page and, say, a meeting has one body. `weight` makes the type the object's **primary** type (the highest weight wins) and `layout` is the descriptor a client renders for it; both are opaque client vocabulary. The type carries properties like any other — a status, a priority, a relation — which is what a registered built-in could never do.

## What a page is made of

| Concern | Where it lives |
|---------|----------------|
| Display name | `any.name` |
| Labels | the built-in `any.tags` (free-form string array) |
| Body | the `editor_blocks` collection the type's part declares — [editor](editor.html) |
| Columns | the type's own properties, at `<typeId>.<propId>` |
| Position in the tree | `nav.parentId`, `nav.pos`, `nav.type` (see [objects](../database/objects.html)) |
| Recency | the derived row-root `modifiedAt` instant, with `modifiedBy` naming who signed that change (see [system fields](../database/system-fields.html)) |

## Creating and listing pages

File a document by creating an object with the type:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects \
  -H 'Content-Type: application/json' \
  -d '{"types": ["'$PAGE'"], "initialProperties": {"any": {"name": "Reading list", "tags": ["books"]}}}'
```

Then write its body through the editor — the object already holds the collection because it carries the declaring type:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/editor_blocks/markdown/append \
  -H 'Content-Type: application/json' \
  -d '{"content": "# Reading list\n\n- [ ] Children of Time"}'
```

List a space's documents with a filter on the type, most recently edited first:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"any.types": "'$PAGE'"}, "sort": ["-modifiedAt"], "limit": 50}'
```

The same body against `…/objects/query/subscribe` gives a live document list. Filter by label with `{"any.tags": "books"}` — array fields match on any element. "Every object with a body, whatever its type" is a filter on every type that shares the editor: the `owners` of `editor_blocks` in `GET /v1/spaces/:spaceId/datasets`, matched with `{"any.types": {"$in": [...]}}`.

## Why a bundle, not a built-in

In a local-first system there is no central moment where "the pages type" gets created. Two members working offline would each create one, and the CRDT would faithfully keep both — real spaces carried several parallel "Pages" types. A registered built-in avoided that but could not carry properties (registered types' definitions are frozen). Registering the type as a bundle keeps it a plain user type — properties, weight, layout, parts — while the registry converges every device on one id: `POST …/bundles` with the type declared on the root is adopt-or-install, so whoever runs it second adopts the first one's type.

> **Note.** A page's body is not part of the type's row. It is the editor collection on the same object, which is why an object can be a page with no blocks yet (an empty `records` array on the `editor_blocks` query) and why the body is searchable through the editor chunker under the `basic` scope (see [search](../search/index.html)).
