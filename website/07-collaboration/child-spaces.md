---
title: Child spaces
description: Spaces created under a parent space — who can do what, the refusal reasons, governing a child from the parent without reading it, rotation pending and the limits.
order: 45
---
# Child spaces

A space can be created under a **parent** space. The child is a full space: its own ACL, its own read key, its own members. Membership in the parent gives no access to it. The parent's owner and admins list a child and remove its members, and the parent's owner hands it to another member or deletes it. They read a child only when they are members of it.

A parent is a regular top-level space. The tech space, a [one-to-one](one-to-one.html) space, a [derived space](derived-spaces.html) and a child have no children: nesting has one level.

## Who can do what

| Action | Who |
|--------|-----|
| Create a child | a writer, an admin or the owner of the parent |
| Read or write a child's data | the members of the child |
| Add a member to a child | the child's admins and owner, an admin below the `admin` role; only members of the parent can be added |
| Give outsiders read-only access to a child | the child's owner, with a [guest key](invites.html) |
| List a parent's children | the parent's admins and owner |
| Remove a member of a child | the child's admins and owner; the parent's admins and owner, without the child's read key |
| Change a child's owner | the child's owner; the parent's owner |
| Delete a child | the child's owner; the parent's owner |
| Delete a parent | the parent's owner, once it has no children |

> **Why it matters.** A hosted backend nests workspaces by giving the top-level admin a view of everything below. Here the parent's owner holds no read key of a child they are not a member of: the coordinator accepts their removal, ownership and delete records on their role in the parent, and the child's content stays encrypted to its own members.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces` | with `parentSpaceId`: create a child |
| POST | `/v1/spaces/:spaceId/retry-push` | ask the coordinator now to accept a refused child |
| GET | `/v1/spaces/:spaceId/children` | the children of a parent, for its admins and owner |
| GET | `/v1/children/:childId/members` | the accounts of a child, read from the parent side |
| POST | `/v1/children/:childId/acl/remove` | remove members of a child as an admin or the owner of the parent |
| POST | `/v1/children/:childId/acl/ownership` | hand a child to one of its members: the owner of the parent, or the child's own owner |
| DELETE | `/v1/children/:childId` | delete a child for everyone: the owner of the parent, or the child's own owner |

In `/v1/spaces/:spaceId/children` the id is the parent's. The routes under `/v1/children/:childId` take the child's id alone: the coordinator reads the parent from the child's header.

## Creating a child

```bash
curl -s -X POST http://127.0.0.1:2046/v1/spaces \
  -H 'Content-Type: application/json' -d '{"name": "Design", "parentSpaceId": "'$PARENT'"}'
```

```json
{ "id": "bafyrei…", "parentSpaceId": "bafyrei…", "name": "Design",
  "status": "active", "ownRole": "owner", "spaceType": "any.space" }
