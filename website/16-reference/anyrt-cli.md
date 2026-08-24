---
title: anyrt CLI
description: Every anyrt subcommand and flag — run one program, serve the agent, deploy a repo, inspect traces, check API drift.
order: 60
---
# anyrt CLI

`anyrt` is the anybao runtime binary: a Rust host with the sandboxed CPython kernel compiled in. `serve` runs the agent next to an any server; `run` executes one program; `deploy` publishes programs and skills into a space; `trace` reads the run files every execution leaves behind.

Logs go to stderr through `tracing` (filter with `RUST_LOG`, default `info`); stdout is reserved for command output — `run` prints exactly one JSON line. Host defaults come from `anybao.toml` ([anybao.toml](anybao-toml.html)); flags override the file.

## `anyrt run`

Run one guest program's `main(args)` and exit.

```
anyrt run <spec> [--args JSON] [--kernel PATH] [--programs DIR] [--traces-dir DIR]
          [--config FILE] [--secrets-file FILE] [--timeout-s N]
          [--from-space NAME|ID] [--addr URL] [--config-file PATH]
```

| Flag | Default | Meaning |
|---|---|---|
| `<spec>` | — | program spec, e.g. `hello@v1` or `agent:toolcaller@v1` |
| `--args` | `{}` | JSON passed to `main` |
| `--kernel` | embedded | local kernel override (dev) |
| `--programs` | `repos/_agent/programs` | local program dir (offline mode) |
| `--traces-dir` | `traces` | where the run's trace file lands |
| `--config` | — | JSON file of guest-visible config keys; shadows the `[config]` table |
| `--secrets-file` | — | dotenv-style `ref=value` lines; `.connectors.env` beside the config file is also read, the flag wins on duplicate refs |
| `--timeout-s` | `120` | wall-clock limit |
| `--from-space` | — | resolve `use()` from this space's deployed programs (name or id) instead of the local dir — serve's resolver, one-shot |
| `--addr` | config `addr` | any server base URL for `--from-space` |
| `--config-file` | `./anybao.toml` when present | host config for overlays and `--from-space` |

Output is one JSON line; the process exits 1 when `status` is not `ok`:

```json
{"status":"ok","traceRef":"run_3f1c…","durationMs":412,"fuelUsed":18234,"value":{"hello":"world"},"error":null}
```

```bash
anyrt run hello@v1 --args '{"name":"any"}'
anyrt run agent:rollup@v1 --from-space bao
```

`run` has no device-local secret store: every seeded secret lives in memory for this run only, and managed OAuth refs work in degraded mode. See [Writing a program](../programs/writing-a-program.html).

## `anyrt serve`

The agent: watch a chat, run conversations and triggers.

```
anyrt serve [--addr URL] [--space NAME] [--agent-name NAME] [--kernel PATH]
            [--traces-dir DIR] [--control-port N] [--config-file PATH]
            [--config FILE] [--secrets-file FILE]
```

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | `http://127.0.0.1:7001` | any server base URL |
| `--space` | `bao` | working space name (chat, memory, your edits) |
| `--agent-name` | `bao` | display name the agent signs its messages with |
| `--kernel` | embedded | dev override |
| `--traces-dir` | `traces` | |
| `--control-port` | `7010` | loopback control API |
| `--config-file` | `./anybao.toml` when present | host config |
| `--config` | — | JSON file shadowing the `[config]` table |
| `--secrets-file` | — | HARD seeds: `ref=value` lines rewrite the device-local secret store (empty value = delete); merged over `.connectors.env`, the flag wins |

Programs and skills load from spaces, not the filesystem — run `anyrt deploy` before the first `serve` against a fresh space. Boot validates every `[overlays]` entry with a strict space lookup and joins it with the configured invite when the space is not yet visible; a missing `agent` overlay space fails with "run anyrt deploy first".

```bash
ANTHROPIC_API_KEY=… anyrt serve          # first run on a fresh space seeds the key device-locally
```

