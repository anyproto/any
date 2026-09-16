---
title: Identity & accounts
description: How an any server is bound to an account, how devices and contacts are identified, and where your profile lives.
order: 0
---
# Identity and accounts

Each Any server runs as one account. Applications on that device call the local API as that account; `/auth` selects which account the server serves. It is not a per-request login system for app users.

An account is identified by a key derived from a recovery phrase. Each device has its own network key. Sharing permissions apply to accounts inside a space.

## Keep account and device identity separate

| Identity | Created from | Used for |
|---|---|---|
| **Account ID** | A recovery phrase and derivation index. | Permissions, authorship, mentions and one-to-one spaces. The same account ID is used on all your devices. |
| **Peer ID** | A device key generated on that installation. | Identifying a particular device on the sync network. Restore the account on each device; do not copy its device key. |
| **Profile** | Name, description and icon set through `PUT /v1/account/metadata`. | Showing who someone is. Profile content is encrypted and resolves when a contact has its key. |

`GET /v1/account` returns the current account. `GET /v1/devices` lists its devices. The members list and [identities directory](identities.html) combine account identity with profile information.

The recovery phrase is needed to restore the account. A standalone wallet stores it locally, optionally protected by a passkey. See [Accounts](accounts.html) for creation, recovery and storage details.

## How a server becomes authorized

A **standalone** server (`any run`, the default) boots the account it can resolve from the data dir (a single wallet, or the one named by `--account`). A **managed** server (`any run --mode managed`, spawned by a desktop or mobile host) resolves nothing from disk: its host supplies the recovery phrase on every launch. Whenever no account is running the server is **unauthorized**: every `/v1` route except health, shutdown, the OpenAPI document and `/v1/auth` answers `401 auth.required` until a client posts to `/v1/auth` and the engine boots in place.

```bash
curl -s http://127.0.0.1:7001/v1/auth
# {"authorized": false, "mode": "standalone", "capabilities": {...}, "accounts": [...]}

curl -s -X POST http://127.0.0.1:7001/v1/auth -d '{}'
# {"accountId": "A8g1…", "created": true, "mnemonic": "w1 … w12"}
```

One process serves one account at a time. A standalone server changes account by restarting; a managed server's host switches or signs out in place, behind a control token. Details on [Accounts](accounts.html).

## What is local, what is synced

- The **account key** is held by your devices. A standalone server stores its wallet in `wallet.key`; a managed server holds the account key in memory for the session and caches its device key in `device.key`. Space read keys are distributed to authorized members through encrypted ACL records.
- The **device registry** is a synced dataset in the account's tech space, so every device sees every other device — and can elect which one runs an app.
- The **identities directory** is a device-local cache of every account you have encountered; the decryption keys behind it are synced but never exposed over HTTP.
- The **profile** is pushed to the network encrypted; a contact resolves your name only after receiving the key through a shared space's ACL or a one-to-one invite.

<div class="cards">
<a href="accounts.html"><strong>Accounts</strong><span>Mnemonic ⇒ account id, `any init`, standalone vs managed servers, the `/v1/auth` flow, per-account data dirs.</span></a>
<a href="devices.html"><strong>Devices</strong><span>The synced device registry and the deterministic active-app election.</span></a>
<a href="identities.html"><strong>Identities</strong><span>The account-global contact directory and why names resolve late.</span></a>
<a href="profile.html"><strong>Profile</strong><span>Reading and updating your own name, description and icon.</span></a>
</div>
