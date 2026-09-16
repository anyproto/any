---
title: Progress and UI
description: How the agent shows up in a client — agent-authored chat messages with a typing indicator and debug link, progress bars over the process registry, and space-resident UI helpers.
order: 70
---
# Progress and UI

An application renders the agent through the existing chat, event, and process APIs. Chat messages carry replies and typing state; the process registry carries progress; the runtime control API starts and interrupts runs.

Use this page when adding agent behavior to a client that already connects to the local Any server. The examples need an existing chat and a running agent. Set `$SPACE` and `$CHAT` to their IDs. See [Chat](../types/chat.html), [Event bus](../realtime/event-bus.html), and [Processes](../notifications/processes.html) for the underlying client contracts.

## Agent-authored chat messages

A chat message may carry a create-only `agent` group marking it as written by an agent on the signer's behalf:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$CHAT/chat/messages \
  -H 'content-type: application/json' \
  -d '{"text": "Looking at the last 20 issues…",
       "agent": {"name": "bao",
                 "debugLink": "any://'$SPACE'/'$DEBUG'#turn_2",
                 "done": false}}'
```

```
any chat send $SPACE $CHAT "Looking at the last 20 issues…" --agent-name bao --agent-done=false
```

| Field | Rule |
|---|---|
| `name` | required, non-empty, ≤ 256 bytes — the display label |
| `debugLink` | optional, ≤ 2 KiB; by convention `any://<spaceId>/<objectId>#turn_<n>` so a UI can deep-link from a message to the turn that produced it |
| `done` | required boolean — `false` while the run is still going |
| `outcome` | optional, ≤ 64 bytes — how a run that did not end normally ended; the runtime's terminal bubbles use `interrupted` and `error`, with the run id as `debugLink` |

The group is a UI hint, not a signature: `creator` is still the change signer. It is immutable post-create and rejected with `400 chat.agent_invalid` on violations.

Two client conventions follow from `done`:

- **Typing indicator** — cycle one while the *last* message in the chat is an agent message with `done: false`. Every run ends with a `done: true` message, including a run the host interrupted.
- **Self-filtering** — an agent subscribed to `chat_messages` responds only to messages where `agent` is absent (typed by a human) or posted under another agent name.

Interim assistant text before a tool call posts as a `done: false` bubble; the final reply posts with `done: true`. Markdown links in a reply whose destination is an `any://` object or file URL become chat attachments automatically, so the UI shows chips without a hand-built map. Details of the message shape: [Chat](../types/chat.html).

## Progress bars

Long jobs report through `progress@v1` and nothing else — programs never hand-roll process events:

```python
pg = use("agent:progress@v1")
pg.start(space, "gmail-backfill", "Syncing mail", total=4200)
for i, batch in enumerate(batches):
    ingest(batch)
    pg.tick(space, "gmail-backfill", current=25 * (i + 1), detail=f"batch {i+1}")
pg.done(space, "gmail-backfill", notify=baoSpaceConfig)
# on error: pg.fail(space, "gmail-backfill", "quota exceeded", notify=baoSpaceConfig)
pg.jobs(space)   # one row per live job — "what's running?"
```

| Call | Effect |
|---|---|
| `start(space, job, label, total=0, current=0, detail="")` | registers the process and publishes the 0% state before work begins; idempotent — resume and retry reuse the same bar |
| `tick(space, job, current, total=None, detail=None, label=None)` | advances; doubles as the liveness heartbeat |
| `done(space, job, notify=None)` / `fail(space, job, error, detail=None, notify=None)` | explicit terminal frame; the row lingers ~60s then expires |
| `jobs(space)` | the live rows |

Underneath, the transport is the server's [process registry](../notifications/processes.html): id `<job>.<spaceId>`, `kind: "agent"`, `scope: "account"`, `target` = the subject space id, `detail` riding `message`. Account scope means every device on the account sees the bar regardless of which space is open. Nothing is persisted — the durable record of an outcome is the job's own state or the notify message.

Callers own the throttle: tick per work chunk or percentage step, and at least every 45 seconds on slow jobs (a running row unseen that long expires; the next tick re-registers it).

**Notify on done.** Pass `notify=` — the agent-space config or a `{spaceId, chatId}` — and the terminal call posts a visible chat message under the agent identity `trigger:<job>` ("Job X is DONE — 4200/4200" or "FAILED: …"). Because the message is not under the serving agent's own name, it triggers the conversation loop like a user message: the agent answers it with the chat history in context, and the nudge is visible and attributed rather than impersonating the user. Best-effort — a notification hiccup never fails the job.

> **Note.** The registry is a live view with staleness expiry, not a log. A client that reconnects after a bar finished sees nothing; use the notify message or the job's own state object for history.

## Presence and status line

A running serve publishes a `bao.status` event on the account's [event bus](../realtime/event-bus.html) every 10 seconds and on every change: `state` (`boot`, `idle`, `working`, `shutdown`), the live run's title and tool-call count, and an optional prose `line`. The agent sets that line with `status@v1.set("reindexing the email corpus")`; it decays 90 seconds after the last set, and a client falls back to the run's title. Clients treat three missed beats as offline.

## UI context

A client stamps a create-only `context` group on each chat message it sends — `{spaceId, objectId?, view?}`, what the user had open. The runtime hands it to the loop, which appends it to the user message as a `[now: … | user's view — …]` suffix and binds it in the kernel as `currentUserSpace`, so "add this to the page" resolves without the user naming the page. A message without a context degrades the suffix to timestamp-only. The suffix rides the model message only — the persisted turn keeps the raw `userText`.

## Space-resident UI helpers

`anyrt serve` exposes a loopback control API with `POST /run`: `{program, args?}` runs a deployed program through the serve resolver; `{source, args?, program?}` runs caller-provided program text without deploying it (the debugging escape hatch). Either form returns `{status, value, error, traceRef, durationMs, fuelUsed}`, and the run leaves a trace and an `agent_runs` summary like any other.

The first consumer is `ui@v1` — UI backend helpers dispatched on `args["method"]`. `emailSummary` fills one `email_messages` record's summary via the cheap model tier and returns `{ok, summary, cached?}`, writing nothing on failure. Because the helper is a program in the agent overlay, updating it is a deploy, not a client release.

```bash
curl -X POST http://127.0.0.1:7010/run \
  -H 'content-type: application/json' \
  -d '{"program": "agent:ui@v1",
       "args": {"method": "emailSummary", "space": "'$SPACE'",
                "mailbox_id": "'$MAILBOX'", "record_id": "18f3…"}}'
```

Runs through `/run` are explicit invocations: they are not gated by the standby election described in [Embedding anyrt](embedding-anyrt.html).
