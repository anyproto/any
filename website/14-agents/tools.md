---
title: Tools
description: Tools are programs — the built-in agent tools, how the model discovers them, and how the agent authors new ones into its working space.
order: 20
---
# Tools

The agent's declared tool is `run_cell` (plus `bash` in a shell-enabled build). Everything else it can do is a **program** the cell code loads with `use("name@vN")`. A program marked `__any_tool__ = True` is a tool: its docstring and method list enter the system prompt, and it is callable from any cell.

## How the model discovers a tool

A tool is a `programs/<name>@v1/` folder (or a single `.py` file) with a module docstring. The system prompt renders each tool the way `help(tool)` does: the module docstring, an `Import:` line, then one `name(signature) [kind] — summary` line per public method, the summary being the first line of the method's docstring. `_`-prefixed methods and `main` stay callable but are not listed. Each method line shows its kind — `getter` (read), `mutator` (write / side-effecting), `setup` (a binder) — at the point of choice; `help(mod.method)` shows the full method doc.

```python
c = use("agent:any@v1")           # overlay-qualified: the agent repo
ws = use("agent:webSearch@v1")
ws.search("anyrt wasm component", "any-sync CRDT")
```

Programs resolve from **spaces, not the filesystem**: the agent's own code lives in the `agent` overlay space (joined read-only), and a working-space program of the same name shadows it. See [Modules and overlays](../programs/modules-and-overlays.html).

## Built-in agent programs

| Program | Tool | Summary |
|---|---|---|
| `any@v1` | yes | The `any` server client — read and write everything in a space. Types and properties are named by xKey, never by raw content id. |
| `llm@v1` | yes | Model calls in the neutral message shape — for one-off structured judgments inside a cell, and `read(file, prompt)` for images and PDFs. |
| `config@v1` | yes | The agent config store — the model behind each LLM tier and search tool. |
| `memory@v1` | yes | The memory write facade over the brain — `save_with_dedup`. |
| `recall@v1` | yes | Recall over one space — semantic search, `by_period`, graph `neighbors`, `hydrate`. |
| `history@v1` | yes | Conversation history reads (turns/chunks) and the boot window. |
| `subagent@v1` | yes | Delegate a self-contained subtask to a quiet fresh agent loop. |
| `webSearch@v1` | yes | Grounded web search — synthesized answers with real source urls; several queries per round-trip. |
| `deepResearch@v1` | yes | Deep research on one question, written into the space as pages — slow, multi-object. |
| `enrich@v1` | yes | Enrich a space from a transcript — sourced facts drafted as a proposal, applied only after user review. |
| `miniapp@v1` | yes | Author and manage Mini Apps — embeddable HTML/JS apps in the space. |
| `programs@v1` | yes | Write, edit and delete programs in a working space — live on the next `use()`. |
| `progress@v1` | yes | Progress bars for long jobs — start/tick/done/fail, one bar per (space, job). |
| `status@v1` | yes | The agent's status line in the user's status bar — one short phrase of prose. |
| `toolcaller@v1` | no | The conversation loop as a guest program. |
| `autorecall@v1` | no | Auto-recall injection — loop plumbing. |
| `extraction@v1` | no | Background memory extraction from conversations (cron). |
| `rollup@v1` | no | Hierarchical history rollup — the chunk pyramid (cron). |
| `linkgen@v1` | no | Hourly link-generation sweep over memory items (cron). |
| `evolution@v1` | no | Neighbor context/keyword refresh for memory items (cron, ships disabled). |
| `reflection@v1` | no | Reflection sweep over the memory store (cron, ships disabled). |
| `decay@v1` | no | Salience decay sweep (cron, ships disabled). |
| `remind@v1` | no | Deliver a scheduled reminder into a chat — the `once` trigger payload. |
| `ui@v1` | no | UI backend helpers invoked through serve's `POST /run` (e.g. fill an email summary). |

Service connectors (Linear, GitHub, Gmail, …) are tools too, deployed to a second overlay — see [Connectors](connectors.html).

## Skills

Skills are markdown documents deployed next to programs. Two tiers:

- **System skills** (`_`-prefixed: `_core`, `_any`, `_coding`, `_memory`, `_space_context`, `_meta_skill`, `_gmailSync`, `_soul`) are composed into every system prompt. `_soul` is the agent's identity and opens the system block verbatim; the rest teach how the tools work, the object-first rule for the space, the memory save policy and the space-context README convention. `_coding` is composed only when the runtime has shell effects. A working-space skill of the same name shadows the shipped one.
- **User skills** are `agent_skill` objects the user curates as playbooks. Only title, one-line description and id are injected; the agent fetches a body with `c.get_markdown(space, skill_id)` when the turn matches, and can author or edit skills itself when asked to capture a workflow.

## Agent-authored programs

The agent can persist a program into its working space through `programs@v1`:

```python
p = use("agent:programs@v1")
p.create_program(space, {"name": "mailWatch", "source": src})
# → {ok, objectId, spec, anyTool, probe, hint?}
p.edit_program(space, "mailWatch@v1", [{"oldText": "...", "newText": "..."}])
p.update_program(space, "mailWatch@v1", new_src)
p.delete_program(space, "mailWatch@v1")
```

A saved program is bit-identical in shape to what `anyrt deploy` writes, so `list_programs`, `use()` and `help()` need no second surface, and an edit is live on the very next `use()`. Write-time validation mirrors deploy's scan and is stricter: docstring present and within caps, valid `name@vN`, syntax gate, import allowlist, `@span` on every public method of a tool. A failed validation raises before anything is written; a program that saves but fails its post-save `use()` probe stays with `any_tool: false` and returns `{ok: false, saved: true, hint}` — recoverable, never a half-bound tool. Specs exported by a joined overlay are refused, so the agent cannot shadow pipeline-owned code.

An authored program runs in the same kernel, through the same effect boundary, under the same fuel governor as a deployed one; it may use existing credential refs but cannot mint or read them, and it may self-register `agent_triggers` records to run on a schedule.

> **Why it matters.** Persistence, not power: a saved program grants nothing `run_cell` did not already have. There is no approval gate because the sandbox is the gate — and the program is an object in your space you can read, diff and delete.

Promotion to the shared overlay repo is a human step: port it into the repo, review, `anyrt deploy`. See [Writing a program](../programs/writing-a-program.html).
