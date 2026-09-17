---
title: Writing data
description: Property writes keyed by propId, the generic /modify endpoint with $set/$unset/$inc/$addToSet/$pull ops, local-scope writes, record deletes, and the ModifyResult receipt every write returns.
order: 60
---
# Writing data

Writes are purpose-built endpoints, not a generic document PUT: property values go through the owner-scoped `set`, dataset records through `/modify`, and the chat and editor modules through their own handlers. Every one of them returns the same receipt.

## The write receipt

```json
{ "versionId": "<change VersionId>",
  "changeId":  "<changeId>",
  "recordIds": ["<id>"],
  "rejections": [] }
```

| Field | Meaning |
|-------|---------|
| `versionId` | The change's position in the object's DAG. Stamp `_ver.<path> = versionId` on the records you just wrote so the matching live event is recognised as your own and not double-applied. |
| `changeId` | The CID of the change — also the handle for [version history](version-history.html). Empty for local-scope writes. |
| `recordIds` | Mirrors the input record order; `recordIds[0]` is the derived id of a created record. |
| `rejections` | Per-op refusals: `{recordIndex, recordId?, opIndex, reason}`. `opIndex: -1` means the whole record was rejected. The write itself succeeded for everything else. |

No write returns the record body. Read it back through [`/query`](reading-data.html).

> **Why it matters.** A write is a local append to the object's change DAG. It succeeds offline, is visible to the next query immediately, and syncs when a peer is reachable. `versionId` is what lets a client order its own writes against remote ones without a server clock.

## Property values

Values are stored at `record[ownerId][propId]`, the **owner** being the object's type or one of its collections. Write them with the owner-scoped set:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/set/$OWNER \
  -H 'Content-Type: application/json' \
  -d '{"patch": {"'$PROP_AUTHOR'": "Frank Herbert", "'$PROP_YEAR'": 1965}}'
```

- **Key by `propId`, never by `xKey`.** The server never sees xKeys; a patch keyed by one fails with `property.not_found`. Resolve `xKey → propId` from `GET …/types/:typeId/properties` or `GET …/collections/:collectionId/properties` first.
- **No write creates its own owner.** A value is admitted only while the object has that type or that collection; otherwise the write is refused. Set the type and file the collections first ([Objects](objects.html)).
- The endpoint auto-routes by the property's declared [scope](data-types.html). Every propId in one patch must resolve to the same scope — mixed-scope or unknown keys are rejected.
- A value whose kind differs from the declared `kind` is `400 property.kind_mismatch`; a value that does not fit the property's `xFormat` slug is `400 property.format_violation`.
- Built-in paths use literal keys: `…/set/any` with `{"patch": {"name": "Dune"}}`. A tree move is this route on the wiki collection — `…/set/<wikiCollectionId>` with `{"patch": {"<parentIdPropId>": "…", "<posPropId>": "…"}}` ([Objects](objects.html)).

Initial values ride object create instead — `initialProperties` keyed the same way (see [Objects](objects.html)).

## Dataset records: `/modify`

`POST /v1/spaces/:spaceId/modify` is the generic write over one object's dataset — a batch of records, each a list of ops, all landing in one change.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/modify \
  -H 'Content-Type: application/json' \
  -d '{
    "objectId": "'$OBJ'",
    "dataset":  "'$TYPE'_notes",
    "records": [
      { "id": "", "upsert": true,
        "ops": [
          { "type": "$set",      "path": "",     "value": { "title": "x", "n": 1 } },
          { "type": "$addToSet", "path": "tags", "value": "idea" } ] },
      { "id": "note_7",
        "ops": [ { "type": "$inc", "path": "n", "value": 1 },
                 { "type": "$unset", "path": "draft" } ] }
    ],
    "traceIds": ["import-42"]
  }'
```

| Op | Effect |
|----|--------|
| `$set` | Assign `value` at `path`. An empty path with an object value sets several top-level keys at once. |
| `$unset` | Remove the field at `path`. |
| `$inc` | Add a number to the field at `path`. |
| `$addToSet` | Append `value` to the array at `path` if absent. |
| `$pull` | Remove `value` from the array at `path`. |

`path` is a dotted field path (`"style.level"` touches one sub-field; `"style"` replaces the object). A record with `id: ""` and `upsert: true` is created with a derived id; a named id with `upsert` creates-or-updates. `traceIds` are opaque labels stored on the change and filterable in history.

`dataset` is a storage collection name — `<typeId>_<key>` for a runtime dataset — that a part of the object's **type** declares, and the object must have that type — otherwise `400 dataset.not_declared` (a name the space does not serve at all is `400 dataset.unknown`). Collections declare no datasets. Module storage collections (`chat_messages`, `editor_blocks`) are written through their own handlers, which stamp derived fields and enforce authorship; `/modify` is for runtime datasets and other dynamic ones. Runtime-dataset rules (required fields, write-once, author-only) are enforced on apply — see [Runtime datasets](runtime-datasets.html).

### Local-scope writes

Add `"scope": "local"` to write fields the dataset schema declares `local` — device-only, no DAG change, never synced, still delivered to query/subscribe with a locally-minted `versionId` and an empty `changeId`:

```json
{ "objectId": "<chat>", "dataset": "chat_messages", "scope": "local",
  "records": [ { "id": "<msgId>",
                 "ops": [ { "type": "$set", "path": "unread", "value": false } ] } ] }
```

Constraints (`400 request.schema`): explicit record ids, no `upsert` (local fields annotate records the synced route created), no `traceIds`, and not the `objects` dataset — local property values go through `…/properties/:objectId/set/:ownerId`. An op targeting a non-local field comes back in `rejections`; the reverse — a synced write touching a local field — fails whole with `400 dataset.validation`. `account` scope is not writable here.

## Delete records

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/delete-records \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$OBJ'", "dataset": "'$TYPE'_notes", "recordIds": ["note_7"]}'
```

A record delete is a sticky tombstone — the id can never be re-created. A later `/modify` addressing it still answers `200`, with a whole-record `rejections` entry (`opIndex: -1`) and nothing stored, so an ensure-by-fixed-id always inspects `rejections`. Subscribers see the id in `removed`.

## Idempotency

POSTs are not idempotent: each call produces a new change. The one exception is [`/upsert`](upsert.html), where the caller-supplied record id is the idempotency key and an identical re-run writes nothing.

## Preflight, don't hope

- Check the object's `any.type` before a dataset write — a part of that type must declare the storage collection; the wrong type is a `400 dataset.not_declared`, not a silent no-op.
- Resolve property ids from the owner's definitions — the type's or the collection's — and validate kinds client-side.
- Read back through `/query`; never expect a write to echo the record.

## Related

- [Objects](objects.html) — create with `type`, `collections` and `initialProperties`.
- [Editor](../types/editor.html), [Chat](../types/chat.html) — the bespoke write handlers.
- [Subscribe](../realtime/subscribe.html) — matching your `versionId` against live frames.
