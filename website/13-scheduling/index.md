---
title: Scheduling
description: Triggers are CRDT records — cron, one-shot and event definitions that fire programs on the device that owns them.
order: 0
---
# Scheduling

A trigger is a record in the `agent_triggers` dataset of your agent space: a program spec, a schedule, arguments, and an `owner` naming the device that runs it. The running agent converges its scheduler on those records every tick, so creating, editing, pausing and moving a job is just writing a record — from the UI, from a program, or from `curl`.

## A trigger record

```json
{
  "name": "daily digest",
  "kind": "cron",
  "spec": {"cron": "0 8 * * *"},
  "program": "agent:rollup@v1",
  "args": {"space": "bao", "chatId": "<chatId>"},
  "owner": "",
  "enabled": true,
  "maxConsecutiveFailures": 3
}
```

| Field | Meaning |
|---|---|
| `kind` | `cron`, `once` or `event` |
| `spec` | `{"cron": "<expr>"}` or `{"every_s": n}` · `{"at": <epoch seconds>}` · `{"dataset", "objectId", "filter?"}` |
| `program` | a program spec — `agent:remind@v1`, a connector, or one you authored |
| `args` | the JSON passed to `main(args)` |
| `owner` | the device pin — a peer id, or `""` for "any active device" |
| `enabled` | pause without deleting |
| `limits` | `{fuelPerRun?, timeoutS?, maxCostPerRun?}` — per-run resource bounds declared on the definition |
| `maxConsecutiveFailures` | circuit-breaker threshold (default 3) |

The runtime adds a rollup on the same record — `lastRunAt`, `lastStatus`, `lastDurationMs`, `lastFuel`, `lastCostUsd`, `runCount`, `consecutiveFailures`, `lastRunRef` — so "did it run?" is one read.

## Where the records live

Triggers sit on the **trigger anchor**: the `bao/triggers/v1` child of the agent space's `bao/v1` bundle. Resolve it by seed, never by name:

```sh
ANCHOR=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/bao%2Fv1/children \
  -H 'Content-Type: application/json' -d '{"seed": "bao/triggers/v1"}' | jq -r .objectId)

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"agent_triggers\",
       \"records\": [{\"id\": \"daily-digest\", \"fields\": {
         \"name\": \"daily digest\", \"kind\": \"cron\", \"spec\": {\"cron\": \"0 8 * * *\"},
         \"program\": \"agent:rollup@v1\", \"args\": {\"space\": \"$SPACE\", \"chatId\": \"$CHAT\"},
         \"enabled\": true}}]}"
```

The record id is yours to choose (a slug). The owning device adopts the record within one 5-second tick.

## How a tick works

```
every 5 s, on each running device:
   reconcile ── read agent_triggers ──▶ adopt / refresh / evict
   health    ── mark inert definitions (invalid_spec, unsupported_kind)
   fire      ── due entries → run program → write trigger_runs + rollup
```

The dataset is the source of truth. A registry-only edit would be reverted by the next reconcile, which is why the control-plane routes write through to the record.

> **Why it matters.** There is no scheduler service. The schedule syncs with your data, encrypted, to every device you own; whichever device holds the pin fires it, and the run log lands in the same space. Take the laptop offline and its pinned jobs pause; repin them to a machine that is up and they move within a tick.

## Semantics in one table

| | cron | once | event |
|---|---|---|---|
| fires when | `now ≥ next occurrence`, armed strictly forward from adoption | `now ≥ at` and never ran | a matching change lands (proposed — see the page) |
| missed while owner down | does not exist — no catch-up burst | fires late on the next tick | live-only (proposed) |
| after firing | re-arms forward | auto-disables, stays as its audit trail | re-arms |
| failure | counts toward the breaker | consumes the shot — no retry | counts toward the breaker |

<div class="cards">
<a href="cron.html"><strong>Cron</strong><span>Recurring schedules — five-field expressions or every_s, strictly-forward arming</span></a>
<a href="once.html"><strong>Once</strong><span>One-shot triggers for reminders and delayed work — late beats lost</span></a>
<a href="event-triggers.html"><strong>Event triggers</strong><span>Fire on a chat message (proposed design) and what the shape reserves</span></a>
<a href="device-pins.html"><strong>Device pins</strong><span>Which device runs a trigger: owner, floating records and the active-instance election</span></a>
<a href="runs-and-monitoring.html"><strong>Runs and monitoring</strong><span>The run log, the rollup, the circuit breaker, health markers and the control API</span></a>
</div>
