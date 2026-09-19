---
title: Credentials
description: Secret refs, host-side header injection, the synced secrets store, and OAuth grants whose tokens guest code never holds.
order: 50
---
# Credentials

Guest code names a credential; the host resolves it. A program passes `credential: {ref, header, prefix?, about?}` on an HTTP call, the broker records the request, *then* reads the secret and sets the header. Key bytes never enter guest memory, the trace, or the model context.

The secret itself is seeded host-side — through [Connectors](../agents/connectors.html) or the [runtime quickstart](../quickstart/anyrt.html) — before any program names it.

## Injection

```python
_CRED = {"ref": "connector.key.github", "header": "Authorization", "prefix": "Bearer ",
         "about": {"label": "GitHub personal access token",
                   "hosts": ["api.github.com"],
                   "help": "https://github.com/settings/tokens?type=beta"}}

@span(kind="getter")
def whoami():
    """The token's user → {ok, login, html_url}."""
    r = http.get("https://api.github.com/user", credential=_CRED)
    d = r.json()
    return {"ok": r.status == 200, "login": d.get("login"), "html_url": d.get("html_url")}
```

The record's canonical input is written before the value is resolved, so the trace shows the request with no secret in it. Redaction is structural — there is no scrubbing step that could miss. `about` is an optional, non-secret descriptor: what the person entering the key is shown, including the hosts the key is meant for.

## Two kinds of ref

| Ref namespace | Kind | How the host resolves it |
|---|---|---|
| `llm.key.<provider>`, `connector.key.<name>` | static | the stored string, injected as-is |
| `connector.oauth.<provider>` | managed OAuth | the cached access token, refreshed at injection time with a 120 s margin |

The ref set is **open**: any `connector.key.<x>` is stored without a runtime change, so a new connector needs no host release.

## Where secrets live

Each secret is one record in the `agent_secrets` store on the agent space's `bao/secrets/v1` child, and its `value` is an ordinary synced field. The agent space belongs to one account, and every change is end-to-end encrypted with the space key before it leaves a device, so a key typed on a phone reaches the desktop that runs the agent and exists in cleartext only on the account's own devices. The broker reads the record at injection time, so a key written while the agent runs is used by the very next request — no restart.

A missing key is a typed failure, not a dead end. A static ref with no value fails the request with `SecretMissing` (`no secret for credential ref "<ref>"`, which connectors turn into their not-connected message); the host stamps the record `status: "missing"` with the `about` descriptor and posts one chat message carrying a `credential_request` attachment, which a client renders as an entry form. A key the destination rejects — a `401`, or a provider's invalid-key `400` — is stamped `rejected` and asked for the same way. Environment variables are not read.

Seeding from a file:

```
# .connectors.env — next to anybao.toml (fallback: cwd), or --secrets-file <path>
llm.key.anthropic=sk-ant-…
connector.key.linear=lin_api_…
connector.key.github=github_pat_…
connector.key.granola=          # empty value DELETES the stored secret
```

On every `anyrt serve` start each ref in the file is written through: missing → bootstrapped, different → rotated, empty → removed. Refs absent from the file are untouched. Precedence: hard seeds (file) > stored > soft seeds (an embedder's bundled defaults, persisted only when nothing is stored). `anyrt run` accepts the same file but has no store — seeds apply to that run only.

## The read guard

The runtime refuses guest HTTP requests whose body names the secrets collection (any collection ending `_agent_secrets`, in any space) or that reference the secrets object, with a `forbidden` effect failure raised *before* execution — the refusal is the recorded fact. `config.get` refuses the `llm.key.*`, `connector.key.*` and `connector.oauth.*` namespaces wholesale.

> **Why it matters.** With hosted functions, a leaked environment variable is a leaked key. Here the guest has no channel through which a key could pass: not memory, not the log, not the config API. An agent-authored program may *use* an existing ref but cannot mint or read one.

## OAuth: consent as an effect that returns no tokens

Providers like Google mint credentials through a user consent flow and expire them hourly, so acquisition and renewal are themselves effects:

| Effect | Class | Returns |
|---|---|---|
| `oauth.connect(provider, scopes?, timeout?)` | mutate | `{ok, provider, grantedScopes, account}` |
| `oauth.status(provider)` | read | `{connected, pending, scopes, account, expiresAt}` — no network |
| `oauth.disconnect(provider)` | mutate | `{ok, provider, revoked}` — provider revoke + stored grant delete |
| `oauth.refresh(provider)` | mutate | host-emitted only; a guest call fails typed `host_only` |

`connect` runs the native-app pattern — system browser plus a single-use loopback receiver, PKCE S256 and `state` generated host-side — and blocks up to `min(timeout | 120 s, 300 s)`. The receiver stays open for a 5-minute window, so slow consent lands late and shows up as `pending` in `oauth.status`. The guest never receives the consent URL either; the host hands it to the human (an embedder's consent hook, else the system browser, with the URL in the host log). It is for user-facing turns only — never call it from a cron program.

Failures are typed: `not_configured`, `not_connected`, `consent_denied`, `consent_timeout`, `state_mismatch`, `token_exchange_failed`, `no_refresh_token`.

Connectors ship their own OAuth client: a desktop-app client id and secret are public by design (PKCE and the loopback redirect are the protection), so `googleAuth@v1` passes them to `connect`, and the host keeps them as non-secret metadata so refresh keeps working across restarts. A self-hosted client is a `connector.oauth.google.client_id` / `.client_secret` secret record, used only when the connector bundles none.

The refresh token is stored as the `connector.oauth.google.refresh` secret and is never injectable — naming a sub-ref like it in a credential is a typed error. Access tokens live only in host memory. Renewal happens inside the boundary when a credentialed request needs it, single-flight per ref, recorded as its own `oauth.refresh` record (`{ok, expiresAt, scope, rotated}`) sequenced before the HTTP record it serves. A dead grant (`invalid_grant`) maps to `oauth_reconsent_required` — re-run `connect`.

One consent covers every connector for that provider: `connector.oauth.google` is shared by the Gmail, Calendar, Drive and Sheets connectors with incremental re-consent when a new one needs more scope.

> **Note.** An empty `connector.oauth.google.refresh=` seed deletes the stored token; the grant stays live on the provider account. Use the provider's `disconnect()` to revoke server-side.

## Redirects with a credential

For credentialed requests the broker owns the follow decision. No `redirects` in the payload → manual: the 3xx and its `location` come back as data. An explicit count follows **same-origin only**, re-attaching the credential per hop; a cross-origin 3xx is returned, never followed. Uncredentialed requests keep the client's default (follow, limit 5).
