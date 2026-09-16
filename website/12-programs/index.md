---
title: Programs
description: Write Python programs, store them alongside your data, run them on your device, and inspect their effects.
order: 0
---
# Programs

A program is Python code that runs on your device through **anyrt**, Any's companion runtime. You can develop it in a local folder, then publish it as an object in a space. Published source syncs alongside your application's data, so a device can hold both the data and the code that works on it.

`any` provides the database and HTTP API. `anyrt` executes programs and hosts the agent: it runs as a separate process beside the server, or as a Rust library embedded in an app. The Any desktop app embeds it. Building the Go server alone does not build anyrt.

## Choose a starting point

| You want to… | Start here |
|---|---|
| run a small Python function | the example below |
| add database or HTTP calls | [Effects](effects.html) |
| publish code for your devices | [Modules and overlays](modules-and-overlays.html) |
| expose a function to an agent | [Writing a program](writing-a-program.html) |
| run code later or on a chat event | [Scheduling](../scheduling/index.html) |
| understand a previous run | [Traces and replay](traces-and-replay.html) |

## Run your first program

**Before you start:** build anyrt using the [runtime setup](../quickstart/anyrt.html#prerequisites) and make its binary available as `anyrt`. This example only computes a return value; it needs no account, server, or model provider.

Create a `programs` directory and save this as `programs/hello@v1.py`:

```python
"""Return a greeting for the supplied name."""

__any_tool__ = False


def main(args):
    return {"message": "Hello, " + args.get("name", "world")}
```

From the directory containing `programs`, run:

```sh
anyrt run hello@v1 --programs ./programs --args '{"name":"Ada"}'
```

The JSON response has `status: "ok"` and `value: {"message": "Hello, Ada"}`. It also contains `error`, `traceRef`, `durationMs`, and `fuelUsed`. The run writes `traces/run_<id>.jsonl`; use its ID with `anyrt trace show run_<id>` to inspect it.

Each invocation starts a fresh Python kernel. Variables survive between cells within a run; data needed by a later run must be written to the database.

## Work with your data

The guest loads other programs with `use("name@vN")`. Database clients and connector tools are programs too. This is the shipped reminder program's shape:

```python
"""Deliver a scheduled reminder into a chat."""

__any_tool__ = False


def main(args):
    c = use("any@v1")
    text = (args or {}).get("text") or "(reminder with no text)"
    return c.chat_send(args["space"], args["chatId"],
                       {"text": f"⏰ Reminder: {text}",
                        "agent": {"name": "bao", "done": True}})
```

Here `any@v1` is in the same published repo as the reminder. Code in your working space uses the repo alias, `use("agent:any@v1")`. The example needs a running server, a configured program repo, and an existing chat. [Schedule a reminder](../scheduling/once.html#schedule-a-reminder) supplies the trigger and arguments.

`use`, `effect`, `span`, and `http` are globals supplied by the runtime. The module's docstring describes its purpose; a tool's public function docstrings tell the agent how to call it.

## How execution works

```text
program source (local folder or space)
    ↓
Python guest inside a WebAssembly sandbox
    ↓ effect(name, payload)
host broker → local database / HTTP provider / other supported effect
    ↓
recorded inputs, outputs, and run outcome
```

The guest has no direct network, filesystem, or clock access. Calls to the outside world pass through the host as **effects**. Loading a module and reading the current time are effects too; randomness derives from a seed recorded in the trace header.

The broker normalizes each request, applies capability checks, executes it, and records the result. During strict replay it returns the recorded results instead. The same source can reproduce a recorded run without making its live requests again; divergence is reported as an error. See [Effects](effects.html) and [Traces and replay](traces-and-replay.html) for the exact contracts.

Local data access can work without an internet connection. An HTTP connector or hosted model call still needs its destination. The runtime also enforces a fuel budget and a wall-clock deadline; [Limits](limits.html) explains how to checkpoint longer jobs.

## Where programs live

Published programs are objects of the hidden `program` type, declared by the runtime. A **repo** is a folder published to a space with `anyrt deploy`. Consumers join that space read-only and load its programs through an alias such as `use("agent:llm@v1")`.

Your working space can also hold your own programs, including programs written by the agent. An unqualified `use("name@vN")` resolves in the working space; dependencies within a loaded program resolve in that program's defining space.

| Invocation | Source and lifetime |
|---|---|
| `anyrt run <name@vN>` | local `--programs` directory; one `main(args)` invocation |
| `anyrt run <name@vN> --from-space <space>` | deployed source; one invocation |
| a trigger record | deployed source, run by its owning device on a schedule or event |
| `anyrt serve` / embedded runtime | deployed programs used by the agent and scheduler |

Deploy publishes local source. A running agent resolves an updated module on its next `use()`; it does not need a runtime rebuild for a program edit. [Modules and overlays](modules-and-overlays.html) covers publishing and resolution rules.

The supplied agent loop, `toolcaller@v1`, is itself a program. Agent tools declare `__any_tool__ = True`. Continue to [Agents](../agents/index.html) to build conversations and persistent memory on this execution model.

<div class="cards">
<a href="writing-a-program.html"><strong>Writing a program</strong><span>File layout, tool functions, docstrings, and allowed imports.</span></a>
<a href="effects.html"><strong>Effects</strong><span>Database, HTTP, time, and other calls through the host.</span></a>
<a href="modules-and-overlays.html"><strong>Publish and share programs</strong><span>Program resolution, repos, configuration, and deploy.</span></a>
<a href="traces-and-replay.html"><strong>Inspect a run</strong><span>Find traces, read effects, and replay recorded results.</span></a>
<a href="credentials.html"><strong>Credentials</strong><span>Credential references, host-side injection, and OAuth.</span></a>
<a href="testing.html"><strong>Test a program</strong><span>Run the guest with a controlled effect boundary.</span></a>
<a href="limits.html"><strong>Execution limits</strong><span>Fuel, deadlines, cancellation, and checkpoints.</span></a>
</div>
