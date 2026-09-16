---
title: Upsert
description: Idempotent batch ingest into a runtime dataset with user-supplied ids — re-running the same batch writes nothing.
order: 100
---
# Upsert

`POST /v1/spaces/:spaceId/upsert` ingests a batch of records into a [runtime dataset](runtime-datasets.html) declared with `idRule: user`. Each record is keyed by the id you supply: absent ids are created, present ids are diffed field by field, and identical records are skipped. Re-running an identical batch is a no-op.

This is the surface for imports, syncs from an external system, and any job that runs on a schedule and must not duplicate what it wrote last time.

## Request

```sh
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' -d '{
  "objectId": "'$OBJ'", "dataset": "'$TYPE'_articles",
  "records": [
    { "id": "a1", "fields": { "title": "Hello", "body": "First draft" } },
    { "id": "a2", "fields": { "title": "World", "body": "…" } }
  ],
  "pageSize": 500,
  "traceIds": ["import-42"] }'

any upsert $SPACE $OBJ --dataset "${TYPE}_articles" --records @batch.json --page-size 500 --trace-id import-42
```

| Field | Meaning |
|---|---|
| `objectId` | The object hosting the dataset — it must carry the dataset's owning type. |
| `dataset` | The runtime dataset's storage collection, `<typeId>_<key>` — the `collection` its declaration returned. |
| `records[]` | `{id, fields}` — `id` must match the dataset's `idPattern` / `idMaxLen`. |
| `pageSize` | Records per CRDT change, default 500. |
| `traceIds` | Optional trace ids stamped on every page's change. |

## What happens per record

| Stored state | Action |
|---|---|
| id absent | Created in one multi-field set. |
| id present, some declared-mutable fields differ | Each changed field lands as a single-path set. Write-once fields are compared but never rewritten. |
| id present, identical | Skipped — no change, no DAG growth. |
| id tombstoned | Rejected `upsert.record_deleted` — ids never reuse. |

One CRDT change is written per page, so a 2 000-record batch at the default page size is four changes, each returned in `pages`.

## Response

The call answers `200` even when some records were rejected — the same partial-success stance as `/modify`:

```json
{ "pages": [ { "versionId": "…", "changeId": "…", "recordIds": [] } ],
  "created": 1, "updated": 1, "skipped": 0,
  "rejections": [
    { "index": 3, "id": "a4", "code": "upsert.immutable_field", "reason": "…" } ] }
```

| Rejection code | Meaning |
|---|---|
| `upsert.immutable_field` | The payload would change a write-once field. |
| `upsert.not_author` | Author-mutable field on a record another identity created. |
| `upsert.record_deleted` | The stored record is a tombstone. |
| `upsert.rejected` | Creation screening failed — missing required field, id pattern or length violation, undeclared field on a non-dynamic dataset, write to a stamped field. `reason` carries the specific cause. |

Whole-call errors: `400 upsert.requires_user_ids` when the dataset is not declared `idRule: user`, `400 dataset.unknown` when the space serves no records storage collection of that name (a module's own, such as `chat_messages`, is never upsertable), and `400 dataset.not_declared` when the object's type does not declare it.

## Concurrency

Upsert is not transactional against concurrent writers. The intended deployment is a single ingest writer per dataset — a scheduled [program](../programs/index.html) or one importer process. Concurrent creates of the same id by *different members* are outside the convergence contract; the same member re-running its own batch is exactly the supported case.

> **Why it matters.** A hosted backend gives you idempotency through a server that dedupes on your behalf. Here the dedupe is a local diff against the replica you already hold, so an importer that crashes halfway and restarts, or a cron job that runs twice, converges on the same data without a coordination service — and the changes it skips never cost the network anything.

> **Note.** Only fields declared `mutableBy: author` or `mutableBy: any` are ever updated on an existing record. If your source system changes a field you declared write-once, the upsert reports `upsert.immutable_field` for that record rather than silently ignoring the difference.
