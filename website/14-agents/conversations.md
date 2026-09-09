---
title: Conversations
description: How one chat message becomes one replayable run — the neutral message model, the single run_cell tool, digests, ceilings, and steering a live run.
order: 10
---
# Conversations

A conversation turn is one invocation of the guest program `toolcaller@v1`. It drives the whole cycle — prompt assembly, model calls, cell execution, chat replies, turn persistence — inside the cage, so a recorded turn replays deterministically.

## The message model

The loop speaks a provider-neutral shape; adapters in `llm@v1` translate it to each provider's wire.

```python
Message  = {role: "user"|"assistant", parts: [Part]}
Part     = Text{text}
         | ToolCall{id, name, args}          # name is always "run_cell"
         | ToolResult{call_id, content, is_error}
         | Thinking{text?, provider_state?}  # opaque, round-tripped
LLMReply = {parts: [Part], stop: "done"|"tool"|"length", usage: Usage}
Usage    = {in, out, cacheRead, cacheWrite}
```

`use("llm@v1").chat(messages, system=…, tier=…, tools=…)` issues exactly one `http.post` effect per model call. The request names a credential ref; the host injects the API key *after* the payload is recorded, so the key never enters the guest or the trace.

| Adapter | Providers | Notes |
|---|---|---|
| `anthropic` | Claude models | thinking state round-tripped byte-exact; prompt-cache breakpoints set automatically at end of system and end of conversation |
| `openai-compat` | vLLM, llama.cpp, SGLang, ollama, OpenRouter | `cached_tokens` surfaces as `usage.cacheRead`; reasoning content is kept in the trace, never resent |
| `fenced` | tool-weak completion models | parses a fenced `cell` block out of plain text into a ToolCall |

Which provider and model a **tier** (`codegen`, `classify`, …) resolves to comes from the `config.get` effect — the `llm.tier.*` keys in [agent config](agent-data.html).

## One tool: `run_cell`

The model has a single tool, `run_cell(code)`. Each call runs a Python cell in a persistent kernel — variables from one cell are visible in the next. Each turn:

```
drain mailbox → llm.chat → stop?
  tool   → run the cell (a "cell" span) → digest → append ToolResult → loop
  done   → post final reply (done: true), append the turn, return
  length → ask the model to wrap up text-only, post the summary
```

Interim assistant text before a tool call posts as a `done: false` progress bubble. A cell error comes back as an `is_error` ToolResult with the error digest — the model self-corrects; there is no separate fix loop.

The cell's result is rendered into the ToolResult as a **digest**:

- **Output** — printed values, numbered; then **Last value**.
- **Side effects** — the cell's effect records grouped as `effect × count`, mutations called out individually with `any://` links.
- Values over a token budget collapse to a stub naming the exact `values.get("<cell>", i)` call that returns the stored value in a later cell — never re-run the producing call to see it again.
- Large values also get a one-paragraph orientation summary from the cheap tier, labelled as model-generated.

## Prompt assembly

The system prompt is composed by the program from skills (`_core`, `_any`, `_memory`, `_soul`, …), each tool's docstring plus one-line method list, and a runtime-context section (space id, chat id, agent name). Its fingerprint is recorded per run so prompt drift is diagnosable from traces. Then the conversation prompt:

1. **Boot window** (`history@v1`) — the newest raw turns at full resolution, then level-1 chunk summaries, then level-2, until a token budget fills. All history is present at decreasing resolution.
2. **Auto-recall** (`autorecall@v1`) — topical memory and history hits injected as a synthetic `run_cell` call and result, so the model treats them as evidence, not doctrine. See [Memory and recall](memory.html).
3. The current user message, with a timestamp and the UI-context suffix (which space and object the user is looking at).

## Ceilings and steering

| Control | Effect |
|---|---|
| `maxTurns` (default 108) | hitting it triggers a **wrap-up turn** — a final constrained call to summarise what is done and pending |
| `maxTokensTotal` (default 1M) | same wrap-up |
| `inject(text)` | appended as a user message before the next model call |
| `break(soft)` | trips the wrap-up turn now |
| `break(hard)` | the host watchdog interrupts the engine, posts the terminal bubble and records the run as interrupted |

Inject and soft break arrive through a mailbox the loop drains between turns as a recorded effect — a replayed conversation replays its interruptions. Chat messages that arrive mid-run on the same conversation are routed into the mailbox rather than starting a second run.

## What gets persisted

The turn record (`agent_turns`, on the chat's log child) holds `userText`, `replies` (what the user saw), `think` (narration that did not go to chat), `effects` one-liners, the `llm` usage scalars, `stopReason` (`done | wrapup | break_soft | break_hard | length | error`), `interrupted`, and `traceRef` — the run id.

```
anyrt trace ls --program toolcaller     # conversations, newest first
anyrt trace show run_<id>               # turns, cells, effects, results
anyrt trace show run_<id> --stats       # per-turn tokens / cache / cost
```

> **Note.** Traces are device-local by default: they are large and rarely read, so syncing them would bloat the encrypted space. The lean layer — turn record, chat, chunks, memory — syncs everywhere; deep replay of an old run works on the device that ran it.

Programs the loop is built from: `toolcaller@v1` (the loop), `llm@v1` (adapters), `history@v1` (turns, chunks, boot window), `autorecall@v1` (injection). All of them are [programs](../programs/index.html) deployed to the agent overlay space — changing loop policy is a deploy, not a runtime release.
