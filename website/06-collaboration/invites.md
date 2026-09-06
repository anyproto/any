---
title: Invites
description: Mint, share, list and revoke invite tokens; the request-to-join flow; public read-only guest keys.
order: 20
---
# Invites

An invite is a shareable token. The owner mints it, passes the string out of band, and the joiner pastes it back to request membership. A guest key is the same mechanism for public read-only access — no request, no approval.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces/:spaceId/invites` | mint an invite; replaces any prior invite |
| GET | `/v1/spaces/:spaceId/invites` | list active invite records |
| GET | `/v1/spaces/:spaceId/invites/:recordId` | one record; `404 invite.not_found` |
| DELETE | `/v1/spaces/:spaceId/invites` | revoke all invites |
| DELETE | `/v1/spaces/:spaceId/invites/:recordId` | revoke one invite |
| POST | `/v1/spaces/join` | join with a token (invite or guest, auto-detected) |
| POST | `/v1/spaces/:spaceId/guest-key` | mint the public read-only guest key (owner only, idempotent) |
| DELETE | `/v1/spaces/:spaceId/guest-key` | revoke the guest key and rotate the read key |

## Mint and share

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/invites
```

```json
{ "spaceId": "bafyrei…", "inviteToken": "5ZHbdx…" }
```

`inviteToken` is a base58-packed `(spaceId, invitePrivKey)`. Share the string however you like — a link, a QR code, a message.

```bash
any invite create <spaceId>
```

## Join

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/join \
  -d '{"inviteToken": "5ZHbdx…", "metadata": {"name": "Bob", "iconCid": "…"}}'
# → 202 SpaceInfo   (status "joining")
```

```bash
any join <invite>
```

The join is a **request**: the SDK posts it, writes a `joining` row into the joiner's space list, and returns `202`. The joiner polls `GET /v1/spaces/:id/members/me` (or watches the [space list](../realtime/space-list.html)) for the status to flip to `active` once the owner accepts with [`POST …/acl/accept`](acl.html). `metadata` seeds the name other members see until the joiner's encrypted [profile](../auth/profile.html) resolves. A malformed or unrecognized token returns `400 invite.invalid`; a token for a space this account deleted returns `409 space.deleted` (a declined or withdrawn join is not deleted in that sense — the same call re-requests).

Until accepted, the joiner can withdraw with `POST /v1/spaces/:spaceId/acl/cancel-join`: the row then reads `deleted` and a later join with a valid token re-requests. Once the owner has accepted or declined, the call answers `409 space.join_not_pending` and the row settles on its own.

## List and revoke

```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/invites
```

```json
{ "invites": [
    { "recordId": "bafyrei…", "permission": "none", "inviteToken": "5ZHbdx…" } ] }
```

Pass `recordId` to `DELETE …/invites/:recordId` to revoke one invite, or `DELETE …/invites` to revoke all in one batch.

```bash
any invite list       <spaceId>
any invite get        <spaceId> <recordId>
any invite revoke     <spaceId> <recordId>
any invite revoke-all <spaceId>
```

> **Note.** `inviteToken` on a read is the same string the mint returned, recovered from the minting account's synced custody — the ACL record itself carries only the invite public key. It is present only on the devices of the account that minted the invite; every other member gets the row without it. Treat the field as optional and offer "regenerate to get a shareable code".

## Guest key: public read-only access

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/guest-key
# → { "spaceId": "…", "inviteToken": "…" }
```

One guest identity per space, idempotent — repeated calls return the same token; owner only. Anyone holding the token joins through the regular `POST /v1/spaces/join`; the guest kind is encoded in the token and auto-detected. No join request, no approval, no per-user ACL entry: the space loads read-only with `ownRole: "guest"`, and writes return `403 space.read_only`.

```bash
any invite guest-key        <spaceId>
any invite guest-key-revoke <spaceId>
```

`DELETE …/guest-key` removes the guest identity from the ACL and **rotates the read key**: every guest copy stops receiving new content and flips to `status: "guest_revoked"` (the local copy stays readable). A later create mints a fresh key; old tokens die permanently.

Guests drop a space with the regular `DELETE /v1/spaces/:spaceId`. For guest spaces the delete marker is non-terminal — a later join with a valid guest token re-adds the space and re-pulls state from the network.

> **Why it matters.** Revocation is not a flag a server checks on each read. Removing a member or a guest rotates the space's read key, so ciphertext written after the rotation is unreadable to anyone holding only the old key — including the relay that stores it.

## Adding by identity instead

When you already know someone's account id you can skip the token round-trip: [`POST …/acl/add`](acl.html) puts them on the ACL directly, and the space surfaces on their side as an `invite_pending` row they accept or decline.

```bash
any invite pending                  # direct-add invites awaiting my approval
any invite accept  <spaceId>
any invite decline <spaceId>
```
