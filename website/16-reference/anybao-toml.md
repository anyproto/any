---
title: anybao.toml
description: Every key of the anyrt host config file — server address, working space, overlays with invites, trace path, and the guest-visible config layer.
order: 90
---
# anybao.toml

One TOML file configures everything host-side for `anyrt`: which any server to talk to, which space is the agent's home, which repo spaces supply its code, and where traces go. CLI flags override file values; secrets never appear in it.

## Discovery and precedence

`--config-file <path>` names the file; without the flag `./anybao.toml` is used when present, else pure defaults. **Unknown keys are a parse error** — a typo fails loudly rather than silently doing nothing.

Host precedence: built-in defaults < `anybao.toml` < CLI flag.

Guest-visible config (what programs see through `config.get`) cascades lowest → highest: `config_defaults.json` < the `[config]` table < `--config` JSON file < the space's `agent_config` overrides.

## Keys

| Key | Type | Default | Meaning |
|---|---|---|---|
| `addr` | string | `http://127.0.0.1:7001` | the any server base URL |
| `[agent].space` | string | `bao` | working space name — chat, memory, `agent_config`, user-authored skills and programs |
| `[agent].name` | string | `bao` | display name the agent signs messages with |
| `[agent].control_port` | u16 | `7010` | loopback control API port |
| `[overlays].<alias>` | string \| `{space, invite?}` | — | named module sources; values are STRICTLY space ids, never names. `agent` is the shipped programs + skills repo |
| `[paths].traces` | path | `traces` | trace file directory |
| `[config]` | table | — | guest-visible cascade layer: FLAT quoted dotted keys |

An overlay entry is either the bare space id or an inline table with an invite token: `agent = { space = "bafy…", invite = "…" }`. The invite is a regular any invite or guest token; on boot, `serve` joins a space it cannot see yet and proceeds, waiting only for program resolution.

Two behaviors follow from the overlay map:

- `use("std:tool@v2")` works the day a `std` entry exists — every entry feeds the resolver alias map verbatim.
- With no `agent` entry the alias binds to the working space itself, so a config-less setup behaves like a single-space install and `deploy` targets the working space too.

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
std = "bafy…"              # a bare id when the space is already visible

[paths]
traces = "traces"

[config]                   # guest-visible cascade layer: FLAT quoted dotted keys
"llm.tier.chat" = { provider = "anthropic", model = "claude-sonnet-5" }
```

## Secrets live elsewhere

API keys and connector credentials are seeded once — from the environment on first `serve`, from a `.connectors.env` file beside the config (dotenv-style `ref=value` lines), or from `--secrets-file` — and persisted device-locally in the space, never synced and never written to `anybao.toml`. An empty value in a seed file deletes the stored secret. See [Credentials](../programs/credentials.html).

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
