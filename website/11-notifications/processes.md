---
title: Processes
description: Report long-running work — agent runs, index passes, imports — as live progress over the event bus, with heartbeats, staleness expiry, and an owner-addressed cancel flow.
order: 20
---
# Processes

A process is a long-running operation that broadcasts its lifecycle as `process.*` events on the [event bus](../realtime/event-bus.html). The server keeps a last-event-wins view with staleness expiry behind `GET /v1/processes`, so "what is running right now" needs no persistence — a restart forgets everything, and owners simply re-register and keep heartbeating.

## Identity

Processes are keyed **`(sender.identity, id)`**. The id is chosen by the publisher and only needs to be unique per publisher; `sender.identity` is server-stamped (and signature-verified on network scopes), so another account can neither collide with nor spoof your process. All devices of one account share an identity — put a run or device uuid in the id when several devices might run the same kind of work.

The id follows the event-target grammar `[A-Za-z0-9._-]{1,128}` because it becomes the envelope `target` (and a pub/sub topic segment such as `ev/process/progress/<id>`).

## The event convention

| Type | `data` |
|---|---|
| `process.started` | `{kind, title, target?}` |
| `process.progress` | `{kind, title, target?, done, total?, message?}` |
| `process.done` / `process.cancelled` | `{kind, title, target?}` |
| `process.failed` | `{kind, title, target?, error: {code?, message}}` |
| `process.cancel` | `{identity}` — a directive at the owner, not a state change |

- The descriptor (`kind` / `title` / `target`) is folded into **every** frame, so one progress heartbeat fully materializes a process on a device that missed `process.started`.
- `data.target` is the process's *subject* (an object id, a run id); the envelope `target` carries the process id.
- `done` / `total` are free-unit counters; absent `total` means unknown. `message` is a short status line (≤ 1024 bytes).
- Counters fold with absent-means-keep semantics on every frame that carries them; a partial frame never regresses a value. Frames from other publishers are sanitized before entering the view (invalid `kind` / `target` dropped, oversized text clipped, negative counters clamped).

The endpoints below emit these frames for you. A publisher in the same space that emits them by hand through `POST /v1/events` shows up in everyone's view identically.

## Endpoints

Account-scoped, behind the `/v1` auth guard. Every POST answers the bus publish reply `{"subscribers": n}` (local matches, fire-and-forget).

| Method | Path | Body | Emits |
|---|---|---|---|
| POST | `/v1/processes` | `{id, kind, title, scope, spaceId?, target?}` | `process.started` |
| POST | `/v1/processes/:id/progress` | `{done?, total?, message?}` — all optional, `{}` is a pure heartbeat | `process.progress` |
| POST | `/v1/processes/:id/finish` | `{status: "done" \| "failed" \| "cancelled", error?}` — `error {code?, message}` required iff `failed` | terminal event |
| POST | `/v1/processes/:id/cancel` | `{identity?}` | `process.cancel` on the process's own scope |
| GET | `/v1/processes` | — | `{"processes": [...]}`, expired rows swept, ordered by first observation |

`kind` is a short vocabulary token, `title` the human display line (≤ 256 bytes). Re-registering an id restarts its row — the supported owner-restart path. Progress and finish require the process to be live in the view under this account's identity, else `404 process.not_found`.

```bash
B=http://127.0.0.1:7001/v1
curl -X POST $B/processes -H 'Content-Type: application/json' \
  -d '{"id":"import-7f3a","kind":"import","title":"Importing notes.zip","scope":"space","spaceId":"SPACE"}'
curl -X POST $B/processes/import-7f3a/progress -H 'Content-Type: application/json' \
  -d '{"done":120,"total":900,"message":"pages"}'
curl -X POST $B/processes/import-7f3a/progress -d '{}'          # heartbeat
curl -X POST $B/processes/import-7f3a/finish -H 'Content-Type: application/json' \
  -d '{"status":"done"}'

any process list
any process cancel import-7f3a
```

A row in the live view:

```json
{ "identity": "A5k…", "self": true, "id": "import-7f3a",
  "kind": "import", "title": "Importing notes.zip",
  "scope": "space", "spaceId": "spc_…",
  "state": "running", "done": 120, "total": 900, "message": "pages",
  "startedAt": 1755000000, "updatedAt": 1755000042 }
```

