---
title: Derived objects
description: Ids computed from keys and a seed instead of minted at random — how any makes every device converge on the same space, root or metadata object with no coordination.
order: 150
---
# Derived objects

A derived object is one whose id is a pure function of its inputs — an account's keys, a space, a fixed seed — rather than a fresh random id minted at creation. Every device and every member computes the same id offline, so "the one and only X" exists without anyone creating it first, and two parties who have never talked still land on the same object.

Derived ids are how any answers a question hosted backends solve with a lock: *which* object is the space's chat, *which* space is the account's agent space, *where* does a space's name live.

## Why check-then-create fails here

The usual pattern — look for the object, create it if missing — needs a single point of truth to be safe. In a local-first system there isn't one: two devices of the same account, offline, both look, both find nothing, both create. Once they sync, the space carries two "general" chats or two "Pages" types, and content that cannot be merged across objects (a chat) is split for good.

A derived id removes the race instead of resolving it. There is nothing to look for and nothing to create: the id is known before any write, both devices write to it, and their writes merge like any other CRDT tree.

> **Why it matters.** Convergence without coordination is the whole point of a CRDT — but it only holds for edits to the *same* object. Derived ids extend it to *existence*: the decision "this object exists at this id" is made by arithmetic, so it can never fork.

## What is derived

| Thing | Derived from | Surface |
|---|---|---|
| **Derived spaces** — well-known per-account spaces such as the agent space `bao` | account keys + registry seed `any/space/<name>/v1` | `GET /v1/spaces/derived`, `POST /v1/spaces/derived/:name` — [Derived spaces](../collaboration/derived-spaces.html) |
| **One-to-one spaces** | both participants' account keys, order-independent | `POST /v1/spaces/one-to-one` — [One-to-one](../collaboration/one-to-one.html) |
| **`spaceIndex` object** — owns the space's name / description / icon and the bundles registry | the space | `SpaceInfo.spaceIndexObjectId` on every single-space response — [Spaces](spaces.html) |
| **Bundle roots** installed with `"derived": true` | space + bundle id (`builtin:bundleRoot:<id>`) | `POST /v1/spaces/:id/bundles` — [Bundles](../collaboration/bundles.html) |
| **Bundle children** | winner root + a client seed | `POST …/bundles/:bundleId/children` |

A derived change carries no identity, signature or timestamp — that is what makes the id reproducible. For a bundle root this means the id depends on nothing but `(space, bundle id)`; for a 1-1 space it means both sides derive the same id, the same immutable two-writer ACL and the same read key with no invite handshake at all.

## Resolve, then materialize

Deriving an id never creates anything. The pattern across the surface is a cheap resolve followed by a lazy, idempotent materialize:

```sh
# resolve: pure computation, reports whether a usable row exists anywhere in the account
curl http://127.0.0.1:7001/v1/spaces/derived
# → {"spaces": [{"name": "bao", "spaceId": "bafy…", "created": false}]}

# materialize: creates on first call, returns the same space on every later one
curl -X POST http://127.0.0.1:7001/v1/spaces/derived/bao
# → 201 SpaceInfo

any space derived
any space derived create bao
```

Bundles follow the same shape: an ensure with `"derived": true` is adopt-or-install — a read when the root is already registered, a write only the first time — and both sides of a partition install on the first attempt because there is no competing id either could mint. The space's chat is the canonical case, installed by the server's catalog:

```sh
curl -X POST http://127.0.0.1:7001/v1/catalog/general-chat/setup \
  -d '{"spaceId": "'$SPACE'"}'
# → {"usecase": "general-chat", "bundles": [{"id": "system:general-chat/v1", "bundle": {"rootId": "bafy…", "derived": true}, "installed": true}]}
```

Clients subscribe to a derived object like any other — for space metadata, a [subscribe](../realtime/subscribe.html) stream over the objects collection filtered on `spaceIndexObjectId`.

## The price: permanence

A derived id names one object for the lifetime of its inputs, so the object cannot be deleted and re-created — delete plus re-derive would replace history under the same id, and a sticky tombstone would wedge the well-known id forever. any therefore refuses:

| Attempt | Answer |
|---|---|
| `DELETE /v1/spaces/:id` on a derived space | `409 space.derived_undeletable` — layered: server pre-check on the registry ids, SDK refusal on the synced `derived` flag, apply-side drop of remote deletes, reconciler exemption |
| Uninstalling a bundle on a derived root | `409 object.derived_undeletable` on `DELETE …/objects/:rootId` — the bundle id stays bound for the space's lifetime |
| Migrating a created bundle root to a derived one | not done — an existing created install is adopted (`installed: false`, `derived: false`); moving content is the client's decision |

Deleting a **1-1 space** is local-only and re-derivable, and a joiner of someone else's derived space never carries the flag, so leaving stays allowed.

> **Note.** Ask for a derived id for things that must exist on both sides of a partition and are never removed — a space's chat, an account's agent space. Anything a user may want to delete belongs on a created object; for those, the [bundles](../collaboration/bundles.html) registry converges concurrent installs on a winner and lets the losers be merged and resolved instead.

## Precedence when both exist

If a created root and a derived root are both claimed for the same bundle id, the derived one wins on every replica — the verdict reads only the add-only claim set, so every device reaches the same answer — and the created root becomes an ordinary, resolvable loser. Nothing is destroyed; the demoted root keeps its content and stays deletable. Because a derived install proceeds after its convergence wait expires, a claim made blind can demote a created install this device had not yet seen; the wait is what narrows that window.
