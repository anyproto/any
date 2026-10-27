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
| `any.collections` minus the built-in markers (`template`, `bin`, `miniapp`) | the new object's collections |
| `<typeId>.<propId>` and `<collectionId>.<propId>` values the definitions still declare | the same values, under the template's type and its own non-marker collections; a collection the request adds brings no values |
| the live records of every dataset the type's parts declare, except a chat's | copied records: editor blocks keep their ids, so the tree holds; a `records` dataset with derived ids (`idRule: auto`, the default) gets fresh ones; stamps (`creator`, `createTime`, …) are re-derived for the copier |

The collection carries two properties, under `template.<propId>` on the
template only:

| property | kind | |
|---|---|---|
| `title` | string | the label a template picker shows — not the pre-filled `any.name`, which may be empty |
| `version` | number | the template's version; a client increments it after changing the template, and an object records the one it was made from (below) |

A template for a collection's rows is the same thing with the
collection in `any.collections`: the template holds that collection's
column values and the new object is filed under it. Placement values
travel verbatim — a wiki template's `parentId` / `pos` land on every
copy — so a tree client sends its own placement in `initialProperties`.

## Make a template

Create an object of the target type filed under `template`, edit it
as usual, then set `template.version` once the content is in — a
template without a version stamps `0`:

```json
POST /v1/spaces/:spaceId/objects
{ "type": "<typeId>",
  "collections": ["template", "<collectionId>"],
  "initialProperties": {
    "template": { "title": "Weekly review" },
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

The `template` group is admitted only with the marker in
`collections` (`400 dataset.not_declared` otherwise), and a copy
carries nothing under a marker: a duplicate of a template — the same
request with a template as `from` — sends `title` and `version` again.

Bump `template.version` **after** a change objects made from the
template should follow — the body and the version are separate writes,
and bumping first would let a copy taken in between carry the new
number with the old body:

```json
POST /v1/spaces/:spaceId/properties/<templateId>/set/template
{ "patch": { "version": 2 } }
```

The number is last-writer-wins. Two devices that both bump 1 → 2 with
different edits leave one version naming two contents; treat it as a
hint for "which objects to look at", not a proof of sameness.

## Copy an object: `from`

`POST /v1/spaces/:spaceId/objects` takes a fourth key, `from`: the id
of an object of the same space to copy. With it, `type` is optional
and must equal the source's type when given
(`400 template.type_mismatch`; an unknown id is `404 type.not_found`,
as on any create), `collections` are **added** to the source's, and
`initialProperties` are written over the copied values, property by
property, so a request value wins and the rest of its group stays
copied. A copied value can be replaced, never cleared: send an empty
value of the kind, not `null`, and clear it after the create with a
`null` patch on `…/set/:ownerId`. `initialProperties.any.type` and
`any.collections` are ignored on every create: membership comes from
`type` and `collections` only.

The copy takes the source's synced `any.*` values, its collections
minus the built-in markers, the declared synced values under the
source's type and its own non-marker collections (a collection the
request adds brings no values from the source; a value of local or
account scope, one under a property its definition no longer declares
or under a collection the space no longer has, stays behind, while a
collection id the space no longer has is carried as it is) and the
records of every dataset
the source's type declares through its parts, except the chat's (a
transcript is its authors' and never travels) and an invalid
declaration. A record field
declared local, derived or stamped stays behind too, and on a dataset
that is not `dynamic` so does a field no definition declares any more.
The derived row-root stamps
(`createdAt`, `modifiedAt`, `modifiedBy`) are the copy's own, and the
link index re-derives links from the copy's records.

Errors: an unknown `from`, or one whose row has not synced to this
device yet, is `404 object.not_found`; a deleted one `410
object.deleted`; a definition's own row (a type or collection root, the
general chat included) `400 membership.meta_type`; a record of the
source its dataset refuses today (a required field unset since) `409
template.record_refused` — the half copy is deleted (a delete that
fails too is logged and the object stays), the source needs repair.

When the source is a template — it is filed under `template` — the new
row is stamped

```json
"any.template": { "id": "<templateId>", "version": 2 }
```

with the template's `template.version` (`0` when it has none). Any
other source is a plain duplicate: a stamp it carries is copied along,
so a duplicate of an object made from a template keeps that lineage.

```json
POST /v1/spaces/:spaceId/objects
{ "from": "<templateId>",
  "initialProperties": { "any": { "name": "Weekly review — 13 Oct" } } }
