---
title: Data directory
description: The per-account layout under the data root, who owns each subdirectory, and what is safe to delete, copy or back up.
order: 30
---
# Data directory

`dataDir` (default `~/.any`) is a **root** that can hold several accounts. Each account keeps its wallet, network pin, instance lock, CRDT storage, file bytes and search index in its own subdirectory; the config file and the embedder model cache sit at the root and are shared.

## Layout

```
<root>/                          # dataDir, default ~/.any (mode 0700)
├── config.yaml                  # optional, if not passed via --config
├── models/                      # shared embedder model cache (all accounts)
├── wallet.key                   # LEGACY flat layout = the DEFAULT account;
├── network.json  server.*       #   its data stays directly at the root
├── sdk/  index/  files/         #   exactly as below
└── <accountId>/                 # every other account
    ├── wallet.key               # standalone: mnemonic + device key (mode 0600)
    ├── device.key               # managed: cached device key (mode 0600); the account key is never written
    ├── network.json             # the any-sync network this account's data belongs to
    ├── server.lock              # per-account single-instance lock (OS file lock)
    ├── server.pid               # holder's pid, for error messages only
    ├── server.addr              # holder's bound address, for the CLI only
    ├── sdk/                     # any-store databases — owned by the SDK; sdk.db also holds the local store
    ├── files/                   # file content, one CARv2 per rootCid — owned by the SDK
    └── index/                   # local search and link index (index.db) — owned by the indexer
```

A `wallet.key` directly at the root is the legacy flat layout: it acts as the default account with its data flat at the root, and nothing migrates it. New accounts always nest. A standalone server keeps the mnemonic and the device key in `wallet.key`, plain JSON unless a passkey encrypts it; a managed server never writes the account key and caches only the device key in `device.key`, minted once so every login keeps the same peer id.

## What each part is

| Path | Owner | Contents | Derived? |
|---|---|---|---|
| `wallet.key` | server | standalone: the mnemonic (the account) and this device's key | **no — the only copy of the device key** |
| `device.key` | server | managed: this device's key, reused on every login of the account | no — removing it registers this install as a new device |
| `network.json` | server | the id of the any-sync network the account's data belongs to, pinned on the first boot | no — without it the next boot adopts whatever network it starts on |
| `server.lock` | server | the single-instance lock itself — an OS file lock, empty, released by the kernel when the process exits | yes |
| `server.pid` | server | the lock holder's pid, written after acquiring; names it in `409 auth.account_in_use` and nothing more | yes |
| `server.addr` | server | the lock holder's bound address; the CLI reads it when `--addr` is not given | yes |
| `sdk/` | SDK | the CRDT storage — every space's change DAGs, materialized records, the tech space — plus the device-local store's `l_*` collections | CRDT content re-syncs from peers; the local store has no other copy |
| `files/` | SDK | file bytes; a durable file's bytes are a cache, a non-durable file's bytes are the only copy | partly — see [Status and durability](../files/status-and-durability.html) |
| `index/` | indexer | `index.db` — BM25 + vector index and link edges per space, cursors, schema version | yes — rebuilt from the synced data |
| `models/` | indexer | the embedding model GGUF (~639 MB), one per root, not per account | yes — re-downloaded |

> **Why it matters.** This directory *is* your database. There is no server-side copy to restore from — a device that syncs a space holds the whole space. It is not encrypted at rest: records, the local store and the search index are readable by anyone who can read the directory, and a plain `wallet.key` holds the mnemonic itself. Treat it the way you would treat a private key directory.

## What is safe to delete

| Delete | Effect |
|---|---|
| `index/` | safe; on the next start every space re-indexes from the beginning — full text first, then the embed drain, which can take a while on a large account. Also the fix for a schema-version or embedding-dimension mismatch at boot. |
| `models/` | safe; the model downloads again on next boot (a model already in a legacy `<account-dir>/index/models/` keeps being used from there) |
| `server.lock` | safe when no server is running; the lock lives in the kernel, not in the file, so a leftover file blocks nothing |
| `server.pid`, `server.addr` | safe any time; they only label the current holder |
| `files/` | **loses non-durable files** — bytes not yet backed up to the network have no other copy. Use the cache endpoints or per-file offload instead ([Cache](../files/cache.html)). |
| `sdk/` | loses every unsynced change, forces a full re-sync of shared spaces and **loses the local store** (`/v1/local` collections have no backup); a wiped storage also restarts the index generation, which the indexer detects and re-indexes |
| `wallet.key` | **loses this device's key**. The account survives if you kept the mnemonic — `any init --mnemonic` derives the same account id with a fresh device key. |
| `network.json` | only to repair the pin: when the account was first booted on the wrong network, or the pin is unreadable (`500 auth.network_pin_corrupt`). Start the server on the account's network afterwards; that boot pins it again. |

## Backup and second devices

Back up the **mnemonic**, not `wallet.key`. Copying `wallet.key` to another machine duplicates the device key, which collides peer ids and breaks realtime sync between the two. The supported second-device flow is:

```bash
any init --mnemonic-stdin < phrase.txt     # same account id, fresh device key
any run
```

The new device then cold-restores its spaces from the network (or from a nearby device over the [local network](networks.html)). Details in [Accounts](../auth/accounts.html) and [Devices](../auth/devices.html).

## Account selection

Which account a standalone `any run` boots is decided in this order: `auth.walletPath` / `--wallet`, then `account:` / `ANY_ACCOUNT` / `--account`, then the root `wallet.key`, then a sole `<root>/<id>/`. With several accounts and no selector the server starts unauthorized and `POST /v1/auth` picks one. `GET /v1/auth` lists the accounts a root holds. A managed server resolves nothing from disk: its host names the account over `POST /v1/auth` on every launch.

```bash
any auth status
```

Two servers may share a root as long as they serve different accounts on different ports; the lock is per account.

## Network pin

An account's data only makes sense on the any-sync network that wrote it. The first boot records that network's id in the account dir's `network.json`, and a server configured for another network refuses the account before touching its dir: `any run` exits naming both ids, `POST /v1/auth` answers `409 auth.network_mismatch` with `details.pinned` and `details.configured`. Keep one data root per network ([Networks](networks.html)).

## Storage upgrades are one-way

The storage engine marks a database with its header magic on open. A newer server still reads databases written by earlier builds, but once it has opened a data dir, a build pinned to an older storage engine refuses the file outright (`btree: database is corrupt` — the data is intact, the old code just does not recognize the header). Keep a copy of the data dir if you need the option to downgrade.

## Mobile

Embedded servers (the Android `.aar`, the iOS `.xcframework`) use the same layout under a directory the host app provides; the embedder model and the vector leg are never present there ([Builds and CI](builds-and-ci.html)).
