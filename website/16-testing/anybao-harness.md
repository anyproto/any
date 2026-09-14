---
title: anybao harness
description: The layered test doctrine of the agent runtime — replay determinism as the testable property, the kernel-fidelity harness, recorded fixtures, and the scratch rig.
order: 20
---
# anybao harness

anybao's verification follows one principle: **everything nondeterministic is an effect**. A program can only reach the world through the effect boundary, so a recorded trace replays the whole system deterministically — and replay determinism is *the* property every layer is built to assert cheaply. The runtime is Rust (`anyrt`); the programs it runs are Python executed inside a wasm kernel. Tests split along that seam.

## The layers

| Layer | What | Runs with | Red means |
|---|---|---|---|
| **L0** runtime unit | Rust policy and logic with fakes — effect boundary, capabilities, broker, trace/replay, deploy, resolver, triggers, routes | `cargo test --manifest-path runtime/Cargo.toml` | runtime logic or determinism broke |
| **L2** guest-module policy | each program's source executed under the real kernel with a fake `effect` / `use` / `now` | `uv run pytest` | a guest module or its loop policy broke |
| **L3** binary end-to-end | `anyrt run` as a subprocess against a stdlib fake serving both backends (any server + LLM provider) | `uv run pytest` when the kernel and the binary exist; skips otherwise | binary, boundary or engine broke |
| **L4** wire integration | a real `any` server; markers `-m integration` | `make test-integration` (opt-in) | the wire contract broke |
| **L5** live evals | golden recall eval against a live index; ROI metrics review | opt-in | retrieval quality regressed |
| **L6** gate walk | the cutover checklist, side by side with the previous agent | manual, once per cutover | human sign-off |

Conventions: offline by default (`addopts = -m 'not integration'`); fixtures are JSONL, one record per line; integration tests poll-then-skip on asynchronous indexer timing but assert firmly on direct reads.

## The kernel-fidelity harness

The L2 harness imports the **real guest kernel** host-side with the wasm host interface stubbed, so program tests run under the kernel's actual semantics — curated builtins, the import allowlist, span machinery, and `use()` module loading — instead of plain host CPython. A stray `import contextlib` fails in the test exactly as it fails in the wasm guest. The only fake is the effect boundary itself.

```python
from kernelenv import load_kernel

app = load_kernel(effect=fake_effect, any_client=fake_any, llm_chat=fake_llm)
mod = app.use("history@v1")          # real source from repos/_agent/programs
```

- `load_kernel(effect, any_client=…, llm_chat=…)` returns the app module; `app.use("name@vN")` loads real sources from the programs directory, in the same flat-file / folder order the runtime uses.
- `any@v1` and `llm@v1` resolve to dispatch shims when a fake is given: their calls cross the boundary as `test.any` / `test.llm`, so existing fake-client objects keep working unchanged.
- Effects the harness does not own (`http.*`, `config.get`, `time.now`, …) go to `effect`; span begin/end are absorbed; **unknown effects raise loudly**.

> **Why it matters.** The sandbox is part of the contract a program is written against. Testing under the real kernel means the allowlist, the builtins and the module loader are exercised on every `pytest` run — a program that passes here loads in production.

## Coverage by subsystem

| Subsystem | L0 | L2 | L3 | L4 |
|---|---|---|---|---|
| trace / replay / determinism | ✓ | — | ✓ trace asserted | — |
| effect boundary + caps | ✓ | — | ✓ effect-only trace | — |
| deploy / resolver / space modules | ✓ | — | ✓ programs loaded | — |
| triggers (schedule / store / standing) | ✓ | — | — | ✓ trigger datasets |
| any client + agent log | ✓ | ✓ | ✓ turn/chunk writes | ✓ |
| LLM adapters | — | ✓ | ✓ provider wire | — |
| tool-calling loop | — | ✓ | ✓ full conversation | — |
| history / rollup / recall / memory | — | ✓ | — | ✓ |
| skills content | — | ✓ | — | — |

## LLM fixtures: one real call

Adapter translation (recorded provider response → neutral parts, neutral → request) is tested purely offline. The wire mapping is locked by one fixture per provider, recorded from a single live call and saved as the raw response body:

```bash
curl -s https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"claude-sonnet-5","max_tokens":64,
       "messages":[{"role":"user","content":"say hi"}]}' \
  > tests/fixtures/llm_anthropic.json
```

After that CI needs no API key. The conversation loop itself replays from traces — `llm.chat` is an effect — so a real conversation recorded once is a golden test for the whole loop with zero further calls. Re-seed a fixture when a provider changes its wire format or when adding a provider.

## Standing rules

- Every new mechanism lands its tests in the same commit.
- LLM discipline is asserted in code — allow-lists, caps, vocabularies — never by trusting prompts.
- A new effect is registered in the runtime broker, documented in the effects catalog, and gets a read/mutate + capability classification test.
- Run the integration suite against a live server before any release-ish moment: `ANYBAO_TEST_SERVER=… uv run pytest -m integration`.

## Running a program from the space

There is no `/run` endpoint on the any server — it stores program source and never executes it. Three surfaces run a program through the effect boundary, all recording a full trace:

| Surface | Resolves `use()` from | Good for |
|---|---|---|
| `anyrt run NAME@vN --programs DIR` | a local directory | the dev loop on undeployed source |
| `anyrt run NAME@vN --from-space SPACE` | the deployed space object, with serve's bootstrap | testing the deployed form, scripted |
| `anyrt serve` | the deployed space object | the full loop: chat, triggers, mailbox |

```bash
anyrt run 'webSearch@v1' --from-space bao --args '{"query": "local-first sync"}'
anyrt trace ls                                                  # anyrt run writes jsonl traces to ./traces
anyrt trace ls --addr http://127.0.0.1:7001 --program toolcaller   # serve's runs, in the server's local store
anyrt trace show --addr http://127.0.0.1:7001 run_<id> --stats
```

`anyrt run` records into a traces directory (`--traces-dir`, default `traces`). `anyrt serve` records into the any server's local store by default (`[traces] backend = "any"`), so its runs are read with `--addr`; `backend = "file"` sends them to `paths.traces` instead.

`--from-space` is production: a tool-calling run posts its reply into the real chat. Point it at a scratch space when that matters — see [Traces and replay](../programs/traces-and-replay.html).

## The scratch rig

For exercising agent changes with a real chat and UI without touching the real install, keep a persistent scratch stack on non-default ports: its own `any` server (`--data-dir <scratch-data-dir> --addr 127.0.0.1:7009`, with an explicit `--config` so it joins the right network), a separate serve control port, an overlay repo space for programs and skills, and a working space the agent recreates on start — all named in a gitignored `anybao.test.toml`. Its traces stay apart by construction: they land in the scratch server's local store.

```bash
anyrt deploy --source . --target agent --config-file anybao.test.toml   # hash-gated publish
anyrt serve --config-file anybao.test.toml
anyrt trace ls --addr http://127.0.0.1:7009 --program toolcaller
```

Deploy is the only publish step — a running serve picks changes up on its next conversation. Reset agent state by deleting the working space (`any --addr 127.0.0.1:7009 space delete --yes <id>`); the account itself persists.
