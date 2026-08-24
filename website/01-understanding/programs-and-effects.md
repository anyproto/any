---
title: Programs and effects
description: anybao in one page — Python programs stored in your space, executed by anyrt inside a wasm cage, touching the world only through a recorded effect boundary, replayable bit-exact, scheduled by trigger records.
order: 70
---
# Programs and effects

anybao is the agent, **anyrt** is its runtime. Programs are Python modules stored in a space; anyrt runs them in a componentized CPython guest inside wasm, and the only way out of the cage is a small set of host-provided *effects* that are recorded as they happen. That one rule gives you confinement and bit-exact replay from the same mechanism.

## Programs live in spaces

A program is a `name@vN` module deployed to a space with `anyrt deploy`. The runtime loads code from spaces, never from the filesystem: the shipped agent comes from a read-only *overlay* space, your own working space can shadow any module by name, and a running `anyrt serve` picks up a redeploy on its next conversation without a restart.

```python
# programs/hello@v1.py  — a guest program
def main(args):
    c = use("any@v1")                       # the any HTTP client, itself a guest module
    return c.chat_send(args["space"], args["chatId"],
                       {"text": f"hello from a program, {args.get('name','world')}",
                        "agent": {"name": "bao", "done": True}})
```

```bash
anyrt run hello@v1 --args '{"space":"bao","chatId":"…","name":"you"}'   # offline dev
anyrt deploy --source ./repo --target bao                              # publish
```

`use("name@vN")` is the import. Resolution is itself recorded, so the trace of a run names the exact versions that ran ([Modules and overlays](../programs/modules-and-overlays.html)).

## The effect boundary

The cell namespace is deny-by-default: curated pure builtins, an allowlist of pure stdlib (`json`, `re`, `math`, `datetime` arithmetic, …), and **shims** for everything that carries ambient authority. `now()`, `rand()`, `env(...)`, `http.get(...)`, `datetime.now()`, `time.sleep()` are effects; `open`, `socket`, `eval`, raw `__import__` do not exist. Every effect call flows through one pipeline on the host:

```
normalize → key → capability check → replay/mock consult → execute → record → return
```

- Effects declare `kind="read"` or `"mutate"` — never guessed from a name.
- Inputs and outputs are JSON; sensitive paths (`headers.authorization`) are redacted *before* the record is written, so a secret structurally cannot enter a trace.
- Secrets are resolved host-side by reference (`llm.key.anthropic`) and injected after the payload is recorded; key bytes never enter guest memory.
- Denials, errors, and mocks are recorded too. If it is not in the trace, it did not happen.

`print()` is the model-facing output channel — traced as a structured value, the primary input to the run digest ([Effects](../programs/effects.html), [Effects catalog](../reference/effects-catalog.html)).

## Replay

Because every nondeterministic input is an effect record, a trace replays bit-exact: the broker answers each effect from the recorded value instead of executing it, and the program takes the same path. `anyrt trace show <run_id>` renders turns, cells, and effects; a trace diff shows only the *new* effects a code change introduced ([Traces and replay](../programs/traces-and-replay.html)).

## Triggers are data

A schedule is a record in the `agent_triggers` dataset of the working space, not a config file:

```json
{ "name": "morning digest", "kind": "cron", "spec": {"cron": "0 8 * * *"},
  "program": "agent:digest@v1", "args": {"space": "bao"},
  "enabled": true, "limits": {"timeoutS": 120} }
```

`kind` is `cron` (`{"cron": expr}` or `{"every_s": n}`), `once` (`{"at": epoch}` — fires once, then self-disables and stays as its own audit trail), or `event` (`{dataset, objectId?, filter?}`). Every running device converges its scheduler on the dataset each tick: `owner` pins a trigger to a device's peer id; an unowned trigger goes to the elected active device. Each run appends to `agent_trigger_runs` with status, duration, fuel, cost, and a trace ref, and the definition carries the rollup (`lastRunAt` / `lastStatus` / `runCount` / `consecutiveFailures`). Three consecutive failures auto-disable it ([Scheduling](../scheduling/index.html)).

Because the trigger is a synced record, the agent creates reminders by writing one, the UI edits one to repin it, and "did it run?" is a query.

## The agent on top

`anyrt serve` watches a chat, runs conversations through an LLM adapter that is itself guest code, keeps memory and history as datasets in the space, and exposes tools to the model that are ordinary programs. Sub-agents, connectors (Gmail, Linear, GitHub), and progress reporting are all programs running inside the same boundary ([Agents](../agents/index.html)).

> **Why it matters.** A hosted agent platform runs your automations on its machines, with your credentials, against a copy of your data. anyrt runs them on your device, inside a cage that cannot reach the network or the clock except through a logged door, against the encrypted data that is already there — and hands you a replayable trace for every run.

## Where to go next

| Section | What it covers |
|---------|----------------|
| [Programs](../programs/index.html) | Writing a program, module resolution, traces, credentials, testing, limits. |
| [Scheduling](../scheduling/index.html) | Cron, once, event triggers, device pins, runs and monitoring. |
| [Agents](../agents/index.html) | Conversations, tools, memory, sub-agents, connectors, embedding anyrt in an app. |
| [anyrt quickstart](../quickstart/anyrt.html) | `anybao.toml`, `anyrt serve`, your first trigger. |
