---
title: Identities
description: The account-global directory of every account you have encountered, and why a contact's name resolves only after their key arrives.
order: 30
---
# Identities

The identities directory is the account-global, device-local cache of **every account identity this account has encountered** — across spaces, one-to-one chats and inbox invites. It is where you turn an id you hold (a chat message's `creator`, an object row's `author` or `modifiedBy`, a mention, a 1-1 peer) into a display name and icon.

## Endpoints

Account-scoped — these routes sit outside the `/v1/spaces/:spaceId` group.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/identities` | every known identity |
| GET | `/v1/identities/:identity` | one identity; `404 identity.not_found` when never seen |
| GET | `/v1/identities/subscribe` | live directory changes over SSE |

```bash
curl -s http://127.0.0.1:7001/v1/identities
```

```json
{ "identities": [
    { "identity": "A5k…",
      "name": "Alice",
      "iconCid": "bafy…",
      "spaceIds": ["bafyspace1…", "bafyspace2…"] } ] }
```

| Field | Meaning |
|-------|---------|
| `identity` | the account id |
| `name`, `description`, `iconCid` | the last resolved profile — omitted until resolved |
| `spaceIds` | spaces where the identity is currently seen; pruned when you leave or offload a space |

```bash
any identities list            # alias: any contacts
any identities get A5k…
any identities subscribe
```

## Profiles are encrypted

A profile is pushed to the network encrypted with an account-derived key. That key reaches a contact only through a channel that is already encrypted — a shared space's ACL metadata, or a one-to-one invite. Until it arrives, the directory row surfaces **id-only**: `name`, `description` and `iconCid` are empty. Once the key is in, a background fetch resolves the profile and the row updates.

The same applies on a freshly restored device: every contact starts id-only and fills in as keys resolve in the background.

> **Why it matters.** The sync nodes store profile bytes they cannot read. A relay learning "who talks to whom" never learns who those people are; only accounts you have actually shared a space or a direct chat with can decrypt your name.

The synced decryption key behind each row is never exposed over HTTP. The tech-space dataset that stores it is deliberately excluded from the generic [space-list query](../realtime/space-list.html) — `GET /v1/identities` is the one read path.

## Tolerate empty names

Render a fallback (a truncated id, a generated avatar) and let the subscribe stream fill the name in. Never block a UI on a resolved name, and do not look for a "fetch raw profile bytes" path — there is none; the directory *is* the resolution surface.

## Live updates

`GET /v1/identities/subscribe` uses the same envelope as the members and sync-status streams:

```
event: ready
data: {}

event: identities
data: { "added":   [ { …IdentityInfo… } ],
        "updated": [ { …IdentityInfo… } ],
        "removed": [ "A5k…" ] }

event: lagged
data: { "total": 3 }

event: closed
data: { "reason": "server_shutdown" }
```

One `identities` frame per change batch; any of `added` / `updated` / `removed` may be empty or absent. A contact whose key arrives after the first sighting appears first in `added` with an empty name, then again in `updated` once resolved. On `lagged`, re-`GET` the list to resynchronise.

## No rights here

The directory carries **no permission field** by design. Roles are per-space and live on the [members list](../collaboration/members-and-roles.html):

```
GET /v1/spaces/:id/members        # roster + roles for ONE space
GET /v1/identities                # global id → profile, across all spaces
```

To show "Alice is an admin of space X", read space X's members. To show her role in every shared space, iterate her `spaceIds` and read each members list — there is no cross-space role rollup.

| Question | Surface |
|----------|---------|
| Who is in this space, with what rights? | `GET /v1/spaces/:id/members` |
| What does this account id look like? | `GET /v1/identities/:identity` |
| Keep an avatar cache fresh | `GET /v1/identities/subscribe` |
