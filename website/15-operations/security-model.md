---
title: Security model
description: Loopback is the process boundary, end-to-end encryption is the data boundary — what each protects, what the CORS allowlist is for, and what the server does not provide.
order: 50
---
# Security model

There are two boundaries. The HTTP socket is **localhost-only and currently has no per-caller authentication** — proper auth and access capabilities are planned — so anyone who can run a process on the machine can call it. The data is **end-to-end encrypted before it syncs**: sync nodes, coordinator, file nodes and the push node relay ciphertext they cannot read. Neither boundary covers the disk or outside providers — the data dir is not encrypted at rest, and an online embedder reads the text it embeds. Understanding which boundary protects what is most of operating `any` safely.

## The socket: loopback, no auth

- The server binds `127.0.0.1` (or another loopback address) and refuses anything else with a clear error. There is no TLS, no auth token, no rate limiting on the API.
- The trust model is the operating-system user: a local process that can open a TCP connection to the port has the same power as the CLI. That is the same trust a local database socket or a browser's local profile directory has.
- Lifecycle is gated by **ownership, not authentication**: a standalone server refuses `POST /v1/shutdown`, `DELETE /v1/auth` and account switches outright (`403`), and a managed server accepts them, and the local-discovery switch (`PUT /v1/local-discovery`), only with the control token its host holds — so another local process cannot log it into a different account, sign it out, or start scanning the LAN over HTTP. Lifetime stays uid-bounded: any same-user process can still `any stop` or `kill` a server, token or not. Everything else on the socket stays open to any local process.

> **Why it matters.** Because the server never listens off-host, the whole remote attack surface is the any-sync protocol, which carries ciphertext and signed ACL records.

## CORS: a fixed allowlist, not a hole

Browsers enforce CORS on top of the socket. The server allows exactly the origins the bundled desktop-shell webview runs at:

| Origin | Why |
|---|---|
| `tauri://localhost` | the desktop app's webview on macOS / Linux |
| `http://tauri.localhost` | the same on Windows |
| `http://localhost:5173`, `http://127.0.0.1:5173` | the desktop app's dev server |

Custom schemes are unclaimable by web content and `.localhost` is pinned to loopback, so no remote web page can present these origins. Allowed headers are `Content-Type`, `Accept`, `Range` (ranged file downloads) and `X-Any-Control-Token` (the webview logging in a managed server). Requests without an `Origin` header — curl, the CLI, same-origin — are unaffected. The allowlist does not widen the trust model: local processes were never gated by CORS in the first place.

## The data: end-to-end encryption

- Every space's content is encrypted with keys derived from the space's ACL; members receive the read key through signed ACL records. Sync and file nodes store and relay ciphertext only.
- Identity is a keypair. The account key is derived from the mnemonic; each device holds its own device key. Membership, roles and invites are signed ACL records, so a node cannot grant itself access ([ACL](../collaboration/acl.html)).
- Profiles pushed to the identity directory are encrypted too — a contact's name resolves only once their key arrives through a shared space or a direct-space invite ([Identities](../auth/identities.html)).
- Push notifications are encrypted by the sender with keys derived from ACL state; the push node sees topics, not content ([Push](../notifications/push.html)).
- Files are encrypted as UnixFS DAGs before leaving the device ([Files](../files/index.html)).
- Search indexing is outside this boundary: with the default `index.embedder: auto` the text of indexed documents and search queries goes to the online embedding provider; `local` keeps it on the device ([Embedders](../search/embedders.html)).

What the network can observe: which peer ids sync which space ids, each space's ACL (member public keys and permissions), object and change ids, DAG shape, sizes and timing, and push topics. What it cannot: field values, records, file contents and names, space names, profiles or message text ([Encryption](../understanding/encryption.html)).

## Models and external services

Neither boundary covers an inference request. A model provider reads the prompt the harness sends it — selected history, recalled memories, tool results — and a connector's service reads its API calls; end-to-end encryption protects the data at rest and between members, not the copy you hand to a provider. Search embeddings are the other outbound path and are configured apart from agent inference: `index.embedder: auto` embeds through the online primary with a local fallback, `local` keeps it on the device ([Configuration](configuration.html#local-embeddings), [Conversations](../agents/conversations.html#the-message-model)).

## Secrets on this machine

| Secret | Where | Notes |
|---|---|---|
| mnemonic | standalone: inside `wallet.key`; managed: memory only — the host supplies it over `POST /v1/auth` on every boot | printed once by `any init`; back it up, it is the account |
| `wallet.key` | `<account-dir>/wallet.key`, mode 0600 | standalone; holds the mnemonic and this device's key as plain JSON unless a passkey encrypts it (`ANY_WALLET_PASSKEY` or `--passkey-stdin`, never an interactive prompt) — whoever reads a plain file holds the account |
| `device.key` | `<account-dir>/device.key`, mode 0600 | managed; the device key only — the host supplies the account key on each boot and the server holds it in memory |
| control token | printed once as `CONTROL_TOKEN <hex>` to the spawning host, or passed in-process | managed; never logged, never on disk; the CLI takes it from `ANY_CONTROL_TOKEN` |
| embedder API key | `index.openai.apiKey` | sent as a Bearer header, never logged |
| data on disk | `sdk/`, `index/`, `files/` | not encrypted at rest: records, the local store and indexed text are readable without the wallet; `files/` holds file content encrypted. Protect the directory like a key store ([Data directory](data-dir.html)) |

Error responses never carry file paths or internal types; stack traces go to the server log, not the body ([Errors](../reference/errors.html)).

## Local-network peers

LAN discovery (`p2p`) announces this device over mDNS. The space exchange proves membership per space and reveals only the set of spaces two peers share; a stranger on the LAN sees an empty list. Disable with `p2p.enabled: false`, or isolate a deployment with `p2p.serviceName` ([Networks](networks.html)).

## What the server does not provide

| Not provided | Consequence |
|---|---|
| remote access | no non-loopback bind, no TLS, no auth tokens |
| several accounts per process | one server = one account; a second account is a second process |
| install / service files | you run `any run` under your own supervisor |
| API-level rate limiting | none; the socket is local |

Programs and agents that run inside the data — the sandboxed Python runtime — have their own boundary, the effect system, which is described under [Programs and effects](../understanding/programs-and-effects.html) and [Limits](../programs/limits.html).
