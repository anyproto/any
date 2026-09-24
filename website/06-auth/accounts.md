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

## Who owns the server: `--mode`

`any run --mode standalone` (the default) is **the user's** server: keys live in `wallet.key`, the account resolves from the data dir, and logout or HTTP shutdown are refused — stop it with `any stop` or Ctrl-C. `any run --mode managed` is **a host's** server (the desktop shell, a mobile app): it never resolves an account from disk, the host supplies the recovery phrase over `POST /v1/auth` on every launch and the account key never touches disk, and sign-out, account switching and `POST /v1/shutdown` are accepted behind a **control token** only the host holds. A CLI-spawned managed server prints the token as the second stdout line (`CONTROL_TOKEN <hex>`, after `LISTENING <addr>`); an in-process host passes its own. The token travels as the `X-Any-Control-Token` header; without it those calls answer `403 control.forbidden`.

The mode is fixed at launch and cannot be changed over HTTP. Clients never branch on it directly: `GET /v1/auth` reports what the server accepts as `capabilities` bits.

A managed login carries the account key only. The device key — what the peerId derives from — is minted once per account at `<root>/<accountId>/device.key` and reused on every later login, so logging in on every app launch does not register a new device each time.

## Authorizing over HTTP: `/v1/auth`

`any run` does not create wallets. When no account resolves from the data dir — a fresh root, several accounts and no `--account` / `ANY_ACCOUNT` selector, or any managed server — the server starts **unauthorized**. Every `/v1` route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json` and `/v1/auth` returns `401 auth.required`.

```bash
curl -s http://127.0.0.1:7001/v1/auth
```

```json
{ "authorized": false,
  "mode": "standalone",
  "capabilities": { "deauthorize": false, "switchAccount": false, "shutdown": false },
  "accounts": [ { "id": "A8tR…", "default": true }, { "id": "A8g1…" } ] }
```

A managed server reports every capability `true` and an empty `accounts` list — it holds no keys, so the client owns the account list.

`POST /v1/auth` boots the engine in place — no restart. `mnemonic` and `accountId` are mutually exclusive:

```bash
# generate
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{}'
# restore at index 1 (device key freshly generated)
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{"mnemonic": "w1 … w12"}'
# restore an anytype-derived account
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{"mnemonic": "w1 … w12", "index": 0}'
# select a wallet already on disk (standalone only)
curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{"accountId": "A8g1…"}'
# managed: switch to another account in place
curl -s -X POST http://127.0.0.1:7001/v1/auth -H "X-Any-Control-Token: $TOKEN" \
     -d '{"mnemonic": "w1 … w12", "replace": true}'
```

```json
{ "accountId": "A8g1…",
  "created": true,
  "mnemonic": "w1 … w12" }
