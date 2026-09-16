---
title: Encryption
description: The end-to-end model — changes are signed and encrypted on the device, keys are derived from account keys and ACL state, and the nodes in between relay ciphertext only.
order: 20
---
# Encryption

Any encrypts space content on a member’s device before sending it through sync. Sync nodes store and relay that ciphertext without holding the keys needed to read it. Membership, identifiers and other routing metadata remain visible as listed below.

That protection has two boundaries to keep in mind:

- **On your device:** the local database and search index are not encrypted at rest by Any. The local API returns readable data to callers on the device.
- **Outside sync:** a model or connector receives the content your application sends to it. Search embeddings can run locally; an explicitly local setup does not send text to an embedding provider. The standalone server’s default, `auto`, tries its configured online endpoint before local fallback. The installation guide explicitly selects `local`. [Choose an embedder](../search/embedders.html).

## What this protects

| Path | Protection |
|---|---|
| Space content exchanged through sync | End-to-end encrypted for authorized members. |
| Local HTTP API and database files | Protected by the device’s access controls, not by sync encryption. |
| Calls to external providers | Governed by the provider configuration and the content sent in each call. |

## The key hierarchy

| Key | Where it comes from | What it does |
|-----|---------------------|--------------|
| **Account key** (Ed25519) | Derived from the BIP-39 mnemonic (SLIP-10 / SLIP-21). The mnemonic is shown once at `any init` / `POST /v1/auth`. | Your identity. Signs every change you write and every ACL record. |
| **Device key** | Generated per device: fresh on every `any init --mnemonic` restore, and minted once per account as `device.key` on a managed server. | Identifies this peer on the network. Never copy `wallet.key` between machines — two devices with one device key fight over one network identity. |
| **Space read key** (symmetric) | Created with the space; delivered to each member through the ACL, encrypted to their account key. Rotates when a member is removed. | Encrypts the payload of every change in the space. |
| **Profile key** (symmetric) | Account-derived. Shared with a contact only through a shared space's ACL metadata or a 1-1 invite. | Encrypts your name / description / icon in the identity repository. |
| **Push keys** | Derived from ACL state — the space push key from the ACL's first metadata key, the payload key from the current read key. Never synced; each device mirrors them onto its own space-list row as `SpaceInfo.push`. | Sign push topics; encrypt push payloads. |

The mnemonic is the root of all of it. Restoring an account on a second device with the same phrase yields the same account key and, through the ACLs, the same read keys.

## What a change looks like on the wire

```
TreeChange {
  treeHeadIds: [ …heads this change was built on… ]   ← cleartext (DAG structure)
  aclHeadId:   …                                      ← cleartext (which ACL state applies)
  readKeyId:   …                                      ← cleartext (which read key decrypts it)
  timestamp:   …                                      ← cleartext (author's clock)
  identity:    <account public key>                   ← cleartext
  changesData: encrypt(readKey, CRDT ops)             ← CIPHERTEXT
}
signature: sign(accountKey, change)                   ← cleartext, on the raw change
```

Nodes verify the signature and the DAG links; they never hold a read key, so `data` is opaque to them.

## What nodes see, and what they don't

| Nodes can see | Nodes cannot see |
|---------------|------------------|
| That a space exists, its id, its header `spaceType` | Its name, description, icon (these live in an encrypted in-space object) |
| The ACL: which account public keys are members, with what permission | Members' names or profiles (encrypted in the identity repo) |
| Object ids, change ids, DAG shape, change sizes and timing | Any field value, any record, any property |
| Encrypted file DAGs and their sizes | File names, mime types, contents (sealed under the read key) |
| Push topics (hashed chat ids) and ciphertext payloads | Message text, sender name |

Space name and description are not exempt: they are stored in a derived in-space object (`spaceIndexObjectId`) as ordinary encrypted CRDT data, and each device mirrors them into its own local space list.

## Consequences you will notice in the API

**Profiles resolve late.** A contact's `name` in `GET /v1/identities` or a members list is empty until their profile key arrives through a shared ACL or a 1-1 invite. Render a fallback and let the `updated` subscribe frame fill it in ([Identities](../auth/identities.html)).

**Pending spaces are not downloaded.** `GET /v1/spaces/:id` on a pending join or incoming 1-1 serves the tech-space row only; the server refuses to pull a space's ciphertext ahead of acceptance.

**Removing a member rotates the key.** any-sync switches the space to a new read key when a member is removed; changes written afterwards are unreadable to the removed identity. Old changes they already received stay readable to them — encryption is forward-looking.

**Files are space data.** Inline files (< 4096 B) ride the CRDT; larger files become an encrypted UnixFS DAG. The cleartext part of a file row is `rootCid`, `size`, `objectId`, and the broker's custody receipt; name, mime, and key are one sealed member-only blob ([Files](../files/index.html)).

**Push is sender-encrypted.** The sending device encrypts the notification payload; the push server fans out opaque ciphertext; the receiving phone decrypts with keys it cached from `SpaceInfo.push` while `any` was running ([Push](../notifications/push.html)).

Sync-node operators can see the metadata in the table above. Possessing a copy of the nodes’ stored data does not give them the space read keys.

## The local boundary

Encryption protects data *between* devices. On the device itself the server is plaintext behind `127.0.0.1` — the trust boundary is the loopback interface, and anyone with a shell on the machine can call the API ([Security model](../operations/security-model.html)). The data dir is not encrypted at rest either: records, the local store and the search index are readable by anyone who can read the directory ([Data directory](../operations/data-dir.html)). A standalone `wallet.key` holds the mnemonic and is plain JSON unless encrypted with a passkey supplied via `ANY_WALLET_PASSKEY` or `--passkey-stdin`; the server never prompts for it interactively. A server started with `--mode managed` keeps no account key on disk at all: its host supplies the phrase over `POST /v1/auth` on every launch ([Accounts](../auth/accounts.html)).

> **Note.** There is no key escrow and no recovery flow. Lose every usable copy of the account key and its recovery phrase, and the service cannot restore access for you. Back the phrase up when `any init` prints it.
