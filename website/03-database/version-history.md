---
title: Version history
description: Every object keeps its full change history locally — list changes, view the object at any version, read one record at a cut, and diff.
order: 120
---
# Version history

Every write is a change in the object's DAG, and the DAG is kept in full on every device. The history surface reads it back: list an object's changes, materialize the object as it was at a given version, fetch one record at that cut, or diff two versions. All of it is read-only and per-object.

A **version is a change id** — the content-hash CID that every write already returns as `changeId` in its `ModifyResult`. It is stable across peers and restarts, so a version handed out by one device resolves on another.

> **Why it matters.** History isn't a feature layered on top of storage — it *is* the storage. The CRDT keeps every change to converge replicas, so "what did this look like last Tuesday" is a local read over data you already hold, with no snapshot service, no retention plan and nothing to pay for.

## Endpoints

```
GET /v1/spaces/:spaceId/objects/:objectId/history
GET /v1/spaces/:spaceId/objects/:objectId/history/diff
GET /v1/spaces/:spaceId/objects/:objectId/history/:version
GET /v1/spaces/:spaceId/objects/:objectId/history/:version/datasets/:dataset/records/:recordId
```

Snapshot-only: there is no subscribe variant.

## Versions are causal, not chronological

"State at version X" is the projection of exactly X's causal past — not "the object at wall-clock time T". Two peers writing concurrently hold versions neither of which precedes the other, so there is no total order to page through. Listing is DAG order plus an opaque cursor, not a timestamp range.

`timestamp` on a change is the **author's clock**, Unix seconds, display-only. Nothing forces the clocks of different devices to agree, so never sort or fence on it.

## List changes

`GET …/history` pages an object's changes newest-first.

| Param | Meaning |
|---|---|
| `dataset` | Only changes touching this dataset. |
| `recordId` | Only changes touching this record; requires `dataset` (else `400 request.invalid_field`). |
| `traceId`, `author` | Filter by trace id or author identity. |
| `limit` | Default 50, capped at 200 (larger values are clamped). |
| `cursor` | Opaque, echoed from the previous page. Empty in a response = history exhausted. |
| `coalesce=true` | Group consecutive same-author changes into one entry. |
| `coalesceWindow` | Max gap in seconds inside a group; default 300, max 86400. |

```sh
curl "http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/history?dataset=editor_blocks&coalesce=true"
```

```json
{
  "changes": [
    { "version":   "bafy…9c",
      "author":    "A5k…",
      "timestamp": 1763040000,
      "dataset":   "editor_blocks",
      "traceIds":  ["trace_…"],
      "touched":   [{ "dataset": "editor_blocks", "recordId": "blk_…", "ops": ["$set"] }],
      "groupSize": 3 }
  ],
  "cursor": "eyJ…"
}
```

With `coalesce`, a group's `version` is its **newest** change id and `groupSize` its member count (1 = ungrouped). Grouping follows the DAG: a linear same-author chain coalesces, a branch does not. The `truncated` field is reserved and always `false` — full local history is kept.

## View at a version

`GET …/history/:version` materializes the object's live records at that cut, grouped by dataset. Records are raw dataset rows in the same shape [`/query`](reading-data.html) returns. `dataset` narrows to one; empty datasets are omitted unless explicitly requested.

```sh
curl "http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/history/bafy…9c?dataset=editor_blocks"
```

The view is opened, serialized and closed within the request. A version whose materialization exceeds the bound answers `413 history.view_too_large` — narrow with `dataset`, or use the record fast path.

## One record at a version

`GET …/history/:version/datasets/:dataset/records/:recordId` reads a single record at the cut without materializing the full view, so it can never hit `view_too_large`. `exists: false` means the record wasn't present at that version; `deleted: true` means it was tombstoned, with the tombstone row in `record`.

## Diff

`GET …/history/diff?version=…[&base=…]` — with `base` omitted you get the per-change *effect* diff: `version` against its own DAG parents, "what did this change do". With `base` you get the cumulative `base..version` diff. Scope with `dataset` and `recordIds` (comma-separated; requires `dataset`).

```json
{
  "base":    "",
  "version": "bafy…9c",
  "datasets": [
    { "dataset": "editor_blocks",
      "records": [
        { "id":   "blk_…",
          "kind": "changed",
          "fields": [ { "path": ["text"], "before": "old", "after": "new" } ] } ] } ]
}
```

`kind` is `added` / `removed` / `changed` / `deleted`. Field diffs are leaf-level; an absent side is omitted (`added` has no `before`). Peer-local bookkeeping such as `_ver` never appears.

## What history does not cover

- **Synced scope only.** Local and account-scoped values never enter the DAG, so they are absent from every view — a history view is not a substitute for a `/query` read. See [System fields](system-fields.html) for scopes.
- **Datasets that opt out.** `chat_messages` sets `skipHistory`: writes succeed as usual but are invisible to every history endpoint. Runtime datasets can opt out the same way (`skipHistory` in the [declaration](runtime-datasets.html)). The DAG retains everything regardless.

| Error | Status | Meaning |
|---|---|---|
| `history.version_not_found` | 404 | the version id is not in this object's DAG |
| `history.view_too_large` | 413 | materialization exceeds the bound — narrow the scope |
| `history.truncated` | 404 | the version lies beyond retained history |
| `object.not_found` | 404 | the object was never created here, or is deleted |

> **Note.** There is no CLI surface for history; use `curl` or the HTTP client of your choice.
