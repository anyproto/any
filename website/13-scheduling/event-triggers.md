---
title: Event triggers
description: The event kind — fire a program on every new message in a chat, and the chat responder record that decides which device answers.
order: 30
---
# Event triggers

An `event` trigger runs a program each time a new message lands in a chat. The owning device keeps one live subscription per watched chat next to its ticker, and every fire is a normal program run with a summary, a trace and the circuit breaker. The same mechanism carries the agent's own chat responder, so "which device answers" is a record you can read, pause and repin.

## Spec

```json
{
  "name": "triage",
  "kind": "event",
  "spec": {"dataset": "chat_messages", "objectId": "<chat object id>", "spaceId": "<space id>"},
  "program": "triage@v1",
  "args": {"label": "inbox"},
  "owner": "",
  "enabled": true
}
```

| Spec field | Meaning |
|---|---|
| `dataset` | the event source; `chat_messages` is the one delivered source |
| `objectId` | the chat object to watch |
| `spaceId` | the space the chat lives in; absent or empty means the agent space |
| `filter` | reserved, ignored |

Always set `spaceId` for a chat outside the agent space. A subscription on an object the space does not hold answers with an empty feed rather than an error, so a chat looked up in the wrong space is a watch that connects and never fires.

| Marker | Cause |
|---|---|
| `invalid_spec` | `dataset` or `objectId` missing or empty |
| `unsupported_source` | a `dataset` other than `chat_messages` |

## Delivery

- **One watch per chat.** The owning device subscribes once per distinct `(spaceId, objectId)` across its enabled event triggers, and stops the subscription when no trigger needs it — disabled, repinned away, deleted or tripped.
- **New messages only.** An added message fires; a reaction toggle or a text edit on an existing message never does.
- **Never its own output.** A message whose `agent.name` is this agent's name, or an agent message with no name, does not fire. A message from another agent identity (a `trigger:<job>` nudge, a peer agent) does.
- **Live only**, mirroring cron: the subscription snapshot on connect only marks what already exists as seen, so a message that arrived while the owner was down or disconnected never fires later. An event that lands while the agent is still waiting for its overlays to sync is dropped, not queued.

The program receives the record's `args` plus an `event` object:

```json
{"label": "inbox",
 "event": {"space": "<space id>", "objectId": "<chat id>", "messageId": "<message id>",
           "text": "can someone look at the build?", "agent": {…}, "attachments": {…}}}
```

`agent` and `attachments` are present only when the message carries them.

```python
"""Route incoming requests: label the message and post a short ack."""

__any_tool__ = False


def main(args):
    ev = args["event"]
    if ev.get("agent"):
        return {"ok": True, "skipped": "agent message"}
    c = use("agent:any@v1")
    label = classify(ev["text"])
    c.chat_send(ev["space"], ev["objectId"],
                {"text": f"filed under {label}", "agent": {"name": "triage", "done": True}})
    return {"ok": True, "label": label}
```

Each fire is a normal run: an `agent_runs` summary with `triggerId`, the `lastRunAt` / `lastStatus` stamp, and the circuit breaker. A hot chat wearing out the breaker is the breaker doing its job. The example posts under `triage`, not the serving agent's name, so its own ack fires every trigger on that chat — this one included; the `agent` check is what keeps it from looping.

## The chat responder is a trigger

Serve boot seeds one reserved record, `chat-watch` ("Chat responder"), on the trigger anchor:

```json
{"name": "Chat responder", "kind": "event",
 "spec": {"dataset": "chat_messages", "objectId": "<general chat>"},
 "program": "internal:chat-watch", "enabled": true, "owner": ""}
```

- `program` is informational. The runtime recognizes the reserved id and routes messages through its conversation watcher — dedup, injecting into a live conversation, deferring while overlays sync, and the reconnect snapshot backlog — instead of a program run. The backlog is the one exception to live-only: messages that arrived while nobody was answering get answered late. A message that lands during a live conversation is injected into it rather than starting a run; each conversation run publishes its `agent_runs` summary under the responder's id, and the record's `lastRunAt` marks the latest conversation start.
- A device watches the chat **only while it owns the enabled `chat-watch` record**. The election-active device claims it and re-stamps it on a takeover; a device that stands down clears the owner on the record. Repinning it moves where the agent answers.
- `enabled: false` pauses answering on every device — legal, visible and reversible. Pinned to a device whose agent is not running, nobody answers, by design.
- Boot seeds it only when no `chat-watch` record exists. Because a deleted id stays tombstoned, a reseed after a delete uses a generation id (`chat-watch-g2`, …), recognized as the same reserved record.

> **Why it matters.** Every background behavior of the agent is one mechanism — a record with an owner — so a remote runner (an always-on box, a VM) inherits all of it by holding pins, and "which device is the agent right now" is queryable state instead of an invisible verdict.

## Not delivered

Non-chat dataset sources, the `filter` field, and liveness-based failover for pinned triggers: pins are the substrate for remote runners, and consensus without a coordinator is not on offer. Where you need to react to other changes, use a short-interval [cron](cron.html) that reads its own cursor from the space. Ownership and claiming are on [Device pins](device-pins.html).
