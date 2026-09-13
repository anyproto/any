---
title: Conversations
description: How one chat message becomes one replayable run — the neutral message model, the run_cell tool, digests, ceilings, and steering a live run.
order: 10
---
# Conversations

A conversation turn is one invocation of the guest program `toolcaller@v1`. It drives the whole cycle — prompt assembly, model calls, cell execution, chat replies, turn persistence — inside the cage, so a recorded turn replays deterministically.

## The message model

The loop speaks a provider-neutral shape; adapters in `llm@v1` translate it to each provider's wire.

```python
Message  = {role: "user"|"assistant", parts: [Part]}
Part     = Text{text}
         | ToolCall{id, name, args}          # name is "run_cell" (or "bash")
         | ToolResult{call_id, content, is_error}
         | Thinking{text?, provider_state?}  # opaque, round-tripped
         | File{media_type, data, name?}     # an image, PDF or text in the turn
LLMReply = {parts: [Part], stop: "done"|"tool"|"length", usage: Usage}
Usage    = {in, out, cacheRead, cacheWrite}
```

`use("agent:llm@v1").chat(messages, system=…, tier=…, tools=…)` issues exactly one `http.post` effect per model call. The request names a credential ref; the host injects the API key *after* the payload is recorded, so the key never enters the guest or the trace.

| Adapter | Serves | Notes |
|---|---|---|
| `anthropic` | Claude models | thinking state round-tripped byte-exact; prompt-cache breakpoints set automatically at end of system and end of conversation |
| `openai-compat` | every `/chat/completions` server — OpenAI, OpenRouter, Gemini, DeepSeek, Groq, Together, vLLM, llama.cpp, ollama | `cached_tokens` surfaces as `usage.cacheRead`; reasoning content is kept in the trace and resent only for models that need it for tool-call continuity |

A per-model **profile** carries the traits the loop budgets from — context window, output cap, prompt style, how tool calls travel. Tool-weak models run under the `fenced` (or `xml`) tool mode: the loop parses a fenced `cell` block out of plain text into a ToolCall.

Which provider and model a **tier** (`codegen`, `classify`, `vision`) resolves to comes from the `config.get` effect — the `llm.tier.*` keys in [agent config](agent-data.html), each `{provider, model, base_url, api_key_ref}`.

## The tools: `run_cell` and `bash`

The model's tool is `run_cell(code)`. Each call runs a Python cell in a persistent kernel — variables from one cell are visible in the next. A runtime built with shell effects adds a second tool, `bash`, which runs one command line on the device and binds its output in the kernel as `sh.last` for the next cell. Each turn:

```
drain mailbox → llm.chat → stop?
  tool   → run the cell (a "cell" span) → digest → append ToolResult → loop
  done   → post final reply (done: true), append the turn, return
  length → ask the model to wrap up text-only, post the summary
```

Interim assistant text before a tool call posts as a `done: false` progress bubble. A cell error comes back as an `is_error` ToolResult with the error digest — the model self-corrects; there is no separate fix loop.

The cell's result is rendered into the ToolResult as a **digest**:

- **Output** — printed values, numbered; then **Last value**.
- **Side effects** — the cell's effect records grouped as `name ×count`, with mutations and failures listed individually by `#seq`.
- Values over a token budget collapse to a stub naming the exact `values.get("<cell>", i)` call that returns the stored value in a later cell — never re-run the producing call to see it again.
- Four or more sequential calls of one effect earn a hint to fan out with `batch`.

## Prompt assembly

The system prompt is composed by the program from the spaces: the identity (`_soul`, first and verbatim), the system skills (`_core`, `_any`, `_memory`, …), the list of user skills, each tool's docstring plus one line per method, the configured repos, the memory categories in use, and a runtime-context section (agent space, chat id, agent name, the installed apps). A fingerprint of the system block — and of the identity — is recorded on the turn, so prompt drift is diagnosable from data. Then the conversation prompt:

1. **Boot window** (`history@v1`) — the newest raw turns at full resolution, then level-1 chunk summaries, then level-2, until a token budget fills (at most a quarter of the model's context window). All history is present at decreasing resolution.
2. **Auto-recall** (`autorecall@v1`) — topical memory and history hits injected as a synthetic `run_cell` call and result, so the model treats them as evidence, not doctrine. See [Memory and recall](memory.html).
3. The current user message, with a timestamp and the user's view (which space and object they had open when they sent it).

## Ceilings and steering

| Control | Effect |
|---|---|
| `maxTurns` (default 300) | hitting it triggers a **wrap-up turn** — a final constrained call to summarise what is done and pending |
| `maxTokensTotal` (default 1M) | same wrap-up |
| context nearly full | a call whose input reaches 85% of the context window is the last before the wrap-up |
| a chat message mid-run | injected as a user message before the next model call |
| soft break | trips the wrap-up turn at the next drain; escalates to hard after 20 s if the run never drained it |
| hard break | the engine traps the guest at its next tick; the host posts `Stopped.` and records the run as interrupted |

Injections and soft breaks arrive through a mailbox the loop drains between turns as a recorded effect — a replayed conversation replays its interruptions. A break is data, not a word: the client posts a chat message with a `control` group `{kind: "break", hard?}`, or an operator calls `POST /break/<runId>` on the control API. Chat messages that arrive mid-run on the same conversation are routed into the mailbox rather than starting a second run.

## What gets persisted

The turn record (`agent_turns`, on the chat's log child) holds `userText`, `replies` (what the user saw), `fromAgent`, `traceRef` (the run id), `interrupted`, and an `llm` object with `stopReason` (`done`, `wrapup`, or `break_hard` for a run the host stopped), token and cell counts, and the prompt fingerprints. A hard-broken run gets a minimal turn written by the host, so the next boot window still sees the stopped exchange.

```
anyrt trace ls --addr http://127.0.0.1:7001 --program toolcaller   # conversations, newest first
anyrt trace show --addr http://127.0.0.1:7001 run_<id>             # turns, cells, effects, results
anyrt trace show --addr http://127.0.0.1:7001 run_<id> --stats     # per-turn tokens / cache / cost
```

> **Note.** Trace bodies stay on the device that ran the run, in its any server's local store: they are large and rarely read, so syncing them would bloat the encrypted space. The lean layer — turn record, chat, chunks, memory, and one `agent_runs` summary per run — syncs everywhere; deep replay of an old run works on the device that ran it.

Programs the loop is built from: `toolcaller@v1` (the loop), `llm@v1` (adapters), `history@v1` (turns, chunks, boot window), `autorecall@v1` (injection). All of them are [programs](../programs/index.html) deployed to the agent overlay space — changing loop policy is a deploy, not a runtime release.
