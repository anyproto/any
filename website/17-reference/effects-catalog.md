---
title: Effects catalog
description: The complete host syscall surface a program can reach from inside the sandbox — every effect, its kind, the capability it names, and what it returns.
order: 70
---
# Effects catalog

This table is the entire host effect catalog — the kernel API between a sandboxed program and the outside world. Everything above it (the any client, `llm`, `memory`, `recall`, `history`, the toolcaller loop) is guest code in `programs/`, built out of these calls.

## Syscalls

| Syscall | Kind | Cap | What |
|---|---|---|---|
| `http.get/head/post/put/patch/delete(url, params?, headers?, json?, body?, timeout?, credential?, redirects?, response?)` | route-derived | route-derived | the one outbound door → `{status, headers, url, body}`; a binary body comes back as a blob ref. `timeout` defaults to 180 s |
| `config.get(key)` | read | `config.get` | agent config from the space's `agent_config` store → `{value}`; secret namespaces refused |
| `config.set(key, value)` | mutate | `config.set` | upsert one `agent_config` row; secret namespaces refused |
| `runtime.get(key)` | read | `runtime.get` | runtime wiring → `{value}`: `any.base_url`, `bao.space`, `overlays.aliases`, `shell` |
| `mailbox.drain()` | read | `mailbox.drain` | loop control as a recorded effect → `{items}` |
| `time.now` | read | `time.now` | `{epoch, offset_s, tz}` — the wall clock plus the host's UTC offset |
| `uuid4` / `sleep(seconds)` / `env.get(name)` | read | own name | determinism pins → `{hex}` / `{slept}` / `{present, value}`; `sleep` caps at 300 s; `env.get` exposes no process environment |
| `fuel.state` | read | `fuel.state` | `{remaining, budget}`, refreshed every epoch tick — a checkpoint signal for long jobs |
| `module.resolve(spec, frm?)` | read | `module.resolve` | what `use()` calls; the output carries the source bytes |
| `batch(name, payloads)` | per-item | per-item | fan-out → `{results}` in input order; a failed item is `{error}` in its slot |
| `oauth.connect(provider, scopes?, timeout?)` | mutate | `oauth.connect` | consent via loopback + PKCE; blocks ≤ 120 s, listener runs a 5-min window → `{ok, provider, grantedScopes, account}` — never token material |
| `oauth.status(provider)` | read | `oauth.status` | `{connected, pending, scopes, account, expiresAt}` — state only, no network |
| `oauth.disconnect(provider)` | mutate | `oauth.disconnect` | provider-side revoke (best-effort) + stored-grant delete → `{ok, provider, revoked}` |
| `oauth.refresh(provider)` | mutate | `oauth.refresh` | **host-emitted only**: the refresh-token exchange recorded before the http record it serves; a direct guest call fails typed `host_only` |
| `trace.effects_of(cell?, span?, run?, all?)` / `trace.effect_get(seq, run?)` | read | own name | trace views over this run, or a past run with `run` |
| `trace.runs(program?, filter?, sort?, limit?)` / `trace.stats(run)` | read | own name | the run finder over run summaries, and one run's cost/shape summary |
| `trace.query(pipeline, coll)` | read | `trace.query` | read-only aggregation over the trace collections `records` / `runs` / `blobs`; `$out` / `$merge` refused |
| `blob.read(hash, offset?, length?)` / `blob.put(data, mime)` | read | own name | bytes as handles: read a slice of a stored blob (base64), or store bytes → a ref |
| `bao.status(line)` | mutate | `bao.status` | set the agent's presence status line (serve only) |
| `sh.run` · `fs.read` · `fs.list` · `fs.write` · `fs.edit` | `sh.run`, `fs.write`, `fs.edit` mutate; `fs.read`, `fs.list` read | own name | processes and files on the serve's device — present only in a runtime built with the `shell` feature |
| `span.begin/end` | — | — | guest-declared grouping (broker machinery, not registry effects) |

**Kind** says what the effect does to the world: strict replay serves every recorded effect from the trace, and `mutate` effects are the ones a replay must never re-execute. **Cap** is the capability name the broker checks against a program's grant set before anything runs — the route-derived name for `http.*`, the effect's own name for everything else. The shipped `run` and `serve` attach no grant set, so every capability is permitted; a denial, when a grant set is present, is recorded as `capability_denied`.

In Python, programs call the facades rather than `effect()` directly: `http.get(...)` returns a `Response` (`.status`, `.headers`, `.url`, `.text`, `.json()`, `.blob`), `now()` wraps `time.now`, and `effects.of / get / runs / stats / query` wrap the `trace.*` views. Randomness needs no effect: `random`, `secrets` and `uuid` draw from a stream seeded once per run from the seed in the trace header.

## Route classification

`http.*` is one door, but the host classifies every request by its method and route (the any server is recognized by its base URL), and that classification decides both the kind and the capability:

| Route | Kind | Cap |
|---|---|---|
| any-API `GET` / `HEAD` | read | `data.read` |
| any-API `POST …/query`, `…/objects/query`, `…/search`, `…/aggregate` | read | `data.read` |
| every other any-API call | mutate | `data.write` |
| LLM completion endpoints (paths ending `/v1/messages`, `/chat/completions`, `/v1/complete`) | read | `llm.chat` |
| any other `GET` / `HEAD` | read | `net.http` |
| any other method | mutate | `net.http` |

A program that only reads can therefore be granted `data.read` and nothing else, and an LLM call is a *read* — replay serves the recorded completion instead of spending tokens again.

```python
# a read against the local server — classified data.read
base = effect("runtime.get", {"key": "any.base_url"})["value"]
r = http.post(f"{base}/v1/spaces/{space}/objects/query",
              json={"filter": {"any.types": "page"}, "limit": 10})
pages = r.json()["records"]
```

## Credential injection

`credential: {ref, header, prefix?, about?}` — the host resolves `ref` and sets the header **after** the payload is recorded; values never reach guest memory or the trace. `about` (`{label, hosts, help, note}`) describes the credential to the human who enters it and is recorded like the rest of the payload.

| Ref shape | Resolution |
|---|---|
| `connector.key.*`, `llm.key.*` | static: the stored secret as-is, read from the space's secret store at injection time |
| `connector.oauth.<provider>` | managed: the host-held token lifecycle — cached access token, refresh-at-injection with a 120 s margin |

A ref with nothing stored fails the call typed `SecretMissing` and the runtime asks for the key in the chat; a static key the destination rejects is marked for re-entry the same way.

```python
http.get("https://api.example.com/me",
         credential={"ref": "connector.key.example", "header": "Authorization", "prefix": "Bearer "})
```

> **Why it matters.** A program never holds a secret and a trace never contains one, so a run can be shared, replayed, or handed to a model for debugging without leaking credentials. See [Credentials](../programs/credentials.html) and [Effects](../programs/effects.html).
