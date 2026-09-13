---
title: Runs and monitoring
description: Synced run summaries, the scheduler stamp on the trigger, the circuit breaker, health markers for inert definitions, and the agent's loopback control API.
order: 50
---
# Runs and monitoring

Every fire is a normal program run: it writes a trace on the device that ran it, publishes a synced summary to the `agent_runs` dataset, and stamps the trigger record. "Did it run, how long did it take, what did it cost, why did it stop" are answered from records — no log grepping.

## The run summary

At the end of every run the runtime upserts one record into `agent_runs` on the `bao/runs/v1` child of the `bao/v1` bundle; the record id is the run id. Summaries sync, so every device sees every device's runs — trigger fires, conversations (under the chat responder's id) and control-API runs alike.

```json
{
  "runId": "run_9f3c2a9d0b4e7f61",
  "triggerId": "daily-digest",
  "program": "agent:rollup@v1",
  "device": "12D3…",
  "startedAt": 1756108800.4,
  "endedAt": 1756108802.7,
  "durationMs": 2311,
  "status": "ok",
  "errorType": null,
  "turns": 1, "cells": 1, "effects": 14, "mutations": 3,
  "tokens": {"in": 5120, "out": 410, "cacheRead": 0, "cacheWrite": 0},
  "costUsd": 0.021,
  "model": "claude-haiku-4-5-20251001",
  "title": "…"
}
```

| Field | Meaning |
|---|---|
| `triggerId` | the trigger that fired the run; the `chat-watch` id for a conversation; `null` for a control-API or embedder run |
| `device` | peer id of the device that ran it — the trace lives there |
| `startedAt` / `endedAt` | unix seconds |
| `status` | `ok`, `FAILED`, `interrupted`, or `incomplete` (no terminal cell record) |
| `errorType` | the failure's type, e.g. `FuelExhausted` |
| `mutations` | effects classified as writes |
| `tokens`, `costUsd`, `model` | LLM usage across the run |

The runs collection is namespaced like the triggers one. Read the last twenty fires of one trigger:

```sh
RUNS_ANCHOR=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/bao%2Fv1/children \
  -H 'Content-Type: application/json' -d '{"seed": "bao/runs/v1"}' | jq -r .objectId)
RUNS=$(curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/datasets \
  | jq -r '.datasets[].name | select(endswith("_agent_runs"))')

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$RUNS_ANCHOR\", \"dataset\": \"$RUNS\",
       \"filter\": {\"triggerId\": \"daily-digest\"}, \"sort\": [\"-startedAt\"], \"limit\": 20}" \
  | jq '.records[] | {runId, startedAt, status, durationMs, errorType, device}'
```

`runId` opens the trace on the device named in `device`: `anyrt trace show <runId> --addr http://127.0.0.1:7001` renders every effect, mutations marked. Trace bodies stay on that device; the summary is what syncs.

## The stamp on the trigger

The trigger record carries only scheduler state, written by the owning device after each fire:

| Field | Meaning |
|---|---|
| `lastRunAt` | time of the last fire (unix seconds) — the `once` consumed guard and the cron anchor |
| `lastStatus` | `ok` / `error` / `interrupted`, or `auto_disabled`, or a health marker (below) |
| `consecutiveFailures` | the breaker count, reset to 0 by an `ok` run |

A run is `interrupted` when a hard break stopped it; a fuel exhaustion or the one-hour wall deadline ends it as `error`. How many times a trigger ran, what it cost and which run was last are questions for `agent_runs`, not for the record.

## The circuit breaker

`maxConsecutiveFailures` (default 3): when `consecutiveFailures` reaches it, the trigger auto-disables — `enabled: false`, `lastStatus: "auto_disabled"`, the failing runs in `agent_runs`. Re-enabling is a manual act, and it resets the counter and re-arms the schedule forward. A background program is budgeted per run, observable per query, and self-quarantining on repeated failure.

## Health markers

An enabled trigger that can never fire is marked, not silent. Each tick the owning device stamps `lastStatus` on its own inert entries:

