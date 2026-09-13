---
title: Derived spaces
description: Well-known per-account spaces derived from the account keys and a fixed seed, so every client and device converges on the same space — permanent by design.
order: 50
---
# Derived spaces

A derived space is a well-known per-account space whose id is computed from the account keys and a fixed seed. The same account resolves the same space id on every device, so clients converge on *the* space without a create-then-find handshake and without ever minting duplicates.

## Why derive

Two clients that each "create the agent space if missing" race: while apart, each creates one, and the account ends up with two. Deriving the id removes the decision — there is nothing to find, and creating is idempotent by construction.

> **Why it matters.** With a hosted backend, uniqueness is a database constraint the server enforces. Offline-first peers have no single place to enforce anything, so convergence has to come from the id itself: a pure function of keys that every device evaluates identically with zero communication.

## The registry

Raw derivation with a caller-supplied seed is deliberately not exposed: a free seed would mint a *permanent* space and invite silent seed collisions between consumers. The vocabulary is a small registry compiled into the server, with seeds following the `any/space/<name>/v1` convention.

| Name | Purpose |
|------|---------|
| `bao` | the account's agent space — where [programs and agents](../programs/index.html) keep their data |

## Endpoints

```
GET  /v1/spaces/derived        → 200 { "spaces": [ { name, spaceId, created, status? } ] }
POST /v1/spaces/derived/:name  → 201 SpaceInfo
```

```bash
curl -s http://127.0.0.1:7001/v1/spaces/derived
```

```json
{ "spaces": [ { "name": "bao", "spaceId": "bafyrei…", "created": false } ] }
```

| Field | Meaning |
|-------|---------|
| `name` | registry name |
| `spaceId` | the derived id — computed once at engine boot, never created by this call |
| `created` | a usable space row exists, materialized here or on any of the account's devices (rows sync) |
| `status` | the row's status (the `SpaceInfo.status` strings) when a row exists; a tombstoned row reports `created: false` |

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/derived/bao
# → 201 SpaceInfo
```

```bash
any space derived               # list
any space derived create bao    # materialize
```

**GET resolves, never creates.** **POST materializes lazily and idempotently** and returns the full single-space `SpaceInfo`. On first materialization the registry's display name is written as the space name; a later rename through `PATCH /v1/spaces/:id` wins. Repeat calls land on the same space. The typical consumer flow is one POST at boot, then use the id like any other space.

| Status | Code | When |
|--------|------|------|
| 404 | `space.derived_unknown` | name not in the registry |
| 409 | `space.deleted` | the row carries a deleted tombstone |

## Permanent by design

`DELETE /v1/spaces/:spaceId` refuses a derived space with `409 space.derived_undeletable`. Because the id is deterministic, delete-and-re-derive would replace history, and the sticky deleted tombstone would wedge the well-known id for the account's lifetime. Enforcement is layered so no single device can break it:

- the server pre-checks the boot-resolved registry ids, which covers entries not yet materialized;
- the SDK refuses rows carrying the synced `derived` flag;
- its space-index handler drops `remoteStatus=deleted` writes on flagged rows arriving from any peer;
- its deletion reconciler exempts them, so a coordinator reporting a space it never saw (derived offline) does not tombstone it.

`SpaceInfo.derived` surfaces the flag. Joiners of someone else's derived space never carry it, so their removal stays allowed.

> **Note.** A derived space is an ordinary space in every other respect — `spaceType: "any.space"`, members, [invites](invites.html), datasets, search. Only its id and its permanence are special.

## Alongside an ad-hoc space

An account that also carries a client-created space serving the same purpose (for example a space named "bao" that a runtime created itself) gets a second, derived space from the registry. The registry id is the convergence point: move or re-import content from the ad-hoc space; do not alternate between them.

## Related

- [Spaces](../database/spaces.html) — creating and listing regular spaces.
- [Bundles](bundles.html) — the same derive-instead-of-create idea applied to objects inside a space.
- [Agent data](../agents/agent-data.html) — what the agent runtime stores in the `bao` space.
