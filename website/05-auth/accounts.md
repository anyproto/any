---
title: Accounts
description: Create or restore an account from a mnemonic, authorize a running server over HTTP, and lay out several accounts in one data dir.
order: 10
---
# Accounts

An account is derived from a twelve-word BIP-39 mnemonic. The same phrase always yields the same account id, while each machine that restores it mints its own device key — that is how a second device joins without cloning the first.

## Mnemonic ⇒ account id

```
mnemonic + index  ──►  account key  ──►  account id
```

The derivation `index` defaults to **1**, the `any` account index. Index 0 is reserved for accounts derived by anytype, so one phrase can serve both products with two distinct accounts. Restoring an anytype-derived account needs an explicit index 0.

> **Note.** The mnemonic is the only recovery path. `any` never stores it; it is printed once at generation and never again. Back it up before writing data you care about.

## First run: `any init`

```bash
any init                                  # generate a fresh account, print the mnemonic once
any init --mnemonic-stdin < phrase.txt    # restore an existing account (fresh device key)
any init --mnemonic "w1 … w12" --index 0  # restore an anytype-derived account
any init --new                            # force an additional fresh account
```

| Flag | Effect |
|------|--------|
| *(none)* | generate a fresh account under `<root>/<accountId>/`; a no-op that lists accounts when any already exist |
| `--mnemonic`, `--mnemonic-stdin` | authorize an existing account; prefer stdin so the phrase stays out of shell history |
| `--index N` | derivation index for `--mnemonic`; default 1 |
| `--new` | create an additional account even when one exists |

Restoring writes a new `wallet.key` with a **freshly generated device key**. Never copy `wallet.key` between machines: that clones the device key, the two peers then present one network identity, and realtime sync between them breaks.

## Authorizing over HTTP: `/v1/auth`

`any run` does not create wallets. When no account resolves from the data dir — a fresh root, or several accounts and no `--account` / `ANY_ACCOUNT` selector — the server starts **unauthorized**. Every `/v1` route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json` and `/v1/auth` returns `401 auth.required`.

```bash
curl -s http://127.0.0.1:7001/v1/auth
```

```json
{ "authorized": false,
  "accounts": [ { "id": "A8tR…", "default": true }, { "id": "A8g1…" } ] }
```

`POST /v1/auth` boots the engine in place — no restart. `mnemonic` and `accountId` are mutually exclusive:

```bash
# generate
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{}'
# restore at index 1 (device key freshly generated)
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{"mnemonic": "w1 … w12"}'
# restore an anytype-derived account
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{"mnemonic": "w1 … w12", "index": 0}'
# select a wallet already on disk
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{"accountId": "A8g1…"}'
```

```json
{ "accountId": "A8g1…",
  "created": true,
  "mnemonic": "w1 … w12" }
```

`created` reports that a new wallet file was written. `mnemonic` is present **only** when the server generated it. `index` is valid only together with `mnemonic`; a selected account's index is baked into its wallet.

CLI equivalents:

```bash
any auth login                            # generate
any auth login --mnemonic-stdin           # restore
any auth login --account A8g1…            # select
any auth status                           # GET /v1/auth
```

If the engine fails to boot after a fresh wallet was created in this call, the half-created account dir is removed so a retry starts clean instead of auto-selecting an un-backed account.

| Status | Code | When |
|--------|------|------|
| 400 | `auth.bad_mnemonic` | phrase fails BIP-39 validation |
| 400 | `request.invalid_field` | `mnemonic` + `accountId` together, or `index` without `mnemonic` |
| 404 | `auth.account_not_found` | `accountId` has no local wallet |
| 409 | `auth.account_in_use` | another process holds that account's instance lock |
| 409 | `auth.mnemonic_mismatch` | the wallet on disk disagrees with the supplied phrase / index |
| 409 | `auth.already_authorized` | the server already booted an account |
| 400 | `auth.passkey_required` | encrypted wallet; the passkey comes from the configured env var, never the body |

## One server, one account

A process serves exactly one account for its lifetime. To switch, stop it and start with `--account <id>` (or let `POST /v1/auth` pick on an unauthorized server). Two accounts at once means two `any run` processes on different ports; they may share one data-dir root because every account dir carries its own instance lock.

## Data dir layout

The data dir is a root that can hold several accounts:

```
<root>/                     # default ~/.any
├── config.yaml
├── models/                 # embedder model cache, shared by all accounts
├── wallet.key              # legacy flat layout = the DEFAULT account
├── server.lock server.pid  #   (its sdk/ and index/ sit directly at the root)
└── <accountId>/
    ├── wallet.key          # mode 0600
    ├── server.lock         # per-account single-instance lock (OS file lock)
    ├── server.pid          # holder's pid, for error messages only
    ├── sdk/                # any-store databases, owned by the SDK
    ├── files/              # file content, owned by the SDK
    └── index/              # local search index
```

Account selection at boot: `auth.walletPath` / `--wallet` → that wallet; `account:` / `ANY_ACCOUNT` / `--account` → `<root>/<id>/`; otherwise a root `wallet.key` is the default, else a sole `<root>/<id>/` dir, else no account. `GET /v1/health` reports the booted account in `account` (`""` while unauthorized).

> **Why it matters.** Because the account is a key and the data dir is just files, there is nothing to migrate between hosting providers and nothing a provider can lock: restoring is "type twelve words on a new machine", and every space the account belongs to syncs back down from the network.

See also [Devices](devices.html) for what the fresh device key registers, and the [operations data-dir page](../operations/data-dir.html) for what is safe to delete.
