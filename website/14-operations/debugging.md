---
title: Debugging
description: Where to look when something is off — health, sync status, the debug snapshots, the process view, logs, and how to read an error response.
order: 70
---
# Debugging

Most problems are one of four things: the server is not up or not authorized, a space has not converged yet, the index has not caught up, or a request was malformed. Each has a dedicated read.

## Is the server up and authorized?

```bash
any status
curl -s http://127.0.0.1:7001/v1/health
```

```json
{ "status": "ok", "version": "any v0.1.0 (sdk v0.0.0)",
  "startedAt": "2026-04-23T18:12:00Z", "account": "A3…", "bootstrapping": false }
```

| Symptom | Read |
|---|---|
| connection refused | no server on that port; start with `any run` (the CLI exits 3 and says so) |
| `"account": ""` and `401 auth.required` everywhere | server is unauthorized — `any auth login` or `POST /v1/auth` ([Accounts](../auth/accounts.html)) |
| `"bootstrapping": true` | the background boot pass is still loading spaces; reads may serve pre-offline state |
| `409 auth.account_in_use` on auth | another process holds this account's pid lock |

Add `--verbose` to any CLI command to see the HTTP exchange on stderr.

## Has this space converged?

`/sync-status` is the production read for sync state; it has a rollup per space, a per-object variant, and SSE streams ([Sync status](../realtime/sync-status.html)).

```bash
any sync-status space $SPACE
any sync-status object $SPACE $OBJECT
any space sync $SPACE          # force a head-sync round now instead of waiting ~30s
```

The **debug** endpoints go one level deeper and are diagnostic only — fields may move.

```bash
any debug space $SPACE                 # GET /v1/spaces/:spaceId/debug
any debug object $SPACE $OBJECT        # GET /v1/spaces/:spaceId/debug/objects/:objectId
curl -s http://127.0.0.1:7001/v1/debug/p2p
```

`debug space` returns per-peer outbound head-sync counters since boot (in-memory, reset on restart; `peers` is empty until one diff round has run):

```json
{ "spaceId": "spc_…",
  "peers": [ { "peerId": "12D3Koo…", "lastSyncAt": "2026-05-15T12:00:00Z",
               "new": 2, "changed": 5, "lastErr": "" } ] }
```

`debug object` is a joint-consistent snapshot of one object's tree and sync state:

```json
{ "objectId": "obj_…", "syncState": "syncing", "pending": ["head_a"],
  "lastSyncAt": "2026-05-15T12:00:00Z", "heads": ["head_a"], "headsCount": 1,
  "branchCount": 0, "treeLen": 3, "snapshots": 1,
  "latestVersionId": "01HX…", "maxAddSeq": 17 }
```

`syncState` is `unknown` / `offline` / `syncing` / `synced` / `error`. `latestVersionId` and `maxAddSeq` are local to this device and never comparable across peers.

> **Note.** `debug object` locks the object tree and walks every change; on a very large tree it can pause local writes noticeably. Never poll it in a tight loop. First touch of a never-loaded object also triggers a cold restore.

`/v1/debug/p2p` shows the local-network layer — own peer id, listener, discovered LAN peers and which spaces they share ([Networks](networks.html)).

## Is search behind?

Long-running index work shows up in the process view:

```bash
any process list                       # GET /v1/processes
```

`index.fts.<spaceId>` is the chunk backlog, `index.embed.<spaceId>` the vector drain with done/total, `index.model_download` the model fetch in bytes. Indexing announces itself only past three seconds of work, so ordinary edits never appear. On a search reply, `vectorStatus: "unavailable"` means the embedder is down or the model is still downloading; `disabled` means this server has none ([Hybrid ranking](../search/hybrid.html)). Content older than the index on this device is not in it — "index from the next change" ([How indexing works](../search/indexing.html)). If boot fails with an index schema-version or dimension mismatch, remove `<account-dir>/index/` and restart.

## Reading an error

Every non-2xx response has one shape:

```json
{ "error": { "code": "space.not_found", "message": "space spc_xyz not found",
             "details": { } } }
```

`code` is the machine-readable handle, grouped by area (`request.*`, `auth.*`, `space.*`, `object.*`, `dataset.*`, `index.*`, …); `message` is safe to show; `details` carries only identifiers the caller supplied — never secrets or paths. Common ones:

| Status | Code | Meaning |
|---|---|---|
| 400 | `request.bad_json` / `request.schema` / `request.missing_field` | the body did not parse or match the endpoint's shape; the message names what |
| 400 | `request.unknown_field` | a top-level key outside the endpoint's accepted set — `details.accepted` lists them |
| 400 | `filter.unknown_operator` / `filter.invalid` | the query filter did not parse; `details.path` points at it |
| 401 | `auth.required` | unauthorized server |
| 404 | `space.not_found` / `object.not_found` / `type.not_found` | the target id is unknown or deleted |
| 409 | `index.disabled` / `push.disabled` | the feature is off on this server |
| 500 | `internal` | unexpected failure — the server log has the stack |
| 503 | `server.unavailable` / `index.embedder_unavailable` | shutting down / embedder outage; retry |

The full namespace is in the [error reference](../reference/errors.html). CLI exit codes: `0` success, `1` user error or 4xx, `2` 5xx, `3` cannot reach the server.

## Logs

One log stream for the whole process, to stderr, level `info` by default.

```bash
any run --log-level debug
ANY_LOG_LEVEL=debug any run
```

```yaml
log:
  defaultLevel: debug
  format: json               # colorized | plaintext | json
  addOutputPaths: ["~/.any/server.log"]
```

5xx responses log at `error` with the full stack; panics are converted to `500 internal` with a generic message and the trace goes to the log. A startup warning that the binary was "built without the fts/vector tags" means search will return nothing — rebuild with `make build` ([Builds and CI](builds-and-ci.html)).

## Watching live

Every subscription is SSE, and the CLI prints one JSON object per frame, so the fastest way to see what a client sees is to attach the same stream:

```bash
any query-subscribe $SPACE --dataset chat_messages --limit 20
any events subscribe --scope space --space $SPACE
any sync-status subscribe
```

Streams end with `closed{reason}` — `server_shutdown`, `sdk_closed`, `overflow`, `drifted` — and recovery is always "reconnect for a fresh snapshot" ([Subscribe](../realtime/subscribe.html)).

## The embedded UI

A standalone `any run` serves a small debug harness at `http://127.0.0.1:7001/ui` (space picker, object tree, members, chat) — handy for a visual check of what the API holds. `webUI.enabled: false` turns it off; app-embedded boots run headless.
