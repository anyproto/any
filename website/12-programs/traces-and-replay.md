---
title: Traces and replay
description: The append-only JSONL run log — record kinds, canonical keys, spans, blob spills — and the strict and loose replay modes built on it.
order: 40
---
# Traces and replay

Every run writes a trace: an append-only, ordered JSONL log with one record per effect call, streamed to disk as it happens. The trace is the replay oracle, the mock source, the debug record and the trigger run log — one format, one toolchain.

## Where traces live

Traces are **device-local**: `traces/run_<id>.jsonl` (the `[paths].traces` directory), plus a `.blobs` sidecar when values spill. They are never synced as objects — a trace is big and rarely read. What syncs is the lean layer: a chat reply's `agent_turns` record and a trigger's run record each carry a `traceRef` naming the local file.

The writer streams: the header lands at run start, every record appends as it commits. `tail -f` works on a live run, and a crashed run leaves a partial trace that `trace show` reports as `status: incomplete`.

## Record kinds

```jsonc
// header (line 1)
{"kind": "header", "schema": 2, "run": {"id": "…", "program": "remind@v1",
 "programHash": "…", "args": {…}, "instance": "…", "startedAt": …}}

// effect — one per syscall crossing
{"kind": "effect", "seq": 17, "effect": "http.get", "cell": "toolu_abc",
 "input": {…}, "key": "sha256:…", "output": …,
 "error": {"type": "…", "message": "…"},
 "meta": {"t": 1234.5, "durMs": 88, "mocked": false, "class": "read", "usage": {…}}}

// span — guest-declared grouping of a facade call
{"kind": "span", "seq": 24, "phase": "begin", "span": "s1", "parent": null,
 "name": "any.create_object", "cell": "toolu_abc", "input": {…}, "key": "sha256:…"}
{"kind": "span", "seq": 31, "phase": "end", "span": "s1", "name": "any.create_object",
 "ok": true, "output": {…}, "meta": {"durMs": 88, "effects": 3, "mutations": 1, "kind": "mutator"}}

// cell — the host verdict at cell end
{"kind": "cell", "seq": 23, "cell": "toolu_abc", "ok": true, "interrupted": false,
 "metrics": {"fuel_used": 184223, "mem_pages": 512, "duration_ms": 240,
             "value_store": {"entries": 7, "bytes": 91234}}}
```

| Field | Meaning |
|---|---|
| `seq` | the record's stable address — cited by errors, `--seq`, and `traceRef` anchors; replay never reads it |
| `key` | sha256 of the canonical input (sorted keys, explicit defaults) — the mock-match identity |
| `meta.class` | `read` or `mutate`, declared at the boundary |
| `span` | id of the innermost open span; absent outside spans |
| `meta.usage` | LLM tokens etc. — cost accounting is aggregation over this |

Effects between a span's begin and end carry `"span": "s1"`. A span is a view-level collapse, never a recording-level one: the inner records stay canonical and are what replay consumes. A cell that traps mid-span gets its spans force-closed (`error.type: "unclosed_span"`) so the log stays well-nested.

Outputs larger than ~64 KB are replaced by `{"__blob": "sha256:…", "bytes": N}` and stored in the sidecar; replay and the viewers resolve them transparently.

## Reading a run

```sh
anyrt trace ls                          # 30 newest runs, all programs
anyrt trace ls --program toolcaller     # conversations only (cron runs outnumber them ~25:1)
anyrt trace show run_<id>               # chronological render: turns, cells, effects (* = mutate)
anyrt trace show run_<id> --full        # lift every clip
anyrt trace show run_<id> --system      # + the system prompt
anyrt trace show run_<id> --boot        # + the boot window verbatim
anyrt trace show run_<id> --stats       # per-turn tokens / cache / cost table
anyrt trace show run_<id> --seq 42      # one record, blob-resolved
anyrt trace follow                      # live-render the newest run as records land
anyrt trace stats traces/               # p50/p95 fuel, duration, tokens over a directory
```

Clipped lines are locators: a clip ends `… (+N chars — --full)`, every effect line prints its `#seq`, and result blocks name the record they were mined from. The raw file is plain JSONL — never pretty-print the file itself:

```sh
jq 'select(.kind=="effect" and .meta.class=="mutate")' traces/run_<id>.jsonl
jq 'select(.kind=="span" and .phase=="begin")' traces/run_<id>.jsonl
```

From a chat reply to its trace:

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$CHAT\", \"dataset\": \"agent_turns\", \"sort\": [\"-seq\"], \"limit\": 1}" \
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

## Promoting a run

Traces stay local by default. The designed escape hatch is an on-demand promote — uploading one trace as an object with file attachments — opt-in per trace, never automatic.
