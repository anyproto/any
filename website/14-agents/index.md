---
title: Agents
description: Build an agent whose programs, structured data, conversations, and memory live in the user's database.
order: 0
---
# Agents

An agent can keep its conversation, memory, and working data in the same database as your application. Its tools can query those records, change them, and save the results for a later session. The user can inspect the same data through an app or the HTTP API.

Any supplies the database and encrypted sync. Its companion runtime, **anyrt**, executes programs on a device. **anybao** is the supplied agent built from those programs: a conversation loop, tools, memory retrieval, and background jobs. This section explains that agent and the pieces you can use in your own harness.

A **harness** is the code around a model: it chooses context, calls the model, executes tools, records results, and decides when to stop. In anybao, much of that policy is Python stored in spaces. Changing a program or skill can change the agent's behavior without rebuilding the runtime.

## Start with your use case

| You want to… | Read |
|---|---|
| run the supplied agent | [Runtime quickstart](../quickstart/anyrt.html) |
| control prompts, model calls, and tool execution | [Conversations](conversations.html) |
| give a custom harness persistent memory | [Memory and recall](memory.html), then [Agent data](agent-data.html) |
| add a tool or external service | [Tools](tools.html) and [Connectors](connectors.html) |
| build agent controls into an app | [Progress and UI](progress-and-ui.html) |
| host the runtime in your app | [Embedding anyrt](embedding-anyrt.html) |

## A session can outlive a process

The database holds structured records for messages, decisions, tasks, memory, and job progress. A later run reads those records to reconstruct the context it needs. For example, a research assistant can keep source notes and open questions; a project agent can keep decisions and task status alongside the user's own edits.

Persistent state does not mean uninterrupted execution. Each run has a fresh kernel and fixed [execution limits](../programs/limits.html). The device must be running the runtime to execute work. Programs that span many runs save checkpoints; scheduled work follows the [trigger's missed-run rules](../scheduling/index.html#semantics-in-one-table).

## What lives where

```text
agent space "bao" (derived per account, encrypted when synced)
 ├── general chat           humans and the agent post here
 │    └── bao/log/v1        agent_turns + agent_chunks (history)
 ├── bao/brain/v1           agent_memory_items (long-term memory)
 ├── bao/config/v1          agent_config (model tiers, search providers)
 ├── bao/secrets/v1         agent_secrets (credentials; guest reads refused)
 ├── bao/triggers/v1        agent_triggers (cron / once / event)
 └── bao/runs/v1            agent_runs (one summary per run)

overlay space "agent"      programs and skills, joined read-only
other overlay spaces       additional program repos, such as connectors

each device                full trace bodies in its local store
```

These stores are harness-owned runtime datasets. The server provides their storage and query operations; it does not hardcode the agent's memory or conversation policy. [Agent data](agent-data.html) lists the fields and shows how to read them yourself.

Memory records and conversation history are persistent. A **prompt** is the selected input assembled for a particular model call. A stored fact becomes model context only when the harness includes it. See [Memory and recall](memory.html) for this distinction and the retrieval flow.

## How a conversation runs

1. A person posts to the general chat. The runtime watches it through a subscription.
2. The runtime invokes `toolcaller@v1` with `{space, chatId, userText}`.
3. The program composes instructions from skills and tool docstrings, adds recent and summarized history, retrieves relevant memory, and calls the configured model.
4. The model requests `run_cell(code)` calls. Python variables remain available between cells in this run. Shell-enabled builds also expose `bash`.
5. The agent posts progress and a final reply. The turn is saved to `agent_turns`; its `traceRef` identifies the run.

HTTP calls, model requests, clock reads, and other effects are recorded. Randomness is seeded per run. Strict replay uses the recorded outputs to reproduce the exchange without repeating its external requests. Full traces stay on the executing device; the chat, history, memory, and run summaries sync.

### Models and data access

The model runs wherever its configured provider runs. A hosted model provider receives the prompt and any data included in tool results sent back to it. Encrypted sync protects storage and exchange between members; it does not conceal an inference request from that provider. Search embeddings have a separate [local or online configuration](../search/embedders.html).

The [conversation adapters](conversations.html#the-message-model) and per-model profiles define supported request shapes and tool behavior. Program storage, memory persistence, and model inference are separate parts of the system.

## Choose how to host it

| Mode | What runs | Typical use |
|---|---|---|
| Embedded | anyrt as a Rust library inside an app; the desktop app uses this mode | a desktop application |
| Standalone | `anyrt serve` beside a local `any` server, configured by `anybao.toml` | a headless device or browser UI |

Avoid starting both modes for the same chat on one device: they can produce duplicate replies. Across devices the runtime uses the device registry and responder ownership to select who answers. A standby device can still run jobs pinned to it. [Embedding anyrt](embedding-anyrt.html) explains the lifecycle and ownership rules.

## Build your own harness

Start by deciding which state must survive a run: messages, task records, retrieved sources, decisions, or job cursors. Give that state explicit [types and datasets](../database/runtime-datasets.html), then write programs that read and update it. Keep prompt assembly and model selection in your harness so you can inspect what each request included.

The supplied programs demonstrate conversation policy, recall, and tool discovery. You can adapt that policy, publish your own programs, and use run traces to inspect the result. Adding a new host capability is a runtime change; editing a program does not grant capabilities beyond the existing effect boundary.

<div class="cards">
<a href="conversations.html"><strong>Conversations</strong><span>Prompt assembly, model adapters, cells, and steering a run.</span></a>
<a href="memory.html"><strong>Memory and recall</strong><span>Persistent facts, history, context selection, and maintenance.</span></a>
<a href="agent-data.html"><strong>Read the agent's data</strong><span>Locate stores and query memory, turns, and run summaries.</span></a>
<a href="tools.html"><strong>Tools</strong><span>Expose programs to an agent and discover their interfaces.</span></a>
<a href="subagents.html"><strong>Subagents</strong><span>Delegate a bounded task and receive a report.</span></a>
<a href="connectors.html"><strong>Connectors</strong><span>Call outside services through host-held credentials.</span></a>
<a href="progress-and-ui.html"><strong>Progress and UI</strong><span>Render replies, run controls, and progress bars.</span></a>
<a href="embedding-anyrt.html"><strong>Host the runtime</strong><span>Build, configure, and stop an embedded or standalone agent.</span></a>
</div>
