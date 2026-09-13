---
title: Embedding anyrt
description: Run the agent as a Rust library inside your app or as the anyrt serve process — build order, configuration, logging, and why only one agent may watch a chat.
order: 80
---
# Embedding anyrt

anyrt is a Rust crate with a thin CLI on top. Apps embed it as a library and run the agent in-process; a headless or browser-UI setup runs the same agent as `anyrt serve` next to an any server. Both modes need the wasm kernel built first, and both must obey one rule: one agent per chat.

## Build order

```
nix develop        # canonical env (or: direnv allow)
uv sync
make kernel        # componentized CPython → bin/kernel.wasm
make runtime       # runtime/target/release/anyrt
make runtime-shell # the same binary with shell effects (the bash tool)
```

`make kernel` is required before **any** cargo build of a consumer: the kernel embeds into the binary or library, so there is no agent binary and no asset tree to ship. The agent's own code (programs, skills) is not shipped either — it loads from spaces at run time.

## Embedded mode (Rust library)

The desktop app bundles anyrt this way: a Cargo path dependency on `anybao/runtime`, the kernel compiled in.

```rust
let mut cfg = anyrt::Config::builder()
    .addr("http://127.0.0.1:7001")
    .agent_space("bao")
    .overlay_with_invite("agent", "<repoSpaceId>", "<inviteToken>")
    .build();
anyrt::config::bootstrap(&mut cfg);       // guest config defaults (llm tiers, search providers)
let agent = anyrt::serve::start(cfg)?;    // non-blocking; threads inside
// … agent.stop() on shutdown
```

`start(cfg)` does everything serve does — agent space and general chat setup, store provisioning, config and secret seeding, overlay join, trigger arming — then spawns the chat watch, ticker and control threads and returns an `AgentHandle`. `stop()` flips a shutdown flag checked by the watch and reconnect loops, the trigger ticker and the control API, then joins; a conversation already running finishes on its own.

Public surface: `Config` / `ConfigBuilder`, `serve::{start, AgentHandle, RunCtx}`, `runner::{Cage, RunOutcome, run_program}` for one-shot runs, `replay`, `deploy`, and `anyapi::Client` (with a `Transport` trait for injection in tests). `ConfigBuilder::consent_hook` hands each OAuth consent URL to the embedder instead of opening a browser.

**Logging rides `tracing`.** Install a subscriber or get silence:

```rust
tracing_subscriber::fmt()
    .with_env_filter("anyrt=info")
    .init();
```

Secrets an embedder ships (a bundled demo-keys resource, for example) go in through `ConfigBuilder::secret_override` as hard seeds, or `ConfigBuilder::secret` as soft seeds that never overwrite a stored value. A key the user enters while the agent runs is `AgentHandle::set_secret(ref, value)` — one row write, read by the next credentialed request, no restart. See [Credentials](../programs/credentials.html).

## Standalone mode (`anyrt serve`)

The same agent as a process, configured by `anybao.toml`:

```toml
addr = "http://127.0.0.1:7001"

[agent]
space = "bao"               # working space: chat, memory, your edits

[overlays]                  # program repos (values are space ids);
agent = { space = "<agentRepoSpaceId>", invite = "<inviteToken>" }

[paths]
traces = "traces"
```

```
./bin/any run --config ./any-config.yml        # 1. the any server, 127.0.0.1:7001
anyrt serve                                     # 2. the agent (reads anybao.toml)
cd ../any-ui && pnpm dev                        # 3. a browser UI proxying /v1
```

| Command | What it does |
|---|---|
| `anyrt serve` | run the agent: watch the chat, run conversations and triggers |
| `anyrt deploy --source repos/_agent --target <space\|overlay>` | publish a repo folder (`programs/`, `skills/`) to a space, hash-gated |
| `anyrt run <name@vN>` | run one program from the local directory (offline dev) |
| `anyrt trace ls` / `show <run_id>` / `follow` | list, render, and live-render runs (`--addr` reads a server's trace store) |

`deploy` is the only publish step; a running serve picks up changes on its next conversation. Serve also exposes a loopback control API (default port 7010): `GET /status`, `GET /election`, `POST /break/:runId`, `GET /triggers[/:id[/runs]]`, `PATCH /triggers/:id`, `POST /triggers/:id/enable|disable`, and `POST /run` — see [Progress and UI](progress-and-ui.html) and [Runs and monitoring](../scheduling/runs-and-monitoring.html). Full flag reference: [anyrt CLI](../reference/anyrt-cli.html) and [anybao.toml](../reference/anybao-toml.html).

## One agent per chat

Browser mode has no embedded agent — `anyrt serve` *is* the agent. Do not also run the desktop app against the same working space: two agents on one chat means doubled replies.

Across **devices** the runtime handles this itself through the [devices registry](../auth/devices.html). Every serve registers under the app slug `bao` and reads the server-computed winner:

| Situation | Outcome |
|---|---|
| this device is the winner | active — claims the chat responder and unassigned triggers, runs the standing jobs |
| no other device row carries `apps.bao` | claims |
| another live device is the winner | standby — chat watch disconnected, no bubbles |
| the winner's row was deleted | claims; concurrent survivors converge on the server tiebreak |

The chat watch runs on whichever device owns the `chat-watch` responder record; a device that stands down releases it and the winner claims it. A standby still fires triggers pinned to its own peer id (pins are election-independent) and still answers `POST /run`. A pruned device fires nothing. When a standby takes over, its reconnect snapshot yields the full unanswered backlog, so nothing typed while the other device slept is lost.

> **Why it matters.** Two laptops on one account, one of them asleep: the sleeper waking and replaying the backlog would double-answer every message. The election is the same substrate a UI uses to show which device is "the agent", and the verdict comes from one server rule rather than two clients guessing.

## Testing an embedding

`anyrt run <name@vN>` runs one program from a local folder with no serve loop and no election, and `POST /run` with inline `source` runs unpublished program text through a live serve; every run writes a trace you can replay. The harness tests are described in [anybao harness](../testing/anybao-harness.html).