```

```bash
any space create --name Design --parent $PARENT
```

The create is local, like any other: it needs no network, the caller owns the child, and nothing is written to the parent. `SpaceInfo.parentSpaceId` names the parent from then on and never changes. A member finds the children it belongs to in its own [space list](../realtime/space-list.html), by that field:

```bash
any space query --filter '{"parentSpaceId": "'$PARENT'"}' --sort -createdAt,-id --limit 100
```

The parent is a space of the account's own list, and the call refuses what that list already shows, naming the parent in `details.parentSpaceId`:

| Status | Code | When |
|--------|------|------|
| 400 | `request.invalid_field` | `parentSpaceId` is not a space id (`details.field`) |
| 404 | `space.not_found` | the account has no such space |
| 409 | `space.deleted` | the parent is deleted |
| 409 | `space.not_accepted` | a join or an invite to the parent is not accepted yet |
| 409 | `space.parent_invalid` | the tech space, a one-to-one space, a derived space or a child |

The caller's role in the parent is not checked by the create. Read it from the parent's `SpaceInfo.ownRole` and offer the action to a `writer`, an `admin` or the `owner`.

## A refused child

The network accepts a child when it is first pushed. The coordinator then checks that the parent exists and is a regular top-level space, that the creator is a writer or above in it at that moment, and that the parent's owner is below its child-spaces limit. A child it refuses stays a usable local space: `status` is `active`, reads and writes work, and no node holds it. `SpaceInfo.refusal` names the failed check.

| `refusal` | Cause | What ends it | What to offer |
|-----------|-------|--------------|---------------|
| `parent_permission_denied` | the account is below writer in the parent | a role of writer or above in the parent | ask an admin of the parent for the role, then retry |
| `child_limit_reached` | the parent's owner is at its child-spaces limit | a higher limit of that account, or fewer children | tell the parent's owner; retry once they have room |
| `parent_not_available` | the parent does not exist on the network or is deleted | nothing, for a deleted parent | delete the child, or keep it as a local space |
| `parent_invalid` | the parent is not a regular top-level space | nothing | delete the child, or keep it as a local space |

`refusal` is omitted when nothing is refused. It is synced: every device of the account reads it, also one that holds no copy of the child, and the raw rows of the [space-list stream](../realtime/space-list.html) deliver it being set and cleared.

Each device that holds the child asks again by itself: a minute after a refusal, then after two, four and so on, up to once an hour, and once after a restart. No device asks again by itself after `parent_invalid`. To ask at once:

```bash
curl -s -X POST http://127.0.0.1:2046/v1/spaces/$CHILD/retry-push
any space retry-push $CHILD
```

| Reply | Meaning |
|-------|---------|
| `204` | accepted: `refusal` is cleared and the space is pushed to the nodes in the background |
| `403 space.parent_permission_denied`, `409 space.child_limit_reached`, `409 space.parent_not_available`, `409 space.parent_invalid` | refused again; `refusal` names that check |
| `409 space.parent_not_available` with `refusal` unchanged | the parent is the account's own and no node holds it yet; the coordinator was not asked, and the server asks when one does |
| `503 network.unreachable` | the coordinator could not be reached; nothing changed |
| `409 space.not_child` | the account's list shows the space has no parent |
| `409 space.not_stored` | this device has no copy of the space; a device that holds it retries the push |
| `400 request.invalid_field` | the id is not a space id |

A refused child is shared with nobody: no node holds it. On it `POST …/invites`, `POST …/guest-key` and `POST …/acl/add` answer at once with the code of the recorded refusal, the one `retry-push` answers.

A refused child that is no longer wanted is removed with `DELETE /v1/spaces/:spaceId`.

## Members of a child

A child's own admins and owner manage its members with the ordinary [ACL](acl.html) and [invite](invites.html) routes. One rule is added: only a member of the parent becomes a member of a child. The coordinator checks every way in:

```json
{"error": {"code": "acl.not_parent_member", "message": "only a member of the parent space becomes a member of a child space", "details": {"spaceId": "bafyrei…"}}}
```

| Call | `409 acl.not_parent_member` when |
|------|----------------------------------|
| `POST …/acl/add` | one of the added accounts is not a member of the parent; nobody of the batch is added |
| `POST …/acl/accept` | the joiner is not a member of the parent any more: it left the parent after it asked |
| `POST /v1/spaces/join` | this account is not a member of the parent; no row is written |
| `POST …/acl/ownership` | the new owner is not a member of the parent, whoever hands the child over |

A guest key is the exception. `POST /v1/spaces/:spaceId/guest-key` works on a child as on any space, and its token gives read-only access to holders outside the parent.

## Listing the children of a parent

```bash
curl -s http://127.0.0.1:2046/v1/spaces/$PARENT/children
any space children $PARENT
```

```json
{ "children": [
    { "id": "bafyrei…", "status": "active" },
    { "id": "bafyrei…", "status": "deletion_pending" } ] }
```

The coordinator answers an admin or the owner of the parent with every child, the ones the account is no member of included. `status` is `active`, or `deletion_pending` when the child's deletion was requested and is not finished: a deleted child is listed that way until the coordinator has finished deleting it, and not at all after that. The list is read on every call and kept nowhere: no row is created for a listed child, and the call needs the network.

| Status | Code | When |
|--------|------|------|
| 403 | `space.parent_permission_denied` | the caller is not an admin or the owner of the space |
| 404 | `space.not_found` | the coordinator does not know the space; a space of the account's own that has not reached the network yet included |
| 409 | `space.deleted` | the coordinator holds the space as deleted or in deletion |
| 503 | `network.unreachable` | the coordinator could not be reached |
| 400 | `request.invalid_field` | the id is not a space id |

## Governing a child from the parent

These calls reach a child from outside it. The caller need not be a member of the child, and the child is not added to the space list, loaded or synced. Each needs the network.

```bash
curl -s http://127.0.0.1:2046/v1/children/$CHILD/members

curl -s -X POST http://127.0.0.1:2046/v1/children/$CHILD/acl/remove \
  -H 'Content-Type: application/json' -d '{"identities": ["A6ux…"]}'

