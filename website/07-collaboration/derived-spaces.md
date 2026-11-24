---
title: Derived spaces
description: Per-account spaces whose id is computed from the account keys and a fixed seed. The server opens one an account already has and creates none; a derived space is permanent and belongs to its account alone.
order: 50
---
# Derived spaces

A derived space is a per-account space whose id is computed from the account keys and a fixed seed. The same account resolves the same space id on every device, with no network.

The server creates no derived space. An account can hold one from an installation that created it: the account keeps it, and the server opens it. The account's agent space is a regular space whose row carries `anySpaceType: "bao"` ([Spaces](../database/spaces.html#the-accounts-agent-space)). A derived `bao` space is the agent space of an earlier installation: its row carries no `anySpaceType`, and the registry below is the one place that names it.

## The registry

Raw derivation with a caller-supplied seed is deliberately not exposed: a free seed would mint a *permanent* space and invite silent seed collisions between consumers. The vocabulary is a small registry compiled into the server, with seeds following the `any/space/<name>/v1` convention.

| Name | Purpose |
|------|---------|
| `bao` | the agent space of an earlier installation |

## Endpoints

```
GET  /v1/spaces/derived        → 200 { "spaces": [ { name, spaceId, created, status? } ] }
POST /v1/spaces/derived/:name  → 200 SpaceInfo
```

```bash
curl -s http://127.0.0.1:2046/v1/spaces/derived
```

```json
{ "spaces": [ { "name": "bao", "spaceId": "bafyrei…", "created": false } ] }
```

| Field | Meaning |
|-------|---------|
| `name` | registry name |
| `spaceId` | the derived id — computed once at engine boot, never created by this call |
| `created` | this device holds a usable row for the space: written here, or received from another of the account's devices (rows sync). `false` means no device had created the space as of this device's last sync of the space list |
| `status` | the row's status (the `SpaceInfo.status` strings) when a row exists; a tombstoned row reports `created: false` |

```bash
curl -s -X POST http://127.0.0.1:2046/v1/spaces/derived/bao
# → 200 SpaceInfo
```

```bash
any space derived               # list
any space derived open bao      # open
```

**GET resolves, never creates.** **POST opens a derived space this device holds a row for** and returns the full single-space `SpaceInfo`. It creates no space; on a device that downloaded the space its [bundles](bundles.html) answer once its registry has arrived. Without a row on this device the answer is `404 space.derived_not_created`, and none is written.

| Status | Code | When |
|--------|------|------|
| 404 | `space.derived_unknown` | name not in the registry |
| 404 | `space.derived_not_created` | this device holds no row for the space; none is created |
| 409 | `space.deleted` | the row carries a deleted tombstone |

## Permanent by design

`DELETE /v1/spaces/:spaceId` refuses a derived space with `409 space.derived_undeletable`. Because the id is deterministic, delete-and-re-derive would replace history, and the sticky deleted tombstone would wedge the well-known id for the account's lifetime. Enforcement is layered so no single device can break it:

- the server pre-checks the boot-resolved registry ids, which covers ids the account has no row for;
- the SDK refuses rows carrying the synced `derived` flag;
- its space-index handler drops `remoteStatus=deleted` writes on flagged rows arriving from any peer;
- its deletion reconciler exempts them, so a coordinator reporting a space it never saw (derived offline) does not tombstone it.

`SpaceInfo.derived` surfaces the flag.

A derived space belongs to its account alone. Invite create, the guest key and a direct add answer `409 space.derived_unshareable`.

> **Note.** A derived space is an ordinary space in every other respect — `spaceType: "any.space"`, datasets, search. Its id, its permanence and its single owner are what is special.

## Related

- [Spaces](../database/spaces.html) — creating and listing regular spaces, and the account's agent space.
- [Bundles](bundles.html) — the same derive-instead-of-create idea applied to objects inside a space.
- [Agent data](../agents/agent-data.html) — what the agent runtime stores in the agent space.
