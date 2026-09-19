---
title: Limits
description: Fuel budgets, wall-clock deadlines and hard breaks — how each is enforced in the wasm cage, what a program sees when it hits one, and how to stay under.
order: 70
---
# Limits

Every guest instruction — CPython's interpreter loop included — is wasm compiled by wasmtime, so all of it can be counted and bounded. A runaway program is a clean, typed failure, never a hung process, and the outcome and its metrics land in the trace.

The kernel is fresh for every invocation, so long-lived work belongs in records: save a cursor, finish a bounded batch, continue in a later [scheduled](../scheduling/index.html) run.

## The bounds

| Limit | Mechanism | Character |
|---|---|---|
| instruction budget | **fuel** — a per-block counter decremented at compile time; zero traps at exactly that instruction | deterministic and replayable: same code + inputs = same cutoff |
| wall timeout | **epoch deadline** — a load+compare at function entries and loop back-edges; a ticker bumps the epoch | wall-clock driven, lands inside any guest loop |
| hard break | the run's interrupt flag, checked on every epoch tick | always lands; the tool for cancellation |

Fuel budgets; the epoch cancels. Epoch checks run only in guest code — inside a host function (an effect) interruption is cooperative, and the effects honor it: HTTP calls carry timeouts, and a blocked shell command is killed when the run is interrupted, so no un-cancellable path remains. No separate memory cap is configured; an allocation that cannot grow the guest's linear memory surfaces as an ordinary `MemoryError`.

## Defaults

| Setting | Value |
|---|---|
| fuel per run | 50 billion instructions — tens of seconds of pure compute; a hard runaway ceiling, not a working budget for ordinary jobs |
| wall timeout | 120 s for `anyrt run` (`--timeout-s`); 3600 s for runs under `anyrt serve` |
| epoch tick | 10 ms |
| blob spill threshold | 64 KB per recorded value |

## What exhaustion looks like

Fuel exhaustion surfaces as a typed **`FuelExhausted`** error whose message says what works: split the work into smaller chunks and check `fuel.state` in long loops. The trap is deterministic, so a verbatim retry can never succeed; in a conversation the runtime posts the typed error into the chat so the next turn acts on it. A wall timeout ends the run as an error too. Only a hard break ends it `interrupted`. The verdict lands in the run's terminal cell record:

```jsonc
{"kind": "cell", "seq": 23, "cell": "main", "ok": false, "interrupted": false,
 "error": {"type": "FuelExhausted", "message": "run exceeded its compute budget …"},
 "metrics": {"fuel_used": 50000000000, "duration_ms": 31877}}
```

For a trigger run, the run summary's `status` is `FAILED` (or `interrupted` after a hard break), the trigger's `lastStatus` is `error`, and the trigger's circuit breaker counts it — see [Runs and monitoring](../scheduling/runs-and-monitoring.html).

## Checkpointing long jobs

Long data jobs avoid the cliff cooperatively. The `fuel.state` syscall returns `{remaining, budget}`, refreshed by the epoch ticker (so up to one tick stale — a checkpoint signal, not an exact meter), and recorded like any effect so replay stays stable:

```python
JOB = "my-sync"

def main(args):
    c = use("agent:any@v1")
    space, brain = c.bao_space(), c.get_brain()["objectId"]
    rows = c.query(space, brain, "agent_job_state", filter={"id": JOB}, limit=1)
    cursor = rows[0].get("cursor", 0) if rows else 0
    for page in pages_from(cursor):              # your own paging helper
        process(page)
        cursor = page["next"]
        f = effect("fuel.state", {})
        if f["remaining"] < f["budget"] * 0.15:  # checkpoint, then exit cleanly
            c.upsert_record(space, brain, "agent_job_state", JOB, {"cursor": cursor})
            return {"ok": True, "resumed_at": cursor, "partial": True}
    c.upsert_record(space, brain, "agent_job_state", JOB, {"cursor": cursor})
    return {"ok": True, "partial": False}
```

Pair this with a cron trigger and the job drains itself across runs, each one budgeted.

## Metrics are always recorded

Each run's terminal cell record carries `fuel_used` and `duration_ms`; each effect record carries `meta.durMs`, and each `llm.chat` span its token usage. `anyrt trace show --stats` renders per-turn tokens, cache and cost; `anyrt trace stats` gives p50/p95 distributions and a fuel-ceiling suggestion over every stored run. Capacity decisions are made from these numbers.

## Other constraints

- **Single-threaded, synchronous** Python. No `async`, no threads. Concurrency is the host's job — `batch` fan-out on [Effects](effects.html).
- **Guest memory lasts for one run.** Each run gets a fresh kernel; assignments persist across the model's cells within that run. Records, program source, and checkpoints written to the database survive it.
- **The value store is uncapped** — guest memory is its backstop.
- **`oauth.connect` blocks up to 120 s** by default on a run thread; never call it from a cron program.

> **Note.** In-process enforcement is complete for honest code: nothing ambient is reachable, so accidental effects and accidental nondeterminism are structurally impossible. The wasm cage is the substrate; the same syscall surface is what any future host implements.
