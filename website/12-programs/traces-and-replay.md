---
title: Traces and replay
description: The append-only run log — where it is stored, record kinds, canonical keys, spans, blob spills — and the strict and loose replay modes built on it.
order: 40
---
# Traces and replay

A trace records what a run requested, what came back, and how it ended. Use it to inspect a database write, understand a model call, or replay a program against recorded results. Records are appended in order as the run executes.

For a completed run, find its ID in the CLI result or [run summary](../scheduling/runs-and-monitoring.html), then use the viewers below. First check where the body was stored: a synced summary can point to a trace held only on another device.

## Where traces live

Trace **bodies are device-local**. `anyrt serve` writes them into the any server's local store, as three never-synced collections of the agent's working space: `trace_records` (one document per record), `trace_blobs` (spilled values) and `trace_runs` (one summary per run). `anyrt run`, and a serve configured with `[traces] backend = "file"`, write `traces/run_<id>.jsonl` plus a `.blobs` sidecar in the `[paths].traces` directory instead. Raw bytes a run fetched or built sit beside either backend as files under `<traces dir>/blobs/`.

What syncs is the lean layer. Every run publishes one `agent_runs` summary — `{runId, program, device, startedAt, endedAt, durationMs, status, errorType, turns, cells, effects, mutations, tokens, costUsd, model, title, triggerId}` — so every device of the account can find every device's runs, and a chat reply's `agent_turns` record carries `traceRef`, the run id. The body stays on the device that ran it.

The writer streams: the header lands at run start and records flush as spans and cells close, so a live run is readable and a crashed run leaves a partial trace that `trace show` reports as `status: incomplete`. A serve expires bodies after `[traces] retain_conversations` (chat runs, default `60d`) and `retain_jobs` (every other program, default `30d`); `"never"` keeps them. Summaries are kept forever.

## Record kinds

```jsonc
// header (first record)
{"kind": "header", "schema": 2, "run": {"id": "run_…", "program": "remind@v1",
 "host": "rust", "startedAt": 1756108800.4, "seed": "<64 hex>"}}

// effect — one per syscall crossing
{"kind": "effect", "seq": 17, "effect": "http.get", "cell": "main",
 "input": {…}, "key": "sha256:…", "output": …, "error": null,
 "meta": {"durMs": 88, "mocked": false, "class": "read"}}

// span — guest-declared grouping of a facade call
{"kind": "span", "seq": 24, "phase": "begin", "span": "s1", "parent": null,
 "name": "any.create_object", "cell": "main", "input": {…}, "key": "sha256:…"}
{"kind": "span", "seq": 31, "phase": "end", "span": "s1", "name": "any.create_object",
 "ok": true, "output": {…}, "meta": {"durMs": 88, "effects": 3, "mutations": 1, "kind": "mutator"}}

// cell — the host verdict at run end
{"kind": "cell", "seq": 23, "cell": "main", "ok": true, "error": null, "interrupted": false,
 "metrics": {"fuel_used": 184223, "duration_ms": 240}}
```

| Field | Meaning |
|---|---|
| `seq` | the record's stable address — cited by errors, `--seq`, and `traceRef` anchors; replay never reads it |
| `key` | sha256 over the effect name and the canonical input (sorted keys) — the mock-match identity |
| `meta.class` | `read` or `mutate`, declared at the boundary |
| `meta.hosted` | set on records the host emits itself, such as an `oauth.refresh` ahead of the request it serves |
| `span` | id of the innermost open span; absent outside spans |
| `startedAt` / `seed` | the run's frozen wall clock and entropy seed — replay re-derives every random value from them |

Effects between a span's begin and end carry `"span": "s1"`. A span is a view-level collapse, never a recording-level one: the inner records stay canonical and are what replay consumes. Model turns are `llm.chat` spans (their output carries the token usage cost accounting sums), and each model cell is a `cell` span. A run that traps mid-span gets its spans force-closed (`error.type: "unclosed_span"`) so the log stays well-nested.

