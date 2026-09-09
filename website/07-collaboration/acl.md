---
title: ACL
description: Owner and admin operations on a space's access-control list — accept or decline requests, change permissions, remove, add by identity, transfer ownership, stop sharing.
order: 30
---
# ACL

Every membership change is a signed record appended to the space's access-control list and verified by every peer. These endpoints write those records; the [members list](members-and-roles.html) is how you read the result.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces/:spaceId/acl/accept` | grant a pending join request a permission |
| POST | `/v1/spaces/:spaceId/acl/decline` | decline a join request |
| POST | `/v1/spaces/:spaceId/acl/permissions` | change permissions, batched |
| POST | `/v1/spaces/:spaceId/acl/remove` | remove accounts — rotates the read key |
| POST | `/v1/spaces/:spaceId/acl/add` | add accounts by identity |
| POST | `/v1/spaces/:spaceId/acl/ownership` | transfer ownership |
| POST | `/v1/spaces/:spaceId/acl/self-remove` | leave the space |
| POST | `/v1/spaces/:spaceId/acl/cancel-join` | withdraw your own join request |
| POST | `/v1/spaces/:spaceId/acl/stop-sharing` | drop everyone and rotate the key |

Every successful operation returns `204 No Content`. `self-remove`, `cancel-join` and `stop-sharing` take no body. Unknown permission strings return `400 request.schema`.

## Accepting a join request

Pending requests appear on `GET /v1/spaces/:spaceId/members/requests`; each row carries a `requestRecordId`.

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/accept \
  -d '{"requestRecordId": "bafy…", "permission": "writer"}'

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/decline \
  -d '{"identity": "A6ux…"}'
```

```bash
any acl accept  <spaceId>
any acl decline <spaceId>
```

On the joiner's side the members row flips from `joining` to `active` (or `declined`).

## Changing permissions

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/permissions \
  -d '{"changes": [
        {"identity": "A6ux…", "permission": "reader"},
        {"identity": "BcdE…", "permission": "admin"}]}'
```

```bash
any acl grant <spaceId> <identity> <permission>
```

Members see their own change on `SpaceInfo.ownRole` and on the [members stream](members-and-roles.html) as a `changed` frame.

## Removing members

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/remove \
  -d '{"identities": ["A6ux…", "BcdE…"]}'
```

```bash
any acl remove <spaceId> <identity>...
```

Removal rotates the space's read key. Content written after the rotation is encrypted under a key the removed accounts never receive.

> **Why it matters.** Removal is enforced by cryptography rather than by a server refusing reads: what a removed member keeps is the ciphertext they already had, and the sync nodes cannot hand them anything newer they could decrypt.

## Adding by identity

When you hold the account id, add directly instead of passing an invite token:

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/add \
  -d '{"accounts": [
        {"identity": "A6ux…", "permission": "writer", "metadata": {"name": "Alice"}}]}'
```

```bash
any acl add <spaceId> <identity>[,<identity>...] <permission>
```

The whole batch lands in one ACL record, and every added account is notified through the coordinator inbox (durable, retried). On their side the space shows up as a **synced** `invite_pending` row in the space list, carrying the sender-supplied name hint. They are already full ACL members; the pending state only governs whether their device materializes the space — nothing is downloaded until accepted.

```
GET  /v1/spaces?status=invite_pending             # discover
POST /v1/spaces/:spaceId/invite/accept            # 200 SpaceInfo, or 202 while loading continues in the background
POST /v1/spaces/:spaceId/invite/decline           # 204 — synced sticky, non-terminal
```

Accept flips the synced status to active on every device and loads the space; it is idempotent and overrides an earlier decline. Decline suppresses the invite account-wide but changes nothing on the ACL — the account remains a member until it self-removes.

| Status | Code | When |
|--------|------|------|
| 404 | `space.not_found` | unknown space id |
| 409 | `space.not_invite_pending` | the row is not awaiting approval |
| 400 | `request.invalid_field` | a one-to-one row — use the [one-to-one](one-to-one.html) endpoints |

## Ownership and leaving

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/ownership \
  -d '{"newOwner": "A6ux…", "oldOwnerPerm": "admin"}'

curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/self-remove
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/cancel-join
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/acl/stop-sharing
```

```bash
any acl ownership    <spaceId>
any acl self-remove  <spaceId>
any acl cancel-join  <spaceId>
any acl stop-sharing <spaceId>
```

| Operation | Effect |
|-----------|--------|
| `ownership` | hands the owner role to `newOwner`; the previous owner keeps `oldOwnerPerm` |
| `self-remove` | requests your own removal; your row shows `removing` until applied |
| `cancel-join` | withdraws your own pending join request: the owner's members row shows `canceled`, your space row reads `deleted`, and a fresh `POST /v1/spaces/join` re-requests |
| `stop-sharing` | drops every other member and rotates the read key — the space becomes private to the owner again |

> **Note.** A one-to-one space has an immutable two-writer ACL, so none of these operations apply to it. "Accepting" a direct chat is a device-local decision, described on [One-to-one](one-to-one.html).
