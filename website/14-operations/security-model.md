---
title: Security model
description: Loopback is the process boundary, end-to-end encryption is the data boundary — what each protects, what the CORS allowlist is for, and what is deferred.
order: 50
---
# Security model

There are two boundaries. The HTTP socket is **localhost-only with no authentication**: anyone who can run a process on the machine can call it. The data is **end-to-end encrypted with keys that never leave the device**: nothing on the network — sync nodes, coordinator, file nodes, push node — can read it. Understanding which boundary protects what is most of operating `any` safely.

## The socket: loopback, no auth

- The server binds `127.0.0.1` (or another loopback address) and refuses anything else with a clear error. There is no TLS, no auth token, no rate limiting on the API.
- The trust model is the operating-system user: a local process that can open a TCP connection to the port has the same power as the CLI. That is the same trust a local database socket or a browser's local profile directory has.
- `POST /v1/shutdown` is likewise unauthenticated — reachable only from the machine.

> **Why it matters.** Because the server never listens off-host, the whole remote attack surface is the any-sync protocol, which carries ciphertext and signed ACL records. A remote-access story — TCP auth, TLS — is deferred until it exists as a designed feature rather than a bolt-on.

## CORS: a fixed allowlist, not a hole

Browsers enforce CORS on top of the socket. The server allows exactly the origins the bundled desktop-shell webview runs at:

| Origin | Why |
|---|---|
| `tauri://localhost` | the desktop app's webview on macOS / Linux |
| `http://tauri.localhost` | the same on Windows |
| `http://localhost:5173`, `http://127.0.0.1:5173` | the desktop app's dev server |

Custom schemes are unclaimable by web content and `.localhost` is pinned to loopback, so no remote web page can present these origins. Allowed headers are `Content-Type`, `Accept` and `Range` (ranged file downloads). Requests without an `Origin` header — curl, the CLI, same-origin — are unaffected. The allowlist does not widen the trust model: local processes were never gated by CORS in the first place.

## The data: end-to-end encryption

- Every space's content is encrypted with keys derived from the space's ACL; members receive the read key through signed ACL records. Sync and file nodes store and relay ciphertext only.
- Identity is a keypair. The account key is derived from the mnemonic; each device holds its own device key. Membership, roles and invites are signed ACL records, so a node cannot grant itself access ([ACL](../collaboration/acl.html)).
- Profiles pushed to the identity directory are encrypted too — a contact's name resolves only once their key arrives through a shared space or a direct-space invite ([Identities](../auth/identities.html)).
- Push notifications are encrypted by the sender with keys derived from ACL state; the push node sees topics, not content ([Push](../notifications/push.html)).
- Files are encrypted as UnixFS DAGs before leaving the device ([Files](../files/index.html)).

What the network can observe: which peer ids sync which space ids, timing and sizes. What it cannot: any record, any file byte, any name.

## Secrets on this machine

| Secret | Where | Notes |
|---|---|---|
| mnemonic | printed once by `any init`, never stored | back it up; it is the account |
| `wallet.key` | `<account-dir>/wallet.key`, mode 0600 | optionally encrypted with a passkey (`ANY_WALLET_PASSKEY` or `--passkey-stdin`, never an interactive prompt) |
| embedder API key | `index.openai.apiKey` | sent as a Bearer header, never logged |
| data on disk | `sdk/`, `files/`, `index/` | plaintext-readable with the wallet — protect the directory like a key store ([Data directory](data-dir.html)) |

Error responses never carry file paths or internal types; stack traces go to the server log, not the body ([Errors](../reference/errors.html)).

## Local-network peers

LAN discovery (`p2p`) announces this device over mDNS. The space exchange proves membership per space and reveals only the set of spaces two peers share; a stranger on the LAN sees an empty list. Disable with `p2p.enabled: false`, or isolate a deployment with `p2p.serviceName` ([Networks](networks.html)).

## What is deferred

| Deferred | Meaning today |
|---|---|
| remote access | no non-loopback bind, no TLS, no auth tokens |
| multi-account per process | one server = one account; a second account is a second process |
| install / service files | you run `any run` under your own supervisor |
| API-level rate limiting | none; the socket is local |

Programs and agents that run inside the data — the sandboxed Python runtime — have their own boundary, the effect system, which is described under [Programs and effects](../understanding/programs-and-effects.html) and [Limits](../programs/limits.html).
