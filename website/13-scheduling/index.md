---
title: Scheduling
description: Triggers are CRDT records — cron, one-shot and event definitions that fire programs on the device that owns them.
order: 0
---
# Scheduling

A trigger is a record in the `agent_triggers` dataset of your agent space: a program spec, a schedule or event, arguments, and an `owner` naming the device that runs it. The running agent converges its scheduler on those records every tick, so creating, editing, pausing and moving a job is just writing a record — from the UI, from a program, or from `curl`.

The record syncs; the execution does not. A trigger fires only on a device where `anyrt` is running, and a stored trigger keeps no stopped device alive. The pinned device runs local work offline; a program that calls a provider needs that provider reachable.

## Before you write a trigger

The server and `anyrt serve` are up ([runtime quickstart](../quickstart/anyrt.html)); the runtime provisions the trigger store on boot. The program you name resolves from the working space or a configured overlay. The shell examples use `$SPACE` (the agent space id) and `$CHAT` (the target chat's object id). `anyrt run --from-space` runs the same program once by hand — the way to check its arguments before scheduling it.

## A trigger record

```json
{
  "name": "daily history rollup",
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
| `spec` | `{"cron": "<expr>"}` or `{"every_s": n}` · `{"at": <epoch seconds>}` · `{"dataset": "chat_messages", "objectId", "spaceId?", "filter?"}` |
| `program` | a program spec — `agent:remind@v1`, a connector, or one you authored |
| `args` | the JSON passed to `main(args)` |
| `owner` | the device pin — a peer id, or `""` for unassigned (the active device claims it) |
| `enabled` | pause without deleting (absent = `true`) |
| `limits` | `{fuelPerRun?, timeoutS?, maxCostPerRun?}` — part of the definition, not enforced; every run gets the runtime's fixed budget ([Limits](../programs/limits.html)) |
| `maxConsecutiveFailures` | circuit-breaker threshold (default 3) |

The runtime stamps three fields back onto the record — `lastRunAt`, `lastStatus` and `consecutiveFailures` — which is scheduler state, not history. Every run's history is a synced summary in the `agent_runs` dataset, keyed by `triggerId`; see [Runs and monitoring](runs-and-monitoring.html).

## Where the records live

Triggers sit on the **trigger anchor**: the `bao/triggers/v1` child of the agent space's `bao/v1` bundle, which `anyrt serve` provisions on boot with the hidden harness type `agent_trigger`. Resolve it by seed, never by name; the child route requires that `type`. The records live in a storage collection namespaced to the harness type (`<typeId>_agent_triggers`); read its name from the space's dataset listing:

```sh
TRG=$(curl -s "http://127.0.0.1:7001/v1/spaces/$SPACE/types?includeHidden=true" \
  | jq -er '.types[] | select(.xKey=="agent_trigger") | .id')
ANCHOR=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/bao%2Fv1/children \
  -H 'Content-Type: application/json' \
  -d "{\"seed\": \"bao/triggers/v1\", \"type\": \"$TRG\"}" | jq -er .objectId)
TRIGGERS=$(curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/datasets \
  | jq -r '.datasets[].name | select(endswith("_agent_triggers"))')

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"$TRIGGERS\",
       \"records\": [{\"id\": \"daily-digest\", \"fields\": {
         \"name\": \"daily history rollup\", \"kind\": \"cron\", \"spec\": {\"cron\": \"0 8 * * *\"},
         \"program\": \"agent:rollup@v1\", \"args\": {\"space\": \"$SPACE\", \"chatId\": \"$CHAT\"},
         \"enabled\": true}}]}"
```

The record id is yours to choose (a slug). Guest code passes the store key instead — `c.upsert_record(space, anchor, "agent_triggers", …)` — and the `any@v1` client resolves the collection. The owning device adopts the record within one 5-second tick.

This example schedules the shipped history rollup at 08:00 UTC. Adoption does not run it immediately: a cron arms its next future occurrence. Look for `daily-digest` in the [trigger registry](runs-and-monitoring.html#the-control-api), then read its `agent_runs` summaries after it fires.

## How a tick works

```
every 5 s, on each running device:
   reconcile ── read agent_triggers ──▶ adopt / claim / refresh / evict
   health    ── mark inert definitions (invalid_spec, unsupported_source)
   events    ── one chat watch per watched (space, chat)
   fire      ── due entries → run program → agent_runs summary + record stamp
```

The dataset is the source of truth. A registry-only edit would be reverted by the next reconcile, which is why the control-plane routes write through to the record.

> **Why it matters.** There is no scheduler service. The schedule syncs with your data, encrypted, to every device you own; whichever device holds the pin fires it, and the run summary lands in the same space. A laptop without a network still fires its pinned jobs; shut it down and they pause, and repinning them to a machine that is running moves them within a tick.

## Semantics in one table

| | cron | once | event |
|---|---|---|---|
| fires when | `now ≥ next occurrence`, armed strictly forward from adoption | `now ≥ at` and never ran | a new message lands in the watched chat |
| missed while owner down | does not exist — no catch-up burst | fires late on the next tick | does not exist — live only |
| after firing | re-arms forward | auto-disables, stays as its audit trail | keeps watching |
| failure | counts toward the breaker | consumes the shot — no retry | counts toward the breaker |

Moving the active agent to another device does not move an already pinned user trigger. Change its `owner` to move it. For work that spans several executions, save a cursor or checkpoint in the database and resume it on the next run; each invocation still has the [runtime's limits](../programs/limits.html).

<div class="cards">
<a href="cron.html"><strong>Cron</strong><span>Recurring schedules — five-field expressions or every_s, strictly-forward arming</span></a>
<a href="once.html"><strong>Once</strong><span>One-shot triggers for reminders and delayed work — late beats lost</span></a>
<a href="event-triggers.html"><strong>Event triggers</strong><span>Fire a program on every new chat message, and the chat responder record</span></a>
<a href="device-pins.html"><strong>Device pins</strong><span>Which device runs a trigger: owner, claiming and the active-instance election</span></a>
<a href="runs-and-monitoring.html"><strong>Runs and monitoring</strong><span>Run summaries, the circuit breaker, health markers and the control API</span></a>
</div>
