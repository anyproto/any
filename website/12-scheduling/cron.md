---
title: Cron
description: Recurring triggers — cron expressions or a fixed interval, armed strictly forward so a missed occurrence never replays.
order: 10
---
# Cron

A `cron` trigger runs a program on a repeating schedule. The spec is either a cron expression or a fixed interval; the scheduler arms the next occurrence *forward from now* on adoption and after every fire.

## Spec

| Spec | Meaning |
|---|---|
| `{"cron": "0 8 * * *"}` | five-field expression (minute hour day month weekday), UTC; a leading seconds field is added for you. Six- or seven-field expressions are accepted as written |
| `{"every_s": 900}` | fire every n seconds, measured from the previous fire |

A cron expression that yields no next occurrence is an inert definition: the health pass stamps `lastStatus: "invalid_spec"` on the record until the spec is fixed.

## Create one

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"agent_triggers\",
       \"records\": [{\"id\": \"inbox-watch\", \"fields\": {
         \"name\": \"inbox watch\", \"kind\": \"cron\", \"spec\": {\"every_s\": 900},
         \"program\": \"mailWatch@v1\",
         \"args\": {\"space\": \"$SPACE\", \"chatId\": \"$CHAT\"},
         \"enabled\": true, \"maxConsecutiveFailures\": 3}}]}"
```

`$ANCHOR` is the trigger anchor from [Scheduling](index.html). From a program the same write is one call on the any client:

```python
c = use("any@v1")
anchor = c.bundle_child(space, "bao/v1", "bao/triggers/v1")["objectId"]
c.upsert_record(space, anchor, "agent_triggers", "inbox-watch", {
    "name": "inbox watch", "kind": "cron", "spec": {"every_s": 900},
    "program": "mailWatch@v1",
    "args": {"space": space, "chatId": chat_id},
    "enabled": True,
})
```

`program` can be anything resolvable from the agent space: a shipped program under its alias (`agent:rollup@v1`), a connector, or a program the agent authored into the working space (unqualified `mailWatch@v1`). A 40-line program on a cron beats scheduling a full reasoning turn every five minutes.

## The program side

The program's `main(args)` receives the record's `args` verbatim. A cron job is a normal run: full trace, fuel budget, wall deadline. Long jobs checkpoint their own cursor in the space and exit before the fuel cliff — the pattern is on [Limits](../programs/limits.html) — so a periodic trigger drains a backlog one budgeted slice at a time.

```python
"""Summarize new mail every quarter hour; posts only when something matters."""

__any_tool__ = False


def main(args):
    c = use("any@v1")
    gmail = use("connectors:gmail@v1")     # a connector from the connectors overlay
    new = fetch_new(gmail)                 # your own helper: pages the connector,
                                           # keeps a cursor in the space
    for m in new:
        if looks_important(m):
            c.chat_send(args["space"], args["chatId"],
                        {"text": f"📬 {m['snippet']}", "agent": {"name": "bao", "done": True}})
    return {"ok": True, "checked": len(new)}
```

## Missed occurrences do not exist

The scheduler computes the next due time from *now* — when the owning device adopts the record, after each fire, whenever the definition is edited, and on an election takeover. A device that was off for a night fires the 8:00 digest tomorrow, not eight times on wake. This is a deliberate guard against the wake-and-replay burst, and it is the opposite of the [once](once.html) rule.

Consequences:

- Editing `spec`, `program`, `args`, `name`, `limits` or `maxConsecutiveFailures` rebuilds the entry and re-arms it forward.
- Flipping `enabled` false → true resets the circuit breaker and re-arms forward; other `enabled` edits are honored in place.
- `every_s` counts from the previous fire, so a job that takes a while drifts; use a `cron` expression when the wall-clock time matters.

> **Note.** During the ≤ one-poll window of an election handover, two devices can both believe they are active and double-fire a floating cron. Pin a job to one device (see [Device pins](device-pins.html)) if a duplicate run would be harmful, or make the program idempotent — most jobs that write through `upsert` already are.

## Pause, edit, delete

Pausing is `enabled: false` on the record; deleting the record evicts it from the registry within a tick. A deleted id stays tombstoned server-side, so recreating a trigger means a new id. The running agent's control API offers the same operations against the live registry and writes them through to the record — see [Runs and monitoring](runs-and-monitoring.html).

## The standing jobs

The shipped agent runs a small set of built-in crons — history rollup hourly, memory extraction every 15 minutes, and similar maintenance — that are code-owned: they follow the active-instance election rather than a record pin and are never evicted by the reconcile. They appear in the trigger list like any other row.
