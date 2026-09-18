---
title: Once
description: One-shot triggers for reminders and delayed work — fire at a time, then self-disable and remain as the audit trail.
order: 20
---
# Once

A `once` trigger fires a single time when `now ≥ spec.at`, provided it has never run, then flips itself to `enabled: false`. The record stays behind with `lastRunAt` and `lastStatus` stamped — the reminder is its own receipt. An owner that was stopped at `spec.at` fires the unconsumed trigger as soon as it starts again.

## Spec

```json
{"kind": "once", "spec": {"at": 1756112400}}
```

`at` is unix seconds, a plain number rather than a `{"$date": …}` instant. A non-numeric or missing `at` is an inert definition and gets `lastStatus: "invalid_spec"` from the health pass until fixed.

## Schedule a reminder

This is what the agent does when you say "remind me in twenty minutes": one record on the trigger anchor, running the shipped `agent:remind@v1` program with the chat and text as args. `$ANCHOR` and `$TRIGGERS` come from [Scheduling](index.html).

```sh
AT=$(( $(date +%s) + 1200 ))
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"$TRIGGERS\",
       \"records\": [{\"id\": \"remind-standup\", \"fields\": {
         \"name\": \"stand up\", \"kind\": \"once\", \"spec\": {\"at\": $AT},
         \"program\": \"agent:remind@v1\",
         \"args\": {\"space\": \"$SPACE\", \"chatId\": \"$CHAT\", \"text\": \"stand up\"},
         \"enabled\": true}}]}"
```

From guest code, using the `now()` shim (a recorded `time.now` effect):

```python
c = use("agent:any@v1")
anchor = c.bundle_child(space, "bao/v1", "bao/triggers/v1")["objectId"]
c.upsert_record(space, anchor, "agent_triggers", "remind-standup", {
    "name": "stand up", "kind": "once", "spec": {"at": now() + 20 * 60},
    "program": "agent:remind@v1",
    "args": {"space": space, "chatId": chat_id, "text": "stand up"},
    "enabled": True,
})
```

The owning device adopts the record within a tick and fires it at `at`. The payload is the shipped `agent:remind@v1`; inside a repo an unqualified `use("any@v1")` resolves in that repo's space, while a program in your working space names the alias (`use("agent:any@v1")`):

```python
"""Deliver a scheduled reminder into a chat (the once-trigger payload)."""

__any_tool__ = False  # trigger-run only; not an agent-callable tool


def main(args):
    c = use("any@v1")
    text = (args or {}).get("text") or "(reminder with no text)"
    return c.chat_send(args["space"], args["chatId"],
                       {"text": f"⏰ Reminder: {text}",
                        "agent": {"name": "bao", "done": True}})
```

## Late beats lost

A `once` whose `at` is already in the past when the owner comes up **fires late on the next tick**. This is the deliberate inverse of the cron rule: a reminder delivered after you reopen the laptop is better than one that silently never happened, and a single late message cannot burst.

## At most once

The shot is consumed before the run starts: the entry disables itself, the run stamps `lastRunAt` whatever its outcome, and a failure lands in the run's `agent_runs` summary rather than being retried. The trigger takes the record's `lastRunAt` as its consumed state, so re-arming means rewriting the definition with a fresh `at`, `enabled: true` and no `lastRunAt` — a whole-record write such as `upsert_record` drops it; a field-level `/upsert` sends `"lastRunAt": null`. Flipping `enabled` alone does not re-fire a consumed shot.

## After it fired

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"$TRIGGERS\",
       \"filter\": {\"kind\": \"once\"}, \"sort\": [\"-lastRunAt\"]}" \
  | jq '.records[] | {id, name, enabled, lastStatus, lastRunAt}'
```

| Field | After a successful fire |
|---|---|
| `enabled` | `false` |
| `lastStatus` | `ok` (or `error` / `interrupted`) |
| `lastRunAt` | the fire time (unix seconds) |

The run itself is the `agent_runs` summary whose `triggerId` is the record id; its `runId` opens the trace with `anyrt trace show <runId> --addr http://127.0.0.1:7001` on the device that ran it. See [Runs and monitoring](runs-and-monitoring.html).

Records accumulate; delete the ones you no longer want as history. A deleted id stays tombstoned, so reuse a fresh slug for the next reminder.

> **Why it matters.** The reminder lives in your encrypted space, not on a notification server. It syncs to every device, fires from whichever one owns it, and its outcome is a record you can query — the same way you query any other data.

## Delayed work, not just reminders

`program` is any resolvable program spec and `args` is free JSON, so a `once` is also "run this backfill at 02:00" or "retry that export in an hour". A detached job can end with a visible nudge — `progress.done(..., notify=…)` posts its outcome into the chat under a `trigger:<job>` identity so the agent reports it with history in context; see [Progress and UI](../agents/progress-and-ui.html).
