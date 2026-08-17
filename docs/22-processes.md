# 22 — Processes

A thin convention + in-memory registry on top of the event bus
(doc 21) for long-running operations: agent runs, (re)index passes,
imports. Processes periodically broadcast `process.*` events; the
server keeps a **last-event-wins view** with staleness expiry behind
`GET /v1/processes`, so "what's running right now" needs no
persistence. Cancel is an event addressed at the owner, who reacts
and emits the terminal event.

**No new transport, no persistence.** Everything rides the bus's
at-most-once delivery; a server restart forgets every process by
design — owners simply re-register and keep heartbeating.

## Identity

Processes are keyed **`(sender.identity, id)`** — id uniqueness is
publisher-local. `sender.identity` is server-stamped (and
signature-verified on network scopes, doc 21 § Sender), so a remote
account can neither collide with nor spoof another publisher's
process. Cancel targets the composite key.

The devices of ONE account share an identity: coordinating id
uniqueness across them is the account's own job (put a run/device
uuid in the id when it matters).

## Event convention

Every process event is a bus envelope with **envelope `target` = the
process id** (the id therefore follows the event-target grammar
`[A-Za-z0-9._-]{1,128}` — it becomes a topic segment, e.g.
`ev/process/progress/<id>`). `data` shapes:

| type | data |
|---|---|
| `process.started` | `{kind, title, target?}` |
| `process.progress` | `{kind, title, target?, done, total?, message?}` |
| `process.done` / `process.cancelled` | `{kind, title, target?}` |
| `process.failed` | `{kind, title, target?, error: {code?, message}}` |
| `process.cancel` | `{identity}` — a directive at the owner, **not** a state change |

- The descriptor (`kind`/`title`/`target`) is folded into **every**
  frame, so one progress heartbeat fully materializes a process on a
  device that missed `process.started`.
- `data.target` is the process's *subject* (objectId, runId, …) —
  distinct from the envelope target, which carries the process id.
- `done`/`total` are free-unit counters (`total` absent = unknown);
  `message` is a short status line (≤ 1024 bytes).

The helper endpoints below emit these for you; a non-`any` publisher
in the same space can emit them by hand through the bus and shows up
in everyone's view identically.

## Heartbeat and staleness

- Owners re-POST progress at least every **15s**, even when idle —
  progress doubles as the heartbeat.
- A **running** row not heard from for **45s** expires from the view
  (owner presumed dead). No synthesized event — it just vanishes; the
  owner's next progress POST answers `404 process.not_found` and it
  re-registers.
- A **terminal** row (done/failed/cancelled) lingers **60s** after
  its terminal event so consumers observe the outcome, then expires.

## Cancel flow

```
canceller → POST /v1/processes/:id/cancel
          → server resolves (identity, id) in the view, emits
            process.cancel {identity: <owner>} on the process's OWN
            scope (device = local hub; account/space = SDK pub/sub)
owner     → listens on GET /v1/events/subscribe?type=process.cancel
            (+ its ids as target filters; internal producers observe
            the hub in-process)
          → stops its work
          → POST /v1/processes/:id/finish {"status":"cancelled"}
```

Cancel never mutates the view; only the owner's terminal event (or
staleness expiry, if the owner is dead) removes a process. An owner
ignores cancels whose `data.identity` isn't its own — same-id
processes of other publishers are unaffected.

## Endpoints

Account-scoped, outside the `/v1/spaces/:spaceId` group, behind the
`/v1` auth guard — same placement as `/v1/events`. All POSTs answer
the bus publish reply `{"subscribers": n}` (local matches;
fire-and-forget, doc 21).

### `POST /v1/processes` — register

Body `{id, kind, title, scope, spaceId?, target?}`; emits
`process.started` on `scope` (device/account/space — same
scope/spaceId validation as event publish). `kind` is a short
vocabulary token, `title` the human display line (≤ 256 bytes),
`target` the optional subject. Re-registering an id restarts the view
row — the supported owner-restart path.

### `POST /v1/processes/:id/progress` — progress / heartbeat

Body `{done, total?, message?}`. Requires the process live in the
view under this account's identity, else `404 process.not_found`
(register first). The server folds the registered descriptor into the
emitted frame.

