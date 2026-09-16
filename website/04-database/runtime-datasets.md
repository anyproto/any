---
title: Runtime datasets
description: Declare a schema-enforced dataset on your own type at runtime — required fields, write-once vs author-mutable, author-only delete, derived stamps, user-supplied ids.
order: 90
---
# Runtime datasets

A runtime dataset is a storage collection you define under a **part** of one of your own types, at runtime, with a declarative schema — the `records` module ([modules](../types/index.html)). The schema syncs like any other data, and every peer enforces it on apply — required fields, who may edit a field after creation, who may delete a record, server-derived creator and time stamps, and whether record ids are auto-derived or caller-supplied.

The records live in a storage collection **namespaced to the type**, `<typeId>_<key>` — the `collection` every declaration reply and listing carries, and the `dataset` value on every read and write. Two types can each declare `entries` without colliding. Registered built-in types (`page`, `dataview`) refuse runtime definitions with `400 type.registered`. Runtime datasets are for *your* types — parts and datasets belong to types alone, never to a collection.

The dataset declaration syncs with the data. Each peer applies its schema and authorship rules when processing changes, including changes received after an offline period.

## Declaring a dataset

A dataset belongs to a part — the display unit a client renders it in — and a part belongs to a type. Create the type, declare the part (or inline the dataset in the part's `datasets`), then the dataset:

```sh
TYPE=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types \
  -H 'Content-Type: application/json' \
  -d '{"name": "Blog", "xKey": "blog"}' | jq -r .typeId)

PART=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/parts \
  -H 'Content-Type: application/json' \
  -d '{"key": "articles", "name": "Articles", "ui": {"type": "table"}}' | jq -r .partId)

curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/parts/$PART/datasets \
  -H 'Content-Type: application/json' -d '{
  "key": "articles", "displayName": "Articles",
  "idRule": "user", "deleteBy": "author",
  "search": { "title": "title", "text": "body" },
  "fields": [
    { "key": "title",     "kind": "string", "required": true, "mutableBy": "author" },
    { "key": "body",      "kind": "string", "mutableBy": "author" },
    { "key": "author",    "stamp": "creator" },
    { "key": "createdAt", "stamp": "createTime" },
    { "key": "updatedAt", "stamp": "modifyTime" }
  ] }'
# → 201 {"datasetDefId": "…", "collection": "<typeId>_articles"}
```

The same with the CLI, `articles.json` holding the dataset body above:

```sh
TYPE=$(any type create $SPACE --name Blog --xkey blog | jq -r .typeId)
PART=$(any type part add $SPACE $TYPE \
  --draft '{"key":"articles","name":"Articles","ui":{"type":"table"}}' | jq -r .partId)
any type part dataset add $SPACE $TYPE $PART --draft @articles.json
```

| Field | Meaning |
|---|---|
| `key` | The dataset's slug inside the type (`[a-z][a-z0-9_]*`), pinned, unique among the type's parts and datasets → `409 dataset.key_conflict`. The storage collection is `<typeId>_<key>`. |
| `module` | The serving module; absent = `records`. An `editor` dataset carries no `fields` (the module owns the schema — `409 dataset.module_owned`); `chat` is reserved to the server's catalog install (`400 dataset.module_reserved`); an unknown module is `400 dataset.module_unknown`. |
| `shared` | Use the module's canonical storage collection instead of a namespaced one (`editor_blocks`, `chat_messages`); never for `records` → `400 dataset.shared_conflict`. |
| `idRule` | `auto` (default: ids derived from the change, explicit client ids rejected) or `user` (caller-supplied, matched against `idPattern` / `idMaxLen`, defaults `[A-Za-z0-9._:-]+` / 128). |
| `deleteBy` | `anyone` (default) or `author` — requires a `stamp: creator` field; deletes by anyone else are dropped at apply. |
| `search` | `{title, text, scope?}` — which fields the search indexer extracts. `text` is one key or a non-empty array of keys joined in order. `scope` picks the index scope (default `basic`). |
| `dynamic` | Keep undeclared keys permitted. |
| `skipHistory` | Exclude the dataset from version history. |

Per field:

| Field | Meaning |
|---|---|
| `key`, `kind` | Field name and leaf kind. `kind` may be omitted on stamped fields (creator ⇒ string, times ⇒ datetime instant). `shape` (`{kind, items?, properties?}`) declares a nested shape instead. |
| `name`, `description`, `xFormat` | The descriptive slice — the same descriptor a property carries ([Data types](data-types.html)), checked against the field's kind. |
| `scope` | The field's [scope](data-types.html); default `synced`. |
| `required` | Must be present on create. Declarable only at creation; incompatible with `stamp`. |
| `mutableBy` | Absent = write-once (writable only in the creating change). `author` (needs a creator stamp) or `any` allow later edits. Each allowed edit bumps `modifyTime` when declared. |
| `stamp` | `creator` / `createTime` / `modifyTime` — derived at apply, client writes rejected. |

A malformed declaration — unknown enum labels, `mutableBy: author` without a creator stamp, duplicate stamp kinds — fails with `400 request.invalid_field` or `400 dataset.decl_invalid`.

## What's pinned and what patches

Behavioral parts are pinned for the definition's life: `key`, `module`, `shared`, `dynamic`, `idRule`/`idPattern`/`idMaxLen`, `deleteBy`, `skipHistory`, and every field's `key`/`kind`/`shape`/`scope`/`required`/`mutableBy`/`stamp`. To change one, remove the definition and add a new one. The part's own display slice — `name`, `icon`, `pos`, `hidden`, `ui`, `uses` — patches through `PATCH …/types/:typeId/parts/:partId`; `DELETE …/parts/:partId` removes the part and every dataset under it.

Display parts patch through `PATCH …/datasets/:defId` with the same `{set, unset}` shape as a property patch, over `description`, `displayName`, `search.title`, `search.text`, `search.scope`:

```sh
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/datasets/$DEF \
  -d '{"set": {"displayName": "Posts", "search.text": ["body", "notes"]}}'

any type part dataset patch $SPACE $TYPE $DEF --set '{"displayName":"Posts"}'
```

A field's display slice — `name`, `description` and every path under `xFormat` — patches through `PATCH …/datasets/:defId/fields/:fieldId` under the property PATCH rules (a `set` targets a leaf); its behavioral declaration stays pinned:

```sh
any type part dataset field patch $SPACE $TYPE $DEF $FIELD --set '{"xFormat.type":"longtext"}'
```

A pinned path → `400 dataset.immutable`; an unknown `defId` → `404 sdk.not_found`. A search-mapping patch applies as records re-index; already-indexed docs keep their extracted text until their object is next written.

## Evolution is additive

- `POST …/datasets/:defId/fields` → `201 {fieldDefId}` appends a field. An added field is never `required` — it would reject the dataset's own history on fresh devices.
- `DELETE …/datasets/:defId/fields/:fieldId` drops a field definition. Stored values stay; later writes to the field are rejected as undeclared on non-dynamic datasets. A removal that would invalidate the rest of the declaration (the creator stamp of an author-gated dataset) is refused.
- `DELETE …/datasets/:defId` tombstones the definition. Existing data is not cleaned up; subsequent writes drop once peers apply the removal; the search index evicts lazily.

```sh
any type part dataset field add    $SPACE $TYPE $DEF --field '{"key":"notes","kind":"string","mutableBy":"any"}'
any type part dataset field remove $SPACE $TYPE $DEF $FIELD
any type part dataset remove       $SPACE $TYPE $DEF
```

## Reading the definition back

`GET …/types/:typeId/datasets` returns the compiled view (`GET …/parts` nests the same definitions under their parts):

```json
{ "datasets": [ { "id": "…", "key": "articles", "collection": "<typeId>_articles",
    "module": "records", "partId": "…", "displayName": "Articles",
    "idRule": "user", "deleteBy": "author",
    "search": { "title": "title", "text": "body" },
    "fields": [ { "id": "…", "key": "title", "kind": "string", "scope": "synced",
                  "required": true, "mutableBy": "author" } ] } ] }
```

A definition whose folded declaration fails validation is listed with `invalid: true` and `invalidReason` — it never registers or accepts data, but stays visible so it can be repaired or removed. Definitions racing in from other members fold by key: the smallest definition id wins the pinned leaves, and a disagreement on one marks the fold invalid.

Runtime datasets also appear in the space's discovery document, `GET /v1/spaces/:spaceId/datasets`, under their storage collection name as JSON Schema with `owners` (the declaring type), `module` and the behavioral keywords `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id` / `x-id-pattern` / `x-id-max-length`, `x-search`, plus the standard `required` list. `any datasets $SPACE` prints the same.

## Writing and reading records

The data path is the ordinary dataset surface — no new endpoints. Records live on an object of the declaring type:

```sh
OBJ=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$TYPE'", "initialProperties": {"any": {"name": "My blog"}}}' | jq -r .objectId)

# write — "upsert": true creates the record; without it the op only updates an existing id
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/modify \
  -H 'Content-Type: application/json' -d '{
  "objectId": "'$OBJ'", "dataset": "'$TYPE'_articles",
  "records": [ { "id": "a1", "upsert": true, "ops": [
    { "type": "$set", "path": "", "value": { "title": "Hello", "body": "…" } } ] } ] }'
# → {"versionId": "…", "changeId": "…", "recordIds": ["a1"]}

# read
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$OBJ'", "dataset": "'$TYPE'_articles", "sort": ["-createdAt"]}'
```

A `200` does not mean every record landed. The change commits, and whatever the handler refused at apply comes back in `rejections` — the same write without `upsert` on a fresh dataset is one, since strict mode updates only ids that exist:

```json
{ "versionId": "…", "changeId": "…", "recordIds": ["a1"],
  "rejections": [ { "recordIndex": 0, "recordId": "a1", "opIndex": -1, "reason": "…" } ] }
```

`opIndex: -1` means the whole record was refused. A clean write has no `rejections` key, so check for it before treating a write as done.

See [Writing data](writing-data.html) and [Reading data](reading-data.html) for the full `modify` / `query` bodies, and [Subscribe](../realtime/subscribe.html) for live updates. `idRule: user` datasets additionally get idempotent batch ingest through [Upsert](upsert.html).

> **Note.** Records live on objects of the owning type, so a write fails with `400 dataset.not_declared` until that type is set — at create through `type`, or later through `POST …/properties/:objectId/type/:typeId`. A definition object hosts its own records, so the type itself is a valid `objectId`. Stamped fields (`creator`, `createTime`, `modifyTime`) are instants and identities the server fills in; a payload that sets them is rejected.
