---
title: Operations
description: Start and configure the local server, choose storage and network settings, protect your data, and diagnose problems.
order: 0
---
# Operations

Run `any` on the device that holds your data. `any run` starts the local HTTP server; the CLI and your application call that server to read and write records. The same server can run embedded in a mobile app.

This section covers process ownership, configuration, storage, sync networks, and troubleshooting. Agent execution is provided by the separate **anyrt** companion runtime; see [Host the runtime](../agents/embedding-anyrt.html) for its setup.

## Start here

| Your task | Guide |
|---|---|
| install and make your first request | [Install](../quickstart/install.html) |
| run the server yourself or host it in an app | [Server lifecycle](server.html) |
| choose local embeddings or a sync network | [Configuration](configuration.html) |
| back up an account or add a device | [Data directory](data-dir.html#backup-and-second-devices) |
| understand who can read data | [Security model](security-model.html) |
| investigate an error or missing data | [Troubleshooting](debugging.html) |

Any is a developer preview. API changes are expected. Open-source release is planned; the license choice is TBD.

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
- The server binds **loopback only** and refuses anything else. Local processes can call the API without caller authentication. Encrypted sync protects data in transit and on sync nodes; the local data directory and outside providers have separate boundaries — see [Security model](security-model.html).
- Everything is under `/v1/`. `GET /v1/health` answers even before an account is booted.

## Start and check a server

After [installing](../quickstart/install.html), create an account if you do not already have one. Save the recovery phrase printed by `any init`. In the first terminal:

```bash
any init                     # create an account, print the mnemonic once (back it up)
any run                      # serve in the foreground
```

`any run` keeps this terminal occupied. In a second terminal, check the server:

```bash
any status
any version
curl -s http://127.0.0.1:7001/v1/health
```

Expect `status: "ok"` and an `account` ID. The exact version, timestamps, and network ID differ by installation:

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

When you want to stop this server, press Ctrl-C in its terminal or run `any stop`. Both request a graceful shutdown; `any stop` signals the process holding the account lock.

By default an authorized server joins the production sync network, and search uses an online embedding primary with a local fallback. Choose your network and embedding settings in [Configuration](configuration.html) before starting a separate test environment.

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
