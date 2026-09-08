---
title: Agents
description: A space-resident AI agent whose conversations, memory, tools and schedule live inside the user's own encrypted data and run as replayable programs.
order: 0
---
# Agents

anybao is an AI agent that lives *in* an any space. Its conversation loop is a Python program, its memory is a dataset, its tools are programs, and every run is a trace you can replay bit-exact. The runtime that hosts it — **anyrt** — is a Rust library or process that cages a CPython guest and lets nothing out except through a recorded effect boundary.

## What "space-resident" means

```
any space "bao"  (derived per account, encrypted, synced)
 ├── general chat           the conversation — humans and the agent post here
 │    └── bao/log/v1        agent_turns + agent_chunks (history)
 ├── bao/brain/v1           agent_memory_items (long-term memory)
 ├── bao/config/v1          agent_config  (llm tiers, overlays)
 ├── bao/secrets/v1         agent_secrets (device-local, never synced)
 └── bao/triggers/v1        agent_triggers + agent_trigger_runs (cron/once/event)

overlay space "agent"       the agent's code: programs/ + skills/ (joined read-only)
overlay space "connectors"  linear, github, gmail, … (programs)

your device                 traces/ — one JSONL trace per run, device-local
```

Everything the agent knows or does is ordinary data that you can query, subscribe to, export and delete with the same HTTP surface as the rest of your space. The agent has no server of its own.

## How a conversation works

1. A human posts to the space's general chat. `anyrt serve` watches the chat over a subscription.
2. The runtime invokes the guest program `toolcaller@v1` with `{space, chatId, userText}`.
3. The program composes the system prompt from skills and tool docstrings, renders the **boot window** (recent turns plus summarised history), runs **auto-recall** over the memory index, and calls the model.
4. The model has one tool: `run_cell(code)`. Each call runs a Python cell in a persistent kernel; the cell's prints, last value and side effects come back as a digest.
5. Interim text posts as chat bubbles with `agent.done: false`; the final reply posts with `done: true`, and the turn is appended to `agent_turns` with a `traceRef` pointing at the run.

Every HTTP call, model call, clock read and random number the loop makes crosses the effect boundary and lands in the trace — so the whole exchange can be replayed offline without the network, and inspected turn by turn with `anyrt trace show`.

> **Why it matters.** Hosted agent frameworks keep threads, messages, usage and a "playground" on their servers. Here the thread *is* your chat, the usage counters are fields on the turn record, and the playground is the trace on your disk — end-to-end encrypted where it syncs, and yours to delete.

## Two ways to run it

| Mode | What it is | When |
|---|---|---|
| Embedded | `anyrt` as a Rust library inside an app (the desktop app bundles it this way) | one app, one device, no extra process |
| Standalone | `anyrt serve` next to an any server, configured by `anybao.toml` | headless machines, browser UI, remote runners |

Both modes run the same agent against the same space. Run only one of them per chat — see [Embedding anyrt](embedding-anyrt.html).

## In this section

<div class="cards">
<a href="conversations.html"><strong>Conversations</strong><span>The turn loop: message model, run_cell, digests, ceilings, break and inject.</span></a>
<a href="tools.html"><strong>Tools</strong><span>Tools are programs — the built-in set, how discovery works, and agent-authored programs.</span></a>
<a href="memory.html"><strong>Memory and recall</strong><span>Distilled memory items, dedup on save, auto-recall, history rollups and the maintenance jobs.</span></a>
<a href="subagents.html"><strong>Subagents</strong><span>Delegate a task to a quiet child loop and get a report back as a value.</span></a>
<a href="connectors.html"><strong>Connectors</strong><span>Linear, GitHub, Gmail and friends — host-held credentials, OAuth without tokens in guest code.</span></a>
<a href="agent-data.html"><strong>Agent data</strong><span>Every dataset the agent owns, its fields, and how to query it yourself.</span></a>
<a href="progress-and-ui.html"><strong>Progress and UI</strong><span>Agent-authored chat messages, typing indicators, progress bars and UI helpers.</span></a>
<a href="embedding-anyrt.html"><strong>Embedding anyrt</strong><span>Run the agent as a Rust library or as anyrt serve, and the one-agent-per-chat rule.</span></a>
</div>
