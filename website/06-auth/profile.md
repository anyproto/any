---
title: Profile
description: Read your own account id and metadata, publish the encrypted name, description and icon other members see, and redeem an alpha access code.
order: 40
---
# Profile

Your profile is the name, description and icon attached to your account id. You write it once; it becomes visible in every space you belong to, without a per-space write, to every contact who holds your profile key.

## Reading your account

```bash
curl -s http://127.0.0.1:7001/v1/account
```

```json
{ "id": "A8g1…",
  "techSpaceId": "bafyrei…",
  "metadata": { "name": "Alice", "description": "writer, reader", "iconCid": "bafy…" } }
```

| Field | Meaning |
|-------|---------|
| `id` | the account identity — what other people put into ACL grants, mentions and one-to-one derivations |
| `techSpaceId` | the account's private tech space — the `:spaceId` for account-level [bundles](../collaboration/bundles.html); it never appears in `GET /v1/spaces` |
| `metadata` | the profile as stored locally; omitted when never set on this device |

`id` is the value you exchange out of band — via a link, a QR code, or a shared space's member list — so that someone can [add you by identity](../collaboration/acl.html) or [open a direct chat](../collaboration/one-to-one.html) with you.

## Updating the profile

```bash
curl -s -X PUT http://127.0.0.1:7001/v1/account/metadata \
  -d '{"name": "Alice", "description": "writer, reader, occasional debugger", "iconCid": "bafy…"}'
# → 204
```

```bash
any account set-metadata --name "Alice" [--description "..."] [--icon-cid CID]
```

At least one of `name` / `description` / `iconCid` must be set; an all-empty body returns `400 request.missing_field`. The SDK persists the bytes to the local tech space and pushes them to the identity repository, so the new profile reaches every space you are a member of. Read it back from a space's roster with `GET /v1/spaces/:id/members/me`.

The icon is a file CID — upload the image as a [file](../files/uploading.html) first and pass its root CID.

## Who can see it

The bytes pushed to the network are encrypted with an account-derived key that is shared with a contact only through an already-encrypted channel: a shared space's ACL metadata, or a one-to-one invite. A peer who has not received the key sees your **account id only**, with empty `name` / `description` / `iconCid`, until the key arrives and their background fetch resolves the profile.

> **Why it matters.** Publishing a display name on a hosted service means the service knows it. Here the relay stores ciphertext; the set of people who can read your name is exactly the set of people you have shared data with.

On the receiving side this shows up in two places, both of which must tolerate an empty name:

| Surface | What it shows |
|---------|---------------|
| `GET /v1/spaces/:id/members` | roster with roles; `name` / `iconCid` resolved from the same cache, with the join-time metadata as the baseline |
| `GET /v1/identities` | the account-global [directory](identities.html); rows surface id-only until resolved |

> **Note.** Profile writes are account-wide, not per space. There is no per-space display name; the join-time `metadata` passed to `POST /v1/spaces/join` seeds what members see before your profile key resolves, and is superseded once it does.

## Redeeming an access code

An alpha invite code raises the account's limits. The server signs `{purpose, ownerAnyId, code, ts}` with the account key and posts it to the invite service configured as `access.redeemUrl`, so the key never leaves the server and the client never talks to that service:

```bash
curl -s -X POST http://127.0.0.1:7001/v1/account/access-code -d '{"code": "K7QX-4MDP-…"}'
# → {"status": "accepted", "redemptionId": "…"}

any account redeem K7QX-4MDP-…
```

`status` is `accepted` (the limits grant is on its way) or `already_redeemed` (this account redeemed a code before; nothing is consumed). The code is compared upper-cased with whitespace removed.

| Status | Code | When |
|--------|------|------|
| 409 | `access.disabled` | no `access.redeemUrl` configured |
| 404 | `access.code_not_found` | unknown code |
| 409 | `access.code_unusable` | disabled, expired or exhausted code (`details.code`) |
| 400 | `access.request_rejected` | the service refused the request (`details.code`) |
| 401 | `access.signature_rejected` | the service could not verify the account's signature |
| 429 | `access.rate_limited` | the service is throttling this client |
| 502 | `access.unavailable` | the service is unreachable or answered unexpectedly |

## Related

- [Accounts](accounts.html) — where the account id comes from.
- [Identities](identities.html) — resolving other people's profiles.
- [Members and roles](../collaboration/members-and-roles.html) — the per-space roster that carries profiles alongside rights.
