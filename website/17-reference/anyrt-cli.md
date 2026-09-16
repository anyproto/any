---
title: anyrt CLI
description: Every anyrt subcommand and flag — run one program, serve the agent, deploy a repo, inspect traces, check API drift.
order: 60
---
# anyrt CLI

`anyrt` is the anybao runtime binary: a Rust host with the sandboxed CPython kernel compiled in. `serve` runs the agent next to an any server; `run` executes one program; `deploy` publishes programs and skills into a space; `trace` reads the runs every execution leaves behind.

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
| `--traces-dir` | `traces` | where the run's JSONL trace lands |
| `--config` | — | JSON file of guest-visible config keys, layered over the built-in defaults; with `--from-space` it shadows the space's `agent_config` reads for this run only |
| `--secrets-file` | — | dotenv-style `ref=value` lines; `.connectors.env` beside the config file is also read, the flag wins on duplicate refs |
| `--timeout-s` | `120` | wall-clock limit |
| `--from-space` | — | resolve `use()` from this space's deployed programs (name or id) instead of the local dir — serve's resolver, one-shot. A space with the agent's config store gets read-through `config.get` / `config.set` like serve |
| `--addr` | config `addr` | any server base URL (a `--config` `any.base_url` key wins) |
| `--config-file` | `./anybao.toml` when present | host config for overlays and `--from-space` |

Output is one JSON line; the process exits 1 when `status` is not `ok`:

```json
{"status":"ok","traceRef":"run_3f1c…","durationMs":412,"fuelUsed":18234,"value":{"hello":"world"},"error":null}
```

```bash
anyrt run hello@v1 --args '{"name":"any"}'
anyrt run agent:rollup@v1 --from-space bao
```

`run` has no secret store: every seeded secret lives in memory for this run only (empty values are dropped), and managed OAuth refs work in degraded mode. See [Credentials](../programs/credentials.html).

## `anyrt serve`

The agent: watch the chat, run conversations and triggers.

```
anyrt serve [--addr URL] [--space NAME] [--agent-name NAME] [--kernel PATH]
            [--traces-dir DIR] [--control-port N] [--config-file PATH]
            [--config FILE] [--secrets-file FILE]
```

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | config `addr` (`http://127.0.0.1:7001`) | any server base URL |
| `--space` | config `agent.space` (`bao`) | working space name (chat, memory, your edits) |
| `--agent-name` | config `agent.name` (`bao`) | display name the agent signs its messages with |
| `--kernel` | embedded | dev override |
| `--traces-dir` | config `paths.traces` (`traces`) | JSONL traces for the `file` backend; raw blobs under `blobs/` for either backend |
| `--control-port` | config `agent.control_port` (`7010`) | loopback control API |
| `--config-file` | `./anybao.toml` when present | host config |
| `--config` | — | JSON file of config keys, merged over the `[config]` table; both are written through to the space's `agent_config` store at start |
| `--secrets-file` | — | HARD seeds: `ref=value` lines write through to the space's secret store (empty value = delete); merged over `.connectors.env`, the flag wins |

Programs and skills load from spaces, not the filesystem — run `anyrt deploy` before the first `serve` against a fresh space. Boot probes every `[overlays]` entry against the space list: a space not yet joined gets a join request with its configured invite and boot proceeds, with program resolution waiting for the sync; a space not joined and with no invite is a boot error. Environment variables are not read for keys — seed them from `.connectors.env` or `--secrets-file`, or enter them in the chat's credential prompt.

