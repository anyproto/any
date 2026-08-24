---
title: Data directory
description: The per-account layout under the data root, who owns each subdirectory, and what is safe to delete, copy or back up.
order: 30
---
# Data directory

`dataDir` (default `~/.any`) is a **root** that can hold several accounts. Each account keeps its wallet, pid lock, CRDT storage, file bytes and search index in its own subdirectory; the config file and the embedder model cache sit at the root and are shared.

## Layout

```
<root>/                          # dataDir, default ~/.any (mode 0700)
├── config.yaml                  # optional, if not passed via --config
├── models/                      # shared embedder model cache (all accounts)
├── wallet.key                   # LEGACY flat layout = the DEFAULT account;
├── server.pid                   #   its data stays directly at the root
├── sdk/  index/  files/         #   exactly as below
└── <accountId>/                 # every other account
    ├── wallet.key               # account + device keys (mode 0600)
    ├── server.pid               # per-account single-instance lock
    ├── sdk/                     # any-store databases — owned by the SDK
    ├── files/                   # file content, one CARv2 per rootCid — owned by the SDK
    └── index/                   # local search index (index.db) — owned by the indexer
```

A `wallet.key` directly at the root is the legacy flat layout: it acts as the default account with its data flat at the root, and nothing migrates it. New accounts always nest.

## What each part is

| Path | Owner | Contents | Derived? |
|---|---|---|---|
| `wallet.key` | server | the account's signing keys and this device's key | **no — the only copy of the device key** |
| `server.pid` | server | lock held by the running process; stale PIDs are reclaimed | yes |
| `sdk/` | SDK | the CRDT storage: every space's change DAGs, materialized records, the tech space | re-syncable from peers for shared content |
| `files/` | SDK | file bytes; a durable file's bytes are a cache, a non-durable file's bytes are the only copy | partly — see [Status and durability](../files/status-and-durability.html) |
| `index/` | indexer | `index.db` — BM25 + vector index per space, cursors, schema version | yes — but rebuilds only from the next change |
| `models/` | indexer | the embedding model GGUF (~639 MB), one per root, not per account | yes — re-downloaded |

> **Why it matters.** This directory *is* your database. There is no server-side copy to restore from — a device that syncs a space holds the whole space, and the account keys in `wallet.key` are what make the encrypted bytes readable. Treat it the way you would treat a private key directory.

## What is safe to delete

| Delete | Effect |
|---|---|
| `index/` | safe; the server rebuilds the index, but only content that changes afterwards is re-indexed ("index from the next change"). Also the fix for a schema-version or embedding-dimension mismatch at boot. |
| `models/` | safe; the model downloads again on next boot (a model already in a legacy `<account-dir>/index/models/` keeps being used from there) |
| `server.pid` | safe when no server is running; reclaimed automatically when stale |
| `files/` | **loses non-durable files** — bytes not yet backed up to the network have no other copy. Use the cache endpoints or per-file offload instead ([Cache](../files/cache.html)). |
| `sdk/` | loses every unsynced change and forces a full re-sync of shared spaces; a wiped storage also restarts the index generation, which the indexer detects and re-indexes |
| `wallet.key` | **loses this device's key**. The account survives if you kept the mnemonic — `any init --mnemonic` derives the same account id with a fresh device key. |

## Backup and second devices

Back up the **mnemonic**, not `wallet.key`. Copying `wallet.key` to another machine duplicates the device key, which collides peer ids and breaks realtime sync between the two. The supported second-device flow is:

```bash
any init --mnemonic-stdin < phrase.txt     # same account id, fresh device key
any run
```

The new device then cold-restores its spaces from the network (or from a nearby device over the [local network](networks.html)). Details in [Accounts](../auth/accounts.html) and [Devices](../auth/devices.html).

## Account selection

Which account `any run` boots is decided in this order: `auth.walletPath` / `--wallet`, then `account:` / `ANY_ACCOUNT` / `--account`, then the root `wallet.key`, then a sole `<root>/<id>/`. With several accounts and no selector the server starts unauthorized and `POST /v1/auth` picks one. `GET /v1/auth` lists the accounts a root holds.

```bash
any auth status
```

Two servers may share a root as long as they serve different accounts on different ports; the lock is per account.

## Storage upgrades are one-way

The storage engine marks a database with its header magic on open. A newer server still reads databases written by earlier builds, but once it has opened a data dir, a build pinned to an older storage engine refuses the file outright (`btree: database is corrupt` — the data is intact, the old code just does not recognize the header). Keep a copy of the data dir if you need the option to downgrade.

## Mobile

Embedded servers (the Android `.aar`, the iOS `.xcframework`) use the same layout under a directory the host app provides; the embedder model and the vector leg are never present there ([Builds and CI](builds-and-ci.html)).
