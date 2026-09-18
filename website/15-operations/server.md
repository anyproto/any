---
title: Server
description: How any run boots, what it serves during the background boot pass, how it shuts down, and the per-account single-instance lock.
order: 10
---
# Server

`any run` is a plain foreground process: it loads config, boots the selected account's engine, binds a loopback listener and serves until it receives SIGINT/SIGTERM (which `any stop` sends) or — on a managed server — its host's `POST /v1/shutdown`. No daemonization — run it under a terminal, `tmux`, `systemd --user` or whatever supervisor you prefer.

`--mode` declares who owns the process: `standalone` (default) is the user's server — keys on disk, account resolved from the data dir, logout and HTTP shutdown refused; `managed` is a host's — the account arrives over `POST /v1/auth` on every launch and sign-out, account switching and `POST /v1/shutdown` are accepted behind a control token the host holds ([Accounts](../auth/accounts.html)).

## Startup

1. **Load config** — file, then `ANY_*` environment overrides, then flags ([Configuration](configuration.html)).
2. **Managed**: skip account resolution — start unauthorized (step 4) and wait for the host's `POST /v1/auth`. **Standalone**: resolve the data-dir root (default `~/.any/`) and pick the account:
   - `auth.walletPath` / `--wallet` → that wallet, data flat at the root (manual mode);
   - `account:` / `ANY_ACCOUNT` / `--account` → `<root>/<id>/` if present, else the root wallet (its derived id must match);
   - no selector → the root `wallet.key` if one exists, else a sole `<root>/<id>/` directory, else **no account**.
3. **With an account, boot its engine** before the listener binds, so boot failures surface immediately: take the instance lock in the account dir → open the wallet → derive the account id → open the SDK → open the search indexer. `any run` never generates a wallet; create accounts with `any init` or over HTTP. Opening checks the account's **CRDT version mark** first: data written by a newer release refuses the boot (`any run` exits with the reason; over HTTP `409 sdk.crdt_version_newer`), so an older server never writes into data shaped by rules it does not know.
4. **Without an account, start unauthorized.** Every `/v1` route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json` and `/v1/auth` returns `401 auth.required` until `POST /v1/auth` creates, restores or selects an account and boots the engine in place — no restart. See [Accounts](../auth/accounts.html).
5. **Bind** `127.0.0.1:<port>` (default 7001), print `LISTENING <addr>` on stdout — followed by `CONTROL_TOKEN <hex>` on a managed server that minted its token — record the address in the account dir's `server.addr` once an engine is up, and serve.

```bash
any run --data-dir ~/.any --addr 127.0.0.1:7001 --log-level info
```

Opening the SDK also starts the one-to-one inbox notifier when a coordinator is configured, so incoming direct-space requests surface as pending rows in the space list without any wiring ([One-to-one spaces](../collaboration/one-to-one.html)).

## The boot pass

The SDK returns from `Open` after local wiring only. Eager space loading and offline catch-up run on one serial background pass, so the server serves as soon as the listener binds; until a space's turn comes, reads against it return the pre-offline state.

`GET /v1/health` reports the pass:

```json
{ "status": "ok", "version": "any v0.1.2 (commit 1a2b3c4, built 2026-09-09)",
  "startedAt": "2026-09-10T08:12:00Z", "networkId": "N83gJpVd…",
  "account": "A3…", "bootstrapping": true,
  "crdtVersion": { "supported": 2, "stored": 2, "newer": false } }
