# Favourites — the account-level bundle (client contract)

Cross-space favourites: a tree of starred objects and folders, private
to the account, synced across its devices, ordered. Favourites is a
CLIENT-REGISTERED bundle — this document is the contract every client
ships verbatim (the declaration below is canonical; identical requests
converge). No favourites code exists on the server.

## Model

One bundle, `favorites/v1`, on the **tech space** (`techSpaceId` from
`GET /v1/account`), installed on a CREATED root — deletable, forking
on concurrent offline installs (see § Forks). One part, `entries`, with
one records dataset of the same key — its collection is the namespaced
**`<rootId>_entries`** (`03-api.md` § Parts and modules), the `dataset`
value in every read and write below; read it off the parts list rather
than composing it. One record per entry, the record id deterministic
from what the entry IS —

| entry | record id |
|---|---|
| starred object | its canonical link: `any://o/<spaceId>/<objectId>` (a record link `any://o/<s>/<o>/<dataset>/<recordId>` is also valid) |
| folder | `f:` + a client-minted token (`[A-Za-z0-9_-]{1,64}`) |

so two devices starring the same object write the *same* record, star
is an idempotent upsert, and "is this starred" is a point lookup. The
id prefix is the kind discriminator — there is no `type` field.

Fields (schema is discoverable: `GET /v1/spaces/<tech>/types/<rootId>/parts`,
each dataset carrying its `collection`):

| field | | |
|---|---|---|
| `parentId` | required | `""` = top level, else a folder's record id |
| `pos` | required | lexid; orders siblings (allocator: `CharsAllNoEscape`, block 4, step 100) |
| `removed` | | `true` = soft-deleted; absent/false = live |
| `name` | | folder name \| mirrored target `any.name` |
| `iconCid` | | mirrored target `any.icon` (folders: optional) |
| `types` | | mirrored target `any.types` (type-default icon fallback) |
| `creator`, `createdAt`, `modifiedAt` | stamped | server-derived; client writes rejected |

Undeclared keys are permitted (`dynamic`) — a newer client's field is
not dropped by an older peer.

## Install and adopt

Startup is a locked read, never an ensure:

```
GET /v1/spaces/<techSpaceId>/bundles     → { "bundles": […], "synced": true|false }
```

The reply answers only after the registry convergence wait. `synced:
true` + no `favorites/v1` row = definitively not installed. `synced:
false` (cold offline device) = provisional; re-read after sync. When
the row exists, take `rootId` — it is the `objectId` for everything
below.

Ensure on FIRST WRITE (the user's first star on a device that has no
row), with the canonical request:

```
POST /v1/spaces/<techSpaceId>/bundles
{ "id": "favorites/v1", "name": "Favorites", "hidden": true,
  "parts": [{ "key": "entries", "datasets": [{
    "key": "entries", "idRule": "user", "dynamic": true,
    "idPattern": "^(any://o/.+|f:[A-Za-z0-9_-]{1,64})$", "idMaxLen": 256,
    "fields": [
      {"key": "parentId", "kind": "string", "required": true, "mutableBy": "any"},
      {"key": "pos", "kind": "string", "required": true, "mutableBy": "any"},
      {"key": "removed", "kind": "boolean", "mutableBy": "any"},
      {"key": "name", "kind": "string", "mutableBy": "any"},
      {"key": "iconCid", "kind": "string", "mutableBy": "any"},
      {"key": "types", "kind": "array", "mutableBy": "any"},
      {"key": "creator", "stamp": "creator"},
      {"key": "createdAt", "stamp": "createTime"},
      {"key": "modifiedAt", "stamp": "modifyTime"}
    ] }] }] }
```

Idempotent — the first call installs, later calls adopt. The root
CARRIES the type it declares, so the entries live on it: on the tech
space that is implied and needs no flag; the same bundle in an
ordinary space would have to ask for it with `"selfTyped": true`
(`03-api.md` § Bundles), or every write to `<rootId>_entries` would be
`400 dataset.not_declared`. Uninstall = `DELETE
/v1/spaces/<tech>/objects/<rootId>`; the id then reads as not
installed and a later install mints a fresh root.

### Forks

Two devices ensuring while apart (each starred something before first
sync) install two roots; the registry converges on one winner, the
other lands in the bundle's `losers`. The client that observes a loser
merges its `entries` into the winner — upsert each live record through
this schema (link ids merge into the same record; folders re-mint) —
then calls `POST …/bundles/favorites%2Fv1/resolve` with
`{"loserRootId": "<loser root>"}`. Ensuring lazily (first write, not
startup) is what keeps this rare.

