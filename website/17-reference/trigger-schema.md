---
title: Trigger schema
description: The agent_triggers record — every field, the three kinds and their spec shapes, the agent_runs summary, and the control-plane routes that write through to it.
order: 80
---
# Trigger schema

A trigger is a record in the agent's `agent_triggers` dataset: a program, a schedule or event that fires it, and a device pin that says which of your devices runs it. The dataset is the source of truth — every running device converges its in-memory registry on these records every tick, so creating a trigger is one write, and editing one is another.

## `agent_triggers` fields

| Field | Type | Meaning |
|---|---|---|
| `name` | string | display name (absent = the record id) |
| `kind` | `cron` \| `event` \| `once` | what fires it; any other value makes the record malformed and skipped |
| `spec` | per kind (below) | |
| `program` | string | program spec, e.g. `agent:rollup@v1`; required |
| `args` | object | passed to `main` (absent = `{}`) |
| `owner` | string | **device pin** — the peer id of the device that runs this trigger; empty = unassigned (the election-active device claims it) |
| `enabled` | bool | absent = `true` |
| `limits` | `{fuelPerRun?, timeoutS?, maxCostPerRun?}` | part of the definition; not enforced — runs get the runtime's fixed budget |
| `maxConsecutiveFailures` | number (default 3) | circuit breaker threshold |
| `lastRunAt` | number | written by the owning device: unix seconds of the last fire |
| `lastStatus` | string | written by the owning device: `ok` / `error` / `interrupted` / `auto_disabled` / a health marker |
| `consecutiveFailures` | number | written by the owning device: the breaker count |

The dataset lives on the hidden `agent_trigger` type (`idRule: user`, `dynamic: true`, no declared fields), so its storage collection is `<typeId>_agent_triggers`; the record id is a slug you choose. The runtime's own writes replace the whole record with the fields above — any other key does not survive them.

### `spec` by kind

| Kind | Spec | Fires when |
|---|---|---|
| `cron` | `{"cron": "<expr>"}` (five fields, UTC; six or seven accepted as written) or `{"every_s": n}` | the next occurrence, armed strictly forward — a missed occurrence does not exist |
| `once` | `{"at": <epoch seconds>}` | `now >= at` and it has never run (`lastRunAt` empty); a past `at` fires late on the next tick, and the trigger disables itself before the run |
| `event` | `{"dataset": "chat_messages", "objectId": "<chat>", "spaceId"?, "filter"?}` | a new message is added to that chat; the program receives `args ∪ {"event": {space, objectId, messageId, text, agent?, attachments?}}`. `spaceId` absent = the agent space. `filter` is reserved |

Event triggers are live-only, like cron: a message that arrives while the owner is down does not fire later. Reactions and edits never fire, and neither do agent messages under the serving agent's own name or with no name.

```bash
# create a reminder — one record write
TRIGGERS=$(curl -s http://127.0.0.1:7001/v1/spaces/$BAO/datasets \
  | jq -r '.datasets[].name | select(endswith("_agent_triggers"))')
curl -X POST http://127.0.0.1:7001/v1/spaces/$BAO/modify -H 'Content-Type: application/json' -d '{
  "objectId": "'$TRIGGER_ANCHOR'", "dataset": "'$TRIGGERS'",
  "records": [{"id": "remind-standup", "upsert": true, "ops": [{"type": "$set", "path": "", "value": {
    "name": "Standup reminder", "kind": "once", "spec": {"at": 1787000000},
    "program": "agent:remind@v1",
    "args": {"space": "'$BAO'", "chatId": "'$CHAT'", "text": "standup in 5"}, "enabled": true }}]}]}'
```

`$TRIGGER_ANCHOR` is the `bao/triggers/v1` child of the `bao/v1` bundle (`POST /v1/spaces/:id/bundles/bao%2Fv1/children {"seed": "bao/triggers/v1", "type": "<typeId>"}`, where `<typeId>` is the hidden `agent_trigger` type from `GET /v1/spaces/:id/types?includeHidden=true`).

## Lifecycle rules