```

`networkId` is the any-sync network the server joins, read from the node configuration it started with and present whether or not an account is signed in ([Networks](networks.html)).

`bootstrapping` is `true` while the pass runs and `false` when it completes (or when the server is unauthorized). It reports the one-shot pass only — per-space convergence is on [sync status](../realtime/sync-status.html). Health never needs SDK state, which makes it the right probe for supervisors and for `any status`.

`crdtVersion` (absent when unauthorized) compares the data-model version this server writes with the one the account records. `newer: true` means another device on a newer release raised it: the server keeps serving reads and refuses every synced write with `409 sdk.crdt_version_newer` until it is upgraded.

> **Note.** Boot also upserts this device's row in the account's [devices registry](../auth/devices.html) (OS and version each boot, hostname as the name on first registration), and — for the well-known derived spaces the account already has — waits for their bundle registries to converge so a client ensuring right after a restore meets real state rather than an empty one.

## Listen address

| Setting | Behaviour |
|---|---|
| default | `127.0.0.1:7001`, plain HTTP, no TLS, no auth |
| `listen.addr` / `--addr host:port` | any loopback address and port |
| `--addr 127.0.0.1:0` | ephemeral port; the `LISTENING <resolved-addr>` stdout line is the readiness handshake the desktop shell relies on, and `server.addr` lets the CLI find the port without `--addr` |
| a non-loopback address | refused with a clear error: remote access is not supported |

## Shutdown

Two triggers, one path, gated by ownership:

1. `SIGINT` / `SIGTERM` — every server. `any stop` sends it: it finds the server serving the data dir's account by its held instance lock (proof of life), signals it and waits for the lock to be released — so it works against a wedged server and one on an ephemeral port. An unauthorized standalone server holds no account lock yet; stop it with Ctrl-C. Windows has no signal to send, so there `any stop` errors and stopping is the host's job.
2. `POST /v1/shutdown` — managed servers only, with the `X-Any-Control-Token` header. A standalone server answers `403 shutdown.not_managed`.

The server tears the engine down — open SSE streams emit their terminal `closed{reason: "server_shutdown"}` frame, in-flight requests drain with a 10-second deadline — closes the listener and exits 0. After the deadline, remaining requests are aborted and the process still exits 0. An app embedding the server stops it through the embedding API in either mode.

A managed server also tears its engine down in place, without exiting: `DELETE /v1/auth` signs out and `POST /v1/auth` with `replace: true` switches account. Streams then end with `closed{reason: "deauthorized"}`, and the listener stays up.

```bash
any stop                                   # standalone (or managed) — signal
curl -s -X POST -H "X-Any-Control-Token: $TOKEN" http://127.0.0.1:7001/v1/shutdown   # managed host
```

## Single-instance lock

When an account's engine boots, the server takes an exclusive OS file lock on `<account-dir>/server.lock` (the root itself for the legacy flat layout) — `flock(2)` on Linux and macOS, `LockFileEx` on Windows. A held lock fails the boot with a clear message — `409 auth.account_in_use` when it happens through `POST /v1/auth`.

The kernel releases the lock when the holder exits by any means, so there is nothing stale to reclaim: a crashed server blocks nobody. None of these files is removed on shutdown. `server.lock` is empty; the holder's pid goes in `<account-dir>/server.pid` purely to name it in that error (`details.pid`, absent when the file is unreadable), and its bound address in `<account-dir>/server.addr` once the listener is up. Treat both as labels, never as proof a server is running: `any stop` and the CLI's address discovery probe the lock itself.

The lock is **per account**: two servers may share one data-dir root as long as they serve different accounts on different ports. An unauthorized server holds no lock until it boots an account; a managed server releases it on sign-out and takes the next account's on a switch.

## One server, one account

A process serves one account at a time. Switching the account of a running server is an ownership right: a managed server's host switches in place (`POST /v1/auth` with `replace: true`) or signs out (`DELETE /v1/auth`); a standalone server refuses both — stop it and start with `--account <id>`, or let `POST /v1/auth` pick on an unauthorized server. Two accounts side by side:

```bash
ANY_ACCOUNT=A3… any run --addr 127.0.0.1:7001
ANY_ACCOUNT=B7… any run --addr 127.0.0.1:7002
```

## Logging

All logging goes through one backend; the HTTP framework's logger is wired to the same stream. The `log` config block applies globally: default level `info`, colorized output to stderr, optional extra output paths (e.g. a file), optional JSON format. `--log-level debug` or `ANY_LOG_LEVEL=debug` raises verbosity for one run. See [Debugging](debugging.html).

## Background workers

| Worker | Runs when | What it does |
|---|---|---|
| search indexer | `index.enabled` (default true) | per-space FTS + embed loops and the link index behind backlinks ([Search](../search/indexing.html)) |
| push | a push node is configured | token registration, subscription sync, notify queue ([Push](../notifications/push.html)) |
| file-cache sweep | `files.gcInterval` set | periodic cache reclamation ([Files cache](../files/cache.html)) |

None of them is on any request's critical path; each reports long work as a [process](../notifications/processes.html).