## Writes

Star (idempotent — re-running re-places and un-removes):

```
POST /v1/spaces/<tech>/upsert
{ "objectId": "<root>", "dataset": "<rootId>_entries", "records": [
  { "id": "any://o/<spaceId>/<objectId>",
    "fields": { "parentId": "", "pos": "<lexid>",
                "name": "<target any.name>", "iconCid": "<target any.icon>",
                "types": ["<target any.types…>"], "removed": false } } ] }
```

Folder:

```
{ "id": "f:<token>", "fields": { "parentId": "", "pos": "<lexid>", "name": "Work" } }
```

Un-star / delete a folder — **soft, always**:

```
POST /v1/spaces/<tech>/modify
{ "objectId": "<root>", "dataset": "<rootId>_entries", "records": [
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
{ "objectId": "<root>", "dataset": "<rootId>_entries", "sort": ["parentId", "pos"] }
```

Render **from the entries alone**: the mirrored `name` / `iconCid` /
`types` make every entry displayable instantly — offline, and on a
device that never loaded the target's space. Layer freshness on top
with **batched target subscriptions**:

### Target subscriptions, in batches

Group the item links by `spaceId` (the id prefix) and open ONE stream
per space that has favourites:

```
POST /v1/spaces/<spaceId>/objects/query/subscribe
{ "filter": { "id": { "$in": [ "<objectId1>", "<objectId2>", … ] } },
  "sort": ["id"], "limit": <number of ids> }
```

- `limit` = the batch size: the window must hold every id, or targets
  past the window read as absent. Favourites are small — one stream
  per space carries the whole batch; there is no per-stream ceiling to
  design around.
- Skip spaces whose row is not loaded on this device (`localStatus`
  from the space-list stream below) — the subscribe would fail; those
  targets render from the mirror as unavailable-fresh.
- **When the batch changes** (star/un-star adds or drops an id in that
  space), close the stream and reconnect with the new `$in` list —
  the windowed contract's recovery rule; there is no in-place filter
  update.
- Interpret deltas: `added`/`updated` rows carry the live `any.name` /
  `any.icon` / `any.types` → refresh the mirror **only when a value
  differs** (after the first device writes, the others see the updated
  entry and skip — no ping-pong). A `removed` entry is definitive ONLY
  with `reason: "deleted"`; `filtered-out` / `displaced` mean the
  object still exists.
- An id absent from the initial snapshot proves nothing (an object
  tombstoned before this device subscribed never enters a window) —
  it is *unavailable*, not deleted. `GET /v1/spaces/<s>/objects/<o>`
  answers `410 object.deleted` when a definitive single check is
  needed.

Plus one account-wide `POST /v1/spaces/query/subscribe` for the raw
space rows — `name` / `icon` / `remoteStatus` / `localStatus` — which
feeds both the "… in <Space>" labels and the space-gone cleanup signal.

Target state, derived from those subscriptions:

| observation | state |
|---|---|
| row present | live |
| `removed { reason: "deleted" }` delta | deleted — definitive |
| space row `remoteStatus: "deleted"` | space gone — definitive |
| space not loaded here / row absent from the snapshot | **unavailable — NOT deleted** |

## Cleanup

A dangling entry costs nothing — rendering is a join; hide or badge it.
Rules:

- On a **definitive** signal only, a client may `$set removed: true`.
- On not-found / unavailable: do nothing. A fresh device or an unloaded
  space looks exactly like a deleted target.

## Tree semantics — client policy, not server rules

Every device must compute the same view from the same records:
resolution is read-side and deterministic, and no client repairs state
with writes. The policy covers:

- an entry whose folder is `removed` — including one that syncs in
  *after* the folder was removed elsewhere (grey the removed folder with
  its late children, treat them as removed with it, or hoist to root);
- cycles from concurrent moves (A: F1→F2, B: F2→F1) — render the cycle
  members at top level;
- a `parentId` with no folder record — render, never hide;
- "is starred" — decide whether it considers the ancestor chain when
  removal is inherited.

## See also

- `03-api.md` § Bundles → Tech-space bundles
- `08-clients.md` § 12 (account-level bundles recipe)
- `09-query.md` (filters, sort, paging), `04-events.md` (subscribe frames)