### Control API (`127.0.0.1:<control-port>`)

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/election` | — | `{app, enabled, active, peerId, winner}` |
| GET | `/triggers` | — | every registry entry with its rollup |
| GET | `/triggers/:id` | — | one trigger record |
| GET | `/triggers/:id/runs` | — | last 20 `trigger_runs` rows, newest first |
| PATCH | `/triggers/:id` | `{spec?, enabled?}` | updated record; a new `spec` re-arms forward |
| POST | `/triggers/:id/enable` | — | record; resets the circuit breaker |
| POST | `/triggers/:id/disable` | — | record |
| POST | `/run` | `{program, args?}` \| `{source, args?, program?}` | the `run` envelope |

The mutating routes write through to the `agent_triggers` dataset — the dataset is the source of truth, and a registry-only edit would be reverted by the next reconcile tick. `POST /run` with `source` runs caller-supplied program text (default name `adhoc@v1`); its `use()` imports still resolve through the space. See [Trigger schema](trigger-schema.html) and [Runs and monitoring](../scheduling/runs-and-monitoring.html).

## `anyrt deploy`

Publish a repo folder to a space, hash-gated, so a running `serve` picks changes up on its next run — no restart.

```
anyrt deploy [--source DIR] [--target SPACE_ID|OVERLAY_NAME] [--addr URL] [--space NAME] [--config-file PATH]
```

| Flag | Default | Meaning |
|---|---|---|
| `--source` | `.` | repo folder: `<src>/programs/`, `<src>/skills/`, `README.md` |
| `--target` | the working space | a raw space id or an overlay name from `[overlays]`; strict — never creates |
| `--space` | config `agent.space` | working space name for the no-target default |

```bash
anyrt deploy --source repos/_agent --target agent
# deploy → ["toolcaller@v1", "rollup@v1", …]
# skills → ["…"]
# readme → updated
```

Details: [Modules and overlays](../programs/modules-and-overlays.html).

## `anyrt trace`

Trace tooling over device-local run files (`traces/<run_id>.jsonl`). A bare run id resolves against the default traces dir, so `trace ls` output feeds straight into `show`.

```
anyrt trace ls [DIR] [--program SUBSTR] [-n N]        # newest first: status, duration, turns, turn-1 title; -n 0 = all (default 30)
anyrt trace show <file|run_id> [--full] [--system] [--stats] [--boot] [--seq N]
anyrt trace follow [<file|run_id>] [--dir DIR] [--program SUBSTR]   # live render; default = newest run in --dir
anyrt trace stats <DIR>                               # distributions + tuning suggestions
```

| `show` flag | Meaning |
|---|---|
| `--full` | lift every clip limit (full text, code, outputs) |
| `--system` | include the system prompt (turn 1's otherwise-invisible channel) |
| `--stats` | per-turn metrics table: tokens, cache, cells, effects, costUsd |
| `--boot` | dump the boot window — the messages fed to the model before turn 1's user text |
| `--seq N` | dump one record by seq, blob-resolved, pretty-printed |

```bash
anyrt trace ls --program toolcaller
anyrt trace show run_3f1c2a9d0b4e7f61 --stats
anyrt trace follow --program rollup
```

`follow` exits when the run completes. See [Traces and replay](../programs/traces-and-replay.html).

## `anyrt drift`

API-drift check: the vendored OpenAPI 3.1 pin against the coverage manifest.

```
anyrt drift [--spec api/openapi.vendored.json] [--manifest api/coverage.json] [--refresh]
```

Exits 1 when the two disagree. `--refresh` rewrites the fingerprints of already-triaged endpoints in place; new and removed endpoints stay human-triaged.

> **Note.** The kernel is compiled into the binary, so `anyrt` ships as one file and `--kernel` exists only to test a rebuilt kernel without recompiling. Embedding the runtime as a Rust library instead of running the CLI is covered in [Embedding anyrt](../agents/embedding-anyrt.html).
