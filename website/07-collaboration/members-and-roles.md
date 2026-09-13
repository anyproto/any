---
title: Members and roles
description: The authoritative per-space roster — who is in a space, with what permission and status — and the live member stream.
order: 10
---
# Members and roles

A space's members list is the one call that answers "who is here, what may they do, and what do they look like". Every row carries the member's permission, their status on the ACL, and their profile.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/spaces/:spaceId/members` | full roster |
| GET | `/v1/spaces/:spaceId/members/me` | the caller's own row |
| GET | `/v1/spaces/:spaceId/members/requests` | pending join requests |
| GET | `/v1/spaces/:spaceId/members/subscribe` | live membership changes over SSE |
| GET | `/v1/spaces/:spaceId/members/:identity` | one member by account id |

```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/members
```

```json
{ "members": [
    { "identity": "A6ux…",
      "permission": "owner",
      "status": "active",
      "name": "Alice",
      "iconCid": "…" } ] }
```

| Field | Meaning |
|-------|---------|
| `identity` | account id |
| `permission` | the role — see the table below |
| `status` | the member's ACL state |
| `name`, `description`, `iconCid` | profile, resolved from the same cache that feeds the [identities directory](../auth/identities.html); join-time metadata is the baseline |
| `requestRecordId` | present only on a member whose join request is pending — pass it to `POST …/acl/accept` |

`GET …/members/requests` lists the pending join requests in their own shape — the `recordId` is what `POST …/acl/accept` takes as `requestRecordId`:

```json
{ "requests": [
    { "recordId": "bafyrei…", "identity": "B7qz…", "name": "Bob" } ] }
```

```bash
any members list     <spaceId>
any members me       <spaceId>
any members get      <spaceId> <identity>
any members requests <spaceId>
any members subscribe <spaceId>
```

## Permission strings

| Wire string | Meaning |
|-------------|---------|
| `none` | no access |
| `reader` | read only |
| `guest` | read only through the space's shared public guest key |
| `writer` | read and write content |
| `admin` | writer plus member management |
| `owner` | everything, including ownership transfer and stop-sharing |

An unknown permission string in an ACL body returns `400 request.schema`.

## Status strings

| Wire string | Meaning |
|-------------|---------|
| `unknown` | not resolvable from ACL state |
| `joining` | join request posted, awaiting the owner's accept |
| `active` | a current member |
| `removed` | removed by an owner/admin |
| `declined` | join request declined |
| `removing` | self-removal requested |
| `canceled` | join request canceled by the requester |

## Your own role from the space list

`SpaceInfo.ownRole` on `GET /v1/spaces[/:id]` mirrors the caller's own permission from ACL state — one pass when the space loads, then one per applied ACL record, so a demotion by another peer lands as a row update. The raw rows on `POST /v1/spaces/query/subscribe` stream role changes live, which lets a UI gate role-dependent controls straight from the [space list](../realtime/space-list.html) without a per-space `members/me` fan-out.

> **Note.** `ownRole: "none"` doubles as "not mirrored yet" — a space this device has not loaded, a pending join, a tombstoned row. Treat it as unknown / no access, and read `GET …/members/me` when the answer matters. On a one-to-one space both participants report `writer`, never `owner`, because the ACL owner slot is a synthetic key nobody holds.

## Live membership changes

`GET /v1/spaces/:spaceId/members/subscribe` streams new members, permission or status flips, and removals:

```
event: ready
data: {}

event: member
data: { "kind": "added" | "changed" | "removed",
        "member": { …Member… },
        "previous": { …Member… } | null }

event: lagged
data: { "total": 3 }

event: closed
data: { "reason": "server_shutdown" }
```

`member` always carries the full post-event row (same shape as `GET …/members/:identity`); `previous` is null on `added`. The forwarder is a small buffered channel — on `lagged`, re-`GET` the roster. The envelope and `closed` reasons are shared with the other [callback streams](../realtime/index.html).

## Roster vs directory

Two surfaces resolve "who is this person" and answer different questions:

| Need | Surface |
|------|---------|
| A members panel with avatars **and** roles for one space | `GET /v1/spaces/:id/members` — authoritative for rights |
| A name/icon for any account id you hold (a chat `creator`, a mention) | `GET /v1/identities[/:identity]` — account-global, carries no rights |

There is no cross-space role rollup: to show someone's role in each shared space, read each space's roster.

> **Why it matters.** Rights are not a column a server consults; they are what the ACL cryptographically grants. The roster you read is a projection of the same signed records every peer verifies, so no member can see a different truth than another.

Profiles are encrypted and resolve only after a contact's key arrives — rows may carry an empty `name`. See [Profile](../auth/profile.html) for the receiving-side rules.