### `POST /v1/processes/:id/finish` — terminal event

Body `{status: done|failed|cancelled, error?}`; `error {code?,
message}` is required iff `failed` and rejected otherwise. Same
local-liveness requirement as progress.

### `POST /v1/processes/:id/cancel` — request cancellation

Body `{identity?}`. Resolves against the whole view (the target may
be remote): explicit `identity` picks the composite key; omitted, a
single match wins, zero answers `404 process.not_found`, several
answer `409 process.ambiguous` (`details.identities` lists them).
Emits `process.cancel` on the process's own scope.

### `GET /v1/processes` — the live view

`{"processes": [...]}`, expired rows swept, ordered by first-seen
time. Row shape (`api.Process`): `{identity, self, id, kind, title,
scope, spaceId?, target?, state, done, total?, message?, error?,
startedAt, updatedAt}` — `startedAt`/`updatedAt` are unix seconds of
**local observation** (this device's clock). There is no
`/processes/subscribe` — watch the raw frames instead:
`GET /v1/events/subscribe?type=process.*`.

## Remote visibility

- **Own devices (account scope): always.** The server holds one
  standing account-scope interest (`ev/process/>`) on the tech space,
  so account-scope processes of this account's other devices
  materialize with no local subscriber.
- **Space members (space scope): while an interest is held.** Pub/sub
  interest is per-space; a space's process broadcasts reach this
  device only while some local subscriber names the space (e.g. a
  progress UI's `/v1/events/subscribe?scope=space&spaceId=…&type=process.*`
  stream — which a client showing progress holds anyway). Expect the
  view to fill within one heartbeat (≤ 15s) of acquiring the
  interest. Best-effort by design.
- **Device scope** never leaves the machine.

## Internal producers

`any` itself reports through the same registry (device scope, via the
in-process hub — no HTTP). Wired today:

- **Indexer embed drain** — id `index.embed.<spaceId>`, kind
  `index.embed`, target the spaceId. One started → progress-per-batch
  → done/failed sequence per drain that found pending docs (`total`
  unknown — pending is paged). Cancel is ignored by this producer: a
  cancelled drain would just restart on the next tick.

## Errors

- `404 process.not_found` — progress/finish on a process not live
  under this account, or cancel with no live match.
- `409 process.ambiguous` — cancel matched several identities;
  `details.identities` lists them, pass `identity` to pick one.
- The emit paths reuse the events codes (doc 21): space resolution
  errors and `409 events.no_read_key` on `scope: space`.

## Server implementation

- `internal/server/processes.go` — `processRegistry`, the
  last-event-wins view: keyed map + lazy sweep (no janitor
  goroutine), fed by a synchronous hub tap (`eventHub.addTap`) so it
  observes every publish loss-free — local emits and bridged network
  broadcasts alike. Created in the same once as the hub.
- `internal/server/handlers_processes.go` — the five handlers + the
  emit helper. Network-scope emits also apply to the registry
  directly after a confirmed publish: the SDK's Self loopback
  re-enters the hub only when a local interest covers the topic, and
  the local view must not depend on interests (the tap upsert is
  idempotent, double-apply is harmless).
- `internal/server/engine.go` — the standing account interest
  (acquired best-effort at boot, released on engine close) and
  `indexEmbedProcess`, the indexer bridge
  (`indexer.Options.OnProcess`).
- `internal/api/process.go` — wire types + state/event-type consts.
- Routes in `internal/server/routes.go`.

## CLI

```
any process list                       # GET  /v1/processes
any process cancel ID [--identity X]   # POST /v1/processes/:id/cancel
```

Watching frames: `any events subscribe --type 'process.*'`.
Registration/progress/finish are owner API calls, not human commands
— agents use the HTTP endpoints directly.

## Limits / future

- Everything doc 21 says: at-most-once, no replay, 64 KiB payloads,
  ~30 msg/s per-peer network budget — heartbeat at 15s costs nothing,
  but coalesce sub-second progress ticks.
- The view is per-device and eventually consistent; `subscribers`
  counts local matches only.
- No `/processes/subscribe` (raw frames cover it) and no
  process-scoped ACL: any space member may cancel — the owner is free
  to ignore.
