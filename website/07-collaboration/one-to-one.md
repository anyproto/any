---
title: One-to-one
description: Direct spaces between two identities, derived from both account keys — no invite, no owner — with a device-local accept/decline gate.
order: 40
---
# One-to-one

A one-to-one (direct) space is shared by exactly two identities and derived deterministically from both account keys. Both peers compute the same space id, the same immutable ACL with both as writers, and the same read key — there is no invite token and no owner handshake.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces/one-to-one` | open (or accept, or un-decline) a 1-1 by peer identity → `201 SpaceInfo` |
| POST | `/v1/spaces/one-to-one/register-incoming` | seed a pending row from an out-of-band signal → `204` |
| POST | `/v1/spaces/:spaceId/one-to-one/accept` | accept a pending row → `200 SpaceInfo` |
| POST | `/v1/spaces/:spaceId/one-to-one/decline` | decline, synced and sticky → `204` |

The peer's identity is the `id` from their `GET /v1/account`, exchanged out of band — a link, a QR code, a shared space's [members list](members-and-roles.html).

## Reach out

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/one-to-one \
  -d '{"otherIdentity": "A5k…"}'
# → 201 SpaceInfo  (status "active", spaceType "any.onetoone")
```

```bash
any one-to-one start <otherIdentity>      # aliases: any 1-1, any direct
```

The space is derived and activated immediately — implicit self-approval. The call is idempotent, and it overrides an earlier decline of the same peer. Classify direct chats in any list on `spaceType: "any.onetoone"`.

| Status | Code | When |
|--------|------|------|
| 400 | `request.missing_field` | `otherIdentity` empty |
| 400 | `request.invalid_field` | undecodable identity, or your own (`details.reason: "self"`) |

## The state machine

Because the ACL is immutable there is nothing to accept *cryptographically*. "Approve incoming" is a local gate on whether **this device** materializes and syncs the derived space, surfaced as space statuses:

```
            peer reaches out
                  │
   inbox notifier / register-incoming
                  ▼
      one_to_one_pending  (device-local row, nothing downloaded)
        │                 │
     accept            decline
        ▼                 ▼
      active      one_to_one_declined  (synced, sticky)
                          │
                  POST /v1/spaces/one-to-one {otherIdentity}
                          ▼
                        active
```

| Status | Meaning |
|--------|---------|
| `one_to_one_pending` | someone reached out; row is device-local, no storage materialized |
| `active` | materialized and syncing |
| `one_to_one_declined` | suppressed on every device of the account; never auto-resurfaces |

**Incoming.** The other side learns about a request in one of two ways: automatically, through the coordinator inbox notifier the server starts when a coordinator is configured; or out of band, by the app calling `register-incoming` with the peer's identity and an optional display hint. Either way a pending row appears.

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/one-to-one/register-incoming \
  -d '{"peerIdentity": "A5k…", "displayHint": {"name": "Alice", "iconCid": "…"}}'
# → 204, idempotent — no-op when a row already exists
```

```bash
any one-to-one register <peerIdentity> [--name ...] [--description ...] [--icon-cid CID]
```

**Accept / decline** by the pending row's space id — the server reads the peer identity off the row, so you never re-derive it:

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/one-to-one/accept    # → 200 SpaceInfo
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/one-to-one/decline   # → 204
```

```bash
any one-to-one accept  <spaceId>
any one-to-one decline <spaceId>
```

Accept is equivalent to re-running the open call with the peer's identity and is idempotent. Decline writes a synced sticky marker; only an explicit later `POST /v1/spaces/one-to-one` with that identity un-declines.

> **Why it matters.** Nothing is downloaded before you accept. A pending row is just an id and a display hint; reading it never pulls the space ciphertext, so an unsolicited request costs you no storage and leaks nothing about whether you looked.

## Discovery

There is no bespoke inbox endpoint. Pending requests are rows in the space list, hidden from the active-only default like deleted spaces:

```bash
curl -s "http://127.0.0.1:7001/v1/spaces?status=one_to_one_pending"        # snapshot
curl -s -N -X POST http://127.0.0.1:7001/v1/spaces/query/subscribe \
  -d '{"filter": {"localStatus": "oneToOnePending"}, "sort": ["-createdAt"], "limit": 50}'   # live
```

```bash
any one-to-one pending
```

On the raw rows the prompt is the device-local `localStatus: "oneToOnePending"`; a synced `remoteStatus` of `active` or a decline on the same row outranks it, which is the rule `?status=one_to_one_pending` applies for you — check both fields when you filter the stream.

The pending row carries the peer's display hint (`name` / `iconCid`) so a UI can render "Alice wants to chat" without syncing anything. `GET /v1/spaces/:id` on a pending row serves the row info only — it never materializes a non-active space.

## After activation

Everything else is a regular space: both members are writers, so create objects, send chat and subscribe as usual, and read the two participants through the normal members collection. Two caveats:

- Both participants report `ownRole: "writer"`, never `owner` — the ACL owner slot is a synthetic key nobody holds. Do not gate anything on owner for `any.onetoone` rows.
- A 1-1's chat is the [general chat](bundles.html#the-general-chat): both sides run the catalog setup and land on the same derived root on the first attempt — the only install shape that cannot fork when the two sides set up while apart.

> **Note.** Deletion is local-only. `DELETE /v1/spaces/:spaceId` offloads the space on this device and propagates the offload to the account's other devices, but a derived space is not node-owned and is never removed from the network; a later `POST /v1/spaces/one-to-one` re-derives and re-materializes it.
