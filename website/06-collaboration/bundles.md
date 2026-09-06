---
title: Bundles
description: Register what a space has installed — one root per bundle, adopted or installed, optionally derived so it can never fork — and resolve the losers when two devices installed apart.
order: 60
---
# Bundles

A bundle is one thing installed into a space — a chat, an app's setup, a marketplace package — registered as one root object under a permanent id, with every setup object derived from that root. One converged id names the whole install, so every member and device reads and writes the same objects instead of each creating their own.

## Why a registry

Clients that each `create a chat object if none exists` leave a space with two or three parallel chats, most visibly in a one-to-one. The registry (the `bundles` dataset on the space's index object) is the convergence point: clients register their own bundles and the server installs nothing on a client's behalf; what it owns is picking the winner when two devices install concurrently, refusing to delete a losing root before it has stopped arriving, and one id namespace — ids under `system:` are the server's, installed only through its embedded catalog (`409 bundle.reserved` on a client ensure).

## Endpoints

```
POST   /v1/spaces/:spaceId/bundles                        → 200 { bundle, installed }
GET    /v1/spaces/:spaceId/bundles                        → 200 { bundles: [...], synced }
GET    /v1/spaces/:spaceId/bundles/:bundleId              → 200 { bundle, synced }
POST   /v1/spaces/:spaceId/bundles/:bundleId/resolve      → 204
POST   /v1/spaces/:spaceId/bundles/:bundleId/children     → 200 { objectId }
```

`synced: true` means an absent bundle is definitively not installed.

Bundle ids carry a slash — the version suffix is part of the id (`favorites/v1`), and ids are permanent, so a successor install takes a new one. In a path segment the slash is percent-encoded: `/bundles/favorites%2Fv1`. Request bodies take the id verbatim.

## Ensure: adopt or install

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles \
  -d '{"id": "notes/v1", "name": "Notes", "hidden": true, "parts": [{"key": "body", "datasets": [{"module": "editor", "shared": true}]}]}'
```

```json
{ "bundle": { "id": "notes/v1", "rootId": "bafy…", "roots": ["bafy…"], "losers": [], "derived": false },
  "installed": true }
```

| Body field | Meaning |
|------------|---------|
| `id` | the whole identity — an app slug, a marketplace id, a versioned convention; ≤256 B |
| `name` | stamped as `any.name` on the root; ≤1024 B |
| `rootTypes` | types attached to the root; must exist in the space; ≤32 |
| `rootProperties` | initial property values, validated against their formats; ≤64 KiB |
| `parts` | parts declared on the root, which then implements itself as a type (`typeId = rootId`) — the [modules](../types/index.html) it holds and its records datasets (`<rootId>_<key>`); ≤32; the same draft shape as `POST …/types/:typeId/parts` |
| `properties` | property definitions on the root type; ≤64; the same draft shape as `POST …/types/:typeId/properties` |
| `xKey` | the root type's handle, unique among the space's types; `409 type.xkey_conflict` |
| `layout` / `weight` | the root type's rendering slice, as on `POST …/types` |
| `hidden` | keeps the root type out of `GET …/types` |
| `derived` | install on the root derived from the bundle id (below) |

A bundle may declare a full type on its root — parts, properties, a handle, layout, weight, hidden. Ids under `system:` are the server's: `409 bundle.reserved` on a client ensure.

With a winner already registered, Ensure is a **pure read** — nothing is written, a reader or guest can resolve an install they could not create, and `installed` is `false`. Otherwise the server creates the root with the requested types and properties, registers it in one change, and replies `installed: true`. That path is a write, so a member without write permission gets `403`; use `GET …/bundles/:bundleId` instead. Type existence and property formats are checked *before* the root is created, so a rejected request (`400 type.not_found`, `400 property.format_violation`) never leaves an orphan.

### The convergence gate

Installing a created root first waits for the registry to converge — bounded at 30 s, cut to 3 s when no peer is connected. A member that ensures against state it has not synced would read "nothing installed" and mint a root competing with the one already out there. Adopting never waits; a read cannot fork anything. A space never set up converges to an empty registry, which is a valid answer.

When the wait cannot complete, who is asking decides: the space's **owner** installs anyway (only its own devices could compete, and the registry converges those), so an offline owner is never blocked. Any other member is refused with `409 bundle.not_ready` and retries when the network is back. A winner whose tree has not reached this device is likewise `409 bundle.not_ready` — its id would reject every write.

> **Note.** `rootId` is provisional until the space syncs. Two devices that install while genuinely apart each register a root; the registry converges on one winner and the other lands in `losers`. Re-read after sync.

## Derived roots

`"derived": true` installs the bundle on the root **derived from its id**. That id is a pure function of (space, bundle id) — the derived root change carries no identity, signature or timestamp — so every device and every member computes it offline. Nothing can fork: each side registers the same id and `losers` stays empty.

This is the answer for a space's chat and the only workable one for a [one-to-one](one-to-one.html): its ACL owner is a synthetic key nobody holds, both participants are writers, neither can take the owner escape, and a created root leaves both refused until they converge — which never happens while apart. With a derived root each side installs immediately and the two copies merge like any other CRDT tree.

The price is permanence, in two directions:

- **No uninstall.** A derived tree cannot be deleted, so the bundle id stays bound to that root for the space's lifetime. Use it for setups that must exist on both sides of a partition, not for anything a user may remove.
- **No migration.** A bundle already installed on a created root is **adopted**, not moved: `installed: false`, `derived: false`, the created `rootId`. Moving content between roots is the client's decision.

If a created and a derived root are both claimed for one id, the derived one wins on every device and the created one becomes an ordinary loser. A derived install runs the same convergence wait but installs when it expires; a blind claim can therefore demote a created install this device had not seen — nothing is destroyed, but for unmergeable content such as chat the pointer moving amounts to the same thing. That is the deliberate trade that lets an offline 1-1 have a chat at all.

> **Why it matters.** A hosted chat service allocates one channel id and everyone uses it. Two encrypted peers that have never spoken cannot ask anyone for an id — but they can both compute one. Derivation replaces the allocator.

## The general chat

A space's chat is the server's: the `chat` module is reserved, so no client bundle may declare a chat part (`400 dataset.module_reserved`). The catalog's `general-chat` usecase installs the one chat, a derived root:

```bash
curl -s -X POST http://127.0.0.1:7001/v1/catalog/general-chat/setup \
  -d '{"spaceId": "'$SPACE'"}'
```

Use the returned `rootId` as the [chat](../types/chat.html) object. Every client that runs the setup lands on the same root, on both sides of a 1-1 too, and no other object may carry its type.

## Children

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/notes%2Fv1/children \
  -d '{"seed": "settings", "types": ["'$PAGE'"]}'
# → { "objectId": "bafy…" }
```

A child is a setup object derived under the bundle's current winner: deterministic per (space, root, seed), materialized on the first call, the same id on every device — a restored device reaches the whole install from the winner alone. Seeds are permanent and ≤256 B. Under a created root the child binds to the parent's tree and cascade-deletes with it; on a member whose copy of the winner has not landed, the call is `409 bundle.not_ready`. Under a derived root the child binds by seed instead — same ids everywhere, and the cascade is moot on a root that can never be deleted.

## Reads

```bash
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/bundles
curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/notes%2Fv1     # 404 bundle.not_found
```

Rows are also readable through the ordinary dataset surface — `POST /v1/spaces/:spaceId/query` with `{"objectId": "<spaceIndexObjectId>", "dataset": "bundles"}` — which is how a client [subscribes](../realtime/subscribe.html) to live conflict updates. That path is read-only; no client can forge a claim. Raw rows carry the stored `rootId` register and no `derived` field — the derived-root verdict is applied by `GET …/bundles[/:bundleId]`, so read those when a bundle may be derived.

## Losers and resolve

`losers` is the live conflict set: claimed roots that are neither the winner nor already deleted. Non-empty means two devices installed concurrently and the loser may hold real content. Cleanup is the client's call — merge what matters out of the losing root and its children, then:

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/notes%2Fv1/resolve \
  -d '{"loserRootId": "bafy…"}'
# → 204
```

The server never merges; it enforces timing. A losing root arrives change by change, and a merge made from a half-arrived tree is a half-merge, so resolve is refused until the SDK reports the root fully synced and it has been observed as a loser for a five-minute grace period. The clock starts when the conflict first became visible on this device (any `GET …/bundles[/:id]` or the boot pass counts), not at the first resolve call. After a timing refusal the server keeps retrying in the background — in memory, dropped on restart — so clients retry too.

| Status | Code | When |
|--------|------|------|
| 409 | `bundle.not_ready` | registry not converged (non-owner), or the winner's tree has not arrived |
| 409 | `bundle.loser_not_ready` | loser not fully synced, or within the grace period — retry |
| 409 | `bundle.not_loser` | the winner, or a root never claimed for this bundle |
| 404 | `bundle.not_found` | unknown bundle id on GET / resolve / children |
| 400 | `type.not_found` | a `rootTypes` entry does not exist in the space |
| 400 | `property.format_violation` | a `rootProperties` value fails its declared format |

Resolving an already-resolved root returns 204 — the call is idempotent.

## Agreeing who installs

Nothing stops two members from ensuring the same bundle; the registry converges and reports a loser. To avoid the conflict entirely, either agree on one installer out of band, or ask for a `derived` root — the id both would compute anyway. Bundle records are permanent and `roots` only grows, so treat ids as a small fixed vocabulary, not a scratch namespace.

On boot the server converges the space list and projects the index of the account's well-known [derived spaces](derived-spaces.html), so a client ensuring right after a restore meets the converged registry instead of an empty one. It installs nothing and deletes nothing itself.
