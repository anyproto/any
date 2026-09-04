---
title: Runtime datasets
description: Declare a schema-enforced dataset on your own type at runtime — required fields, write-once vs author-mutable, author-only delete, derived stamps, user-supplied ids.
order: 90
---
# Runtime datasets

A runtime dataset is a collection you define under a **part** of one of your own types, at runtime, with a declarative schema — the `records` module ([modules](../types/index.html)). The schema syncs like any other data, and every peer enforces it on apply — required fields, who may edit a field after creation, who may delete a record, server-derived creator and time stamps, and whether record ids are auto-derived or caller-supplied.

The records live in a collection **namespaced to the type**, `<typeId>_<key>` — the `collection` every declaration reply and listing carries, and the `dataset` value on every read and write. Two types can each declare `entries` without colliding. Registered built-in types (`data_view`, `nav`) refuse runtime definitions with `400 type.registered`. Runtime datasets are for *your* types.

> **Why it matters.** There is no server-side function to put validation in. Every device applies every change, so the rules have to travel with the data. A dataset declaration is that rule set: an offline peer, a second device, and a member on another continent all reject the same malformed write, without ever agreeing on a leader.

## Declaring a dataset

A dataset belongs to a part — the display unit a client renders it in. Declare the part first (or inline the dataset in the part's `datasets`):

```sh
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

any type part add    $SPACE $TYPE --draft '{"key":"articles","name":"Articles"}'
any type dataset add $SPACE $TYPE $PART --draft @articles.json
```

| Field | Meaning |
|---|---|
| `key` | The dataset's slug inside the type (`[a-z][a-z0-9_]*`), pinned, unique among the type's parts and datasets → `409 dataset.key_conflict`. The collection is `<typeId>_<key>`. |
| `module` | The serving module; absent = `records`. `editor` / `chat` datasets carry no `fields` (the module owns the schema — `409 dataset.module_owned`); an unknown module is `400 dataset.module_unknown`. |
| `shared` | Use the module's canonical collection instead of a namespaced one (`editor_blocks`, `chat_messages`); never for `records` → `400 dataset.shared_conflict`. |
| `idRule` | `auto` (default: ids derived from the change, explicit client ids rejected) or `user` (caller-supplied, matched against `idPattern` / `idMaxLen`, defaults `[A-Za-z0-9._:-]+` / 128). |
| `deleteBy` | `anyone` (default) or `author` — requires a `stamp: creator` field; deletes by anyone else are dropped at apply. |
| `search` | `{title, text, scope?}` — which fields the search indexer extracts. `text` is one key or a non-empty array of keys joined in order. `scope` picks the index scope (default `basic`). |
| `dynamic` | Keep undeclared keys permitted. |
| `skipHistory` | Exclude the dataset from version history. |

Per field:

| Field | Meaning |
|---|---|
| `key`, `kind` | Field name and leaf kind. `kind` may be omitted on stamped fields (creator ⇒ string, times ⇒ datetime instant). |
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

any type dataset patch $SPACE $TYPE $DEF --set '{"displayName":"Posts"}'
```

A pinned path → `400 dataset.immutable`; an unknown `defId` → `404 sdk.not_found`. A search-mapping patch applies as records re-index; already-indexed docs keep their extracted text until their object is next written.

## Evolution is additive

- `POST …/datasets/:defId/fields` → `201 {fieldDefId}` appends a field. An added field is never `required` — it would reject the dataset's own history on fresh devices.
- `DELETE …/datasets/:defId/fields/:fieldId` drops a field definition. Stored values stay; later writes to the field are rejected as undeclared on non-dynamic datasets. A removal that would invalidate the rest of the declaration (the creator stamp of an author-gated dataset) is refused.
- `DELETE …/datasets/:defId` tombstones the definition. Existing data is not cleaned up; subsequent writes drop once peers apply the removal; the search index evicts lazily.

```sh
any type dataset field add    $SPACE $TYPE $DEF --field '{"key":"notes","kind":"string","mutableBy":"any"}'
any type dataset field remove $SPACE $TYPE $DEF $FIELD
any type dataset remove       $SPACE $TYPE $DEF
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

Runtime datasets also appear in the space's discovery document, `GET /v1/spaces/:spaceId/datasets`, under their collection name as JSON Schema with `owners` (the declaring type), `module` and the behavioral keywords `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id` / `x-id-pattern` / `x-id-max-length`, `x-search`, plus the standard `required` list. `any datasets $SPACE` prints the same.

## Writing and reading records

The data path is the ordinary dataset surface — no new endpoints:

```sh
# write (the object must carry the owning type; attach it at create via "types")
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/modify -d '{
  "objectId": "'$OBJ'", "dataset": "'$TYPE'_articles",
  "records": [ { "id": "a1", "ops": [
    { "type": "$set", "path": "", "value": { "title": "Hello", "body": "…" } } ] } ] }'

# read
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -d '{"objectId": "'$OBJ'", "dataset": "'$TYPE'_articles", "sort": ["-createdAt"]}'
```

See [Writing data](writing-data.html) and [Reading data](reading-data.html) for the full `modify` / `query` bodies, and [Subscribe](../realtime/subscribe.html) for live updates. `idRule: user` datasets additionally get idempotent batch ingest through [Upsert](upsert.html).

> **Note.** Records live on objects carrying the owning type, so the first write to a fresh object fails with `400 dataset.not_declared` until the type is attached. Stamped fields (`creator`, `createTime`, `modifyTime`) are instants and identities the server fills in; a payload that sets them is rejected.
