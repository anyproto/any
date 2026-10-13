---
title: Templates
description: Pre-filled objects of a type, filed under the built-in template collection and copied with one key on create — plus plain duplicate and save-as-template.
order: 33
---
# Templates

A template is an object a client copies to make new objects: a pre-filled name, description, icon, property values, collections and body for one type. It is an ordinary object of the type it is for, filed under the registered `template` collection, and it is edited with the same routes as any other object. The one thing the server adds is a `from` key on object create that copies it.

`$SPACE`, `$TYPE`, `$TPL` and `$OBJ` are ids the API returned.

## The `template` collection

`template` is a registered built-in collection, present in every space, hidden from the default listing (`GET …/collections?includeHidden=true` shows it) and static. It adds two columns to a member, under `template.<propId>`:

| Column | Kind | Meaning |
|---|---|---|
| `title` | string | The label a template picker shows — distinct from the pre-filled `any.name`, which may be empty. |
| `version` | number | The template's version. Bump it after a change its objects should follow. |

A template holds its type's values at `<typeId>.<propId>` and its body in the type's editor storage collection, exactly like a member of the type, because it **is** one. That is what makes it editable with the normal editor, and what makes it match `{"any.type": "<typeId>"}` — see [Every list excludes templates](#every-list-excludes-templates).

## Make a template

Create an object of the target type filed under `template`, then edit it as usual:

```bash
TPL=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$TYPE'", "collections": ["template"],
       "initialProperties": {"template": {"title": "Weekly review", "version": 1},
                             "any": {"name": "Weekly review — "}}}' \
  | jq -r .objectId)

curl -X PUT http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$TPL/editor/editor_blocks/markdown \
  -H 'Content-Type: application/json' \
  -d '{"content": "## What went well\n\n## What to change"}'
```

A template for a collection's rows is the same object with that collection in `collections`: it holds the collection's column values, and an object made from it is filed under the collection. Placement values travel verbatim — a wiki template's `parentId` and `pos` land on every copy — so a tree client sends its own placement in `initialProperties`.

## Create from a template

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"from": "'$TPL'", "initialProperties": {"any": {"name": "Weekly review — 13 Oct"}}}'
# → 201 {"objectId": "…"}
any object create $SPACE --from $TPL --properties '{"any":{"name":"Weekly review — 13 Oct"}}'
```

The new object has the template's type, its collections minus the built-in markers (`template`, `bin`, `miniapp`), its `any.*` values, its declared property values for every owner the new object has, and a copy of its body and every other dataset its type's parts declare: editor blocks keep their ids, a `records` dataset with derived ids gets fresh ones, stamps are re-derived for the copier. Request values win: `collections` are added to the copied set, and `initialProperties` are written over the copied values (replaced, never cleared — send an empty value, not `null`). `type` is optional and must equal the template's (`400 template.type_mismatch`).

The row is stamped with what it was made from:

```json
"any.template": {"id": "<templateId>", "version": 1}
```

A later edit of the template never changes an object made from it. An agent finds the objects behind a template's current version with `{"$and": [{"any.type": "<typeId>"}, {"any.template.id": "<templateId>"}, {"any.template.version": {"$lt": 2}}, {"any.collections": {"$nin": ["template"]}}]}` — the type clause keeps the scan small, the `$nin` keeps out a template copied from the template.

> **Why it matters.** The copy is one request and the reply comes after the whole of it: the row lands as one change, then each dataset as changes of its own, and a copy that fails midway is deleted. There is no template format to parse: a template renders, edits and syncs as the object it is.

What a copy does not carry: **files** stay bound to the template (a copy's file links break when the template or the file is deleted — attach the file to the copy to make it its own); **saved views** live on the template's `dataview` object (copy that object with `from` too, with `initialProperties.dataview.host` set to the new object); a **link to the template itself** keeps pointing at it. A record the source's dataset refuses today (a required field unset since) is `409 template.record_refused`, and the half copy is deleted. A value of local or account scope, a record field declared local, derived or stamped, and on a dataset that is not `dynamic` a field no definition declares any more, stay behind.

## Duplicate, and save as template

`from` on any other object is a plain duplicate — collections as they are (minus the markers), a stamp it carries kept, so lineage survives:

```bash
any object create $SPACE --from $OBJ
```

Save an existing object as a template by copying it into the marker:

```bash
any object create $SPACE --from $OBJ --collection template \
  --properties '{"template":{"title":"Weekly review","version":1}}'
```

## Pick and default

Templates for a type:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/query \
  -H 'Content-Type: application/json' \
  -d '{"filter": {"$and": [{"any.collections": "template"},
                           {"any.type": "'$TYPE'"},
                           {"any.collections": {"$nin": ["bin"]}}]},
       "sort": ["template.title"]}'
```

For a collection's rows use `{"any.collections": {"$all": ["template", "<collectionId>"]}}` instead of the type clause.

The default template of a user type or a collection is a client convention on the definition's `meta`, next to `meta.defaultType`: `meta.defaultTemplate: "<templateId>"`. The server does not read it. A registered type (`page`, `dataview`) takes no `meta`, so a default for plain pages lives on the collection the page is created in, or in the client's own state.

## Every list excludes templates

A template matches its type and its collections like any member. Every ordinary list, picker, tree, view source and search filter excludes templates the way it excludes the bin — the same clause, one more id:

```json
{"any.collections": {"$nin": ["bin", "template"]}}
```

The sidebar is the exception: it lists what was pinned. Reads that carry no object membership — `/search` hits without the `filter`, a shared dataset read across its objects, backlinks — cannot exclude templates; filter those by the hit's object row.

A template is binned and deleted like any object; objects made from it keep `any.template.id` naming the dead id, which a reader ignores as it ignores a dead collection id.
