---
title: Runtime datasets
description: Declare a schema-enforced dataset on your own type at runtime — required fields, write-once vs author-mutable, author-only delete, derived stamps, user-supplied ids, shared datasets read across objects, and declared indexes.
order: 90
---
# Runtime datasets

A runtime dataset is a storage collection you define under a **part** of one of your own types, at runtime, with a declarative schema — the `records` module ([modules](../types/index.html)). The schema syncs like any other data, and every peer enforces it on apply — required fields, who may edit a field after creation, who may delete a record, server-derived creator and time stamps, and whether record ids are auto-derived or caller-supplied.

The records live in a storage collection **namespaced to the type**, `<typeId>_<key>` — the `collection` every declaration reply and listing carries, and the `dataset` value on every read and write. Two types can each declare `entries` without colliding. Registered built-in types (`page`, `dataview`) refuse runtime definitions with `400 type.registered`. Runtime datasets are for *your* types — parts and datasets belong to types alone, never to a collection.

> **Why it matters.** There is no server-side function to put validation in. Every device applies every change, so the rules have to travel with the data. A dataset declaration is that rule set: an offline peer, a second device, and a member on another continent all reject the same malformed write, without ever agreeing on a leader.

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
| `key` | The dataset's slug inside the type (`[a-z][a-z0-9_]*`), pinned, unique among the type's parts and datasets → `409 dataset.key_conflict`. The storage collection is `<typeId>_<key>`. A records dataset requires it. An `editor` dataset may omit it, or name `editor_blocks`, and is then the module's canonical storage collection. |
| `module` | The serving module; absent = `records`. An `editor` dataset carries no `fields` (the module owns the schema — `409 dataset.module_owned`); `chat` is reserved to the server's catalog install (`400 dataset.module_reserved`); an unknown module is `400 dataset.module_unknown`. |
| `idRule` | `auto` (default: ids derived from the change, explicit client ids rejected) or `user` (caller-supplied, matched against `idPattern` / `idMaxLen`, defaults `[A-Za-z0-9._:-]+` / 128). |
| `deleteBy` | `anyone` (default) or `author` — requires a `stamp: creator` field; deletes by anyone else are dropped at apply. |
| `search` | `{title, text, scope?}` — which fields the search indexer extracts. `text` is one key or a non-empty array of keys joined in order. `scope` picks the index scope (default `basic`). |
| `dynamic` | Keep undeclared keys permitted. |
| `skipHistory` | Exclude the dataset from version history. |
| `shared` | Keep every object's records in one storage collection per space, readable across objects — [Shared datasets](#shared-datasets). A module dataset refuses it (`400 dataset.decl_invalid`). |
| `indexes` | Declared secondary indexes, `[{key, fields, sparse?}]` — [Declared indexes](#declared-indexes). |

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

Behavioral parts are pinned for the definition's life: `key`, `module`, `dynamic`, `idRule`/`idPattern`/`idMaxLen`, `deleteBy`, `skipHistory`, `shared`, every field's `key`/`kind`/`shape`/`scope`/`required`/`mutableBy`/`stamp`, and every part of an index. To change one, remove the definition and add a new one. The part's own display slice — `name`, `icon`, `pos`, `hidden`, `ui`, `uses` — patches through `PATCH …/types/:typeId/parts/:partId`; `DELETE …/parts/:partId` removes the part and every dataset under it.

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
- `DELETE …/datasets/:defId/fields/:fieldId` drops a field definition. Stored values stay; later writes to the field are rejected as undeclared on non-dynamic datasets. A removal that would invalidate the rest of the declaration (the creator stamp of an author-gated dataset) is refused, and so is the removal of a field a declared index names — `400 dataset.decl_invalid`; remove the index first.
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

A [shared dataset](#shared-datasets) carries `shared: true`, and a dataset with [declared indexes](#declared-indexes) lists them as `indexes: [{id, key, fields, sparse?, invalid?, invalidReason?}]` — `id` is what `DELETE …/indexes/:indexId` takes.

A definition whose folded declaration fails validation is listed with `invalid: true` and `invalidReason` — it never registers or accepts data, but stays visible so it can be repaired or removed. Definitions racing in from other members fold by key: the smallest definition id wins the pinned leaves, and a disagreement on one marks the fold invalid.

Runtime datasets also appear in the space's discovery document, `GET /v1/spaces/:spaceId/datasets`, under their storage collection name as JSON Schema with `owners` (the declaring type), `module` and the behavioral keywords `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id` / `x-id-pattern` / `x-id-max-length`, `x-search`, plus the standard `required` list. A shared dataset's entry adds `shared: true`, and `indexes` lists its valid declared indexes, as `[{key, fields, sparse?}]`. `any datasets $SPACE` prints the same.

## Writing and reading records

The data path is the ordinary dataset surface; a [shared dataset](#shared-datasets) adds one read scope across objects. Records live on an object of the declaring type:

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

## Shared datasets

A `records` dataset declared `"shared": true` keeps the records of every object of its type in one storage collection per space. A record still belongs to the object it is written on; what changes is that the dataset can also be read across objects. `shared` is pinned and a dataset that already exists keeps its storage, so declare it shared from the start.

A series kept per day object is the shape it fits: one `Day` object per date, one record per hour, and a chart that reads a week across seven objects. Declare the dataset inline in a part:

```sh
DAY=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types \
  -H 'Content-Type: application/json' \
  -d '{"name": "Day", "xKey": "day"}' | jq -r .typeId)

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$DAY/parts \
  -H 'Content-Type: application/json' -d '{
  "key": "activity", "name": "Activity", "ui": {"type": "table"},
  "datasets": [ {
    "key": "hours", "idRule": "user", "shared": true,
    "fields": [
      { "key": "start", "kind": "datetime", "required": true },
      { "key": "steps", "kind": "number",   "mutableBy": "any" } ],
    "indexes": [ { "key": "by_start", "fields": ["start"] } ] } ] }'
# → 201 {"partId": "…"}

HOURS=${DAY}_hours
```

`any type part add $SPACE $DAY --draft @activity-part.json` declares the same part from a file. The record ids are fixed-width hours (`2026-01-05T14`): with `idRule: user`, one object's time range is a range of `id` with no index, and a re-import through [upsert](upsert.html) is idempotent. Store a dense series in chunks like this — one record per hour, not one per sample — because every record carries its own version map and keys.

### Writing

Writes go through the per-object routes — `modify`, `delete-records`, `upsert` — exactly as on any dataset, and the object's type must declare the dataset:

```sh
MON=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$DAY'", "initialProperties": {"any": {"name": "2026-01-05"}}}' | jq -r .objectId)
TUE=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'Content-Type: application/json' \
  -d '{"type": "'$DAY'", "initialProperties": {"any": {"name": "2026-01-06"}}}' | jq -r .objectId)

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' -d '{
  "objectId": "'$MON'", "dataset": "'$HOURS'",
  "records": [
    { "id": "2026-01-05T14", "fields": { "start": {"$date": "2026-01-05T14:00:00Z"}, "steps": 812 } },
    { "id": "2026-01-05T15", "fields": { "start": {"$date": "2026-01-05T15:00:00Z"}, "steps": 1260 } } ] }'

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' -d '{
  "objectId": "'$TUE'", "dataset": "'$HOURS'",
  "records": [
    { "id": "2026-01-06T09", "fields": { "start": {"$date": "2026-01-06T09:00:00Z"}, "steps": 430 } } ] }'
```

The record id itself — auto-derived or caller-supplied — never holds a `/`.

### Reading one object, or every object

Every read returns a record with `id: "<objectId>/<recordId>"` and `_objectId: "<objectId>"`. One object's records are read through the per-object scope, as on any dataset — `…/query[/subscribe]` and `…/aggregate` with `objectId` + `dataset`:

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$MON'", "dataset": "'$HOURS'", "sort": ["id"]}'
```

Every object's records are read through the dataset scope. The body names `dataset` — the storage collection, `<typeId>_<key>` — and no `objectId`; the reply shapes and the SSE frames are the per-object ones:

```
POST /v1/spaces/:spaceId/datasets/query             { dataset, filter?, sort?, limit?, offset?, includeTotal?, projection? }
POST /v1/spaces/:spaceId/datasets/query/subscribe   same body; SSE
POST /v1/spaces/:spaceId/datasets/aggregate         { dataset, pipeline, groupLimit?, accumArrayLimit?, memoryLimitBytes?, explain? }
```

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/datasets/query \
  -H 'Content-Type: application/json' -d '{
  "dataset": "'$HOURS'",
  "filter": {"start": {"$gte": {"$date": "2026-01-05T15:00:00Z"}}},
  "sort": ["start"], "limit": 100, "projection": {"_ver": -1}}'
```

```json
{ "records": [
    { "id": "bafy…mon/2026-01-05T15", "_objectId": "bafy…mon",
      "start": { "$date": "2026-01-05T15:00:00.000Z" }, "steps": 1260 },
    { "id": "bafy…tue/2026-01-06T09", "_objectId": "bafy…tue",
      "start": { "$date": "2026-01-06T09:00:00.000Z" }, "steps": 430 } ] }
```

The filter and sort lead with `start`, so the `by_start` index serves them as a range read ([Indexes](indexes.html#runtime-datasets)). The read covers the records this device has synced; no object is opened for it. A projection that lists fields to include drops `_objectId`, since `id` carries the object — add `"_objectId": 1` to keep it.

`_ver` orders the changes of one object only. Records of different objects share no version order, so the dataset scope sorts on a declared field — `start` here.

The same scope takes a pipeline and a live window:

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/datasets/aggregate \
  -H 'Content-Type: application/json' -d '{
  "dataset": "'$HOURS'",
  "pipeline": [ {"$group": {"_id": "$_objectId", "steps": {"$sum": "$steps"}}},
                {"$sort": {"steps": -1}} ] }'
# → {"records": [{"id": "bafy…mon", "steps": 2072}, {"id": "bafy…tue", "steps": 430}]}

any aggregate $SPACE --all-objects --dataset $HOURS \
  --pipeline '[{"$group":{"_id":"$_objectId","steps":{"$sum":"$steps"}}}]'

any query-subscribe $SPACE --all-objects --dataset $HOURS --sort -start --limit 48
```

A dataset-scope subscription carries the changes of every object's records. Deleting an object removes its records, and the stream sees them leave as `removed` entries in batches with an empty `versionId` — apply them directly. Give the stream a `filter`, or a `sort` with a `limit`: an unbounded one holds every record of the dataset ([Subscribe](../realtime/subscribe.html)).

### Record ids on writes

A write takes the plain record id, or the `id` a read returned for a record of the request's own `objectId`. `modify` returns `recordIds` in the `<objectId>/<recordId>` form:

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/modify \
  -H 'Content-Type: application/json' -d '{
  "objectId": "'$MON'", "dataset": "'$HOURS'",
  "records": [ { "id": "2026-01-05T14",
                 "ops": [ { "type": "$set", "path": "steps", "value": 900 } ] } ] }'
# → {"versionId": "…", "changeId": "…", "recordIds": ["bafy…mon/2026-01-05T14"]}
```

Another object's record — `$TUE/2026-01-06T09` in a write to `$MON` — is refused whole on `modify` and `delete-records`, and comes back as a per-record rejection on `upsert`:

```json
{ "error": { "code": "record.wrong_object", "message": "…",
             "details": { "objectId": "…", "dataset": "…" } } }
```

### Errors, links and search

- `400 dataset.not_shared` — a dataset-scope read names a dataset that is not declared `shared`, or one the space does not hold (`details.dataset`). Read such a dataset per object through `…/query`.
- `405 space.unsupported` — the dataset scope on the tech space.
- In an `any://` record link, a search hit and a link source, `recordId` is the plain record id next to its `objectId`: `any://o/<spaceId>/<objectId>/<typeId>_hours/2026-01-05T14` ([Links](../types/links.html)).

> **Why it matters.** In a hosted database "every hour of every day" is one table with a foreign key, and ownership is a column the server enforces. Here the object is the unit of sync and access: a record is written on its object and leaves when that object is deleted. `shared` adds the read across objects without moving that ownership, and the read answers from this device's replica — no object is opened and no peer is asked.

## Declared indexes

A `records` dataset declares secondary indexes in the draft's `indexes` — `by_start` above — or later, also on a dataset that already holds records:

```sh
DEF=$(curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/types/$DAY/datasets \
  | jq -r '.datasets[] | select(.key == "hours") | .id')

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$DAY/datasets/$DEF/indexes \
  -H 'Content-Type: application/json' \
  -d '{"key": "by_steps", "fields": ["-steps", "_objectId"]}'
# → 201 {"indexDefId": "…"}

curl -s -X DELETE http://127.0.0.1:7001/v1/spaces/$SPACE/types/$DAY/datasets/$DEF/indexes/$INDEX
# → 204
```

`$INDEX` is the `indexDefId` the add returned, which the definition listing shows as the index's `id`. The CLI:

```sh
any type part dataset index add    $SPACE $DAY $DEF --index '{"key":"by_steps","fields":["-steps","_objectId"]}'
any type part dataset index remove $SPACE $DAY $DEF $INDEX
```

| Field | Meaning |
|---|---|
| `key` | The index's slug, unique within the dataset. |
| `fields` | One to four paths, in order; a `-` prefix keeps a path descending. Each names a declared field of kind `string`, `number`, `boolean` or `datetime`, or `_ver.id` (creation order within one object); a shared dataset also takes `_objectId`. |
| `sparse` | Leave a record out of the index unless it carries every indexed field. |

A dataset holds at most eight indexes, and none is unique. Every part of an index is pinned: replace one by removing it and adding another. A filter or sort on a leading run of `fields` is a range read; a field deeper in the index helps only once the fields before it are pinned by equality ([Indexes](indexes.html#runtime-datasets)).

A malformed or unsatisfiable draft — an unknown field, a field of another kind, a taken key, a ninth index — is `400 dataset.decl_invalid`; an index on a module dataset is `409 dataset.module_owned`; an unknown `defId` or `indexId` is `404 sdk.not_found`.

### How each device builds them

Definitions sync, so every device holds the same set, and each device builds its own indexes:

- **A shared dataset** is indexed once per space, in the background, when the definition reaches the device. The build reads the whole collection, and **every write on the server waits until it ends**; queries on the dataset scan until then. While `index.enabled`, the build shows in the [process view](../notifications/processes.html) as `dataset.index.<spaceId>`.
- **A per-object dataset** indexes each object's records the next time that object's dataset is read or written.

Declare the index a cross-object read needs with the dataset, while it is empty: an index added later is built over every record, and every write on the device waits for that build.

`GET …/types/:typeId/datasets` lists every index, an invalid one included: an index naming a field that is not a declared scalar field of the dataset is built nowhere and stays listed, with `invalid: true` and `invalidReason`, until removed. The discovery document, `GET /v1/spaces/:spaceId/datasets`, lists only the built ones.

> **Why it matters.** A hosted database runs `CREATE INDEX` once, on the server every query goes to. Here every device answers its own queries from its own replica, so an index is a definition that syncs with the schema and each device builds it over the records it holds — a late index on a large shared dataset is a build on every device, which is why the cheap moment to declare one is with the dataset.