curl -s -X POST http://127.0.0.1:2046/v1/children/$CHILD/acl/ownership \
  -H 'Content-Type: application/json' -d '{"newOwner": "BcdE…", "oldOwnerPerm": "writer"}'

curl -s -X DELETE http://127.0.0.1:2046/v1/children/$CHILD
```

```bash
any child members   <childId>
any child remove    <childId> <identity>...
any child ownership <childId> --new-owner <identity> --old-owner-perm writer
any child delete    <childId> --yes
```

| Route | The network accepts it from | Effect in the child |
|-------|-----------------------------|---------------------|
| `GET …/members` | any account: it is the read of the parent's admins and owner, and the nodes serve the ACL of a space to whoever asks | none; lists the accounts of the child with role and status, and says whether a key rotation is pending |
| `POST …/acl/remove` | an admin or the owner of the parent, and nobody else: no role in the child allows it | the members lose their permissions at once; the read key is not changed; the child's owner cannot be removed, and an admin of the parent does not remove an account that is an admin or the owner of the parent |
| `POST …/acl/ownership` | the owner of the parent; the child's own owner | `newOwner`, an active member of the child and a member of the parent, becomes its owner; the previous owner stays a member with `oldOwnerPerm` (`reader`, `writer` or `admin`) |
| `DELETE` | the owner of the parent; the child's own owner | the child is deleted on the network, for all its members |

The child's own owner is accepted on the last two whatever its role in the parent, as the owner of any space changes its owner and deletes it.

`GET …/members` is how the parent side learns who is in a child: the identities to remove, and the active members one of whom can become the owner.

```json
{ "members": [
    { "identity": "A6ux…", "permission": "owner",  "status": "active" },
    { "identity": "BcdE…", "permission": "writer", "status": "active" } ],
  "rotationPending": false }
```

The rows carry no `name` and no `iconCid`: an ACL names accounts by identity, and a profile is not readable without the child's keys. Resolve a name through the parent's own [roster](members-and-roles.html) or the [identities directory](../auth/identities.html).

| Status | Code | When |
|--------|------|------|
| 503 | `network.unreachable` | no node or coordinator could be reached; nothing changed |
| 403 | `space.parent_permission_denied` | the network does not accept the call from this account (the table above), or an admin of the parent removes an account that is an admin or the owner of the parent |
| 409 | `acl.not_parent_member` | the new owner is no member of the parent |
| 404 | `members.not_found` | an identity is no member of the child: one of the removed, or the new owner |
| 403 | `acl.forbidden` | the child's ACL forbids the record: the child's owner or the caller among the removed identities; a new owner who is the owner already or a guest identity |
| 409 | `space.not_child` | the account's list shows the space has no parent |
| 404 | `space.not_found` | the coordinator does not know the space |
| 409 | `space.deleted` | the coordinator holds the space as deleted or in deletion; `DELETE` of such a child is done and answers `204` |
| 400 | `request.invalid_field` | the id is not a space id, an identity is malformed or listed more than once, or `oldOwnerPerm` is `none`, `guest` or `owner` |
| 400 | `request.missing_field` | no `identities`, no `newOwner`, or no `oldOwnerPerm`: the role the previous owner keeps is always named |

`acl/remove` and `acl/ownership` fail when the child's ACL changes while they run, and are safe to repeat.

An admin or the owner of the child removes members with the child's own [ACL](acl.html) route instead: `POST /v1/spaces/:spaceId/acl/remove` changes the read key in the same record. The child's owner transfers the ownership with `POST /v1/spaces/:spaceId/acl/ownership` as well.

## Rotation pending

A removal from the parent side is written without the child's read key, so the removed members keep the current key: they are members no longer, and read what is written until the key changes. Both sides read the state as `rotationPending`: the child's members on its roster, the parent side on the read it removes from.

```bash
curl -s http://127.0.0.1:2046/v1/spaces/$CHILD/members      # a member of the child
curl -s http://127.0.0.1:2046/v1/children/$CHILD/members    # an admin or the owner of the parent
```

```json
{ "members": [ … ], "rotationPending": true }
```

It is `true` from the removal until the child's key is changed. The parent side reads it from the ACL the nodes serve, so read it again after a removal to see whether the child rotated.

No call ends it. A device writes the key change by itself when its account is an admin or the owner of the child and the device holds the child with its read key:

- when `any` starts, for every child the device holds;
- while it runs, when the removal reaches a child that is loaded: one loaded at start, or opened by a request since;
- with the network reachable: a change that could not be published is retried for as long as the rotation is pending;
- once: when two devices write it at the same time, the network takes one record.

A device holds a child once it has loaded it: the device that created it or accepted the invite or the join, and any other device of the account after the first request that opens the space there (`GET /v1/spaces` opens every active space). A device that has only the row of the child does not rotate.

`rotationPending` reads `false` on a device once the change is applied there; the same change gives the space row a new `push.encKeyId` on the [space-list stream](../realtime/space-list.html).

When no key-holder is left in a child, the rotation cannot happen: the child has no device of an admin or the owner that holds it, and `rotationPending` stays `true`. Deleting the child with `DELETE /v1/children/:childId` is the only lever.

> **Why it matters.** Removal by an ordinary admin rotates the key in the same record, because that admin holds it. The parent's admin does not, and the design keeps it that way: the removal takes effect on the ACL at once, and the cryptographic cut follows from a device that is allowed to read the space.

## Deleting

`DELETE /v1/spaces/:spaceId` and `DELETE /v1/children/:childId` are different calls.

| Call | On a child |
|------|------------|
| `DELETE /v1/spaces/:spaceId` | for the child's owner: deletes it on the network; for any other member, the parent's owner included: removes it from the account alone |
| `DELETE /v1/children/:childId` | for the parent's owner and for the child's own owner: deletes it on the network for everyone; the coordinator answers before the reply, and a copy in the account's own list then reads `deleted` |

A parent is deleted after its children, and a deleted child is one until the coordinator has finished deleting it. So delete the children, and delete the parent once the list is empty:

```bash
curl -s http://127.0.0.1:2046/v1/spaces/$PARENT/children          # list them
curl -s -X DELETE http://127.0.0.1:2046/v1/children/$CHILD         # each active one

