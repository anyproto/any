# Task: structured `agent` field on chat messages (replaces `fromAgent`)

Cross-repo contract with `../any-ui/docs/tasks/agent-message-field.md`
(UI side: render name, "Go to debug" context action, anchor navigation
on the debug page, typing indicator). Change both or neither.

**Status: `any` side SHIPPED** (server + CLI + web + bobrik-watch +
kernel; `fromAgent` removed outright — no read fallback, no prod data).
The any-ui side is still open. Note one deviation from the spec below:
new writes with `fromAgent` are rejected as `field_not_allowed` by the
handler (DAG path) and silently dropped at the HTTP layer (closed
request struct), and `dataVersion` was bumped to `chat_messages-v2`.

## Problem

`fromAgent` is a bare opaque string. The UI needs three things from an
agent-authored message that one string can't carry:

1. **name** — display label ("bao"), today abused as the whole tag.
2. **debugLink** — a drill-down into the run's `agent_debug_log` page,
   down to the specific turn that produced this message (right-click →
   "Go to debug").
3. **done** — liveness. While the agent is still running, its trailing
   messages are not "done"; the UI cycles a typing indicator
   (`<name> <verb> …`) until a `done: true` message lands.

## Wire shape

`fromAgent` (string) is **removed** and replaced by an optional `agent`
object on `ChatSendRequest` and on the stored/queried message record:

```json
{
  "text": "✅ done, here's the summary…",
  "agent": {
    "name": "bao",
    "debugLink": "any://<spaceId>/<debugLogObjectId>#turn_3",
    "done": true
  }
}
```

- `name` — required, non-empty string, ≤ 256 bytes (reuse the
  `MaxFromAgentBytes` budget; rename the constant `MaxAgentNameBytes`).
  Same trust model as `fromAgent`: a UI hint, NOT signature-verified;
  `creator` stays the change signer.
- `debugLink` — optional, non-empty when present, ≤ 2 KiB (same budget
  as attachment links). By convention an `any://<spaceId>/<objectId>`
  internal link, optionally with a `#turn_<n>` fragment where `n` is
  the 1-based LLM-turn ordinal (the `n` passed to `dcLogTurn`). The
  server treats it as an opaque string — no URL validation beyond size.
- `done` — required boolean. `false` = "the run that produced this
  message is still going"; `true` = terminal.
- No other keys inside `agent` (reject `agent.unknown_field` style, same
  as the top-level allow-list).
- The whole `agent` group is **create-only and immutable** — reject any
  `BeforeModify` op under `agent` (it already falls into the default
  reject branch; add a test).

No migration: existing records keep their stored `fromAgent`; new
writes with `fromAgent` are rejected (`chat.unknown_field`). Prototype
rules — readers treat `fromAgent`-only records as legacy (the UI task
covers an optional read fallback).

## Server changes (`any`)

- `internal/api/chat.go` — drop `FromAgent`; add
  `Agent *ChatAgentMeta` with `Name string` / `DebugLink string,omitempty`
  / `Done bool`. Replace `ErrChatFromAgentInvalid` with
  `ErrChatAgentInvalid = "chat.agent_invalid"`.
- `internal/chat/chat.go` — `FieldFromAgent` → `FieldAgent = "agent"`;
  update the header doc + `MaxAgentNameBytes`, add
  `MaxDebugLinkBytes = 2048` (or reuse the attachments link constant).
- `internal/chat/handler.go` — in `validateCreatePayload`, replace the
  `FieldFromAgent` case with `FieldAgent`: must be an object; visit
  keys with the name/debugLink/done rules above; unknown key rejects.
- `internal/server/handlers_chat.go` — replace the `fromAgent too long`
  pre-validation with the same checks against the parsed
  `ChatSendRequest.Agent` (nil = fine; non-nil → name required, sizes,
  no extra request-side fields to check since the struct is closed).
