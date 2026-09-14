---
title: Connectors
description: Service connectors as programs — Linear, GitHub, Gmail, Calendar and more — with credentials held by the host and injected after the request is recorded.
order: 50
---
# Connectors

A connector is a tool program in the `connectors` overlay space that wraps one external API. The API key never enters guest code: every request names a **credential ref**, and the runtime injects the header after the request is recorded, so the key is absent from the trace and the model context.

## The connectors

| Program | Summary |
|---|---|
| `linear@v1` | Linear issue-tracker connector (GraphQL) — issues, search, writes (`create_issue`, `update_issue`, `create_comment`); `gql()` is the raw escape hatch. |
| `github@v1` | The user's GitHub work context — issues, PRs, commits, repos, files, notifications; `request()` is the one raw write path, pinned to api.github.com. |
| `granola@v1` | Read-only Granola connector — AI meeting notes, transcripts, folders. |
| `attio@v1` | Read-only Attio CRM connector — records, lists, notes, members. |
| `figma@v1` | Read-only Figma connector — file metadata, comments, extracted text content. |
| `intercom@v1` | Read-only Intercom connector — conversations, contacts, help-center articles. |
| `googleAuth@v1` | Google account connection — one consent covers gmail/calendar/drive/sheets; `connect`, `status`, `disconnect`. |
| `gmail@v1` | Read-only Gmail connector — search, messages, threads, labels. |
| `gmailSync@v1` | Gmail → space sync: chunked full sync, history ticks, `clean_html`; mail lands as `email_messages` records. |
| `googleCalendar@v1` | Read-only Google Calendar connector — calendars, events, upcoming view, incremental sync tokens. |
| `googleDrive@v1` | Google Meet transcripts (primary) plus Drive Doc-export fallback, read-only. |
| `googleSheets@v1` | Read-only Google Sheets connector — discovery, tabs, ranges, header-mapped records. |

Every method returns `{ok, ...}` or `{ok: False, error}` with an actionable message — a missing key explains how to connect, never a traceback. Fields keep the upstream API's own names, trimmed rather than renamed, so what the model knows about the API transfers.

```python
ln = use("connectors:linear@v1")
ln.my_issues()                       # {ok, issues: [...]}
gh = use("connectors:github@v1")
gh.get_issue("anyproto", "any", 42)
```

## Credentials

A connector's requests carry `credential: {ref, header, prefix, about}` — `about` describes the key (label, the hosts it is sent to, where to get one); the host resolves `ref` against the agent's secret store at request time.

| Ref kind | Shape | Resolution |
|---|---|---|
| static | `connector.key.<name>`, `llm.key.<provider>` | a stored string injected as-is |
| managed OAuth | `connector.oauth.<provider>` | a handle: the host caches the access token in memory, refreshes it from the stored refresh token when stale, and injects `Authorization: Bearer …` |

Secrets are seeded from a dotenv-style `.connectors.env` next to `anybao.toml` (or `anyrt serve --secrets-file <path>`), keyed by the ref itself:

```
llm.key.anthropic=sk-ant-…
connector.key.linear=lin_api_…
connector.key.github=github_pat_…
connector.key.granola=            # empty value deletes the stored secret
```

On every serve start each ref in the file is written through to the store — missing becomes `bootstrapped`, different becomes `rotated`, empty becomes `removed`; refs absent from the file are untouched. The ref set is open, so a new connector needs no runtime change. In the desktop app, Help → Import connector keys writes the same rows without a restart.

A key that is missing — or that the destination rejects — needs no file at all: the runtime posts a credential request into the chat (a `credential_request` attachment naming the ref), the client renders a password field for it, and saving the key lets the agent carry on. The request comes from the host, never from the model.

Each secret is a row in the `agent_secrets` dataset of the agent space; its `value` syncs end-to-end encrypted to the account's own devices, so a key entered on a phone reaches the device running the agent. Guest reads of that dataset are refused before execution, so the refusal is the recorded fact and no secret ever reaches a trace. Full detail: [Credentials](../programs/credentials.html).

## OAuth: Google without tokens in guest code

Static keys do not fit Google — the credential is minted by a user consent flow and expires hourly, so acquisition and renewal are themselves side effects. The custody rule: **no access token, refresh token or authorization code is ever returned to guest code, written to a synced field, or recorded in a trace.** The guest also never receives the consent URL.

```python
ga = use("connectors:googleAuth@v1")
ga.connect()      # runs consent in the user's browser; blocks ≤120s
                  # → {ok, provider, grantedScopes, account}
ga.status()       # → {connected, pending, scopes, account, expiresAt}
ga.disconnect()   # revokes at Google AND deletes the local grant
```

`googleAuth@v1` ships its own OAuth client — a Google desktop-app client is public by design, so PKCE and the loopback redirect are the protection; a `connector.oauth.google.client_id` row in the secret store overrides it with a self-hosted one. The host runs authorization-code + PKCE over a loopback listener, keeps the refresh token as `connector.oauth.google.refresh` in the secret store, and the four Google connectors share the one `connector.oauth.google` ref. `connect` is for user-facing turns only, never cron; after a `consent_timeout` the consent window stays open a few minutes — poll `status()`. Provider descriptors (authorize/token/revoke URLs, default scopes, auth params) are a host-side table, so adding another provider is a table row.

> **Note.** An empty `connector.oauth.google.refresh=` in the seed file deletes the stored token only — the grant stays live at Google until `disconnect()` revokes it.

## Gmail sync

`gmailSync@v1` turns a mailbox into space data: one `mailbox` object per address, one `email_messages` record per message (record id = Gmail message id, body = cleaned markdown, `labelIds` the only provider-mutable field), declared as a [runtime dataset](../database/runtime-datasets.html) with an `email` search scope and written through [upsert](../database/upsert.html). Each cron tick lists a bounded slice, hydrates in 25-message batches, checkpoints after commit, and exits early when fuel runs low; steady state is `history.list` increments coalesced per message. The initial drain is a self-chaining `once` trigger chain with a circuit breaker, and the final hop nudges the agent to post the outcome in chat. The `_gmailSync` skill tells the agent to smoke-check the credential and settle the scope (time window, exclusions) with the user before arming a backfill.