```

`created` reports that the account had no local state before this call. `mnemonic` is present **only** when the server generated it. `index` is valid only together with `mnemonic`; a selected account's index is baked into its wallet.

While an account is running, the reply follows the account the request derives to — the server never reads a wallet or boots anything to decide:

| | `{}` | same account | different account |
|---|---|---|---|
| standalone | `409 auth.already_authorized` | `200 {"alreadyAuthorized": true}` | `403 auth.not_managed` |
| managed | `409 auth.already_authorized` | `200 {"alreadyAuthorized": true}` | `409 auth.account_mismatch`, or a switch with `"replace": true` |

The same account is always a no-op — a retry never drops your streams, and `alreadyAuthorized` on a mnemonic request confirms the phrase you hold belongs to the running account (an `accountId` request confirms only the id). A refusal never echoes the account a rejected phrase derives to. A switch ends every open stream with `closed{"reason": "deauthorized"}`; if the new account fails to boot after the teardown the server is left unauthorized — re-read `GET /v1/auth`.

`DELETE /v1/auth` (managed, control token) signs out in place: the engine is torn down, streams end with `deauthorized`, and the server stays up unauthorized. Delete the stored phrase too, or the account is not actually signed out on that device.

CLI equivalents:

```bash
any auth login                            # generate
any auth login --mnemonic-stdin           # restore
any auth login --account A8g1…            # select (standalone)
ANY_CONTROL_TOKEN=… any auth login --mnemonic-stdin --replace   # switch (managed)
ANY_CONTROL_TOKEN=… any auth logout       # DELETE /v1/auth (managed)
any auth status                           # GET /v1/auth
```

Pass the control token through `ANY_CONTROL_TOKEN`; `--control-token` exists too, but a flag value is visible in the process list.

If the engine fails to boot after this call created the account dir, the half-created dir is removed so a retry starts clean instead of auto-selecting an un-backed account.

| Status | Code | When |
|--------|------|------|
| 400 | `auth.bad_mnemonic` | phrase fails BIP-39 validation |
| 400 | `request.invalid_field` | `mnemonic` + `accountId` together, `index` without `mnemonic`, `replace` without a credential, or `accountId` on a managed server |
| 403 | `control.forbidden` | managed server, control token missing or wrong |
| 403 | `auth.not_managed` | standalone server: a different account, or `DELETE` |
| 404 | `auth.account_not_found` | `accountId` has no local wallet |
| 409 | `auth.account_in_use` | another process holds that account's instance lock |
| 409 | `auth.account_mismatch` | managed server runs another account; pass `replace: true` to switch |
| 409 | `auth.mnemonic_mismatch` | the wallet on disk disagrees with the supplied phrase / index |
| 409 | `auth.already_authorized` | `{}` while an account runs — a fresh account is never minted in place |
| 400 | `auth.passkey_required` | encrypted wallet with a missing or wrong passkey; the passkey comes from the configured env var, never the body |
| 409 | `sdk.crdt_version_newer` | the account's data was written by a newer release; upgrade before opening it |
| 500 | `auth.device_key_corrupt` | managed: the cached `device.key` is unreadable; remove it to mint a new device identity (this install then registers as a new peer) |

## One server, one account

A process serves one account at a time. A managed server's host switches in place (`replace`) or signs out (`DELETE /v1/auth`); a standalone server refuses both — stop it and start with `--account <id>`, or let `POST /v1/auth` pick on an unauthorized server. Two accounts at once means two `any run` processes on different ports; they may share one data-dir root because every account dir carries its own instance lock.

## Data dir layout

The data dir is a root that can hold several accounts:

```
<root>/                     # default ~/.any
├── config.yaml
├── models/                 # embedder model cache, shared by all accounts
├── listen.port             # last port a port-0 listen address bound
├── wallet.key              # legacy flat layout = the DEFAULT account
├── server.lock server.pid  #   (its sdk/ and index/ sit directly at the root)
└── <accountId>/
    ├── wallet.key          # standalone: the wallet, mode 0600
    ├── device.key          # managed: the cached device key, mode 0600 — never copy it
    ├── server.lock         # per-account single-instance lock (OS file lock)
    ├── server.pid          # holder's pid, for error messages only
    ├── server.addr         # holder's bound address, read by the CLI
    ├── sdk/                # any-store databases, owned by the SDK
    ├── files/              # file content, owned by the SDK
    └── index/              # local search index
```

Account selection at boot (standalone): `auth.walletPath` / `--wallet` → that wallet; `account:` / `ANY_ACCOUNT` / `--account` → `<root>/<id>/`; otherwise a root `wallet.key` is the default, else a sole `<root>/<id>/` dir, else no account. A managed server selects nothing — the host states the account on every launch. Data lives under `<root>/<accountId>/` in both modes, so the same account reached under either custody finds its existing data; the legacy flat-root account is standalone-only. `GET /v1/health` reports the booted account in `account` (`""` while unauthorized).

> **Why it matters.** Because the account is a key and the data dir is just files, there is nothing to migrate between hosting providers and nothing a provider can lock: restoring is "type twelve words on a new machine", and every space the account belongs to syncs back down from the network.

See also [Devices](devices.html) for what the fresh device key registers, and the [operations data-dir page](../operations/data-dir.html) for what is safe to delete.
