# Templates (built-in `template` collection)

A **template** is an object a client copies to make new objects: a
pre-filled name, description, icon, property values, collections and
body for one type. Templates are ordinary objects of the type they are
for, filed under the registered `template` collection. Nothing about
them is special beyond that marker and the one key on object create
that copies them.

`template` is a **registered built-in collection**, present in every
space by construction and `hidden` (`GET …/collections` lists it only
with `?includeHidden=true`), static (`400 collection.registered` on a
metadata write), `builtIn: true` with `xKey` equal to the id, which
reserves `template` against user definitions. Like `bin`, an object
opts into it; nothing stamps it.

## Model

A template **is** an object of its target type. Its `any.type` is the
type it is for, so it holds that type's values under `<typeId>.<propId>`
and its body in the type's editor storage collection like any other
object of the type, and it is edited with the same routes — there is
no template editor. Everything on it is the pre-fill:

| on the template | becomes, on an object created from it |
|---|---|
| `any.name`, `any.description`, `any.icon`, `any.tags` | the same values; an empty one stays empty |
| `any.collections` minus `template` | the new object's collections |
| `<typeId>.<propId>` and `<collectionId>.<propId>` values | the same values, for every owner the new object has |
| the records of every dataset the type's parts declare | copied records, same ids and fields |

The collection carries two properties, under `template.<propId>` on the
template only:

| property | kind | |
|---|---|---|
| `title` | string | the label a template picker shows — not the pre-filled `any.name`, which may be empty |
| `version` | number | the template's version; a client increments it when it changes the template, and an object records the one it was made from (below) |

A template for a collection's rows is the same thing with the
collection in `any.collections`: the template holds that collection's
column values and the new object is filed under it.

## Make a template

Create an object of the target type filed under `template`, then edit
it as usual:

```json
POST /v1/spaces/:spaceId/objects
{ "type": "<typeId>",
  "collections": ["template", "<collectionId>"],
  "initialProperties": {
    "template": { "title": "Weekly review", "version": 1 },
    "any": { "name": "Weekly review — " },
    "<typeId>": { "<statusPropId>": ["todo"] } } }
→ 201 {"objectId": "<templateId>"}
```

Save an existing object as a template with `from` (§ Copy an object)
and the marker:

```json
{ "from": "<objectId>",
  "collections": ["template"],
  "initialProperties": { "template": { "title": "Weekly review", "version": 1 } } }
```

Bump `template.version` when a template changes in a way objects made
from it should follow:

```json
POST /v1/spaces/:spaceId/properties/<templateId>/set/template
{ "patch": { "version": 2 } }
```

## Copy an object: `from`

`POST /v1/spaces/:spaceId/objects` takes a fourth key, `from`: the id
of an object to copy. With it, `type` is optional and must equal the
source's type when given (`400 template.type_mismatch`), `collections`
are **added** to the source's, and `initialProperties` are written over
the copied values, owner by owner, so a request value wins. The copy
takes the source's synced `any.*` values, its collections, the value
groups of every owner the new object has, and the records of every
dataset the source's type declares through its parts — except a
reserved module's (the chat). Derived fields (`any.links`, the
row-root stamps) are recomputed, not copied. An unknown `from` is
`404 object.not_found`, a deleted one `410 object.deleted`, and a
definition's own row (a type or collection root)
`400 membership.meta_type`.

When the source is a template — it is filed under `template` — two
things differ: `template` is dropped from the copied collections, and
the new row is stamped

```json
"any.template": { "id": "<templateId>", "version": 2 }
```

with the template's `template.version` (`0` when it has none). Any
other source is a plain duplicate: collections as they are, no stamp.

```json
POST /v1/spaces/:spaceId/objects
{ "from": "<templateId>",
  "initialProperties": { "any": { "name": "Weekly review — 13 Oct" } } }
→ 201 {"objectId": "…"}
```

One request, one object; the copy is whole before the id comes back.
A later edit of the template never changes an object made from it.

## Pick, default, outdated

Templates for a type, for a picker:

```json
POST /v1/spaces/:spaceId/objects/query
{ "filter": { "$and": [ { "any.collections": "template" },
                        { "any.type": "<typeId>" },
                        { "any.collections": { "$nin": ["bin"] } } ] },
  "sort": ["template.title"] }
```

The default template of a type or a collection is a client convention
on the definition's `meta`, next to `meta.defaultType`:
`meta.defaultTemplate: "<templateId>"`, through `PATCH …/types/:typeId`
or `PATCH …/collections/:collectionId`. The server does not read it.

Objects made from a template, and the ones behind its current version:

```json
{ "filter": { "any.template.id": "<templateId>" } }
{ "filter": { "any.template.id": "<templateId>",
              "any.template.version": { "$lt": 2 } } }
```

`any.template` is an ordinary synced row field: the server writes it
on create, a client may rewrite it after bringing an object up to date,
and a reader treats an absent one as "not made from a template".

## Every list excludes templates

A template matches `{"any.type": "<typeId>"}` and
`{"any.collections": "<collectionId>"}` like any member, because it is
one. Every ordinary list, picker, tree, dataview source and search
filter excludes templates the way it excludes the bin — the same
clause, one more id:

```json
{ "any.collections": { "$nin": ["bin", "template"] } }
```

`/search` does not know about templates either: a template's text
surfaces as a hit unless the `filter` carries the clause.

## Remove

A template is binned and deleted like any object. Objects made from it
keep `any.template.id` naming the dead id; ignore an id that is no
longer a template member, as with a dead collection id.
