---
title: Credentials
description: Secret refs, host-side header injection, device-local storage, and OAuth grants whose tokens guest code never holds.
order: 50
---
# Credentials

Guest code names a credential; the host resolves it. A program passes `credential: {ref, header, prefix?}` on an HTTP call, the broker records the request, *then* sets the header from a device-local store. Key bytes never enter guest memory, the trace, or the model context.

## Injection

```python
_CRED = {"ref": "connector.key.github", "header": "Authorization", "prefix": "Bearer "}

@span(kind="getter")
def repo(owner, name):
    """One repository → {full_name, html_url, stargazers_count}."""
    r = http.get(f"https://api.github.com/repos/{owner}/{name}", credential=_CRED)
    d = r.json()
    return {k: d[k] for k in ("full_name", "html_url", "stargazers_count")}
```

The record's canonical input is written before the value is resolved, so the trace shows the request with no secret in it. Redaction is structural — there is no scrubbing step that could miss.

## Two kinds of ref

| Ref namespace | Kind | How the host resolves it |
|---|---|---|
| `llm.key.<provider>`, `connector.key.<name>` | static | the stored string, injected as-is |
| `connector.oauth.<provider>` | managed OAuth | the cached access token, refreshed at injection time with a 120 s margin |

The ref set is **open**: any `connector.key.<x>` is stored without a runtime change, so a new connector needs no host release. A missing key surfaces as the connector's own actionable not-connected error.

## Where secrets live

Secrets persist **device-locally**, as never-synced local values on the per-space secrets object (the `agent_secrets` dataset). Config stays guest-readable; secrets do not. Environment variables are not read. Seeding paths:

```
# .connectors.env — next to anybao.toml (fallback: cwd), or --secrets-file <path>
llm.key.anthropic=sk-ant-…
connector.key.linear=lin_api_…
connector.key.github=github_pat_…
connector.key.granola=          # empty value DELETES the stored secret
```

On every `anyrt serve` start each ref in the file is written through: missing → bootstrapped, different → rotated, empty → removed. Refs absent from the file are untouched. Precedence: hard seeds (file) > stored > soft seeds (an embedder's bundled defaults, persisted only when nothing is stored). `anyrt run` accepts the same file but has no store — seeds apply to that run only.

A fresh space, or a deleted-and-recreated one, has an empty store. With no LLM key the agent still starts but warns, and LLM effects fail on first use until one is imported.

## The read guard

The runtime refuses guest HTTP requests that reference the `agent_secrets` dataset or the guarded object id, with a `forbidden` effect failure raised *before* execution — the refusal is the recorded fact. `config.get` refuses secret keys the same way, and the `connector.oauth.*` namespace wholesale.

> **Why it matters.** With hosted functions, a leaked environment variable is a leaked key. Here the guest has no channel through which a key could pass: not memory, not the log, not the config API. An agent-authored program may *use* an existing ref but cannot mint or read one.

## OAuth: consent as an effect that returns no tokens

Providers like Google mint credentials through a user consent flow and expire them hourly, so acquisition and renewal are themselves effects:

| Effect | Class | Returns |
|---|---|---|
| `oauth.connect(provider, scopes?, timeout?)` | mutate | `{ok, provider, grantedScopes, account}` |
| `oauth.status(provider)` | read | `{connected, pending, scopes, account, expiresAt}` — no network |
| `oauth.disconnect(provider)` | mutate | `{ok, provider, revoked}` — provider revoke + local delete |
| `oauth.refresh(provider)` | mutate | host-emitted only; a guest call fails typed `host_only` |

`connect` runs the native-app pattern — system browser plus a single-use loopback receiver, PKCE S256 and `state` generated host-side — and blocks up to `min(timeout | 120 s, 300 s)`. The receiver stays open for a 5-minute window, so slow consent lands late and shows up as `pending` in `oauth.status`. The guest never receives the consent URL either; the host hands it to the human (an embedder's consent hook, else the system browser, else stderr). It is for user-facing turns only — never call it from a cron program.

Failures are typed: `not_configured` (the message names the `.connectors.env` keys to seed), `consent_denied`, `consent_timeout`, `state_mismatch`, `token_exchange_failed`, `no_refresh_token`.

Setup is bring-your-own client, seeded like any other ref:

```
connector.oauth.google.client_id=…
connector.oauth.google.client_secret=…
connector.oauth.google.refresh=        # empty = delete the stored token LOCALLY only
```

The refresh token is held device-locally and is never injectable — naming a sub-ref like `connector.oauth.google.refresh` in a credential is a typed error. Access tokens live only in host memory. Renewal happens inside the boundary when a credentialed request needs it, single-flight per ref, recorded as its own `oauth.refresh` record (`{ok, expiresAt, scope, rotated}`) sequenced before the HTTP record it serves. A dead grant (`invalid_grant`) maps to `oauth_reconsent_required` — re-run `connect`.

One consent covers every connector for that provider: `connector.oauth.google` is shared by the Gmail, Calendar, Drive and Sheets connectors with incremental re-consent when a new one needs more scope.

> **Note.** An empty `.refresh=` seed deletes the token locally; the grant stays live on the provider account. Use the provider's `disconnect()` to revoke server-side.

## Redirects with a credential

For credentialed requests the broker owns the follow decision. No `redirects` in the payload → manual: the 3xx and its `location` come back as data. An explicit count follows **same-origin only**, re-attaching the credential per hop; a cross-origin 3xx is returned, never followed. Uncredentialed requests keep the client's default (follow, limit 5).
