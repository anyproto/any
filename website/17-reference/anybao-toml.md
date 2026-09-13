---
title: anybao.toml
description: Every key of the anyrt host config file — server address, working space, overlays with invites, trace storage and retention, and the agent-config seeds.
order: 90
---
# anybao.toml

One TOML file configures everything host-side for `anyrt`: which any server to talk to, which space is the agent's home, which repo spaces supply its code, and where and how long traces are kept. CLI flags override file values; secrets never appear in it.

## Discovery and precedence

`--config-file <path>` names the file; without the flag `./anybao.toml` is used when present, else pure defaults. **Unknown keys are a parse error** — a typo fails loudly rather than silently doing nothing.

Host precedence: built-in defaults < `anybao.toml` < CLI flag.

Agent config — what programs read through `config.get` — lives in the working space's `agent_config` store, and `config.get` reads it on every call. `serve` seeds that store at start: the `[config]` table (plus a `--config` JSON file) is written through, overwriting a stored value that differs; the runtime's built-in defaults are written only for keys with no row. `anyrt run` has no store and answers from the built-in defaults under its own `--config` file.

## Keys

| Key | Type | Default | Meaning |
|---|---|---|---|
| `addr` | string | `http://127.0.0.1:7001` | the any server base URL |
| `[agent].space` | string | `bao` | working space — chat, memory, `agent_config`, user-authored skills and programs. A name in the server's [derived-spaces](../collaboration/derived-spaces.html) registry (`bao`) resolves to the account's derived space; any other name is looked up, and created on a miss |
| `[agent].name` | string | `bao` | the `agent.name` the agent signs its chat messages with |
| `[agent].control_port` | u16 | `7010` | loopback control API port |
| `[overlays].<alias>` | string \| `{space, invite?}` | — | named module sources; values are STRICTLY space ids, never names. `agent` is the shipped programs + skills repo |
| `[paths].traces` | path | `traces` | JSONL trace directory for the `file` backend; raw blob files (`blobs/`) for either backend |
| `[traces].backend` | `"any"` \| `"file"` | `"any"` | where `serve` lands traces: the any server's local store, scoped to the working space, or JSONL files under `[paths].traces` |
| `[traces].retain_conversations` | duration \| `"never"` | `"60d"` | how long conversation run bodies are kept (`d`, `h`, `m`, `s` units) |
| `[traces].retain_jobs` | duration \| `"never"` | `"30d"` | how long every other program's run bodies are kept |
| `[config]` | table | — | agent-config hard seeds: FLAT quoted dotted keys |

An overlay entry is either the bare space id or an inline table with an invite token: `agent = { space = "bafy…", invite = "…" }`. The invite is a regular any invite or guest token; on boot, `serve` sends a join request for a space the account has not joined and proceeds, waiting only for program resolution. An overlay that is not joined and has no invite is a boot error.

Two behaviors follow from the overlay map:

- `use("std:tool@v2")` works the day a `std` entry exists — every entry feeds the resolver alias map verbatim.
- With no `agent` entry the alias binds to the working space itself, so a config-less setup behaves like a single-space install; `anyrt deploy` without `--target` publishes to the working space.

Retention runs inside `serve` (first pass a minute after boot, then hourly) on the `any` backend. It deletes run bodies only: every run's synced summary is kept, so "did it run" questions and `traceRef` links still resolve after the body is gone.

## Full example

```toml
# anybao.toml
addr = "http://127.0.0.1:7001"   # any server

[agent]
space = "bao"              # working space: chat, brain, memory,
name = "bao"               #   agent_config, user-authored skills/programs
control_port = 7010

[overlays]                 # named module sources (the alias namespace);
agent = { space = "bafy…", invite = "2gChBt…" }   # shipped programs + skills, joined on boot
std = "bafy…"              # a bare id when the space is already joined

[paths]
traces = "traces"

[traces]
backend = "any"
retain_conversations = "90d"
retain_jobs = "never"

[config]                   # agent-config hard seeds: FLAT quoted dotted keys
"llm.tier.codegen" = { provider = "anthropic", model = "claude-sonnet-5", base_url = "https://api.anthropic.com", api_key_ref = "llm.key.anthropic" }
```

## Secrets live elsewhere

API keys and connector credentials are stored as rows of the working space's secret store — synced between the account's own devices, end-to-end encrypted, and never readable by guest code. They are entered through the chat's credential prompt, or seeded from a `.connectors.env` file beside the config (dotenv-style `ref=value` lines; fallback: the current directory) or `--secrets-file`, which `serve` writes through on every start. An empty value in a seed file deletes the stored secret. Environment variables are not read. See [Credentials](../programs/credentials.html).

```
# .connectors.env
llm.key.anthropic=sk-ant-…
connector.key.github=ghp_…
```

## Embedded use

A Rust host builds the same resolved config without a file:

```rust
let mut cfg = anyrt::Config::builder()
    .addr("http://127.0.0.1:7001")
    .agent_space("bao")
    .overlay_with_invite("agent", "<repoSpaceId>", "<inviteToken>")
    .build();
anyrt::config::bootstrap(&mut cfg);
let agent = anyrt::serve::start(cfg)?;
```

> **Note.** Run one agent per working space. A desktop app embedding anyrt and a standalone `anyrt serve` pointed at the same space are two agents on one chat — every message gets two replies. Guides: [anyrt quickstart](../quickstart/anyrt.html), [Modules and overlays](../programs/modules-and-overlays.html), [Embedding anyrt](../agents/embedding-anyrt.html).
