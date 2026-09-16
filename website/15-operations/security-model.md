---
title: Security model
description: Understand encrypted sync, local API and disk access, model-provider exposure, and how credentials are stored.
order: 50
---
# Security model

Any encrypts space content before it syncs between devices. Members hold the keys; sync and file nodes store and relay encrypted content. On a device, the local server reads the data and exposes it through its loopback API.

These protections apply to different parts of an application:

| Surface | Who can read it | What to configure or protect |
|---|---|---|
| Synced content | members with the space's read key | [Membership and roles](../collaboration/acl.html) |
| Local HTTP API | processes that can connect to the loopback port | trust in applications running on the device |
| Local database and index | anyone who can read the data directory | operating-system access and device storage protection |
| Hosted model or embedding request | the provider receiving the request | model choice and which data the harness sends |

The sections below describe these boundaries, the wallet files, and the browser-origin allowlist.

## The socket: loopback, no auth

- The server binds `127.0.0.1` (or another loopback address) and refuses anything else with a clear error. There is no TLS, no auth token, no rate limiting on the API.
- The trust model is the operating-system user: a local process that can open a TCP connection to the port has the same power as the CLI. That is the same trust a local database socket or a browser's local profile directory has.
- Lifecycle is gated by **ownership, not authentication**: a standalone server refuses `POST /v1/shutdown`, `DELETE /v1/auth` and account switches outright (`403`), and a managed server accepts them only with the control token its host holds — so another local process cannot log it into a different account or sign it out over HTTP. Lifetime stays uid-bounded: any same-user process can still `any stop` or `kill` a server, token or not. Everything else on the socket stays open to any local process.

The loopback API is intended for local applications. Space membership controls access to synced data; it is not authentication for callers of this socket.

## Browser origins

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

A model provider receives the prompt you send, including selected history, recalled memories, and any tool results included in the next request. A connector's service likewise receives its API requests. End-to-end sync encryption does not hide these inputs from their destination.

Search embeddings are configured separately from agent inference. The default `index.embedder: auto` uses an online primary and a local fallback. Use `index.embedder: local` for embeddings computed on the device; the model and native libraries must be available. This does not change the provider used by an agent conversation. See [Configuration](configuration.html#use-local-search-embeddings) and [Conversations](../agents/conversations.html#the-message-model).

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

Programs run through the companion anyrt runtime's effect boundary. The host handles credentials and enforces execution limits; the guest receives only the supported capabilities. See [Effects](../programs/effects.html), [Credentials](../programs/credentials.html), and [Limits](../programs/limits.html).