`startedAt` / `updatedAt` are unix seconds of **local observation** on this device's clock. There is no `/processes/subscribe`; watch the raw frames with `GET /v1/events/subscribe?type=process.*` (or `any events subscribe --type 'process.*'`) and re-read the view when one lands.

## Heartbeat and staleness

- Owners POST progress at least every **15 s**, even when idle — progress doubles as the heartbeat. Explicit values set (`total: 0` back to unknown, `message: ""` to blank); absent values keep.
- A **running** row not heard from for **45 s** expires from the view — no synthesized event, it just vanishes; the owner's next progress POST answers `404 process.not_found` and it re-registers.
- A **terminal** row (`done` / `failed` / `cancelled`) lingers **60 s** so consumers observe the outcome, then expires.
- Stop heartbeating *before* finishing: a progress frame arriving after the terminal event resurrects the row to `running` (last-event-wins) until the next finish or expiry.

> **Why it matters.** Nothing is written anywhere to track progress, so a crashed owner leaves no stuck row to clean up, and a phone that joins a space mid-import fills its view from the next heartbeat without any history to replay.

## Cancel flow

```
canceller  POST /v1/processes/:id/cancel
           → server resolves (identity, id) in its view
           → emits process.cancel {identity: <owner>} on the process's own scope
owner      listens on GET /v1/events/subscribe?type=process.cancel&target=<id>
           → ignores cancels whose data.identity is not its own
           → stops work
           → POST /v1/processes/:id/finish {"status": "cancelled"}
```

Cancel never mutates the view; only the owner's terminal event (or staleness expiry if the owner is dead) removes a process. With `identity` omitted, a single match wins; zero matches answer `404 process.not_found`, several answer `409 process.ambiguous` with `details.identities` listing them — pass `identity` to pick one. Any space member may cancel; the owner is free to ignore.

## Remote visibility

| Scope | Visible on other devices |
|---|---|
| `device` | never leaves the machine |
| `account` | always — every server holds a standing interest on `ev/process/>` in the account's tech space, so processes on the account's other devices materialize with no local subscriber |
| `space` | while some local subscriber holds an interest on that space that covers `process.*` — `type=process.*`, a subset like `type=process.cancel`, or no type filter at all. A stream filtered to other types feeds nothing into the view |

A progress UI for a space keeps `GET /v1/events/subscribe?scope=space&spaceId=…&type=process.*` open and expects the view to fill within one heartbeat (≤ 15 s). The view is per device and eventually consistent.

## Built-in producers

The server reports its own long work through the same view, all at device scope and under `index.*` kinds. Ordinary indexing stays silent: the FTS and embedding passes announce only once they have been running for more than 3 s, so a single edit never appears while a cold re-index or a large catch-up shows up with its counters already carrying the work done so far.

| Process id | `kind` | Counters |
|---|---|---|
| `index.embed.<spaceId>` | `index.embed` | `done` / `total` in documents; the total is re-read every round so late arrivals extend the bar |
| `index.fts.<spaceId>` | `index.fts` | `done` in processed changes; `total` unknown |
| `index.model_download` | `index.model_download` | `done` / `total` in bytes; announces at download start, even offline |

A failed download attempt is not terminal — it retries forever, keeping the row `running` with a generic message; the only terminals are `done` and, on shutdown, `cancelled`. A worker stopped mid-drain finishes as `cancelled` rather than leaving a ghost. Failure messages are generic on the wire (details go to the server log), and all three producers ignore cancel requests. This is how a client answers "why is semantic search empty right now" — see [Indexing](../search/indexing.html).

## Errors

| Code | When |
|---|---|
| `404 process.not_found` | progress / finish on a process not live under this account; cancel with no match |
| `409 process.ambiguous` | cancel matched several identities; `details.identities` lists them |
| `events.*`, `space.*` | the emit path reuses the event-bus codes, e.g. `409 events.no_read_key` on `scope: space` |

> **Note.** Everything the event bus says applies: at-most-once, no replay, 64 KiB payloads, ~30 msg/s per-peer network budget. A 15 s heartbeat costs nothing; coalesce sub-second progress ticks.