| Marker | Cause |
|---|---|
| `invalid_spec` | a cron expression with no next occurrence; a `once` without a numeric `at`; an `event` without `dataset` and `objectId` |
| `unsupported_source` | an `event` whose `dataset` is not `chat_messages` |

Markers clear once the definition is fixed and are overwritten by the first real run status. The ticker's other silent paths — overlays still syncing, a failed reconcile query — log on transitions, never per tick, so "why isn't it firing" is answerable from the record or one log line.

> **Why it matters.** The records are the monitoring surface. Any client that can query a dataset — the desktop UI, `curl`, a program on another device — sees the same summaries, offline, without a dashboard service. The trace behind a `runId` is the full story when the summary isn't enough.

## The control API

A running `anyrt serve` exposes a loopback control API (default `127.0.0.1:7010`, `[agent].control_port`). It reads the live registry and **writes trigger mutations through to the dataset** — the record is the source of truth, and a registry-only edit would be reverted by the next reconcile tick. Errors answer `400 {"error": "…"}`.

| Route | Effect |
|---|---|
| `GET /triggers` | every entry in this device's registry: `{id, name, kind, owner, enabled, lastRunAt, lastStatus, consecutiveFailures, limits}` |
| `GET /triggers/:id` | the full record |
| `GET /triggers/:id/runs` | the 20 newest `agent_runs` summaries for the trigger |
| `PATCH /triggers/:id` | `{spec?, enabled?}` — a new spec re-arms forward |
| `POST /triggers/:id/enable` | enable, reset the breaker, re-arm |
| `POST /triggers/:id/disable` | pause |
| `POST /run` | `{program, args?}` or `{source, args?, program?}` — a one-shot run through the serve resolver, returning `{status, value, error, traceRef, durationMs, fuelUsed}` |
| `POST /break/:runId` | `{"hard": bool}` — stop any run in flight; a soft break becomes hard after 20 s unless the run drains it |
| `GET /election` | `{app, enabled, active, peerId, winner}` |
| `GET /status` | the presence beat this device publishes next, plus `beatSec` |

```sh
curl -s http://127.0.0.1:7010/triggers | jq '.[] | {id, enabled, lastStatus, consecutiveFailures}'
curl -s -X POST http://127.0.0.1:7010/triggers/daily-digest/disable
curl -s -X PATCH http://127.0.0.1:7010/triggers/daily-digest \
  -H 'Content-Type: application/json' -d '{"spec": {"cron": "0 9 * * 1-5"}}'
curl -s -X POST http://127.0.0.1:7010/run \
  -H 'Content-Type: application/json' -d '{"program": "agent:rollup@v1", "args": {"space": "bao", "chatId": "…"}}'
```

Creating and deleting triggers is done on the record (see [Scheduling](index.html)); the control API operates on ids the reconcile has already adopted here. `POST /run` runs inline on the control thread and is not gated by the election — an explicit invocation, same as the CLI.

## Reading a run's trace

A serve keeps trace bodies in its any server's local store, so the viewers take `--addr`:

```sh
anyrt trace ls --addr http://127.0.0.1:7001 --program rollup    # runs of one program, newest first
anyrt trace show run_<id> --addr http://127.0.0.1:7001          # turns, cells, effects (* = mutate)
anyrt trace show run_<id> --addr http://127.0.0.1:7001 --stats  # tokens, cache, cost per turn
anyrt trace stats --addr http://127.0.0.1:7001                  # fuel/duration/token distributions
```

Cron runs typically outnumber conversations many times over — filter with `--program`. Details on the format and the viewers: [Traces and replay](../programs/traces-and-replay.html).

## Progress for long runs

A run that takes more than a few seconds reports through the `progress@v1` tool — `start` / `tick` / `done` / `fail` — which rides the server's process registry and renders as a global bar in the UI; `done(..., notify=…)` posts the outcome into the chat under a `trigger:<job>` identity so the agent reports it. See [Processes](../notifications/processes.html) and [Progress and UI](../agents/progress-and-ui.html).
