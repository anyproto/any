---
title: Trigger schema
description: The agent_trigger record — every field, the three kinds and their spec shapes, the trigger_runs dataset, and the control-plane routes that write through to it.
order: 80
---
# Trigger schema

A trigger is a record in the agent's `agent_triggers` dataset: a program, a schedule or event that fires it, and a device pin that says which of your devices runs it. The dataset is the source of truth — every running device converges its in-memory registry on these records every tick, so creating a trigger is one write, and editing one is another.

## `agent_trigger` fields

| Field | Type | Meaning |
|---|---|---|
| `name` | string | display name |
| `kind` | `cron` \| `event` \| `once` | what fires it |
| `spec` | per kind (below) | |
| `program` | string | program spec, e.g. `agent:rollup@v1` |
| `args` | object | passed to `main` |
| `owner` | string | **device pin** — the peer id of the device that runs this trigger; empty = floating (the election winner runs it) |
| `enabled` | bool | |
| `logRuns` | bool | keep `trigger_runs` records |
| `limits` | `{fuelPerRun?, timeoutS?, maxCostPerRun?}` | per-run budgets, enforced by the executor and the llm effect |
| `maxConsecutiveFailures` | number (default 3) | circuit breaker threshold |
| `lastRunAt` / `lastDurationMs` / `lastStatus` / `runCount` / `lastRunRef` | rollup | observability, written by the owning device |
| `lastFuel` / `lastCostUsd` / `lastMemPages` / `avgDurationMs` / `failureRate` | rollup | aggregated resource stats computed from the retained runs window |

### `spec` by kind

| Kind | Spec | Fires when |
|---|---|---|
| `cron` | a cron expression string | the next occurrence, armed strictly forward — a missed occurrence does not exist |
| `once` | `{"at": <epoch seconds>}` | `now >= at` and it has never run (`lastRunAt` empty); a past `at` fires late on the next tick, then the trigger auto-disables |
| `event` | `{"dataset": "chat_messages", "objectId": "<chat>", "filter"?}` | a new message lands in that chat; the program receives `args ∪ {"event": {space, objectId, messageId, text, agent?}}`. `chat_messages` is the one supported source; other datasets are stamped `unsupported_source`. `filter` is reserved |

Event triggers are live-only, like cron: a message that arrives while the owner is down does not fire later. Self-authored messages (the agent's own name) never fire.

```bash
# create a reminder — one record write
curl -X POST http://127.0.0.1:7001/v1/spaces/$BAO/modify -d '{
  "objectId": "'$TRIGGER_ANCHOR'", "dataset": "agent_triggers",
  "records": [{"id": "remind-standup", "upsert": true, "ops": [{"type": "$set", "path": "", "value": {
    "name": "Standup reminder", "kind": "once", "spec": {"at": 1787000000},
    "program": "agent:remind@v1", "args": {"text": "standup in 5"}, "enabled": true, "logRuns": true }}]}]}'
```

## Lifecycle rules

- **Adopt** — a record pinned to this device, or (on the election-active device) an unowned record, which gets this device's peer id stamped and persisted.
- **Refresh** — an edit to the definition core (`kind`, `spec`, `program`, `args`, `name`, `limits`, `maxConsecutiveFailures`) rebuilds the entry: crons re-arm forward; a `once` takes the record's `lastRunAt` as its consumed state, so rewriting the definition re-arms the shot. `enabled` edits apply in place; a `false → true` flip resets the breaker and re-arms forward.
- **Evict** — a record repinned to another device, or deleted, leaves the registry within a tick. Deleted ids stay tombstoned; recreating a trigger means a new id.
- **Repin** — write `owner`; the old device evicts, the new one adopts on its next tick. An offline pinned device simply does not fire. Clearing `owner` floats the trigger back to the election winner.
- **Circuit breaker** — after `maxConsecutiveFailures` consecutive failures the trigger auto-disables with `lastStatus: "auto_disabled"`; re-enable is manual.
- **Health markers** — an enabled trigger that can never fire gets `lastStatus` stamped `invalid_spec` (cron with no next occurrence, `once` without a numeric `at`) or `unsupported_source`; markers self-clear when fixed and are overwritten by the first real run.
- **Chat responder** — boot seeds one reserved record `chat-watch` (`kind: event` on the general chat, `program: internal:chat-watch`, floating). Disabling it stops the agent answering chat everywhere; pinning it moves where it answers. Dispatch is native, not a program run.

## `trigger_runs`

One record per fire, on the trigger object, keep-last-N:

| Field | Meaning |
|---|---|
| `ts` | start time |
| `durationMs` | |
| `status` | `ok` \| `failed` \| … |
| `error` | present on failure |
| `traceRef` | the run id; inline for small traces, a file attachment for large ones |
| `fuel` / `costUsd` / `tokens` / `memPages` | metrics extracted from the trace at write time |

Read them through `POST /v1/spaces/:id/query` with `dataset: "agent_trigger_runs"`, `filter: {"triggerId": "<id>"}`, `sort: ["-ts"]`.

## Control-plane routes

The `anyrt serve` control API (`127.0.0.1:7010` by default) exposes the registry as a monitoring surface. Mutating routes write through to the dataset record.

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/triggers` | — | every entry: definition + owner + enabled + rollup + resource stats |
| GET | `/triggers/:id` | — | one record |
| GET | `/triggers/:id/runs` | — | last 20 runs, newest first |
| PATCH | `/triggers/:id` | `{spec?, enabled?}` | updated record; a new spec re-arms forward |
| POST | `/triggers/:id/enable` | — | record; resets the breaker |
| POST | `/triggers/:id/disable` | — | record |

```bash
curl -s http://127.0.0.1:7010/triggers | jq '.[] | {id, kind, owner, enabled, lastStatus}'
curl -X POST http://127.0.0.1:7010/triggers/remind-standup/disable
```

> **Note.** The trigger type is a plain user-created type; the invariants above are enforced by the runtime, not by a server handler. Guides: [Cron](../scheduling/cron.html), [Once](../scheduling/once.html), [Event triggers](../scheduling/event-triggers.html), [Device pins](../scheduling/device-pins.html), [Runs and monitoring](../scheduling/runs-and-monitoring.html).