- `internal/cli/chat.go` — replace `--from-agent` with `--agent-name`,
  `--agent-debug-link`, `--agent-done` (bool). `--agent-name` set ⇒
  send the `agent` object; the other two require it.
- `internal/server/web/index.html` — dumb UI: read `m.agent` instead of
  `m.fromAgent` (`🤖 <name>` tag); the send form's `fromAgent` input
  becomes an agent-name input that sends `{agent: {name, done: true}}`.
- Tests: `internal/chat/handler_test.go`,
  `internal/server/handlers_chat_test.go`,
  `internal/e2e/multipeer_chat_test.go` — port `fromAgent` cases to
  `agent`, add: missing name, non-bool done, unknown key, oversized
  debugLink, modify-rejection of `agent.done`.
- Docs: `docs/03-api.md` § Chat (wire example lines ~785/833),
  `docs/08-clients.md` if it mentions `fromAgent`, root `CLAUDE.md`
  status item 6, regenerate `internal/server/docs/docs.go`.

**Out of scope:** `agent_turns.fromAgent` and
`agent_memory_items.fromAgent` (`internal/agentlog`, `internal/agentmem`)
keep their plain-string field — different datasets, "which agent wrote
this" is a name tag there, not a liveness/drill-down surface.

## bobrik-watch changes

Every bobrik message is agent-authored; the Go side owns `name`
(`--agent-name` flag, default "bao"), the JS kernel owns `debugLink` +
`done` because only it knows the debug page id (`_dc.pageId` from
`dcInit`) and the current turn ordinal.

- `main.go::chatSend` — body sends
  `agent: {name: agentName, debugLink, done}` instead of `fromAgent`.
- `main.go::parseChatReplyArg` — the object shape grows two optional
  keys: `{text, attachments?, debugLink?, done?}`. Bare-string calls
  and omitted `done` default to **`done: true`** — a mistakenly-true
  intermediate just stops the typing animation early; a
  mistakenly-false terminal spins it forever. Omitted `debugLink` ⇒
  field absent.
- `main.go::handleChanges` — the skip filter becomes
  `doc.Agent != nil` (decode `agent` as `json.RawMessage` or a struct;
  legacy `fromAgent`-tagged records can't re-fire — only fresh Added
  records trigger — so no fallback needed).
- `main.go::runAgent` — on `runWrapperProgram` error, post a terminal
  chat message (`done: true`, text = short error line) instead of
  stderr-only, so the UI indicator always resolves. Best-effort.
- `programs/toolcall_core@v1.js` — audit EVERY `chatReply` call site
  and tag it explicitly:
  - ~~run-start ack (`text: "…"`, `done: false`) right after `dcInit`~~
    — **removed (2026-06)**. A chat message existing only to drive a
    typing indicator was the wrong layer; the UI now starts the
    indicator locally when the user sends a message to the bao chat
    (`../any-ui/docs/tasks/agent-thinking-on-send.md`). The UI keeps
    hiding legacy stored `"…"` agent messages.
  - intermediate per-turn narration / skill-miss notes
    (`missA`/`missT`, per-turn text parts): `done: false`,
    `debugLink: any://<spaceId>/<pageId>#turn_<n>`.
  - terminal paths — final `✅` reply, LLM error, `max_tokens`
    auto-close/failure, empty-response, no-tool_use termination:
    `done: true` + the same `#turn_<n>` link.
  - `__quiet` (sub-agent) capture path is unaffected — captured
    replies never reach chatSend.
- `programs/toolcall_core@v1.js::buildTurnRec` (~line 1936) — the
  `agent_turns` record keeps its plain `fromAgent` name tag (out of
  scope above); no change, just don't "fix" it to the object shape.
- Docs: `cmd/bobrik-watch/BOBRIK.md`, `cmd/bobrik-watch/CLAUDE.md`
  (storage-shape bullet), root `CLAUDE.md` status item 6
  (`--from-agent` flag mention).

## Open questions

(The `"…"` ack questions are moot — the ack was removed, see above.)