curl -s http://127.0.0.1:2046/v1/spaces/$PARENT/children          # later: {"children": []}
curl -s -X DELETE http://127.0.0.1:2046/v1/spaces/$PARENT          # then the parent
```

A child in deletion stays in the list as `deletion_pending` and leaves it when its deletion is final. While the list still holds one, the delete of the parent is refused and changes nothing:

```json
{"error": {"code": "space.has_children", "message": "the space still has child spaces: delete them, then delete the space once its children list is empty", "details": {"spaceId": "bafyrei…"}}}
```

That answer is given before anything is written. A child in the account's own space list refuses the delete with no network; otherwise the signed delete is sent on the call path, the call waits for the coordinator at most five seconds, and the coordinator refuses it for a child the account is no member of or one still in deletion.

With no coordinator in reach the delete stays [offline-first](../database/spaces.html): the call answers `204` and the network delete is sent later. If the coordinator then refuses it for children, the deleted row reads `refusal: "has_children"`. The children routes work on such a parent. The server sends the waiting delete again by itself, on every pass of its reconciler and after each accepted `DELETE /v1/children/:childId`; it goes through once the last child is finally deleted, and `refusal` clears.

## Limits

The number of child spaces is a limit of the account that owns the parent. It is a limit of its own, apart from the shared-spaces limit, and it counts the children of all the spaces that account owns, whoever created them. The owner reads it with [`GET /v1/account/limits`](../auth/profile.html):

```json
{ "childSpaces": { "limit": 10, "fetchedAt": "2026-08-20T17:06:40Z" } }
```

A child created past it is refused with `refusal: "child_limit_reached"`. The member who created the child cannot read the owner's limit: the refusal is what they see.

## Without the network

| Call | Offline |
|------|---------|
| create a child | works; the child is pushed when the network is back |
| a refusal | nothing is asked; the recorded reason stays |
| a pending rotation | retried until published |
| `retry-push` | `503 network.unreachable`, nothing changes |
| `GET …/children`, `GET /v1/children/:childId/members` | `503 network.unreachable` |
| `acl/remove`, `acl/ownership` under `/v1/children` | `503 network.unreachable`; one ACL record, accepted by the network or not written |
| `DELETE /v1/children/:childId` | `503 network.unreachable`, nothing changes |
| `DELETE` of a parent | `409 space.has_children` from the account's own list alone; otherwise deferred, and a later refusal is `refusal: "has_children"` on the deleted row |

## Related

- [Spaces](../database/spaces.html) — creating, listing and deleting spaces, the `SpaceInfo` shape.
- [ACL](acl.html), [Invites](invites.html), [Members and roles](members-and-roles.html) — the routes a child's own members use.
- [Space list](../realtime/space-list.html) — `parentSpaceId` and `refusal` on the raw rows.
