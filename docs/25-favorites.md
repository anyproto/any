# Favourites — the built-in account-level bundle

Cross-space favourites: a tree of starred objects and folders, private
to the account, synced across its devices, ordered. The server owns the
declaration and ensures the bundle at boot; everything else — writes,
reads, rendering, tree policy — is the client's, on the generic record
surface. No favourites endpoints exist.

## Model

One bundle, `favorites/v1`, on the **tech space** (`techSpaceId` from
`GET /v1/account`). One dataset, `entries`: one record per entry, the
record id deterministic from what the entry IS —

| entry | record id |
|---|---|
| starred object | its canonical link: `any://o/<spaceId>/<objectId>` (a record link `any://o/<s>/<o>/<dataset>/<recordId>` is also valid) |
| folder | `f:` + a client-minted token (`[A-Za-z0-9_-]{1,64}`) |

so two devices starring the same object write the *same* record, star
is an idempotent upsert, and "is this starred" is a point lookup. The
id prefix is the kind discriminator — there is no `type` field.

Fields (schema is discoverable: `GET /v1/spaces/<tech>/types/<rootId>/datasets`):

| field | | |
|---|---|---|
| `parentId` | required | `""` = top level, else a folder's record id |
| `pos` | required | lexid; orders siblings (nav allocator params: `CharsAllNoEscape`, 4, 100 — any-ui's `lib/lexid` port matches) |
| `removed` | | `true` = soft-deleted; absent/false = live |
| `name` | | folder name \| mirrored target `any.name` |
| `iconCid` | | mirrored target `any.iconCid` (folders: optional) |
| `types` | | mirrored target `any.types` (type-default icon fallback) |
| `creator`, `createdAt`, `modifiedAt` | stamped | server-derived; client writes rejected |

Undeclared keys are permitted (`dynamic`) — a newer client's field is
not dropped by an older peer.

## Getting the root

The bundle is ensured at engine boot — never install it yourself
(`POST …/bundles` with `favorites/v1` returns `409 bundle.reserved`).

```
GET /v1/spaces/<techSpaceId>/bundles
→ { "bundles": [ { "id": "favorites/v1", "rootId": "<root>", "derived": true, … } ] }
```

`rootId` is the `objectId` for every call below and is identical on
every device of the account.

## Writes

Star (idempotent — re-running re-places and un-removes):

```
POST /v1/spaces/<tech>/upsert
{ "objectId": "<root>", "dataset": "entries", "records": [
  { "id": "any://o/<spaceId>/<objectId>",
    "fields": { "parentId": "", "pos": "<lexid>",
                "name": "<target any.name>", "iconCid": "<target any.iconCid>",
                "types": ["<target any.types…>"], "removed": false } } ] }
```

Folder:

```
{ "id": "f:<token>", "fields": { "parentId": "", "pos": "<lexid>", "name": "Work" } }
```

Un-star / delete a folder — **soft, always**:

```
POST /v1/spaces/<tech>/modify
{ "objectId": "<root>", "dataset": "entries", "records": [
  { "id": "any://o/…", "ops": [ { "type": "$set", "path": "removed", "value": true } ] } ] }
```

Never hard-delete (`delete-records`): CRDT tombstones are sticky, so a
deleted id can never be created again — the link would be un-star-able
forever. Move / reorder / rename are `$set` of `parentId` / `pos` /
`name` through the same `modify` shape.

## Reading and rendering

The whole tree is one subscription — the set is small, hold it whole:

```
POST /v1/spaces/<tech>/query/subscribe
{ "objectId": "<root>", "dataset": "entries", "sort": ["parentId", "pos"] }
```

Render **from the entries alone**: the mirrored `name` / `iconCid` /
`types` make every entry displayable instantly — offline, and on a
device that never loaded the target's space. Layer freshness on top:
per space that has favourites, one

```
POST /v1/spaces/<spaceId>/objects/query/subscribe
{ "filter": { "id": { "$in": [ …objectIds… ] } }, "sort": ["id"], "limit": n }
```

plus one `POST /v1/spaces/query/subscribe` for space name / icon /
status. When a live row's `any.name` / `any.iconCid` / `any.types`
differs from the mirror, write the difference back (**write only on
difference** — after the first device refreshes, the others see the
updated record and skip). Group by space client-side on the id prefix.

Target state, derived from those subscriptions:

| observation | state |
|---|---|
| row present | live |
| `removed { reason: "deleted" }` delta | deleted — definitive |
| space row status deleted / removed | space gone — definitive |
| space not loaded here / row absent from the snapshot | **unavailable — NOT deleted** |

`GET /v1/spaces/<s>/objects/<o>` answers `410 object.deleted` for the
definitive single-lookup case (an object tombstoned before this device
ever subscribed never enters a snapshot, so absence alone proves
nothing).

## Cleanup

A dangling entry costs nothing — rendering is a join; hide or badge it.
Rules:

- On a **definitive** signal only, a client may `$set removed: true`.
- On not-found / unavailable: do nothing. A fresh device or an unloaded
  space looks exactly like a deleted target.
- Hard deletion of definitively-gone targets is reserved for a future
  server-side reference-index mechanism; favourites does not wait for it.

## Tree semantics — client policy, not server rules

Every device must compute the same view from the same records:
resolution is read-side and deterministic, and no client repairs state
with writes. Decide and document (product):

- an entry whose folder is `removed` — including one that syncs in
  *after* the folder was removed elsewhere (grey the removed folder with
  its late children, treat them as removed with it, or hoist to root);
- cycles from concurrent moves (A: F1→F2, B: F2→F1) — render the cycle
  members at top level;
- a missing parent (only possible after a future hard delete) — render,
  never hide;
- "is starred" — decide whether it considers the ancestor chain when
  removal is inherited.

## See also

- `03-api.md` § Bundles (tech-space bundles, built-in account bundles)
- `08-clients.md` § 12 (account-level bundles recipe)
- `09-query.md` (filters, sort, paging), `04-events.md` (subscribe frames)
