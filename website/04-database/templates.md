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
| `version` | number | The template's version. Bump it when the template changes in a way its objects should follow. |

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

A template for a collection's rows is the same object with that collection in `collections`: it holds the collection's column values, and an object made from it is filed under the collection.

## Create from a template

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"from": "'$TPL'", "initialProperties": {"any": {"name": "Weekly review — 13 Oct"}}}'
# → 201 {"objectId": "…"}
any object create $SPACE --from $TPL --properties '{"any":{"name":"Weekly review — 13 Oct"}}'
```

The new object has the template's type, its collections minus `template`, its `any.*` values, its property values for every owner the new object has, and a copy of its body and every other dataset its type's parts declare. Request values win: `collections` are added to the copied set, and `initialProperties` are written over the copied values. `type` is optional and must equal the template's (`400 template.type_mismatch`).

The row is stamped with what it was made from:

```json
"any.template": {"id": "<templateId>", "version": 1}
```

A later edit of the template never changes an object made from it. An agent finds the objects behind a template's current version with `{"any.template.id": "<templateId>", "any.template.version": {"$lt": 2}}`.

> **Why it matters.** The copy is one request and the object is whole before its id comes back, so a subscriber never sees a half-made object. There is no template format to parse: a template renders, edits and syncs as the object it is.

## Duplicate, and save as template

`from` on any other object is a plain duplicate — collections as they are, no stamp:

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

The default template of a type or a collection is a client convention on the definition's `meta`, next to `meta.defaultType`: `meta.defaultTemplate: "<templateId>"`. The server does not read it.

## Every list excludes templates

A template matches its type and its collections like any member. Every ordinary list, picker, tree, view source and search filter excludes templates the way it excludes the bin — the same clause, one more id:

```json
{"any.collections": {"$nin": ["bin", "template"]}}
```

A template is binned and deleted like any object; objects made from it keep `any.template.id` naming the dead id, which a reader ignores as it ignores a dead collection id.
