---
title: Programs
description: Sandboxed Python that runs next to your data — every side effect recorded, every run replayable bit-exact.
order: 0
---
# Programs

A program is a Python module stored as an object in a space and run by **`anyrt`**, the companion runtime — a separate process beside the server, or a Rust library embedded in a host app (the desktop app embeds it); building the Go server does not build it. It executes inside a wasm cage on your own device, reaches the world only through a small set of recorded *effects*, and leaves behind a trace that replays the run exactly. Think of hosted-backend "functions" — but local-first, encrypted with everything else, and auditable to the byte.

## The shape of a program

```python
"""Deliver a scheduled reminder into a chat (the once-trigger payload).

Runs as a trigger program: args carry {"space", "chatId", "text"}
written by whoever created the trigger. Posts the reminder as an agent
chat message and returns the send receipt.
"""

__any_tool__ = False  # trigger-run only; not an agent-callable tool


def main(args):
    c = use("any@v1")  # noqa: F821 - guest global
    text = (args or {}).get("text") or "(reminder with no text)"
    return c.chat_send(args["space"], args["chatId"],
                       {"text": f"⏰ Reminder: {text}",
                        "agent": {"name": "bao", "done": True}})
```

Three things stand out. The docstring *is* the documentation — there is no separate description file. `use("any@v1")` loads another program from a space, version pinned, instead of a Python `import` — here from the repo this shipped program lives in; a program in your working space names the repo's alias, `use("agent:any@v1")`. And the module never imports the runtime: `use`, `effect`, `span` and `http` are globals the kernel provides.

Run it one-shot from a local checkout:

```sh
anyrt run remind@v1 --args '{"space": "bao", "chatId": "<chatId>", "text": "stand up"}'
```

The command prints one JSON envelope — `{status, value, error, traceRef, durationMs, fuelUsed}` — and writes `traces/run_<id>.jsonl`. Under `anyrt serve` the same trace lands in the `any` server's local store instead. Each invocation starts a fresh Python kernel: variables survive between cells within a run, and anything a later run needs goes to the database.

### A first run with no account

A program that only computes needs no server, account or model provider. Save this as `programs/hello@v1.py`:

```python
"""Return a greeting for the supplied name."""

__any_tool__ = False


def main(args):
    return {"message": "Hello, " + args.get("name", "world")}
```

```sh
anyrt run hello@v1 --programs ./programs --args '{"name":"Ada"}'
```

The envelope answers `status: "ok"`, `value: {"message": "Hello, Ada"}` and a `traceRef`; `anyrt trace show run_<id>` reads the trace it wrote. Building `anyrt` is covered in the [runtime quickstart](../quickstart/anyrt.html#prerequisites).

## How it runs

```
   program source (an object in a space)
            │  use("name@vN")  → module.resolve (recorded)
            ▼
   ┌──────────────────────────────┐
   │  CPython guest, wasm cage    │   fuel budget, wall deadline —
   │  print() / http.* / use()    │   no sockets, no fs
   └──────────────┬───────────────┘
                  │  one host call: effect(name, payload)
                  ▼
   ┌──────────────────────────────┐
   │  broker                      │   normalize → key → capability
   │  (the effect boundary)       │   → replay/mock → execute → record
   └──────────────┬───────────────┘
                  ▼
   the trace (local store or .jsonl)  +  the any server (127.0.0.1:7001)
```

The guest has no network, no filesystem and no clock of its own. Everything nondeterministic — an HTTP call, the current time, loading a module — is an effect, and every effect is a record in the trace; randomness derives from one seed recorded in the trace header. If it isn't in the trace, it didn't happen.

> **Why it matters.** Hosted function runtimes give you logs. A program in any gives you the complete, ordered list of everything it touched, with inputs and outputs, and a runtime that can re-execute the same code against those records with no server and no keys. Divergence between the recording and a re-run is a loud error, not a silent wrong answer.

## Where programs live

Programs are objects of the hidden `program` type, which the runtime declares in each space it writes programs to. A published set of programs is a **repo**: a folder deployed to a space with `anyrt deploy`, which other spaces join read-only and load from under an alias (`use("agent:llm@v1")`). Your working space can hold its own programs too — including ones written by the agent at runtime — and an unqualified `use("name@vN")` resolves there; a dependency inside a loaded program resolves in that program's defining space.

Programs load from spaces, not from disk: deploy is the only publish step, and a running agent picks up a redeploy on its next `use()`.

## Two ways to invoke

| Surface | What it is |
|---|---|
| `anyrt run <name@vN>` | one program, `main(args)`, from a local `--programs` folder |
| `anyrt run <name@vN> --from-space <space>` | the same, from the deployed copy |
| a trigger record | the deployed program on a schedule or an event, run by its owning device — see [Scheduling](../scheduling/index.html) |
| `anyrt serve` / the embedded runtime | deployed programs, used by the agent loop and the scheduler |

The agent loop itself (`toolcaller@v1`) is just another program, and agent tools are programs that declare `__any_tool__ = True`. See [Agents](../agents/index.html) for that side.

<div class="cards">
<a href="effects.html"><strong>Effects</strong><span>The syscall catalog — http.*, config, time, oauth — and the read/mutate classes</span></a>
<a href="writing-a-program.html"><strong>Writing a program</strong><span>File layout, docstrings, tools, spans, result shapes, the import allowlist</span></a>
<a href="modules-and-overlays.html"><strong>Modules and overlays</strong><span>use(), resolution order, repos, anybao.toml and deploy</span></a>
<a href="traces-and-replay.html"><strong>Traces and replay</strong><span>Where traces live, the record shapes, strict and loose replay, reading a run</span></a>
<a href="credentials.html"><strong>Credentials</strong><span>Secret refs, host-side injection, OAuth tokens the guest never sees</span></a>
<a href="testing.html"><strong>Testing</strong><span>Kernel-fidelity tests with a faked effect boundary</span></a>
<a href="limits.html"><strong>Limits</strong><span>Fuel, wall time, hard breaks, and what happens when a run hits them</span></a>
</div>