→ 201 {"objectId": "…"}
```

The reply comes after the whole copy: open the new object from the
`201`, never from a subscription event. The row is one change and each
dataset follows in changes of its own, up to 1000 records each, every
one moving the row's `modifiedAt`, and nothing on the row marks the
copy as complete — so a subscriber, here or on a peer, sees the typed,
named row and then its body in pieces ([`04-events.md`](04-events.md)
§ Datasets and how clients should consume them). A copy that fails
midway is deleted: `added`, then `removed` with `reason: "deleted"`.
A later edit of the template never changes an object made from it.

### Not copied

- **Chat messages.** A transcript is its authors' (`creator` and
  `createdAt` come from the change envelope); the copy's chat starts
  empty.
- **Files.** A file is bound to the object it was attached to. A copy
  keeps the template's file links (body images, a file-valued
  property), which resolve while the template and its files exist and
  break when either is deleted. To make a file the copy's own, attach
  its bytes to the copy ([`17-files.md`](17-files.md); the per-space
  dedup keeps one blob) and rewrite the link in the block or the value.
- **Saved views.** Views live on the template's `dataview` object, not
  on the template ([`24-data-views.md`](24-data-views.md)). Copy that
  object with `from` too, with `initialProperties.dataview.host` set to
  the new object. Never `from` a dataview object without re-pointing
  its host, or the copy's views show on the template's host as well.
- **Links into the template.** A relation value or a body link that
  names the template itself keeps naming it.

## Pick, default, outdated

Templates for a type, for a picker:

```json
POST /v1/spaces/:spaceId/objects/query
{ "filter": { "$and": [ { "any.collections": "template" },
                        { "any.type": "<typeId>" },
                        { "any.collections": { "$nin": ["bin"] } } ] },
  "sort": ["template.title"] }
```

For a collection's rows use `{"any.collections": {"$all": ["template",
"<collectionId>"]}}` instead of the type clause. A template without
`title` is labelled by its `any.name`. A type's template copied with
`collections: ["<collectionId>"]` lands in the collection too; whether
a collection's picker offers the type's templates is the client's
choice.

The default template of a user type or a user collection is a client
convention on the definition's `meta`, as `meta.defaultType` is on a
collection: `meta.defaultTemplate: "<templateId>"`, through
`PATCH …/types/:typeId` or `PATCH …/collections/:collectionId`. The
server does not read it; a default that no longer names a template
outside the bin is ignored. A registered definition takes no `meta`
(`400 type.registered` for `page` and `dataview`,
`400 collection.registered` for `bin`, `miniapp` and `template`): a
default for plain pages lives on the collection the page is created
in, or in the client's own state.

Objects made from a template, and the ones behind its current version:

```json
{ "filter": { "$and": [ { "any.type": "<typeId>" },
                        { "any.template.id": "<templateId>" },
                        { "any.collections": { "$nin": ["template"] } } ] } }
{ "filter": { "$and": [ { "any.type": "<typeId>" },
                        { "any.template.id": "<templateId>" },
                        { "any.template.version": { "$lt": 2 } },
                        { "any.collections": { "$nin": ["template"] } } ] } }
```

`any.template` is not indexed: keep the `any.type` clause so the scan
stays within the type. The `$nin` keeps out a template that was itself
copied from the template.

There is no merge endpoint, and the stamp names a version, not a
change: the template's content at an older version is not
retrievable. Bringing an object up to date is the client's own write
of the template's current values and body over the object, then of
the stamp:

```json
POST /v1/spaces/:spaceId/properties/<objectId>/set/any
{ "patch": { "template": { "id": "<templateId>", "version": 2 } } }
{ "patch": { "template": null } }                                 // detach
```

`any.template` is an ordinary synced field of the `any` group, listed
by `GET …/types/any/properties` as kind `object` with no descriptor:
the server writes it on copy and checks nothing on a rewrite. A
property panel shows it read-only, as it shows `any.type`. A reader
treats an absent one as "not made from a template".

## Every list excludes templates

A template matches `{"any.type": "<typeId>"}` and
`{"any.collections": "<collectionId>"}` like any member, because it is
one. Every ordinary list, picker, tree, dataview source and search
filter excludes templates the way it excludes the bin — the same
clause, one more id:

```json
{ "any.collections": { "$nin": ["bin", "template"] } }
```

The sidebar is the exception: it lists what was pinned, a template
included. Reads that carry no object membership cannot exclude
templates: `/search` hits without the `filter`, a shared dataset read
across its objects (`…/datasets/query`), backlinks and links. Filter
those by the hit's object row.

## Remove

A template is binned and deleted like any object; a binned one is
still a template, out of the picker by the `bin` clause, and `from` on
it still works. Objects made from it keep `any.template.id`: a binned
template still counts as theirs; one unfiled from `template`, deleted
(`410`) or not synced here (`404`) reads as "template gone", never as
outdated — as with a dead collection id.