### Control API (`127.0.0.1:<control-port>`)

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/status` | — | `{envelope, beatSec}` — the presence beat the agent publishes next |
| GET | `/election` | — | `{app, enabled, active, peerId, winner}` |
| POST | `/break/:runId` | `{hard?}` | `{run, hard}` — stop a run in flight (soft wraps up, hard interrupts) |
| GET | `/triggers` | — | every registry entry: `{id, name, kind, owner, enabled, lastRunAt, lastStatus, consecutiveFailures, limits}` |
| GET | `/triggers/:id` | — | one trigger record |
| GET | `/triggers/:id/runs` | — | the 20 newest `agent_runs` summaries for the trigger, newest first |
| PATCH | `/triggers/:id` | `{spec?, enabled?}` | updated record; a new `spec` re-arms forward |
| POST | `/triggers/:id/enable` | — | record; resets the circuit breaker |
| POST | `/triggers/:id/disable` | — | record |
| POST | `/run` | `{program, args?}` \| `{source, args?, program?}` | the `run` envelope |

A failed request answers `400 {"error": "…"}`. The mutating trigger routes write through to the `agent_triggers` dataset — the dataset is the source of truth, and a registry-only edit would be reverted by the next reconcile tick. `POST /run` with `source` runs caller-supplied program text (default name `adhoc@v1`); its `use()` imports still resolve through the space. See [Trigger schema](trigger-schema.html) and [Runs and monitoring](../scheduling/runs-and-monitoring.html).

## `anyrt deploy`

Publish a repo folder to a space, hash-gated, so a running `serve` picks changes up on its next run — no restart.

```
anyrt deploy [--source DIR] [--target SPACE_ID|OVERLAY_NAME] [--addr URL] [--space NAME] [--config-file PATH]
```

| Flag | Default | Meaning |
|---|---|---|
| `--source` | `.` | repo folder: `<src>/programs/`, `<src>/skills/`, `README.md` |
| `--target` | the working space | an overlay name from `[overlays]` or a raw space id; strict — never creates |
| `--addr` | config `addr` | any server base URL |
| `--space` | config `agent.space` | working space name for the no-target default |

Each program and skill reports `created`, `updated` or `unchanged`:

```bash
anyrt deploy --source repos/_agent --target agent
# deploy → {"autorecall@v1": "unchanged", "toolcaller@v1": "updated", …}
# skills → {"_core": "unchanged", …}
# readme → unchanged
```

Details: [Modules and overlays](../programs/modules-and-overlays.html).

## `anyrt trace`

Trace tooling. With `--addr`, a subcommand reads the trace storage collections a serve keeps in that any server's local store (`--space` names the agent space, default `bao`); without it, it reads a JSONL traces directory — what `anyrt run` and the `file` backend write. A bare run id resolves against `traces/`, so `trace ls` output feeds straight into `show`.

```
anyrt trace ls [DIR] [--addr URL] [--space NAME] [--program SUBSTR] [-n N]
anyrt trace show <file|run_id> [--addr URL] [--space NAME] [--full] [--system] [--stats] [--boot] [--seq N]
anyrt trace follow [<file|run_id>] [--dir DIR] [--addr URL] [--space NAME] [--program SUBSTR]
anyrt trace stats [DIR] [--addr URL] [--space NAME]
anyrt trace blob <hash> [--dir DIR] [-o FILE]
anyrt trace import [DIR] --addr URL [--space NAME] [--dest-dir DIR]
```

| Subcommand | What it does |
|---|---|
| `ls` | newest first: status, duration, turns, turn-1 title; runs still going list as `in-flight`; `-n 0` = all (default 30) |
| `show` | render one run: turns, cells, effects (`*` = mutate), results |
| `follow` | live render as records land; default = the newest run; exits when the run completes |
| `stats` | fuel, duration and token distributions plus tuning suggestions |
| `blob` | write one raw blob's bytes (an image, a PDF) to stdout or `-o` |
| `import` | copy a JSONL traces directory into a server's local store — records, blobs and a summary per run; raw blobs go to `--dest-dir/blobs`; idempotent |

| `show` flag | Meaning |
|---|---|
| `--full` | lift every clip limit (full text, code, outputs) |
| `--system` | include the system prompt (turn 1's otherwise-invisible channel) |
| `--stats` | per-turn metrics table: tokens, cache, cells, effects, costUsd |
| `--boot` | dump the boot window — the messages fed to the model before turn 1's user text |
| `--seq N` | dump one record by seq, blob-resolved, pretty-printed |

```bash
anyrt trace ls --addr http://127.0.0.1:7001 --program toolcaller
anyrt trace show --addr http://127.0.0.1:7001 run_3f1c2a9d0b4e7f61 --stats
anyrt trace follow --addr http://127.0.0.1:7001 --program rollup
anyrt trace show run_3f1c2a9d0b4e7f61          # a run written by anyrt run
```

See [Traces and replay](../programs/traces-and-replay.html).

## `anyrt drift`

API-drift check: the vendored OpenAPI 3.1 pin against the coverage manifest.

```
anyrt drift [--spec api/openapi.vendored.json] [--manifest api/coverage.json] [--refresh]
```

Exits 1 when the two disagree. `--refresh` rewrites the fingerprints of already-triaged endpoints in place; new and removed endpoints stay human-triaged.

> **Note.** The kernel is compiled into the binary, so `anyrt` ships as one file and `--kernel` exists only to test a rebuilt kernel without recompiling. Embedding the runtime as a Rust library instead of running the CLI is covered in [Embedding anyrt](../agents/embedding-anyrt.html).
