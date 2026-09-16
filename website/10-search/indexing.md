---
title: How indexing works
description: Chunkers turn dataset records into index documents; the indexer consumes the change feed, evicts what disappears, and never blocks a write.
order: 40
---
# How indexing works

The index is a consumer of each space's change feed, not part of the write path. A set of chunkers describe how each dataset becomes index documents; a per-space worker pulls changed objects through them into a local database and keeps a cursor so it only ever does incremental work.

## Documents and ids

Every index document is `{id, scope, objectId, dataset, recordId, chunk?, data, title, hash, applySeq, vector?, pending?}` in a storage collection per space, and its id is

```
<objectId>:<dataset>:<recordId>            a record's first chunk
<objectId>:<dataset>:<recordId>␟<n>        chunk n of a long record (␟ = U+001F)
```

That shape is what makes removal cheap: deleting an object is a prefix delete on `objectId:`, retyping it is a prefix delete on `objectId:<dataset>:` for every storage collection the old type owned, and deleting one record is a range delete over its chunks. The database lives at `<account-dir>/index/index.db` and is entirely derived state.

## Chunkers, scopes, gating

| Source | Dataset in hits | Gate | Scope | Document |
|---|---|---|---|---|
| chat | `chat_messages` | the object's type owns the storage collection | `chat` | one message, `text` only (no author, reactions, attachments) |
| editor | each editor storage collection — `editor_blocks` or `<typeId>_<key>` | per storage collection: the object's type owns it | `basic` | a **window** of consecutive blocks (~1.5 KB, broken before each heading), `recordId = win_<firstBlockId>` |
| object name / description | `prop` | — | `basic` | raw value; `recordId` = `name` / `description` |
| user property values | `prop` | — | `props` (default) | `"<prop name>: <value>"`; `recordId` = the propId |
| runtime datasets | the dataset's storage collection | per dataset: the object's type declares it | `basic` (default) | title line + text fields from the declared `x-search` mapping |

- **Gating.** Module and runtime storage collections are indexed only while the object's type owns them — the `owners` a storage collection lists in `GET …/datasets`. Retype the object away from the owner and its documents for that storage collection are evicted.
- **Editor windows.** One document per block would give ~100-character chunks that embed badly and skew BM25 length normalization; windows avoid both. Because a window spans several records, the editor chunker rebuilds the object's full window set per editor storage collection and the indexer diffs it against stored content hashes — only new or changed windows are re-embedded, and an edit in a part's own editor never touches the shared body's documents.
- **Property values** index by default. A property definition's `meta.index` is a three-state override: absent means scope `props`, `"<scope>"` routes it there (and into the vector pipeline), `"none"` excludes it — the opt-out for blobs and noisy enums. String, array (newline-joined) and number values index; booleans, null and objects never do. Type-definition rows are skipped wholesale.
- **Runtime datasets** are indexed only when their declaration carries an `x-search` mapping (`{title, text, scope}`, see [Runtime datasets](../database/runtime-datasets.html)); `text` may be one field or a list. An invalid scope slug makes the dataset unsearchable rather than silently landing in the default.
- **Never indexed:** runtime datasets declared without `x-search` (a program's source, for one), and file bytes.

Scopes are an open set of slugs; `basic`, `chat` and `props` are the vocabulary, and property or dataset overrides can mint more.

## The advance loop

```
Changes().Subscribe ─▶ dirty ─▶ debounce 250ms ─▶ advance
                                                    │  page ChangedSince(cursor, 256)
                                                    │  per object: evict or chunk
                                                    │  one write tx per page
                                                    └─ persist cursor
```

Per dirty object the worker reads the shared objects row once, then: a deleted object → prefix delete `objectId:`; a storage collection the object's type does not own → prefix delete `objectId:<dataset>:`; otherwise stream the chunker's entries past the cursor — an entry with empty `data` deletes the record's documents, any other is split into chunk documents of at most ~2000 runes (chunk 0 keeps the record's id, later chunks carry a suffix and re-prefix the title) and upserted where its content hash changed, with chunks the record no longer produces deleted. All of a page's deletes and upserts land in one transaction, then the cursor moves. Re-applying a page is idempotent, so a crash mid-page is safe. Text-bearing upserts are marked `pending` for the [embed loop](vector.html) — full-text is searchable before any embedding happens.

The cursor is the SDK's per-space, per-device apply sequence. It is an opaque local ordering key, never comparable across devices.

## Removal

| What happened | Index operation |
|---|---|
| a record deleted or its value cleared | delete the record's documents (every chunk) |
| a record shrank to fewer chunks | delete the trailing chunks, upsert the changed ones |
| any edit to an editor document | delete vanished windows, upsert changed ones, keep the rest |
| the object retyped | prefix delete `objectId:dataset:` for each storage collection the old type owned |
| a part or runtime dataset definition removed | prefix delete `objectId:dataset:` on each object's next change |
| an object deleted | prefix delete `objectId:` |

Every removal rides the same change window as content, so no out-of-band purge can race the cursor. Removing what was never indexed is a no-op, so all operations apply unconditionally.

> **Note.** Definition-removal eviction is lazy: an object that is never touched again keeps stale documents for a removed part or runtime dataset, and a restart forgets which names were retired.

## Rebuilds and the cursor

The pipeline is cursor-driven, and a fresh cursor starts at zero, so a new index walks the whole change feed: a first boot, a deleted `<account-dir>/index/` directory and a dropped space all re-index everything the device holds (and re-embed it). The one thing the cursor does not bring back is a record below it whose documents were evicted: setting the old type again restores an editor body and property values at once (the retype is itself a change to the object), but a chat message or runtime record returns to the index only on its own next write.

For exhaustive reads, use a [query](../database/reading-data.html); the index is for recall, not enumeration.

## Space lifecycle and generations

The indexer discovers spaces from the space list and its live subscription: a new space spawns a worker, a removed or deleted space stops it and drops its index storage collection. Each space's cursor is stored next to the SDK's generation for that space. When the SDK store was rebuilt (the generation changed) or an older store was restored under a cursor that ran ahead of it, the worker drops the space's documents and re-indexes from zero rather than leaving the index silently frozen.

## Links

The same pass feeds a second sink. Next to its text, every chunker reports the `any://` edges a record holds — a block's links, a message's mentions and attachments, the values of fields marked `xFormat.links` — and the worker lands them in a per-space link storage collection in the same page transaction, behind the same cursor, so edges leave with their text. This is the index behind `GET …/objects/:objectId/backlinks`, `…/links` and `GET /v1/backlinks` ([objects](../database/objects.html#links-and-backlinks), [links](../types/links.html)). A page that changed edges publishes a device-scope `links.updated` event naming the affected targets. A space whose link storage collection is missing or on an older layout is rebuilt from the records when its worker starts, without re-embedding anything.

## Watching progress

Long-running index work is reported as device-scope [processes](../notifications/processes.html): `index.fts.<spaceId>` (chunk backlog), `index.embed.<spaceId>` (done/total docs), `index.links_backfill.<spaceId>` (objects) and `index.model_download` (bytes). Indexing announces itself only past three seconds of work, so ordinary per-edit updates never appear.

```bash
any process list
```

## Freshness summary

| Leg | Fresh after |
|---|---|
| full-text | the 250 ms debounce plus one page transaction |
| vector | one embed round — nudged after each page, retried every minute on failure |
