---
title: Auth
description: How an any server is bound to an account, how devices and contacts are identified, and where your profile lives.
order: 0
---
# Auth

There is no login form, no password database and no session cookie. An **account** is a keypair derived from a mnemonic, a **device** is a keypair minted on each machine, and every request to the local server runs as the one account the server booted. This section covers how those keys come to exist, how they are named on the network, and how other people see you.

## The three identities

```
mnemonic (12 words)
   └── account key  ──►  account id        "who"   — same on every device
         └── device key ──►  peer id       "where" — unique per install
                                 └── profile  (name / description / icon)
```

| Thing | Derived from | Visible as | Scope |
|-------|--------------|------------|-------|
| Account id | the mnemonic + a derivation index | `GET /v1/account` → `id`, `SpaceInfo.author`, chat `creator` | one per person, shared by all their devices |
| Peer id | a device key generated at `any init` / `POST /v1/auth` | `GET /v1/devices` → `self`, row ids in the device registry | one per install, never copied |
| Profile | `PUT /v1/account/metadata` | members list, identities directory | encrypted; readable only by contacts holding your key |

The account id is what other people address — it goes into an ACL grant, a mention, a one-to-one space derivation. The peer id is what the sync network routes to. Keeping them separate is what lets you add a second laptop with the same twelve words and have both sync as the same person without fighting over one network identity.

> **Why it matters.** With a hosted backend, identity is a row in someone else's user table. Here the account *is* the key: nothing on a server can impersonate you, revoke you, or read the profile you publish unless you handed it the decryption key through an encrypted channel. The cost is that the mnemonic is the only recovery path — there is no "forgot password".

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

- The **keys** never leave the machine: a standalone server keeps them in `wallet.key`; a managed server holds the account key in memory for the session and caches only its device key (`device.key`).
- The **device registry** is a synced dataset in the account's tech space, so every device sees every other device — and can elect which one runs an app.
- The **identities directory** is a device-local cache of every account you have encountered; the decryption keys behind it are synced but never exposed over HTTP.
- The **profile** is pushed to the network encrypted; a contact resolves your name only after receiving the key through a shared space's ACL or a one-to-one invite.

<div class="cards">
<a href="accounts.html"><strong>Accounts</strong><span>Mnemonic ⇒ account id, `any init`, standalone vs managed servers, the `/v1/auth` flow, per-account data dirs.</span></a>
<a href="devices.html"><strong>Devices</strong><span>The synced device registry and the deterministic active-app election.</span></a>
<a href="identities.html"><strong>Identities</strong><span>The account-global contact directory and why names resolve late.</span></a>
<a href="profile.html"><strong>Profile</strong><span>Reading and updating your own name, description and icon.</span></a>
</div>
