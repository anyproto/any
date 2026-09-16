---
title: Programs and effects
description: anybao in one page — Python programs stored in your space, executed by anyrt inside a WebAssembly sandbox, touching the world only through a recorded effect boundary, replayable bit-exact, scheduled by trigger records.
order: 70
---
# Programs and effects

anybao is the agent, **anyrt** is its runtime. Programs are Python modules stored in a space; anyrt runs them in a componentized CPython guest inside WebAssembly. Operations that interact with the host use *effects*: a defined set of host functions whose results are recorded for inspection and replay.

## Programs live in spaces

A program is a `name@vN` module deployed to a space with `anyrt deploy`. `anyrt serve` loads code only from spaces: the shipped agent comes from a read-only *overlay* space reached through its alias (`agent:name@vN`), your working space holds your own programs and skills (a skill there overrides a shipped one by name), and a running serve picks up a redeploy on its next conversation without a restart.

```python
# programs/hello@v1.py  — a guest program
def main(args):
    c = use("agent:any@v1")                 # the any HTTP client, itself a guest module
    return c.chat_send(args["space"], args["chatId"],
                       {"text": f"hello from a program, {args.get('name','world')}",
                        "agent": {"name": "bao", "done": True}})
```

```bash
anyrt deploy --source ./repo --target bao                                          # publish
anyrt run hello@v1 --from-space bao --args '{"space":"bao","chatId":"…","name":"you"}'   # run the deployed copy
```

`use("name@vN")` is the import: unqualified it resolves in the current space, `alias:name@vN` in the overlay behind the alias. Resolution is itself recorded, so the trace of a run names the exact versions that ran ([Modules and overlays](../programs/modules-and-overlays.html)).

## The effect boundary

An effect is an operation the runtime performs for a program, such as reading data, requesting a model response, or checking the time. It makes external inputs and side effects explicit.

The cell namespace is deny-by-default: curated pure builtins, an allowlist of pure stdlib (`json`, `re`, `math`, `datetime` arithmetic, …), and **shims** for everything that carries ambient authority. `now()`, `uuid4()`, `env(...)`, `http.get(...)`, `datetime.now()`, `time.sleep()` are effects (`rand()` draws from a per-run seeded stream); `open`, `socket`, `eval`, raw `__import__` do not exist. Every effect call flows through one pipeline on the host:

```
normalize → key → capability check → replay/mock consult → execute → record → return
```

- Every effect record is classed as a read or a mutation by the host — per syscall, and for HTTP by method and URL — never guessed from a name.
- Inputs and outputs are JSON. Credentials are named by reference in the payload (`llm.key.anthropic`) and injected host-side after the payload is recorded, so a key never enters a trace or guest memory.
- Denials, errors, and mocks are recorded too. If it is not in the trace, it did not happen.

`print()` is the model-facing output channel — traced as a structured value, the primary input to the cell digest the model sees ([Effects](../programs/effects.html), [Effects catalog](../reference/effects-catalog.html)).

## Replay

Because every nondeterministic input is an effect record, a trace replays bit-exact: the broker answers each effect from the recorded value instead of executing it, and the program takes the same path. `anyrt trace show <run_id>` renders turns, cells, and effects ([Traces and replay](../programs/traces-and-replay.html)).

## Triggers are data

A schedule is a record in the `agent_triggers` dataset of the working space, not a config file:

```json
{ "name": "morning digest", "kind": "cron", "spec": {"cron": "0 8 * * *"},
  "program": "agent:digest@v1", "args": {"space": "bao"},
  "enabled": true }
```

`kind` is `cron` (`{"cron": expr}` in UTC, or `{"every_s": n}`), `once` (`{"at": epoch}` — fires once, then self-disables and stays as its own audit trail), or `event` (`{"dataset": "chat_messages", "objectId": chatId, "spaceId"?}` — new messages in one chat). Every running device converges its scheduler on the dataset each tick: `owner` pins a trigger to a device's peer id; an unowned trigger goes to the elected active device. Each run publishes a synced summary to `agent_runs` (status, duration, cost, tokens, trace ref, `triggerId`), and the record keeps only scheduler state (`lastRunAt` / `lastStatus` / `consecutiveFailures`). Three consecutive failures auto-disable it ([Scheduling](../scheduling/index.html)).

Because the trigger is a synced record, the agent creates reminders by writing one, the UI edits one to repin it, and "did it run?" is a query.

## The agent on top

`anyrt serve` watches a chat, runs conversations through an LLM adapter that is itself guest code, keeps memory and history as datasets in the space, and exposes tools to the model that are ordinary programs. Sub-agents, connectors (Gmail, Linear, GitHub), and progress reporting are all programs running inside the same boundary ([Agents](../agents/index.html)).

The runtime executes locally, but an effect can call an external service. The program’s capabilities and provider configuration determine which calls are allowed and where content goes. Local execution does not make a remote model call local.

## Where to go next

| Section | What it covers |
|---------|----------------|
| [Programs](../programs/index.html) | Writing a program, module resolution, traces, credentials, testing, limits. |
| [Scheduling](../scheduling/index.html) | Cron, once, event triggers, device pins, runs and monitoring. |
| [Agents](../agents/index.html) | Conversations, tools, memory, sub-agents, connectors, embedding anyrt in an app. |
| [anyrt quickstart](../quickstart/anyrt.html) | `anybao.toml`, `anyrt serve`, your first trigger. |
