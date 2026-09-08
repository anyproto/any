---
title: Limits
description: Fuel, wall-clock deadlines and memory caps — how each is enforced in the wasm cage, what a program sees when it hits one, and how to stay under.
order: 70
---
# Limits

Every guest instruction — CPython's interpreter loop included — is wasm compiled by wasmtime, so all of it can be counted and bounded. A runaway program is a clean, typed failure inside the cell, never a hung process.

## The three bounds

| Limit | Mechanism | Character |
|---|---|---|
| instruction budget | **fuel** — a per-block counter decremented at compile time; zero traps at exactly that instruction | deterministic and replayable: same cell + inputs = same cutoff |
| wall timeout | **epoch deadline** — a load+compare at function entries and loop back-edges, a ticker bumps the epoch | wall-clock driven, lands inside any guest loop; the tool for cancellation |
| memory cap | **linear-memory limiter** — `memory.grow` routed through a host callback | denial ⇒ ordinary `MemoryError` in the cell; the kernel namespace survives |

Fuel budgets; the epoch cancels. A hard break (`interrupt()`) is an epoch bump and always lands. Epoch checks run only in guest code — inside a host function (an effect) interruption is cooperative, and the effects honor cancellation (HTTP timeouts), so no un-cancellable path remains.

## Defaults

| Setting | Value |
|---|---|
| fuel per run | 50 billion instructions — tens of seconds of pure compute; a hard runaway ceiling, not a working budget for ordinary jobs |
| wall timeout (`anyrt run --timeout-s`) | 120 s |
| epoch tick | 10 ms |
| blob spill threshold | ~64 KB per recorded value |

## What exhaustion looks like

Fuel exhaustion surfaces as a typed **`FuelExhausted`** error whose message says what works: split the work into smaller chunks. The trap is deterministic, so a verbatim retry can never succeed; in a conversation the runtime forwards the message into the chat so the next turn acts on it. A timeout or hard break marks the cell `interrupted`. Every limit hit is recorded in the cell's terminal trace record:

```jsonc
{"kind": "cell", "seq": 23, "cell": "toolu_abc", "ok": false, "interrupted": true,
 "error": {"type": "FuelExhausted", "message": "…"},
 "metrics": {"fuel_used": 50000000000, "mem_pages": 512, "duration_ms": 31877,
             "value_store": {"entries": 7, "bytes": 91234}}}
```

For a trigger run, the run record's `status` is `error` or `interrupted` and the trigger's circuit breaker counts it — see [Runs and monitoring](../scheduling/runs-and-monitoring.html).

## Checkpointing long jobs

Long data jobs avoid the cliff cooperatively. The `fuel.state` syscall returns `{remaining, budget}`, refreshed by the epoch ticker (so up to one tick stale — a checkpoint signal, not an exact meter), and recorded like any effect so replay stays stable:

```python
def main(args):
    c = use("any@v1")
    state = c.query_records(args["space"], args["objectId"], "sync_state", {"limit": 1})
    cursor = (state[0] if state else {}).get("cursor", 0)
    for page in pages_from(cursor):
        process(page)
        cursor = page["next"]
        f = effect("fuel.state", {})
        if f["remaining"] < f["budget"] * 0.15:
            save_cursor(c, args, cursor)     # checkpoint, then exit cleanly
            return {"ok": True, "resumed_at": cursor, "partial": True}
    save_cursor(c, args, cursor)
    return {"ok": True, "partial": False}
```

Pair this with a cron trigger and the job drains itself across runs, each one budgeted.

## Metrics are always recorded

Each cell's terminal record carries `fuel_used`, `mem_pages` (linear-memory size after the cell), `duration_ms` and `value_store {entries, bytes}`; each effect record carries `meta.durMs`, and LLM records `meta.usage`. `anyrt trace show --stats` renders per-turn tokens, cache and cost; `anyrt trace stats traces/` gives p50/p95 distributions and tuning suggestions over a directory. Capacity decisions are made from these numbers.

## Other constraints

- **Single-threaded, synchronous** Python. No `async`, no threads. Concurrency is the host's job — `batch` fan-out on [Effects](effects.html).
- **Nothing survives a process restart.** Top-level assignments persist across cells within one conversation; the state a program needs later goes in the space.
- **The value store is uncapped** — guest memory is its backstop; a runaway store hits the memory limit as a `MemoryError`.
- **`oauth.connect` blocks up to 120 s** on a run thread; never from a cron program.

> **Note.** In-process enforcement is complete for honest code: nothing ambient is reachable, so accidental effects and accidental nondeterminism are structurally impossible. The wasm cage is the substrate; the same syscall surface is what any future host implements.
