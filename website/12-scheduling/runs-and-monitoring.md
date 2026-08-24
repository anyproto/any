---
title: Runs and monitoring
description: The run log, the per-trigger rollup, the circuit breaker, health markers for inert definitions, and the agent's loopback control API.
order: 50
---
# Runs and monitoring

Every fire is a normal program run: it writes a trace, a row in the `agent_trigger_runs` dataset, and a rollup onto the trigger record itself. "Did it run, how long did it take, what did it cost, why did it stop" are answered from records — no log grepping.

## The run record

After each fire the runtime upserts one row into `agent_trigger_runs` on the trigger anchor, id `<triggerId>:<timestamp-ms>`:

```json
{
  "triggerId": "daily-digest",
  "ts": 1756108800.4,
  "status": "ok",
  "durationMs": 2311,
  "error": null,
  "traceRef": "run_9f3c…",
  "fuel": 184223,
  "costUsd": null
}
```

| `status` | Meaning |
|---|---|
| `ok` | `main` returned |
| `error` | the program raised, or the run could not start |
| `interrupted` | wall timeout, fuel exhaustion or a hard break |

`traceRef` names the device-local trace: `anyrt trace show run_9f3c…` renders it — every effect, every mutation marked. Traces stay on the device that ran the job; the run record is what syncs.

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"agent_trigger_runs\",
       \"filter\": {\"triggerId\": \"daily-digest\"}, \"sort\": [\"-ts\"], \"limit\": 20}" \
  | jq '.records[] | {ts, status, durationMs, error, traceRef}'
```

## The rollup on the trigger

The same fire updates the trigger record, so a single list query monitors everything:

| Field | Meaning |
|---|---|
| `lastRunAt` | time of the last fire |
| `lastStatus` | `ok` / `error` / `interrupted`, or a marker (below) |
| `lastDurationMs`, `lastFuel`, `lastCostUsd` | the last run's resource use |
| `runCount` | fires since the record was created |
| `consecutiveFailures` | reset to 0 by an `ok` run |
| `lastRunRef` | the last run's trace id |

## The circuit breaker

`maxConsecutiveFailures` (default 3): when `consecutiveFailures` reaches it, the trigger auto-disables — `enabled: false`, `lastStatus: "auto_disabled"`, the reason in the last run record. Re-enabling is a manual act, and it resets the counter and re-arms the schedule forward. A background program is budgeted per run, observable per query, and self-quarantining on repeated failure.

## Health markers

An enabled trigger that can never fire is marked, not silent. Each tick the owning device stamps `lastStatus` on its own inert entries:

| Marker | Cause |
|---|---|
| `invalid_spec` | a cron expression with no next occurrence; a `once` without a numeric `at` |
| `unsupported_kind` | `kind: "event"` — reserved in the shape, no evaluator yet |

Markers clear once the definition is fixed and are overwritten by the first real run status. The ticker's other silent paths — overlays still syncing, a failed reconcile query — log on transitions, never per tick, so "why isn't it firing" is answerable from the record or one log line.

> **Why it matters.** The record is the monitoring surface. Any client that can query a dataset — the desktop UI, `curl`, a program on another device — sees the same rollup, offline, without a dashboard service. The trace behind `lastRunRef` is the full story when the summary isn't enough.

## The control API

A running `anyrt serve` exposes a loopback control API (default `127.0.0.1:7010`, `[agent].control_port`). It reads the live registry and **writes mutations through to the dataset** — the record is the source of truth, and a registry-only edit would be reverted by the next reconcile tick.

| Route | Effect |
|---|---|
| `GET /triggers` | every registered trigger as a rollup row: definition, owner, enabled, last-run fields, `failureRate`, `limits` |
| `GET /triggers/:id` | the full record |
| `GET /triggers/:id/runs` | the 20 newest run records |
| `PATCH /triggers/:id` | `{spec?, enabled?}` — a new spec re-arms forward |
| `POST /triggers/:id/enable` | enable, reset the breaker, re-arm |
| `POST /triggers/:id/disable` | pause |
| `POST /run` | `{program, args?}` or `{source, args?, program?}` — a one-shot run through the serve resolver, returning `{status, value, error, traceRef, durationMs, fuelUsed}` |
| `GET /election` | `{app, enabled, active, peerId, winner}` |

```sh
curl -s http://127.0.0.1:7010/triggers | jq '.[] | {id, enabled, lastStatus, runCount, failureRate}'
curl -s -X POST http://127.0.0.1:7010/triggers/daily-digest/disable
curl -s -X PATCH http://127.0.0.1:7010/triggers/daily-digest \
  -H 'Content-Type: application/json' -d '{"spec": {"cron": "0 9 * * 1-5"}}'
curl -s -X POST http://127.0.0.1:7010/run \
  -H 'Content-Type: application/json' -d '{"program": "agent:rollup@v1", "args": {"space": "bao", "chatId": "…"}}'
```

Creating and deleting triggers is done on the record (see [Scheduling](index.html)); the control API operates on ids the reconcile has already adopted here. `POST /run` runs inline on the control thread and is not gated by the election — an explicit invocation, same as the CLI.

## Reading a run's trace

```sh
anyrt trace ls --program rollup        # runs of one program, newest first
anyrt trace show run_<id>              # turns, cells, effects (* = mutate)
anyrt trace show run_<id> --stats      # tokens, cache, cost per turn
anyrt trace stats traces/              # fuel/duration/token distributions over all runs
```

Cron runs typically outnumber conversations many times over in a traces directory — filter with `--program`. Details on the format and the viewers: [Traces and replay](../programs/traces-and-replay.html).

## Progress for long runs

A run that takes more than a few seconds reports through the `progress@v1` tool — `start` / `tick` / `done` / `fail` — which rides the server's process registry and renders as a global bar in the UI; `done(..., notify=…)` posts the outcome into the chat so the agent reports it. See [Processes](../notifications/processes.html) and [Progress and UI](../agents/progress-and-ui.html).
