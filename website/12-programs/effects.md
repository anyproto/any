---
title: Effects
description: The complete host syscall surface a program can reach, how each call is classified, and why the boundary is where the truth lives.
order: 10
---
# Effects

An effect is the only way guest code touches the world. The catalog below is the entire host surface — small and stable by design. Everything else a program uses (the any client, LLM adapters, memory, the conversation loop) is itself guest Python loaded with `use()`.

## The catalog

| Syscall | Class | What it does |
|---|---|---|
| `http.get/head/post/put/patch/delete(url, params?, headers?, json?, body?, timeout?, credential?, redirects?, response?)` | route-derived | the one outbound door → `Response` with `.status`, `.headers`, `.url`, `.text` / `.json()` for a text body, `.blob` for bytes |
| `config.get(key)` | read | agent config; secret namespaces are refused |
| `config.set(key, value)` | mutate | writes one agent config row |
| `runtime.get(key)` | read | runtime wiring — `any.base_url`, `overlays.aliases`, `bao.space`, `shell` |
| `mailbox.drain()` | read | loop control (injected messages, breaks) as a recorded effect |
| `time.now` / `uuid4` / `sleep` / `env.get` | read | determinism pins — `time.now` returns `{epoch, offset_s, tz}`; `sleep` caps at 300 s |
| `fuel.state` | read | `{remaining, budget}` — a checkpoint signal for long loops |
| `module.resolve(spec, frm?)` | read | what `use()` calls; output carries the source |
| `batch(name, payloads)` | per item | fan-out; records land in input order |
| `blob.read(hash, offset, length)` / `blob.put(data, mime)` | read | bytes behind a `Blob` handle, held by the host |
| `oauth.connect(provider, scopes?, timeout?)` | mutate | consent via loopback + PKCE; returns `{ok, provider, grantedScopes, account}`, never tokens |
| `oauth.status(provider)` | read | `{connected, pending, scopes, account, expiresAt}` — no network |
| `oauth.disconnect(provider)` | mutate | provider-side revoke (best effort) + stored grant delete |
| `oauth.refresh(provider)` | mutate | host-emitted only; a direct guest call fails typed `host_only` |
| `trace.effects_of(cell?, span?, run?)` / `trace.effect_get(seq, run?)` | read | a run's trace as a view — this run, or a past one by run id |
| `trace.runs(program?, filter?, sort?, limit?)` / `trace.stats(run)` / `trace.query(pipeline, coll?)` | read | the run finder, one run's cost summary, read-only aggregation over the trace store |
| `bao.status(line)` | mutate | the agent's live status line |
| `sh.run/spawn/poll/kill`, `fs.read/list/write/edit` | mutate (`fs.read`/`fs.list` read) | processes and files on the device — only in a runtime built with the `shell` feature |
| `span.begin/end` | — | guest-declared grouping (broker machinery, not registry effects) |

`http.*` derives its capability from the route (below); every other syscall's capability is its own name.

Ergonomic shims sit on top: `now()`, `rand()`, `env(...)`, `uuid4()` as globals, and proxied `datetime` / `time` / `os.environ` modules whose "what time is it" calls route through the same effects. The present is always a record — `datetime.now()` and `time.time()` are real `time.now` records. Randomness is not one record per draw: `random`, `secrets` and `uuid` run on a stream seeded from the trace header's `seed`, so a replay re-derives every value.

## Pythonic surface

```python
r = http.get("https://api.github.com/repos/anyproto/any",
             headers={"Accept": "application/vnd.github+json"}, timeout=20)
if r.status == 200:
    print(r.json()["stargazers_count"])   # print() is the output channel
```

`print()` is the model-facing output channel: each call is captured as a structured value in the cell's result and is the primary input to its digest. `http.*` mirrors the requests idiom (`params=`, `headers=`, `json=`); a call returns the response as data, including 3xx when redirects are manual. The host decides text versus bytes from the response itself: a binary body comes back as a `Blob` handle on `.blob` (`.text` raises), and a `Blob` passed as a request `body` or inside `json` is expanded by the host at send time while the trace keeps only the reference.

## Read or mutate is a boundary fact

Every record carries `meta.class` of `read` or `mutate`. For HTTP the class and the required capability derive from `(method, url)` against a route table scoped to the any server's base URL:

| Route | Class | Capability |
|---|---|---|
| `GET` / `HEAD` | read | `data.read` on the any server, `net.http` elsewhere |
| any server `POST …/query`, `…/objects/query`, `…/search`, `…/aggregate` | read | `data.read` |
| every other any server call | mutate | `data.write` |
| LLM completion endpoints (`/v1/messages`, `/chat/completions`, `/v1/complete`) | read | `llm.chat` |
| anything else | mutate | `net.http` |

Guest code cannot declare its own class. A `@span(kind="getter")` facade whose span contains a mutate is a legible inconsistency a viewer can flag; the log never lets the label override the boundary fact. Reads are safe to re-execute; mutates are not, and replay tooling relies on that distinction.

## The pipeline

Every effect call, no exceptions, flows through one path:

```
normalize → key → capability check → replay/mock consult → execute → record → return
```

- **Normalize** produces the canonical input — the payload as JSON with sorted keys; sha256 over the effect name and that JSON is the record's `key`.
- **Capability check** consults the active grant set before anything runs. The runtime ships a permissive default; a denial is recorded as `error.type: "capability_denied"`.
- **Record always** — success, error, denial, mock. A failure is data in the record *and* a typed `EffectError` (`"<type>: <message>"`) raised into the guest, so a cell may catch it and nothing is silently swallowed.

> **Why it matters.** The guest-side facade (`http.get`, `print()`) holds zero authority — bypassing it gains nothing. Classification, secrets and recording all happen on the host side of one serialized crossing. The cage and its syscalls never change; everything the agent *is* lives above the boundary as deployed code.

## Fan-out

Cells are single-threaded. `http.get_many(items)` carries a list of requests across the boundary once as a `batch` effect; the host runs each item as its own effect call and appends one record per item **in input order**, so strict replay stays deterministic. A per-item failure is an `EffectError` value in that slot, never a whole-batch exception. Looping the same effect sequentially is correct-but-slow, never wrong.

## Introspecting your own run

`effects.of(cell)` returns a scope's immediate children — bare effect records plus child span-end rows — and `effects.get(seq)` resolves one full record; both take `run=` to read a past run. `effects.runs(...)` finds runs by program, status or cost, and `effects.query(pipeline)` aggregates over every recorded run. This is how the agent answers "why did you do that?" from inside a conversation; the human-side equivalent is `anyrt trace show` on [Traces and replay](traces-and-replay.html).
