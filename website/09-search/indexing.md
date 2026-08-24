---
title: How indexing works
description: Chunkers turn dataset records into index documents; the indexer consumes the change feed, evicts what disappears, and never blocks a write.
order: 40
---
# How indexing works

The index is a consumer of each space's change feed, not part of the write path. A set of chunkers describe how each dataset becomes index documents; a per-space worker pulls changed objects through them into a local database and keeps a cursor so it only ever does incremental work.

## Documents and ids

Every index document is `{id, scope, objectId, dataset, recordId, data, hash, addSeq, vector?, pending?}` in a collection per space, and its id is

```
<objectId>:<dataset>:<recordId>
```

That shape is what makes removal cheap: deleting an object is a prefix delete on `objectId:`, detaching a type is a prefix delete on `objectId:<dataset>:`, and deleting one record is a primary-key delete. The database lives at `<account-dir>/index/index.db` and is entirely derived state.

## Chunkers, scopes, gating

| Source | Dataset in hits | Type gate | Scope | Document |
|---|---|---|---|---|
| chat | `chat_messages` | `chat` | `chat` | one message, `text` only (no author, reactions, attachments) |
| editor | `editor_blocks` | `editor` | `basic` | a **window** of consecutive blocks (~1.5 KB, broken before each heading), `recordId = win_<firstBlockId>` |
| object name / description | `prop` | — | `basic` | raw value; `recordId` = `name` / `description` |
| user property values | `prop` | — | `props` (default) | `"<prop name>: <value>"`; `recordId` = the propId |
| runtime datasets | the dataset's name | per dataset | `basic` (default) | title line + text fields from the declared `x-search` mapping |

- **Gating.** A gated chunker runs only while its type is in the object's `any.types`. Detach the type and the object's documents for that dataset are evicted.
- **Editor windows.** One document per block would give ~100-character chunks that embed badly and skew BM25 length normalization; coalescing fixed both. Because a window spans several records, the editor chunker rebuilds the object's full window set and the indexer diffs it against stored content hashes — only new or changed windows are re-embedded.
- **Property values** index by default. A property definition's `meta.index` is a three-state override: absent means scope `props`, `"<scope>"` routes it there (and into the vector pipeline), `"none"` excludes it — the opt-out for blobs and noisy enums. String, array (newline-joined) and number values index; booleans, null and objects never do. Type-definition rows are skipped wholesale.
- **Runtime datasets** are indexed only when their declaration carries an `x-search` mapping (`{title, text, scope}`, see [Runtime datasets](../database/runtime-datasets.html)); `text` may be one field or a list. An invalid scope slug makes the dataset unsearchable rather than silently landing in the default.
- **Never indexed:** program source, miniapps, file bytes.

Scopes are an open set of slugs; `basic`, `chat` and `props` are the vocabulary, and property or dataset overrides can mint more.

## The advance loop

```
Changes().Subscribe ─▶ dirty ─▶ debounce 250ms ─▶ advance
                                                    │  page ChangedSince(cursor, 256)
                                                    │  per object: evict or chunk
                                                    │  one write tx per page
                                                    └─ persist cursor
```

Per dirty object the worker reads the shared objects row once, then: a deleted object → prefix delete `objectId:`; a gated chunker whose type is not attached → prefix delete `objectId:<dataset>:`; otherwise stream the chunker's entries past the cursor — an entry with empty `data` deletes its document, any other upserts it. All of a page's deletes and upserts land in one transaction, then the cursor moves. Re-applying a page is idempotent, so a crash mid-page is safe. Text-bearing upserts are marked `pending` for the [embed loop](vector.html) — full-text is searchable before any embedding happens.

The cursor is any-sync's per-space, per-device delivery counter (`_addSeq`). It is an opaque local ordering key, never comparable across devices.

## Removal

| What happened | Index operation |
|---|---|
| a record deleted or its value cleared | delete `objectId:dataset:recordId` |
| any edit to an editor document | delete vanished windows, upsert changed ones, keep the rest |
| a type detached | prefix delete `objectId:dataset:` |
| a runtime dataset definition removed | prefix delete `objectId:dataset:` on each object's next change |
| an object deleted | prefix delete `objectId:` |

Every removal rides the same change window as content, so no out-of-band purge can race the cursor. Removing what was never indexed is a no-op, so all operations apply unconditionally.

> **Note.** Definition-removal eviction is lazy: an object that is never touched again keeps stale documents for a removed runtime dataset, and a restart forgets which names were retired.

## "Index from the next change"

The pipeline is cursor-driven, and rows that predate indexing on this device have no position in the change window. Consequences:

- Content written before the index existed on a server appears in search only after its next write.
- Deleting `<account-dir>/index/` is safe, but rebuilds only content that changes afterwards.
- Re-attaching a detached type does not resurrect documents below the cursor.

For exhaustive reads, use a [query](../database/reading-data.html); the index is for recall, not enumeration.

## Space lifecycle and generations

The indexer discovers spaces from the space list and its live subscription: a new space spawns a worker, a removed or deleted space stops it and drops the collection. Each space's cursor is stored next to the SDK's re-index generation for that space; when the generation changes (a handler version bump, a rebuilt storage), the space is dropped and re-indexed rather than left silently frozen.

## Watching progress

Long-running index work is reported as device-scope [processes](../notifications/processes.html): `index.fts.<spaceId>` (chunk backlog), `index.embed.<spaceId>` (done/total docs) and `index.model_download` (bytes). Indexing announces itself only past three seconds of work, so ordinary per-edit updates never appear.

```bash
any process list
```

## Freshness summary

| Leg | Fresh after |
|---|---|
| full-text | the 250 ms debounce plus one page transaction |
| vector | one embed round — nudged after each page, retried every minute on failure |
