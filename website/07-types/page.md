---
title: Page
description: The built-in page marker type — how a document is declared, what fields it is made of, and how to list and file pages.
order: 30
---
# Page

`page` is the built-in marker for "this object is a document". It is a pure declaration: no dataset, no properties. Everything a page is made of — name, labels, body, tree position, recency — comes from fields every object already has.

## What a page is made of

| Concern | Where it lives |
|---------|----------------|
| Display name | `any.name` |
| Labels | the built-in `any.tags` (free-form string array) |
| Body | the [editor](editor.html) type's `editor_blocks` dataset, attached on the first block write |
| Position in the tree | `nav.parentId`, `nav.pos`, `nav.type` (see [objects](../database/objects.html)) |
| Recency | the derived row-root `modifiedAt` instant, with `modifiedBy` naming who signed that change (see [system fields](../database/system-fields.html)) |

## Creating and listing pages

File a document by creating an object with the `page` type:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects \
  -H 'Content-Type: application/json' \
  -d '{"types": ["page"], "initialProperties": {"any": {"name": "Reading list", "tags": ["books"]}}}'
```

Then write its body through the editor — a first block write attaches the `editor` type for you:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/markdown/append \
  -H 'Content-Type: application/json' \
  -d '{"content": "# Reading list\n\n- [ ] Children of Time"}'
```

List a space's documents with a filter on the type marker, most recently edited first:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"any.types": "page"}, "sort": ["-modifiedAt"], "limit": 50}'
```

The same body against `…/objects/query/subscribe` gives a live document list. Filter by label with `{"any.tags": "books"}` — array fields match on any element.

## Why a registered marker

`page` is registered by the server, not created by a client, so it exists in every space by construction. That replaces the pattern where each client mints its own user "pages" type: the check-then-create race across members left real spaces with several parallel "Pages" types, and documents filed under different ones. With one built-in id there is nothing to race for.

> **Why it matters.** In a local-first system there is no central moment where "the pages type" gets created. Two members working offline would each create one, and the CRDT would faithfully keep both. A registered type sidesteps the problem entirely — every peer agrees on the id before any of them writes a byte.

A user type that claims the `page` xKey collides with the built-in id and is rejected with `409 type.xkey_conflict`. Existing user types with a similar name are left untouched.

## No properties, by design

`page` declares no properties. Registered types' property definitions are frozen — they cannot be added, patched or removed (`400 type.registered`) — so a built-in `choice` would carry a permanently empty, uneditable option set. Per-space columns such as "status" or "priority" remain a user-type concern: attach a user type alongside `page` and put the properties there. See [types and properties](../database/types-and-properties.html).

> **Note.** A page's body is not part of the `page` type. It is the `editor` type's dataset on the same object, which is why an object can be a page with no blocks yet (an empty `records` array on the `editor_blocks` query) and why the body is searchable through the editor chunker under the `basic` scope (see [search](../search/index.html)).
