# 15 — UI command channel

An account-wide, **in-memory** channel that lets an agent (bobrik) drive a
connected any-ui window: *"open that space"*, *"open that document in that
space"*, and — later — other UI-side operations. The agent `POST`s a command;
the server fans it out over SSE to every connected UI window. **Nothing is
stored, nothing syncs, delivery is at-most-once.**

This is a consumer-side control channel, not an SDK method — the same kind of
exception as `/search`. It deliberately does **not** go through the
dataset/handler/CRDT machinery: a UI command is a transient directive to *this*
device's window, not shared space data.

## Why in-memory (not a dataset)

A persisted `ui_commands` dataset was considered and rejected. The problems it
created — and that the in-memory channel simply doesn't have:

- **No replay.** A persisted dataset replays its history to any reconnecting
  subscriber (jump to an hour-old document). In-memory has no snapshot — a
  subscriber sees only commands published *after* it connects.
- **At-most-once is correct here.** A command fired while no UI is connected is
  dropped. You do not want a queued "open doc" firing minutes later on
  reconnect.
- **No growth, no space coupling.** No object to host it, no per-space
  subscription, no derived ids to discover. One account per server ⇒ one global
  channel. The command names its *target* space/object in the payload, so a
  single subscription drives navigation anywhere.

## Endpoints

Both are account-scoped and sit **outside** the `/v1/spaces/:spaceId` group
(like `/v1/sync-status/subscribe`). They are behind the standard `/v1` auth
guard — an account must be authorized.

### `POST /v1/ui/commands` — publish

Body is the command:

```jsonc
{
  "action":   "open_space" | "open_object",  // open slug set; extensible
  "spaceId":  "<target space id>",            // required
  "objectId": "<target object id>",           // required iff action == "open_object"
  "source":   "bobrik"                         // optional UI hint, free-form
}
```

Response:

```json
{ "subscribers": 1 }
```

`subscribers` is how many connected UI windows received the command. `0` means
nobody was listening — the publish still succeeds (fire-and-forget). It's the
only delivery signal; there is no per-UI ack.

Validation (`400`):

- `request.bad_json` — body isn't valid JSON.
- `request.missing_field` — `action` empty, `spaceId` empty, or `objectId`
  empty when `action == "open_object"`.

`action` is an **open slug set**: unknown actions publish fine and are ignored
by clients that don't handle them. New UI operations add a new action (plus any
payload fields) with **no server change**.

### `GET /v1/ui/commands/subscribe` — subscribe (SSE)

One long-lived stream per UI window. Frames:

```
event: ready
data: {}

event: command
data: {"action":"open_object","spaceId":"…","objectId":"…","source":"bobrik"}

: keepalive

event: closed
data: {"reason":"server_shutdown"}
```

- `ready` — emitted once on connect. **No snapshot follows** (there is none).
- `command` — one per published command, the command object verbatim.
- `: keepalive` — comment heartbeat every ~25s while idle.
- `closed` — terminal frame with a `reason`:
  - `server_shutdown` — the server is exiting.
  - `overflow` — this subscriber fell too far behind (its 16-deep buffer
    filled) and was dropped; reconnect for a fresh stream.

  Reason strings are shared with the other subscribe streams so a client can
  branch on one reason set. A plain client disconnect writes no frame (the peer
  is already gone).

## Server implementation

- `internal/server/uicmd_hub.go` — `uiCmdHub`, a process-global broadcaster
  (mutex + `map[int]chan api.UICommand`). `publish` is a **non-blocking** fan
  out: a subscriber whose buffer is full is dropped (channel closed) rather
  than blocking the publisher. Created lazily via `deps.uiHub()` so every deps
  construction path gets one — the hub has no engine/SDK dependency.
- `internal/server/handlers_ui_commands.go` — `uiCommandPublish` (validate →
  `publish` → `{subscribers}`) and `uiCommandSubscribe` (the SSE driver,
  modeled on `streamStatusSSE`: `ready`, keepalive goroutine, terminal `closed`
  on shutdown/overflow, registered with `streamsWG` so graceful shutdown waits
  for it).
- `internal/api/uicommand.go` — `UICommand`, `UICommandPublishResponse`, the
  action constants.
- Routes registered in `internal/server/routes.go`.

## CLI

```
any ui open-space  <spaceId>
any ui open-object <spaceId> <objectId>
any ui subscribe                          # one JSON line per SSE frame
```

`any ui subscribe` is the easy way to watch the channel while testing the agent
tool end-to-end.

## Consumers

- **bobrik tool** — `ui.openSpace` / `ui.openObject`, a thin `POST
  /v1/ui/commands`. See `cmd/bobrik-watch/CLAUDE.md` and the tool sources under
  `cmd/bobrik-watch/`.
- **any-ui** — one `EventSource('/v1/ui/commands/subscribe')` mounted on boot,
  dispatching `command` frames into navigation. See
  `../any-ui/docs/tasks/ui-commands.md`.

## Limits / future

- Fire-and-forget, at-most-once. No persistence, no replay, no retry, no ack
  beyond the `subscribers` count. This is intentional for navigation directives.
- Single account per server ⇒ the hub is genuinely global. If
  multi-account-per-process ever lands, key the hub by account.
- More commands = new `action` values; document them here.