- **Adopt** — a record pinned to this device; or, on the election-active device, an unassigned record (empty owner or an `anyrt-<pid>` stamp), which gets this device's peer id stamped and persisted.
- **Refresh** — an edit to the definition core (`kind`, `spec`, `program`, `args`, `name`, `limits`, `maxConsecutiveFailures`) rebuilds the entry: crons re-arm forward; a `once` takes the record's `lastRunAt` as its consumed state, so a rewrite without `lastRunAt` re-arms the shot. `enabled` edits apply in place; a `false → true` flip resets the breaker and re-arms forward.
- **Evict** — a record repinned to another device, or deleted, leaves the registry within a tick. Deleted ids stay tombstoned; recreating a trigger means a new id.
- **Repin** — write `owner`; the old device evicts, the new one adopts on its next tick. An offline pinned device simply does not fire. Clearing `owner` reassigns the trigger to the device that is active now. Moving the active device does not move claimed triggers.
- **Circuit breaker** — after `maxConsecutiveFailures` consecutive failures the trigger auto-disables with `lastStatus: "auto_disabled"`; re-enable is manual.
- **Health markers** — an enabled trigger that can never fire gets `lastStatus` stamped `invalid_spec` (cron with no next occurrence, `once` without a numeric `at`, `event` without `dataset` and `objectId`) or `unsupported_source` (an event dataset other than `chat_messages`); markers self-clear when fixed and are overwritten by the first real run.
- **Standing built-ins** — `rollup`, `extraction`, `linkgen`, `evolution`, `decay`, `reflection` are code-owned: the reconcile ignores their records and they follow the election.
- **Chat responder** — boot seeds one reserved record `chat-watch` (`kind: event` on the general chat, `program: internal:chat-watch`) when none exists, under a generation id (`chat-watch-g2`, …) if the bare id is tombstoned. The active device claims it and re-stamps it on takeover; a device that stands down clears its owner. Disabling it stops the agent answering chat everywhere; pinning it moves where it answers. Dispatch is native, not a program run.

## `agent_runs`

One synced record per run — fires, conversations and control-API runs — on the `bao/runs/v1` child, in the same `agent_trigger` type's `agent_runs` dataset. The record id is the run id.

| Field | Meaning |
|---|---|
| `runId` | the run id — `anyrt trace show <runId> --addr …` on the device that ran it |
| `triggerId` | the firing trigger's id; the `chat-watch` id for a conversation; `null` otherwise |
| `program`, `device` | the program spec and the running device's peer id |
| `startedAt`, `endedAt` | unix seconds |
| `durationMs` | |
| `status` | `ok` \| `FAILED` \| `interrupted` \| `incomplete` |
| `errorType` | the failure's type, when there is one |
| `turns`, `cells`, `effects`, `mutations` | run shape |
| `tokens` | `{in, out, cacheRead, cacheWrite}` |
| `costUsd`, `model`, `title` | |

Read them through `POST /v1/spaces/:id/query` with `objectId` = the runs child, `dataset` = the `<typeId>_agent_runs` collection, `filter: {"triggerId": "<id>"}`, `sort: ["-startedAt"]`.

## Control-plane routes

The `anyrt serve` control API (`127.0.0.1:7010` by default) exposes the registry as a monitoring surface. Mutating routes write through to the dataset record.

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/triggers` | — | every registry entry: `{id, name, kind, owner, enabled, lastRunAt, lastStatus, consecutiveFailures, limits}` |
| GET | `/triggers/:id` | — | one record |
| GET | `/triggers/:id/runs` | — | the 20 newest `agent_runs` summaries |
| PATCH | `/triggers/:id` | `{spec?, enabled?}` | updated record; a new spec re-arms forward |
| POST | `/triggers/:id/enable` | — | record; resets the breaker |
| POST | `/triggers/:id/disable` | — | record |

```bash
curl -s http://127.0.0.1:7010/triggers | jq '.[] | {id, kind, owner, enabled, lastStatus}'
curl -X POST http://127.0.0.1:7010/triggers/remind-standup/disable
```

> **Note.** `agent_trigger` is a hidden harness type, not a server built-in; the invariants above are enforced by the runtime, not by a server handler. The full control API is on [Runs and monitoring](../scheduling/runs-and-monitoring.html). Guides: [Cron](../scheduling/cron.html), [Once](../scheduling/once.html), [Event triggers](../scheduling/event-triggers.html), [Device pins](../scheduling/device-pins.html).
