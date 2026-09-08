---
title: Event triggers
description: The event kind — reserved in the trigger shape today, with a proposed design that fires programs on new chat messages and makes the chat responder itself a trigger.
order: 30
---
# Event triggers

`kind: "event"` is part of the trigger record shape, but no evaluator ships for it yet: an enabled event trigger is stamped `lastStatus: "unsupported_kind"` by the health pass rather than left silently inert. This page documents the reserved shape and the **proposed** design for the first event source. Everything under "Proposed" describes intent, not current behavior.

## Today

| Spec | Behavior |
|---|---|
| `{"dataset": "…", "objectId": "…", "filter": …}` | parsed and accepted; never fires; record carries `unsupported_kind` until the kind ships |

A record with an unknown `kind` (anything other than `cron`, `once`, `event`) is skipped loudly by the reconcile and never enters the registry.

If you need "react to a change" now, the working patterns are a short-interval [cron](cron.html) that reads its own cursor from the space, or the visible nudge: a program posts a message under a `trigger:<job>` agent identity and the chat loop answers it like user input.

## Proposed: chat-message sources

The design narrows the spec to one source in its first version:

```json
{
  "name": "triage",
  "kind": "event",
  "spec": {"dataset": "chat_messages", "objectId": "<chat object id>"},
  "program": "triage@v1",
  "args": {"space": "bao"},
  "owner": "",
  "enabled": true
}
```

- **One source**: `dataset: "chat_messages"` with `objectId` a chat object in the agent space. Any other dataset parses fine and is marked `unsupported_source` by the health pass. `filter` is reserved and ignored.
- **Delivery**: the owning device — the pin, or the active device for a floating record — keeps one SSE subscription per distinct chat across its enabled event triggers, alongside the ticker. A new message fires `main(args ∪ {"event": {"space", "objectId", "messageId", "text", "agent"?}})`. The agent's own messages never fire a trigger.
- **Missed occurrences are live-only**, mirroring cron: a message that arrives while the owner is down does not fire the trigger later. No replay, no backlog.
- **Bookkeeping is unchanged**: each fire is a normal run with a run record, rollup, circuit breaker and limits. A hot chat wearing out the breaker is the breaker doing its job.

```python
"""Route incoming requests: label the message and post a short ack."""

__any_tool__ = False


def main(args):
    ev = args["event"]
    if ev.get("agent"):
        return {"ok": True, "skipped": "agent message"}
    c = use("any@v1")
    label = classify(ev["text"])
    c.chat_send(ev["space"], ev["objectId"],
                {"text": f"filed under {label}", "agent": {"name": "triage", "done": True}})
    return {"ok": True, "label": label}
```

## Proposed: the chat responder is a trigger

The biggest background behavior of the agent — watching the general chat and answering — is proposed to become a reserved event-trigger record, `chat-watch` ("Chat responder"), seeded at boot:

```json
{"kind": "event", "spec": {"dataset": "chat_messages", "objectId": "<general chat>"},
 "program": "internal:chat-watch", "enabled": true, "owner": ""}
```

- `program` is informational; the runtime recognizes the reserved id and routes fires through its existing conversation watcher — dedup, inject-into-live-conversation, deferred-while-not-ready, snapshot backlog — instead of a program run. Conversations are already logged as `agent_turns`, so no per-message run records; the rollup counts conversation starts.
- Floating by default, so the election-active device answers. **Repinning it moves where the agent answers.**
- `enabled: false` pauses answering everywhere — legal, visible in the UI, reversible. Pinned to an offline device means nobody answers, by design.
- Boot re-seeds it if deleted.

> **Why it matters.** With this in place every background behavior of the agent is one mechanism — a record with an owner — and a remote runner (an always-on box, a VM) inherits all of it by holding pins. "Which device is the agent right now" becomes visible, queryable state instead of an invisible election verdict.

## Proposed: floating means floating

Alongside the event kind, the ownership semantics tighten: an ownerless record (`owner: ""`) is run by the election-active device and **never stamped** with its peer id, so when the election moves, the trigger moves. A peer id is written only by an explicit act — a repin in the UI or a write from a program. See [Device pins](device-pins.html) for the current behavior this amends.

## Out of scope in the first version

Non-chat dataset sources, the `filter` field, cross-space sources, an event-creation UI (records come from the agent or programs; the UI renders and manages them), and liveness-based failover for pinned triggers — pins are the substrate for remote runners, and consensus without a coordinator is not on offer.
