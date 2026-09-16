---
title: Operations
description: Running the any server — lifecycle, configuration, the data directory, networks, the security model, release builds and debugging.
order: 0
---
# Operations

`any` is one binary: `any run` is the server, every other `any …` command is an HTTP client for it. This section covers what an operator needs — how the process starts and stops, where its state lives, how it is configured, which network it joins, what the trust boundary is, and how to look inside when something is off.

## The process model

```
any run                      foreground, 127.0.0.1:7001
  ├── engine                 instance lock → keys → SDK → indexer (torn down in place on a managed sign-out)
  │     └── boot pass        eager space loading + offline catch-up (background)
  ├── HTTP listener          /v1/… — REST + SSE
  └── background workers     search indexer, push, file-cache GC (opt-in)
```

- One server serves **one account** at a time. Two accounts are two processes on two ports; they may share a data-dir root.
- `--mode` says who owns the process: `standalone` (the user — keys on disk) or `managed` (a host app that supplies the account on every launch and holds a control token for sign-out, switching and shutdown).
- The server binds **loopback only** and refuses anything else. There is no auth on the socket; the encrypted data and the account keys are the real boundary — see [Security model](security-model.html).
- Everything is under `/v1/`. `GET /v1/health` answers even before an account is booted.

## Day-one commands

```bash
any init                     # create an account, print the mnemonic once (back it up)
any run                      # serve in the foreground
any status                   # GET /v1/health
any stop                     # SIGTERM the server holding the account lock — graceful
any version
```

```bash
curl -s http://127.0.0.1:7001/v1/health
```

```json
{
  "status": "ok",
  "version": "any v0.1.2 (commit 1a2b3c4, built 2026-09-09)",
  "startedAt": "2026-09-10T08:12:00Z",
  "networkId": "N83gJpVd9MuNRZAuJLZ7LiMntTThhPc6DtzWWVjb1M3PouVU",
  "account": "A3…",
  "bootstrapping": false,
  "crdtVersion": { "supported": 2, "stored": 2, "newer": false }
}
```

> **Why it matters.** There is no hosted control plane to log into. The operator's whole surface is one local process, one directory on disk, and one YAML file — and the same holds on a phone, where the server runs embedded in the app.

## Where things are decided

| Concern | Page |
|---|---|
| ownership modes, start, boot pass, shutdown, instance lock, listen address | [Server](server.html) |
| YAML file, `ANY_*` env vars, flags, precedence, every key | [Configuration](configuration.html) |
| accounts, wallets and device keys, `sdk/`, `files/`, `index/`, `models/`, backups | [Data directory](data-dir.html) |
| production default, staging and local nodeconfs, LAN p2p | [Networks](networks.html) |
| loopback trust, CORS allowlist, E2E encryption, what is deferred | [Security model](security-model.html) |
| `make build`, build tags, release tarballs, mobile artifacts, CI | [Builds and CI](builds-and-ci.html) |
| health, sync status, debug endpoints, processes, logs, error codes | [Debugging](debugging.html) |

<div class="cards">
<a href="server.html"><strong>Server</strong><span>Startup sequence, boot pass, shutdown, single-instance lock</span></a>
<a href="configuration.html"><strong>Configuration</strong><span>File → env → flags, and the full key reference</span></a>
<a href="data-dir.html"><strong>Data directory</strong><span>Per-account layout, what is owned by whom, what is safe to delete</span></a>
<a href="networks.html"><strong>Networks</strong><span>Which any-sync network you join, and local-network sync</span></a>
<a href="security-model.html"><strong>Security model</strong><span>Localhost-only, no socket auth, encryption as the boundary</span></a>
<a href="builds-and-ci.html"><strong>Builds and CI</strong><span>Build tags, nix shell, tarballs, .aar and .xcframework</span></a>
<a href="debugging.html"><strong>Debugging</strong><span>Health, sync status, debug snapshots, logs, errors</span></a>
</div>
