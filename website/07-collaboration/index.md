---
title: Collaboration
description: How a space gets shared — members and roles, invites, ACL operations, direct one-to-one spaces, derived spaces and bundles.
order: 0
---
# Collaboration

Share data by sharing a **space**. Its access-control list (ACL) identifies members and their permissions. Each member works with local data, and encrypted changes sync between devices.

## Choose a sharing flow

- **Invite someone to a team space:** create an invite, let them request to join, then approve the request. [Invites](invites.html)
- **Grant access to a known account:** add its identity through the ACL. The recipient accepts the incoming space before loading it. [Access control](acl.html)
- **Create a private space for two people:** use the deterministic one-to-one flow. [One-to-one spaces](one-to-one.html)
- **Let people read without membership approval:** use a public guest key, which grants read-only access to anyone holding it. [Guest access](invites.html)

Check membership and space status before showing shared data. A successful join request means approval is pending; it does not mean the space is ready to query.

## Membership flows

```
owner ── mints invite ──► token ──► joiner: POST /v1/spaces/join  (status: joining)
   │                                            │
   └── acl/accept {requestRecordId, permission} ─┘  ──► member (status: active)

owner ── acl/add {identity, permission} ──► added account sees invite_pending ──► invite/accept

me ── one-to-one {otherIdentity} ──► both derive the same space, both writers
```

| Concept | What it is | Page |
|---------|------------|------|
| Member | an identity on a space's ACL with a `permission` and a `status` | [Members and roles](members-and-roles.html) |
| Invite | a shareable token that lets someone request to join, or a public read-only guest key | [Invites](invites.html) |
| ACL operation | owner/admin actions: accept, decline, change permissions, remove, add by identity, transfer ownership, stop sharing | [ACL](acl.html) |
| One-to-one space | a two-identity space derived from both account keys — no invite, no owner | [One-to-one](one-to-one.html) |
| Derived space | a well-known per-account space every device resolves to the same id | [Derived spaces](derived-spaces.html) |
| Bundle | one root object per install registered in the space, so members converge on one chat, one setup; the server's usecase catalog installs the well-known apps | [Bundles](bundles.html) |

## Roles

| Permission | Can |
|------------|-----|
| `owner` | everything, including ACL operations and ownership transfer |
| `admin` | manage members |
| `writer` | read and write content |
| `reader` | read content |
| `guest` | read-only via a shared public guest key; writes return `403 space.read_only` |
| `none` | no access (also "not mirrored yet" on `SpaceInfo.ownRole`) |

Your own role in every space is mirrored onto the space list as `SpaceInfo.ownRole`, so a UI can gate role-dependent controls straight from `GET /v1/spaces` — no per-space fan-out. `GET /v1/spaces/:id/members/me` is the authoritative read when it matters.

Granting membership delivers the space’s read key to the member through the ACL. Removing a member rotates that key for future changes; it does not erase content they already received. Sync nodes store encrypted space content without holding its read key. [Encryption boundaries](../understanding/encryption.html)

## Shared objects with stable identities

Two themes recur across the section:

- **Derive instead of create** when several parties must land on the same object. A one-to-one space id is a function of both account keys; a derived space id is a function of the account keys and a fixed seed; a bundle root can be derived from its bundle id. Nothing to race, nothing to reconcile.
- **Approval is local when the cryptography cannot gate it.** A one-to-one ACL is immutable and a direct-add already puts you on the ACL, so "accept" governs whether *this device materializes the space*, surfaced as a space `status`, not an ACL write.

<div class="cards">
<a href="members-and-roles.html"><strong>Members and roles</strong><span>The authoritative per-space roster, permission and status strings, live member events.</span></a>
<a href="invites.html"><strong>Invites</strong><span>Mint, list and revoke invite tokens; request-to-join; public guest keys.</span></a>
<a href="acl.html"><strong>ACL</strong><span>Accept, decline, change permissions, remove, add by identity, transfer ownership, stop sharing.</span></a>
<a href="one-to-one.html"><strong>One-to-one</strong><span>Direct spaces derived from two identities, with the pending/declined state machine.</span></a>
<a href="derived-spaces.html"><strong>Derived spaces</strong><span>Well-known per-account spaces every device converges on; permanent by design.</span></a>
<a href="bundles.html"><strong>Bundles</strong><span>Adopt-or-install roots, derived roots, losers and resolve, the usecase catalog and the general chat.</span></a>
</div>
