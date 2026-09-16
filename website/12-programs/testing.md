---
title: Testing
description: Run program sources under the real guest kernel with only the effect boundary faked, plus one-shot runs and golden replay.
order: 60
---
# Testing

Test a program by controlling the responses to its effects. The anybao test harness loads the guest kernel and replaces the host-call boundary, letting a test provide database or HTTP results without calling live services. It also exercises the guest builtins, import allowlist, spans, and module loading.

These examples are for a development checkout of anybao with its test dependencies installed. Use [Writing a program](writing-a-program.html) for source conventions and [Traces and replay](traces-and-replay.html) to investigate an existing run.

## The harness

`tests/kernelenv.py` in the anybao repo exposes one entry point:

```python
from tests.kernelenv import load_kernel

def test_remind_posts_to_chat():
    sent = []

    class FakeAny:
        def chat_send(self, space, chat_id, body):
            sent.append((space, chat_id, body))
            return {"versionId": "v1", "changeId": "c1", "recordIds": ["m1"]}

    app = load_kernel(any_client=FakeAny())
    remind = app.use("remind@v1")
    remind.main({"space": "bao", "chatId": "chat1", "text": "stand up"})

    assert sent[0][2]["text"] == "⏰ Reminder: stand up"
    assert sent[0][2]["agent"] == {"name": "bao", "done": True}
```

`load_kernel(effect=None, any_client=None, llm_chat=None, programs_dir=None, module_source=None, shell=None)` returns a fresh kernel app module; `app.use("name@vN")` loads real sources from `repos/_agent/programs` with the runtime's own resolution order (`<spec>.py`, then `<spec>/program.py`).

| Parameter | What it fakes |
|---|---|
| `any_client` | `use("any@v1")` becomes a shim whose every method call crosses the boundary as `test.any` and dispatches to your object; exceptions become guest-catchable `AnyError` (with `status`/`code` when the fake sets them) |
| `llm_chat` | `use("llm@v1").chat(...)` crosses as `test.llm` and calls your function |
| `effect(name, payload)` | everything else — `http.*`, `config.get`, `time.now`, … |
| `programs_dir` | another repo's `programs/` (the connectors repo tests its own) |
| `module_source(spec)` | consulted first; return source text or `{"source", "marker"}` — bump the marker to model an edited program and exercise the module cache |
| `shell` | what `runtime.get("shell")` answers; set it to model a runtime built with the `shell` feature, so `sh` / `fs` are bound in cells |

Alias-qualified specs (`agent:any@v1`) shim identically. `span.begin/end` are absorbed. An unknown effect raises loudly, so a test cannot silently pass over a call it never expected.

> **Why it matters.** A stray `import contextlib` that the allowlist rejects fails in the test exactly as it would in the wasm guest. The only thing your test replaces is the boundary — the same seam replay uses.

## Faking HTTP

Connectors are tested by recording live replies once and replaying them from fixtures. The shape is just the effect fake:

```python
import json
from pathlib import Path

FIX = {"login": "octocat", "id": 1, "name": "The Octocat",
       "html_url": "https://github.com/octocat"}

def fake_effect(name, payload):
    if name == "http.get":
        return {"status": 200, "headers": {}, "body": json.dumps(FIX),
                "url": payload["url"]}
    if name == "time.now":
        return {"epoch": 1_700_000_000.0, "offset_s": 0}
    raise AssertionError(f"unexpected effect {name}")

app = load_kernel(effect=fake_effect, programs_dir=Path("repos/_connectors/programs"))
gh = app.use("github@v1")
assert gh.whoami()["login"] == "octocat"
```

Fixtures under `tests/fixtures/*.jsonl` are JSONL — one record per line is the parse contract; view them with `jq .` and never reformat the file.

## One-shot runs

For an end-to-end check against a real server, run the program from disk with the local resolver, or the deployed copy with the space resolver:

```sh
anyrt run remind@v1 --args '{"space":"bao","chatId":"…","text":"hi"}'
anyrt run remind@v1 --from-space bao --args '{…}'        # the deployed source
anyrt run mytool@v1 --secrets-file ./.connectors.env       # seeds for this run only
```

Both print `{status, value, error, traceRef, durationMs, fuelUsed}` and write a trace. A `--from-space` run also reads and writes that space's agent config the way serve does. A running `anyrt serve` offers the same through its loopback control API — `POST http://127.0.0.1:7010/run` with `{program, args?}` for a deployed program or `{source, args?, program?}` for inline text (the source is served from the request body; its `use()` imports still resolve through the space).

## Golden replay

A recorded trace is a test asset. In strict mode the broker consumes the log as a cursor: the next effect call must match the next record's effect and key, cell and span records are checkpoints, and any drift is a divergence error naming the expected record. Record once, assert forever — no server, no keys. The mechanics and the loose `mock` variant are on [Traces and replay](traces-and-replay.html).

## Running the suite

```sh
nix develop          # canonical env
uv sync
make kernel          # componentized CPython → bin/kernel.wasm
make runtime         # runtime/target/release/anyrt
uv run pytest        # guest-module + wire tests; runtime e2e skips without kernel + binary
make test            # kernel + cargo test + pytest
make lint            # ruff + clippy -D warnings + fmt --check
```

Program tests need no kernel build — the harness runs the guest module host-side. The runtime e2e tests need `make kernel` and `make runtime` and skip otherwise.
