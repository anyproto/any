---
title: Effects
description: The complete host syscall surface a program can reach, how each call is classified, and why the boundary is where the truth lives.
order: 10
---
# Effects

An effect is the only way guest code touches the world. The catalog below is the entire host surface — small and stable by design. Everything else a program uses (the any client, LLM adapters, memory, the conversation loop) is itself guest Python loaded with `use()`.

## The catalog

| Syscall | Class | Capability | What it does |
|---|---|---|---|
| `http.get/head/post/put/patch/delete(url, params?, headers?, json?, body?, timeout?, credential?, redirects?)` | route-derived | route-derived | the one outbound door → `Response` with `.status`, `.headers`, `.text`, `.json()`, `.url` |
| `config.get(key)` | read | `config.get` | non-secret config; secret keys are refused |
| `mailbox.drain()` | read | `mailbox.read` | loop control (injected messages) as a recorded effect |
| `time.now` / `random.random` / `uuid4` / `sleep` / `env.get` | read | — | determinism pins — real wall clock and randomness, recorded |
| `module.resolve(spec, frm?)` | read | — | what `use()` calls; output carries the source bytes |
| `batch(name, payloads)` | per item | per item | fan-out; records land in input order |
| `fuel.state` | read | — | `{remaining, budget}` — a checkpoint signal for long loops |
| `oauth.connect(provider, scopes?, timeout?)` | mutate | `oauth.connect` | consent via loopback + PKCE; returns `{ok, provider, grantedScopes, account}`, never tokens |
| `oauth.status(provider)` | read | `oauth.status` | `{connected, pending, scopes, account, expiresAt}` — no network |
| `oauth.disconnect(provider)` | mutate | `oauth.disconnect` | provider-side revoke (best effort) + local delete |
| `oauth.refresh(provider)` | mutate | `oauth.refresh` | host-emitted only; a direct guest call fails typed `host_only` |
| `trace.effects_of(cell?, span?)` / `trace.effect_get(seq)` | read | — | the program's own trace, as a view |
| `span.begin/end` | — | — | guest-declared grouping (broker machinery, not registry effects) |

Ergonomic shims sit on top: `now()`, `rand()`, `env(...)` as globals, and proxied `datetime` / `random` / `time` / `os.environ` modules whose nondeterministic calls route through the same effects. Guest code never sees a fake clock — `datetime.now()` is a real `time.now` record.

## Pythonic surface

```python
r = http.get("https://api.github.com/repos/anyproto/any",
             headers={"Accept": "application/vnd.github+json"}, timeout=20)
if r.status == 200:
    print(r.json()["stargazers_count"])   # print() is the output channel
```

`print()` is the model-facing output channel: each call is a structured-value record in the trace and the primary input to a cell's result digest. `http.*` mirrors the requests idiom (`params=`, `headers=`, `json=`); a call returns the response as data, including 3xx when redirects are manual.

## Read or mutate is a boundary fact

Every record carries `meta.class` of `read` or `mutate`. For HTTP the class and the required capability derive from `(method, url)` against a route table scoped to the any server's base URL:

| Route | Class | Capability |
|---|---|---|
| any `GET …` | read | `data.read` |
| any `POST …/query`, `…/objects/query`, `…/search`, `…/aggregate` | read | `data.read` |
| every other any write | mutate | `data.write` |
| LLM completion endpoints (`/v1/messages`, `/chat/completions`) | read | `llm.chat` |
| anything else | per verb (`get`/`head` read, others mutate) | `net.http` |

Guest code cannot declare its own class. A `@span(kind="getter")` facade whose span contains a mutate is a legible inconsistency a viewer can flag; the log never lets the label override the boundary fact. Reads are safe to re-execute; mutates are not, and replay tooling relies on that distinction.

## The pipeline

Every effect call, no exceptions, flows through one path:

```
normalize → key → capability check → replay/mock consult → execute → record → return
```

- **Normalize** produces the canonical input (sorted keys, explicit defaults, headers lowercased); its sha256 is the record's `key`.
- **Capability check** intersects grants over the import chain — a program can never lend more authority than it holds. Denials are recorded as `error.type: "capability_denied"`.
- **Record always** — success, error, denial, mock. A failure is data in the record *and* a typed `EffectError(seq, type, message)` raised into the guest, so a cell may catch it and nothing is silently swallowed.

> **Why it matters.** The guest-side facade (`http.get`, `print()`) holds zero authority — bypassing it gains nothing. Classification, secrets and recording all happen on the host side of one serialized crossing. The cage and its syscalls never change; everything the agent *is* lives above the boundary as deployed code.

## Fan-out

Cells are single-threaded. Concurrency is the host's job: `effect("batch", {"name": "http.get", "payloads": [...]})` carries a list across the boundary once, executes on a pool, and appends one record per item **in input order** regardless of completion order — so strict replay stays deterministic. A per-item failure is an `EffectError` value in that slot, never a whole-batch exception. Looping the same effect sequentially is correct-but-slow, never wrong.

## Introspecting your own run

`trace.effects_of(cell)` returns a scope's immediate children — bare effect records plus child span-end rows — and `trace.effect_get(seq)` resolves one full record. This is how the agent answers "why did you do that?" from inside a conversation; the human-side equivalent is `anyrt trace show` on [Traces and replay](traces-and-replay.html).