Values larger than 64 KB are replaced by `{"__blob": "sha256:…", "bytes": N}` and stored with the trace; raw bytes are referenced as `{"__blob", "bytes", "mime"}`. Replay and the viewers resolve both transparently.

## Reading a run

Every `trace` subcommand reads a server's local store with `--addr <any url> [--space bao]`, or a jsonl directory or file without it:

```sh
anyrt trace ls --addr http://127.0.0.1:7001                        # 30 newest runs, all programs
anyrt trace ls --addr http://127.0.0.1:7001 --program toolcaller   # conversations only (cron runs outnumber them)
anyrt trace show --addr http://127.0.0.1:7001 run_<id>             # chronological render: turns, cells, effects (* = mutate)
anyrt trace show … run_<id> --full      # lift every clip
anyrt trace show … run_<id> --system    # + the system prompt
anyrt trace show … run_<id> --boot      # + the boot window verbatim
anyrt trace show … run_<id> --stats     # per-turn tokens / cache / cost table
anyrt trace show … run_<id> --seq 42    # one record, blob-resolved
anyrt trace follow --addr …             # live-render the newest run as records land
anyrt trace stats --addr …              # p50/p95 fuel, duration, tokens over every run
anyrt trace blob sha256:<hex> -o out.png   # one raw blob's bytes
anyrt trace ls traces/                  # a jsonl directory instead
```

Clipped lines are locators: a clip ends `… (+N chars — --full)`, every effect line prints its `#seq`, and result blocks name the record they were mined from. A jsonl trace is one record per line — never pretty-print the file itself:

```sh
jq 'select(.kind=="effect" and .meta.class=="mutate")' traces/run_<id>.jsonl
jq 'select(.kind=="span" and .phase=="begin")' traces/run_<id>.jsonl
```

Cross-run questions — which run created an object, what wrote this week, which runs failed — are aggregation pipelines over `trace_records` and `trace_runs` through `POST /v1/local/aggregate`, the same queries a program runs with `effects.query`.

From a chat reply to its trace: the turn log is the `bao/log/v1` child of the general chat's bundle, typed with the hidden harness type `agent_log`, and its turns live in the `<typeId>_agent_turns` storage collection.

```sh
LOGT=$(curl -s "http://127.0.0.1:7001/v1/spaces/$SPACE/types?includeHidden=true" \
  | jq -er '.types[] | select(.xKey=="agent_log") | .id')
LOG=$(curl -s -X POST "http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/system%3Ageneral-chat%2Fv1/children" \
  -H 'Content-Type: application/json' \
  -d "{\"seed\": \"bao/log/v1\", \"type\": \"$LOGT\"}" | jq -er .objectId)
TURNS=$(curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/datasets \
  | jq -r '.datasets[].name | select(endswith("_agent_turns"))')
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$LOG\", \"dataset\": \"$TURNS\", \"sort\": [\"-seq\"], \"limit\": 1}" \
  | jq -r '.records[0].traceRef'
```

## Two replay modes

| Mode | Matching | Use |
|---|---|---|
| **strict** (`replay`) | a cursor — the next effect call must match the next unconsumed record (same effect + key, in sequence); cell and span records are checkpoints | golden tests, deterministic evaluation |
| **loose** (`mock`) | by `(effect, key)` anywhere in the log, FIFO per key; unmatched calls execute live or fail, per option, and are flagged `mocked: false` | debugging *edited* code against old effects |

A mismatch in strict mode is a **divergence error** carrying both expected and actual. A map can answer "seen this input?"; only the cursor detects reordered, extra or missing calls — which is what makes determinism testable. Mocked calls skip execution but still record (`mocked: true`). Host-emitted records (an `oauth.refresh` ahead of the HTTP call it serves) are drained by the cursor ahead of the guest record that triggered them.

Replay never needs a secret: credentials are resolved after the canonical input is recorded, so nothing sensitive is in the log to begin with, and mocked calls don't execute.

> **Why it matters.** Golden replay tests record once and assert forever, with no server and no API keys. Because `module.resolve` records carry the source bytes, a trace is self-contained — it replays after the program was edited, redeployed or deleted.
