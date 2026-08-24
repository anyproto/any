---
title: Effects catalog
description: The complete host syscall surface a program can reach from inside the sandbox — every effect, its kind, the capability it needs, and what it returns.
order: 70
---
# Effects catalog

This table is the entire host effect catalog — the frozen kernel API between a sandboxed program and the outside world. Everything above it (the any client, `llm`, `memory`, `recall`, `history`, the toolcaller loop) is guest code in `programs/`, built out of these calls.

## Syscalls

| Syscall | Kind | Cap | What |
|---|---|---|---|
| `http.get/post/put/patch/delete(url, params?, headers?, json?, body?, timeout?, credential?)` | route-derived | route-derived | the one outbound door → `{status, headers, body}` |
| `config.get(key)` | read | `config.get` | non-secret config (secrets resolve only inside syscalls) |
| `mailbox.drain()` | read | `mailbox.read` | loop control as a recorded effect |
| `time.now` / `random.random` / `uuid4` / `sleep` / `env.get` | read | — | determinism pins |
| `module.resolve(spec, frm?)` | read | — | `use()` resolution |
| `batch(name, payloads)` | per-item | per-item | fan-out with input-order records |
| `oauth.connect(provider, scopes?, timeout?)` | mutate | `oauth.connect` | consent via loopback + PKCE; blocks ≤ 120 s, listener runs a 5-min window → `{ok, provider, grantedScopes, account}` — never token material |
| `oauth.status(provider)` | read | `oauth.status` | `{connected, pending, scopes, account, expiresAt}` — state only, no network |
| `oauth.disconnect(provider)` | mutate | `oauth.disconnect` | provider-side revoke (best-effort) + device-local delete → `{ok, provider, revoked}` |
| `oauth.refresh(provider)` | mutate | `oauth.refresh` | **host-emitted only**: the refresh-token exchange recorded before the http record it serves; a direct guest call fails typed `host_only` |
| `trace.effects_of(cell?, span?)` / `trace.effect_get(seq)` | read | — | agent-side trace views |
| `span.begin/end` | — | — | guest-declared grouping (broker machinery, not registry effects) |

**Kind** says what the effect does to the world: `read` effects are replayed from the trace bit-exactly, `mutate` effects are the ones a replay must never re-execute. **Cap** is the capability a program must hold for the call to be permitted; `—` means the effect is always available.

## Route classification

`http.*` is one door, but the host classifies every request by its route (scoped to the any server's base URL), and that classification decides both the kind and the capability:

| Route | Kind | Cap |
|---|---|---|
| any-API `GET …` | read | `data.read` |
| any-API `POST …/query`, `…/objects/query`, `…/search`, `…/aggregate` | read | `data.read` |
| every other any-API write | mutate | `data.write` |
| LLM completion endpoints (`/v1/messages`, `/chat/completions`) | read | `llm.chat` |
| everything else | — | `net.http` |

A program that only reads can therefore be granted `data.read` and nothing else, and an LLM call is a *read* — replay serves the recorded completion instead of spending tokens again.

```python
# a read against the local server — classified data.read
r = http.post(f"{config.get('any.base_url')}/v1/spaces/{space}/objects/query",
              json={"filter": {"any.types": "page"}, "limit": 10})
pages = r["body"]["records"]
```

## Credential injection

`credential: {ref, header, prefix?}` — the host resolves `ref` and sets the header **after** the payload is recorded; values never reach guest memory or the trace.

| Ref shape | Resolution |
|---|---|
| `connector.key.*`, `llm.key.*` | static: the stored secret as-is |
| `connector.oauth.<provider>` | managed: the host-held token lifecycle — cached access token, refresh-at-injection with a 120 s margin |

```python
http.get("https://api.example.com/me",
         credential={"ref": "connector.key.example", "header": "Authorization", "prefix": "Bearer "})
```

> **Why it matters.** A program never holds a secret and a trace never contains one, so a run can be shared, replayed, or handed to a model for debugging without leaking credentials. See [Credentials](../programs/credentials.html) and [Effects](../programs/effects.html).
