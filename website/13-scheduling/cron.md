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
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"$TRIGGERS\",
       \"records\": [{\"id\": \"inbox-watch\", \"fields\": {
         \"name\": \"inbox watch\", \"kind\": \"cron\", \"spec\": {\"every_s\": 900},
         \"program\": \"mailWatch@v1\",
         \"args\": {\"space\": \"$SPACE\", \"chatId\": \"$CHAT\"},
         \"enabled\": true, \"maxConsecutiveFailures\": 3}}]}"
```

`$ANCHOR` and `$TRIGGERS` (the anchor id and its storage collection name) come from [Scheduling](index.html). From a program the same write is one call on the any client, which takes the store key:

```python
c = use("agent:any@v1")
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
    c = use("agent:any@v1")
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

The scheduler computes the next due time from *now* — when the owning device adopts the record, after each fire, whenever the definition is edited, and, for the standing jobs, on an election takeover. A device that was off for a night fires the 8:00 digest tomorrow, not eight times on wake. This is a deliberate guard against the wake-and-replay burst, and it is the opposite of the [once](once.html) rule.

Consequences:

- Editing `spec`, `program`, `args`, `name`, `limits` or `maxConsecutiveFailures` rebuilds the entry and re-arms it forward.
- Flipping `enabled` false → true resets the circuit breaker and re-arms forward; other `enabled` edits are honored in place.
- `every_s` counts from the previous fire, so a job that takes a while drifts; use a `cron` expression when the wall-clock time matters.

> **Note.** A claimed cron runs only on its owner, so an election handover never double-fires it. The exposure is the ≤ one-poll window in which two devices both believe they are active: both may claim a brand-new unassigned record before the record converges on one owner, and the standing jobs can overlap. Make programs idempotent where a duplicate run would hurt — most jobs that write through `upsert` already are. See [Device pins](device-pins.html).

## Pause, edit, delete

Pausing is `enabled: false` on the record; deleting the record evicts it from the registry within a tick. A deleted id stays tombstoned server-side, so recreating a trigger means a new id. The running agent's control API enables, disables and edits `spec` on triggers it has already adopted and writes those through to the record; creating and deleting are record writes only — see [Runs and monitoring](runs-and-monitoring.html).

## The standing jobs

The shipped agent runs a small set of built-in crons that are code-owned: they follow the active-instance election rather than a record pin, and the reconcile never adopts, edits or evicts them. The active device writes their records, so they appear in the trigger list like any other row — but edits to those records are ignored, and every boot restores the definitions below.

| id | Program | Every | Ships |
|---|---|---|---|
| `rollup` | `agent:rollup@v1` | 1 h | enabled |
| `extraction` | `agent:extraction@v1` | 15 min | enabled |
| `linkgen` | `agent:linkgen@v1` | 1 h | enabled |
| `evolution` | `agent:evolution@v1` | 6 h | disabled |
| `decay` | `agent:decay@v1` | 24 h | disabled |
| `reflection` | `agent:reflection@v1` | 24 h | disabled |
