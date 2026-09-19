---
title: Debugging
description: Where to look when something is off — health, sync status, the debug snapshots, the process view, logs, and how to read an error response.
order: 70
---
# Debugging

Most problems are one of four things: the server is not up or not authorized, a space has not converged yet, the index has not caught up, or a request was malformed. Each has a dedicated read.

| Symptom | First check |
|---|---|
| API connection fails or returns `auth.required` | [Server health](#is-the-server-up-and-authorized) |
| a device cannot see a recent change | [Space sync status](#has-this-space-converged) |
| an object exists but does not appear in search | [Index progress](#is-search-behind) |
| an object or dataset write is rejected | [Error codes](#reading-an-error) |
| a scheduled program did not run | [Runs and monitoring](../scheduling/runs-and-monitoring.html) — trigger state and `agent_runs` |

`$SPACE`, `$OBJECT` and `$CHAT` below are ids from your own space; `--addr` points the CLI at a server on another loopback port.

## Is the server up and authorized?

```bash
any status
curl -s http://127.0.0.1:7001/v1/health
```

```json
{ "status": "ok", "version": "any v0.1.2 (commit 1a2b3c4, built 2026-09-09)",
  "startedAt": "2026-09-10T08:12:00Z", "networkId": "N83gJpVd…", "account": "A3…", "bootstrapping": false,
  "crdtVersion": { "supported": 2, "stored": 2, "newer": false } }
```

| Symptom | Read |
|---|---|
| connection refused | no server on that port; start with `any run` (the CLI exits 3 and says so) |
| `"account": ""` and `401 auth.required` everywhere | server is unauthorized — `any auth login` or `POST /v1/auth` ([Accounts](../auth/accounts.html)) |
| `"bootstrapping": true` | the background boot pass is still loading spaces; reads may serve pre-offline state |
| two devices list different spaces for the same account | compare `networkId` — each network holds its own copy of the account ([Networks](networks.html)) |
| `409 auth.account_in_use` on auth | another process holds this account's instance lock |
| `"crdtVersion": {"newer": true}` and `409 sdk.crdt_version_newer` on writes | another device raised the account's data version — upgrade this server; reads keep working |
| `403 control.forbidden` | a managed server's auth or shutdown call without its control token (`ANY_CONTROL_TOKEN` for the CLI) |

Add `--verbose` to any CLI command to see the HTTP exchange on stderr. Without `--addr` the CLI connects to the address the account's running server recorded in `server.addr`, so a server on an ephemeral port is found too; with several servers under one root, pass `--addr`.

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

`index.fts.<spaceId>` is the change backlog (done counts changes, total unknown), `index.embed.<spaceId>` the vector drain with done/total, `index.links_backfill.<spaceId>` a rebuild of the link index, `index.model_download` the model fetch in bytes. Indexing announces itself only past three seconds of work, so ordinary edits never appear. On a search reply, `vectorStatus: "unavailable"` means the embedder is down or the model is still downloading; `disabled` means this server has none ([Hybrid ranking](../search/hybrid.html)). If boot fails with an index schema-version or dimension mismatch, remove `<account-dir>/index/` and restart: every space then re-indexes from the beginning, visible as the processes above ([How indexing works](../search/indexing.html)).

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
| 400 | `request.missing_field` on object creation | supply the object's required `type`; collections are separate memberships |
| 400 | `dataset.not_declared` | the object's type does not declare the dataset you are writing; set the intended type first |
| 400 | `property.not_found` | resolve the property's `xKey` to its `propId`, and write under the owner that declares it |
| 400 | `filter.unknown_operator` / `filter.invalid` | the query filter did not parse; `details.path` points at it |
| 401 | `auth.required` | unauthorized server |
| 404 | `space.not_found` / `object.not_found` / `type.not_found` | the target id is unknown or deleted |
| 405 | `space.unsupported` | the route does not apply to the tech space |
| 409 | `index.disabled` / `push.disabled` / `local.disabled` / `access.disabled` | the feature is off on this server |
| 500 | `internal` | unexpected failure — the server log has the stack |
| 503 | `server.unavailable` / `index.embedder_unavailable` | shutting down / embedder outage; retry |

The full namespace is in the [error reference](../reference/errors.html). CLI exit codes: `0` success, `1` user error or 4xx, `2` 5xx, `3` cannot reach the server.

No write sets a type or files a collection as a side effect — a value or dataset write is admitted only while the object's type or one of its collections declares it ([Types and properties](../database/types-and-properties.html), [Runtime datasets](../database/runtime-datasets.html)).

## Logs

One log stream for the whole process, to stderr, level `info` by default.

```bash
any run --log-level debug
ANY_LOG_LEVEL=debug any run
```

```yaml
log:
  defaultLevel: debug
  format: 2                  # 0 colorized | 1 plaintext | 2 json
  outputPaths: ["/var/log/any/server.log"]   # absolute; ~ is not expanded
```

5xx responses log at `error` with the full stack; panics are converted to `500 internal` with a generic message and the trace goes to the log. A boot error saying `index.embedder "local"` needs `-tags llamacpp` means the binary has no local embedder — rebuild with `make build`, or use `index.embedder: auto` ([Builds and CI](builds-and-ci.html)).

## Watching live

Every subscription is SSE, and the CLI prints one JSON object per frame, so the fastest way to see what a client sees is to attach the same stream:

```bash
any query-subscribe $SPACE $CHAT --dataset chat_messages --sort=-_ver.id --limit 20
any events subscribe --scope space --space $SPACE
any sync-status subscribe
```

Streams end with `closed{reason}` — `server_shutdown`, `deauthorized`, `sdk_closed`, `overflow`, `drifted` — and recovery is always "reconnect for a fresh snapshot"; after `deauthorized`, re-read `GET /v1/auth` first ([Subscribe](../realtime/subscribe.html)).

## The embedded UI

A standalone `any run` serves a small debug harness at `http://127.0.0.1:7001/ui` (space picker, object tree, members, chat) — handy for a visual check of what the API holds. `webUI.enabled: false` turns it off; app-embedded boots run headless.
