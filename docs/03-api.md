# HTTP API

## Table of Contents

- [Conventions](#conventions)
  - [Write responses](#write-responses)
- [Endpoint catalog](#endpoint-catalog)
  - [Meta](#meta)
  - [Auth](#auth)
  - [Account](#account)
  - [Spaces](#spaces)
    - [Query / subscribe the space list](#query--subscribe-the-space-list)
    - [Dataset schema discovery](#dataset-schema-discovery)
    - [Update space metadata](#update-space-metadata)
    - [Per-space settings (account-private)](#per-space-settings-account-private)
    - [Force a head-sync round (sync now)](#force-a-head-sync-round-sync-now)
  - [Bundles](#bundles)
  - [Catalog](#catalog)
  - [Objects](#objects)
    - [Blocks](#blocks)
      - [Read blocks](#read-blocks)
      - [Create](#create)
      - [Patch](#patch)
      - [Delete](#delete)
      - [Subscribe](#subscribe)
    - [Create an object](#create-an-object)
    - [The wiki tree](#the-wiki-tree)
    - [Moves (drag-and-drop)](#moves-drag-and-drop)
    - [Object deletion](#object-deletion)
    - [Backlinks](#backlinks)
  - [Data plane](#data-plane)
    - [Snapshot request body (shared by both `…/query` and `…/query/subscribe`)](#snapshot-request-body-shared-by-both-query-and-querysubscribe)
    - [Aggregate](#aggregate)
    - [Subscribe (Server-Sent Events)](#subscribe-server-sent-events)
  - [Version history](#version-history)
    - [List changes](#list-changes)
    - [View at a version](#view-at-a-version)
    - [One record at a version](#one-record-at-a-version)
    - [Diff](#diff)
  - [Types](#types)
    - [Parts and modules](#parts-and-modules)
    - [Runtime dataset schemas](#runtime-dataset-schemas)
    - [Upsert records](#upsert-records)
    - [Built-in `dataview` type](#built-in-dataview-type)
    - [Built-in hidden types: `page`, `miniapp`, `bin`](#built-in-hidden-types-page-miniapp-bin)
  - [Properties (values on objects)](#properties-values-on-objects)
  - [Chat (the `chat` module)](#chat-the-chat-module)
    - [Message wire shape (read path)](#message-wire-shape-read-path)
    - [Send](#send)
    - [Read](#read)
    - [Edit / delete (own only)](#edit--delete-own-only)
    - [React (toggle)](#react-toggle)
  - [Enrichment (moved userspace)](#enrichment-moved-userspace)
  - [Agent data (moved userspace)](#agent-data-moved-userspace)
  - [Files (files v2)](#files-files-v2)
    - [Upload (attach)](#upload-attach)
    - [Download (content)](#download-content)
    - [Payload-row query / subscribe](#payload-row-query--subscribe)
    - [File cache (account-wide)](#file-cache-account-wide)
  - [Members](#members)
  - [Invites](#invites)
  - [ACL operations](#acl-operations)
    - [Permission / status strings](#permission--status-strings)
  - [Sync status](#sync-status)
  - [Events](#events)
  - [Processes](#processes)
  - [Local store](#local-store)
  - [Push notifications](#push-notifications)
  - [Debug (diagnostic)](#debug-diagnostic)
- [Body shapes (examples)](#body-shapes-examples)
- [Middleware](#middleware)
- [Pagination](#pagination)
- [Idempotency](#idempotency)

## Conventions

- **Framework**: `github.com/labstack/echo` (v4).
- **Base path**: `/v1/`. All endpoints are versioned from day one.
- **Media type**: `application/json; charset=utf-8` — requests with a body
  and every response. No other content types in v1.
- **IDs in the path**: `{spaceId}`, `{objectId}`, `{typeId}`, `{propId}`
  are URL-safe strings (base58). Path segments are URL-encoded.
- **Success**: `200 OK` for reads, `201 Created` for creates,
  `204 No Content` for side-effect-only endpoints (e.g.
  `PUT /v1/account/metadata`, `PATCH /v1/spaces/:spaceId`,
  `DELETE /v1/spaces/:spaceId/objects/:objectId`).
- **Errors**: see `06-errors.md`. Always JSON, always the same shape.
- **Binding**: use `echo.Context.Bind` for request bodies. Share the
  request/response types between server and CLI via `internal/api/`.
- **Dataset reads go through `/query` and `/query/subscribe`.**
  The compiled-in modules (chat, editor) keep bespoke handlers for
  *writes* only — POST/PATCH/DELETE and reactions. Reads always go
  through the per-object query primitive with the matching `dataset`
  value — the collection name (`chat_messages`, `editor_blocks`, a
  namespaced `<typeId>_<key>`, …). One read path for every dataset,
  one wire shape for every snapshot. The lone exception is
  `GET …/editor/:collection/markdown`, which renders blocks to
  markdown bytes — a transform, not a dataset read.

### Write responses

Every dataset write — `Space.Modify` / `Space.Delete` and the bespoke
chat (send / edit / delete / react) and editor (create / patch /
delete) handlers — returns the same shape, `api.ModifyResult`:

```json
{ "versionId": "<change VersionId>",
  "changeId":  "<changeId>",
  "recordIds": ["<id>"],
  "rejections": [] }
```

- `versionId` is the **change's** VersionId. Clients running the
  subscribe-then-query-then-apply recipe stamp `_ver.<op.path> =
  versionId` on the touched paths to pre-seed dedup against the matching
  live event, and use it to order their own writes against remote ones.
  (Distinct from `_ver.id`, the per-record creation marker — a stable
  id, not a per-edit version.)
- `recordIds` mirrors the input record order. On creates it carries the
  server-derived id (`recordIds[0]`); on edit / delete / react it echoes
  the target id.
- `rejections` is omitted unless a handler dropped an op (partial
  success). The bespoke chat/editor handlers turn any rejection into a
  4xx instead, so it's always empty there.

Writes never return the record body — read it back through `/query` (or
live via `/query/subscribe`). One write shape across the whole API.

## Endpoint catalog

### Meta

| Method | Path               | Purpose                                                        |
|--------|--------------------|----------------------------------------------------------------|
| GET    | `/v1/health`       | server health, version, account id                             |
| POST   | `/v1/shutdown`     | graceful shutdown — managed servers only, control-token gated  |
| GET    | `/v1/openapi.json` | the API's OpenAPI 3.1 spec                                     |

`POST /v1/shutdown` belongs to the server's owner (`02-server.md`
§ Modes): a managed server stops on it with the `X-Any-Control-Token`
header (`403 control.forbidden` without), a standalone server answers
`403 shutdown.not_managed` — it is the user's, stopped with `any stop`
or a signal. Outside the auth guard, so an unauthorized managed server
is still stoppable.

`/v1/openapi.json` serves the **OpenAPI 3.1** document generated from
the handler annotations and the `internal/api` request structs — the
discovery surface for spec-reading clients (the UI apps, anybao's
helper layer, external agents). Schema descriptions come from the
struct field comments, so they carry the same guidance the error
messages do. Request schemas whose endpoints enforce the closed body
vocabulary (`request.unknown_field`) are served with
`additionalProperties: false` — the spec is the authoritative list of
which endpoints are strict, declared at discovery time. Not available
in the mobile build (404).

`/v1/health` works on an unauthorized server too — `account` is then
`""`.

`bootstrapping` (bool): `true` while a booted engine's SDK background
boot pass (eager space loading + offline catch-up) is still running —
serving, offline catch-up in background; per-space convergence stays
on `/sync-status`. `false` when unauthorized and after the pass
completes. See `02-server.md` § Startup / § Health.

`crdtVersion` (`{supported, stored, newer}`, absent when unauthorized):
the account's CRDT data-model version — the one this server's SDK
writes (`supported`), the one recorded on the account's tech space
(`stored`; every release stamps it on first open, and the mark only
ever rises), and `newer`, true when the recorded one is above the
supported one. A newer account refuses to boot (`POST /v1/auth` →
`409 sdk.crdt_version_newer`, `any run` exits with the same reason);
when the raise arrives through sync while the server is running — a
second device upgraded first — the account turns **read-only**: reads
keep serving, every synced write answers `409 sdk.crdt_version_newer`,
and a client shows "upgrade required" off this field. See
`02-server.md` § Health.

### Auth

| Method | Path        | Purpose                                                                 |
|--------|-------------|-------------------------------------------------------------------------|
| GET    | `/v1/auth`  | authorization state, ownership mode + capabilities, local accounts      |
| POST   | `/v1/auth`  | generate / restore / select an account, boot SDK; `replace` switches; `409 sdk.crdt_version_newer` for an account a newer release wrote |
| DELETE | `/v1/auth`  | tear the account down in place, stay up unauthorized (managed only)     |

A server started without a resolvable account (fresh data dir, several
accounts and no selector, or **any managed server** — see `02-server.md`
§ Startup) is **unauthorized**: every `/v1` route except `/v1/health`,
`/v1/shutdown`, `/v1/openapi.json` and `/v1/auth` returns
`401 auth.required`. `POST /v1/auth` boots the account in place; no
restart.

**Ownership gates the verbs.** On a managed server (`02-server.md`
§ Modes) every `POST` and `DELETE /v1/auth` requires the
`X-Any-Control-Token` header — `403 control.forbidden` without it —
so no other local process can log the server into an account or sign
it out. A standalone server needs no token and refuses the operations
a managed one allows.

```json
// GET /v1/auth
{ "authorized": true, "accountId": "A8g1…",
  "mode": "standalone",                       // or "managed" — informative only
  "capabilities": {                           // branch on THESE, never on mode
    "deauthorize":   false,                   // DELETE /v1/auth accepted
    "switchAccount": false,                   // POST /v1/auth {…, replace:true} accepted
    "shutdown":      false },                 // POST /v1/shutdown accepted
  "accounts": [                               // wallets on disk — standalone only,
    {"id":"A8tR…","default":true},            //   legacy root wallet.key
    {"id":"A8g1…"} ] }                        //   <root>/<id>/ dirs; [] on managed
```

A managed server reports every capability `true` and an empty
`accounts` list: it holds no keys, so the client owns the account list
(build the picker from its keystore). Clients read this before rendering
any sign-out / switch / quit affordance and show each only where its bit
is true, so a future third mode does not break them.

```json
// POST /v1/auth — mnemonic and accountId are mutually exclusive:
{}                                    // generate a fresh account (index 1)
{ "mnemonic":"w1 … w12" }             // restore at the default index (1):
                                      // same phrase ⇒ same account
{ "mnemonic":"w1 … w12", "index":0 }  // restore an anytype-derived account
{ "accountId":"A8g1…" }               // select an existing local wallet
                                      //   (standalone only — 400 on managed)
{ "mnemonic":"w1 … w12",
  "replace": true }                   // managed: switch to this account in place

// → 200
{ "accountId":"A8g1…",
  "created": true,        // no local state for this account before the call
  "mnemonic":"w1 … w12" } // ONLY when generated — shown once, back it up

// → 200, the server already runs THIS account (nothing booted)
{ "accountId":"A8g1…", "created": false, "alreadyAuthorized": true }
```

**The target account is derived from the request before anything is
decided**, so the reply while an account is running follows one table:

|                          | `{}`                           | same account                      | different account                                                |
|--------------------------|--------------------------------|-----------------------------------|------------------------------------------------------------------|
| standalone, authorized   | `409 auth.already_authorized`  | `200 {alreadyAuthorized: true}`   | `403 auth.not_managed`                                           |
| managed, authorized      | `409 auth.already_authorized`  | `200 {alreadyAuthorized: true}`   | `409 auth.account_mismatch`; with `replace: true` → switch, 200  |

- **Same account never tears down.** A retry, reconnect or duplicate
  mount is a no-op — and `alreadyAuthorized` on a **mnemonic** request
  is how a client confirms a phrase it holds belongs to the running
  account, so it is safe to persist afterwards (an `accountId` request
  confirms only that the id matches, which `GET /v1/auth` already
  tells).
- **Refusals never echo the derived id** — otherwise the endpoint would
  be a phrase-to-account oracle.
- **Switching is opt-in.** `replace` tears the running engine down
  (every stream ends with `closed{reason: deauthorized}`, see
  `04-events.md`) and boots the target; a boot failure after the
  teardown leaves the server **unauthorized** and returns the boot
  error — re-read `GET /v1/auth`. `replace` is meaningless outside
  managed (the 403 stands), and `replace` with `{}` is
  `400 request.invalid_field`: a fresh account is never a replacement.
- `{}` while authorized is always refused, `replace` or not: minting an
  account must never be a side effect of a stale request.

Custody by mode: **standalone** writes / reads `wallet.key` (restore
mints a fresh device key on this machine); **managed** keeps the
account key in memory for this boot and caches the device key at
`<root>/<accountId>/device.key`, so every later login keeps the same
peerId (`02-server.md` § Modes). In managed mode `created` reports the
first login on this install.

`index` is the account-derivation index and is valid **only with
`mnemonic`** (a selected account's index is baked into its wallet; a
generated one is always the `any` default, 1). Omitted means 1; index
0 is anytype's, passed explicitly to restore an anytype-derived
account. Any `index` without `mnemonic` — including an explicit 0 —
is `400 request.invalid_field`. If the engine fails to boot after this
call created the account dir (e.g. SDK init error), the half-created
dir is removed, so a retry — or `generate` getting a new phrase —
starts clean rather than auto-selecting an un-backed account.

**`DELETE /v1/auth`** (managed, control token) tears the engine down —
streams end with `closed{reason: deauthorized}`, in-flight requests
drain — and leaves the listener up in the unauthorized state; `204`,
idempotent. A client must then delete its stored credential, or the
account is not actually signed out on that device. A standalone server
answers `403 auth.not_managed`: sign out by stopping it.

Errors: `400 auth.bad_mnemonic` (BIP-39 validation),
`400 request.invalid_field` (mnemonic+accountId together, index
without mnemonic, `replace` without a credential, `accountId` on a
managed server), `403 control.forbidden` (managed, token missing or
wrong), `403 auth.not_managed` (standalone: a different account, or
DELETE), `409 auth.account_mismatch` (managed: a different account
without `replace`), `409 auth.already_authorized` (`{}` while
authorized), `404 auth.account_not_found` (accountId without a local
wallet), `409 auth.account_in_use` (another process holds that
account's single-instance lock; `details.pid` names the holder when
known), `409 auth.mnemonic_mismatch` (existing wallet file
disagrees with the supplied phrase/index), `400 auth.passkey_required`
(encrypted wallet — the passkey still comes from the configured env
var, never the request body), `500 auth.device_key_corrupt` (managed:
the account's cached `device.key` is unreadable; it is never re-minted
silently — remove the file to mint a new device identity, which
registers this install as a new peer).

### Account

| Method | Path                         | Purpose                                |
|--------|------------------------------|----------------------------------------|
| GET    | `/v1/account`                | own id, `techSpaceId`, metadata        |
| PUT    | `/v1/account/metadata`       | `Account.UpdateMetadata`               |

`GET /v1/account` also returns `techSpaceId` — the account's tech
space, the `:spaceId` for account-level bundles (§ Bundles, "Tech-space
bundles"). It never appears in `GET /v1/spaces`.

```json
// PUT /v1/account/metadata
{ "name":"Alice",
  "description":"writer, reader, occasional debugger",
  "iconCid":"bafy..." }
// → 204
```

The SDK persists the bytes to the local tech-space and pushes them to
identityRepo, so the new profile becomes visible in every space the
caller is a member of without further per-space writes (read it back
via `GET /v1/spaces/:id/members/me`). At least one of `name` /
`description` / `iconCid` must be set; an all-empty body returns
`400 request.missing_field`.

> **Profiles are encrypted.** The bytes pushed to identityRepo are
> encrypted with an account-derived key that is shared with a contact
> only through an already-encrypted channel — a shared space's ACL
> metadata or a 1-1 invite. A peer who has not yet received the key sees
> the account **id only**, with `name` / `description` / `iconCid` empty,
> until the key arrives and the SDK's background fetch resolves the
> profile. Clients must tolerate an empty name everywhere a contact
> profile surfaces (members list, identities directory).

### Identities (account-global directory)

| Method | Path                          | Purpose                              |
|--------|-------------------------------|--------------------------------------|
| GET    | `/v1/identities`              | `Identities.List` — every known id   |
| GET    | `/v1/identities/:identity`    | `Identities.Get` (404 when unknown)  |
| GET    | `/v1/identities/subscribe`    | `Identities.Subscribe` (SSE)         |

The directory is the account-global, device-local cache of **every
account identity this account has encountered** — across spaces, 1-1s,
and inbox invites. It is the place to resolve a display name/icon for an
identity you only hold an id for (a chat message `creator`, a 1-1 peer).
Account-scoped — these routes sit outside the `:spaceId` group.

```json
// GET /v1/identities → 200
{ "identities": [
    { "identity":"A5k…",
      "name":"Alice", "iconCid":"bafy…",
      "spaceIds":["bafyspace1…","bafyspace2…"] } ] }
```

Each row carries the last resolved profile (`name` / `description` /
`iconCid`, omitted until resolved — see the encryption note above) and
`spaceIds`, the set of spaces where the identity is currently seen
(pruned when you leave/offload a space). The synced decryption key behind
each row is **never** exposed.

**The directory carries no rights.** Roles
(`owner`/`admin`/`writer`/`reader`) are per-space and live on the members
list (`GET /v1/spaces/:id/members`), which is the authoritative roster.
To show an identity's role you read the members list of the relevant
space; there is no cross-space role rollup. See
[clients §8](08-clients.md) for the members-vs-directory recipe.

`GET /v1/identities/subscribe` streams directory changes as
`event: identities` frames carrying `{added, updated, removed}` batches
(same `ready` → … → `closed` envelope and reason set as the sync-status
streams — see [events](04-events.md)).

### Spaces

| Method | Path                            | Purpose                             |
|--------|---------------------------------|-------------------------------------|
| POST   | `/v1/spaces`                    | `Service.Create`                    |
| GET    | `/v1/spaces`                    | `Service.List` → `[]SpaceInfo` (active-only by default, see note) |
| POST   | `/v1/spaces/query`              | `Service.Query` (spaces dataset) snapshot |
| POST   | `/v1/spaces/query/subscribe`    | `Service.Query` (spaces dataset) subscribe (SSE) |
| GET    | `/v1/spaces/:spaceId`           | `Space.Info`                        |
| PATCH  | `/v1/spaces/:spaceId`           | `Space.SetMetadata`                 |
| PATCH  | `/v1/spaces/:spaceId/settings`  | `Spaces().SetSettings` — account-private settings |
| POST   | `/v1/spaces/:spaceId/sync`      | `Space.SyncHeads`                   |
| DELETE | `/v1/spaces/:spaceId`           | `Service.Delete`                    |
| POST   | `/v1/spaces/join`               | `Service.Join`                      |
| POST   | `/v1/spaces/one-to-one`         | `Service.OneToOne` — open a 1-1 (direct) space |
| POST   | `/v1/spaces/one-to-one/register-incoming` | `Service.RegisterIncoming` — out-of-band incoming |
| POST   | `/v1/spaces/:spaceId/one-to-one/accept`   | `Service.AcceptOneToOne`            |
| POST   | `/v1/spaces/:spaceId/one-to-one/decline`  | `Service.DeclineOneToOne`           |
| POST   | `/v1/spaces/:spaceId/invite/accept`       | `Service.AcceptInvite` — direct-add invite |
| POST   | `/v1/spaces/:spaceId/invite/decline`      | `Service.DeclineInvite`             |
| POST   | `/v1/spaces/:spaceId/search`    | local search index (no SDK method — see below) |
| GET    | `/v1/spaces/derived`            | embedded registry → `Service.DeriveId` per entry |
| POST   | `/v1/spaces/derived/:name`      | `Service.Derive` for a registry entry |

**Raw `Service.Derive` / `DeriveId` are deliberately not exposed.** A
client-supplied seed would mint a *permanent* space (derived spaces
cannot be deleted) and invite silent seed collisions between consumers.
Derivation is reachable only through the closed registry vocabulary of
`/v1/spaces/derived` (below); there is no free-seed `/v1/spaces/derive`
route by design.

**`DELETE` is a real, offline-first deletion** (`any-sync-sdk v0.0.12`).
It returns `204` as soon as the local half is done — no network round
trip on the call path: the SDK writes the synced `remoteStatus=deleted`
tombstone (propagates to the account's other devices, drives the
`Subscribe` `Removed` event), offloads all local state (closes watchers
+ Store, evicts the any-sync space, drops the per-space CRDT
collections and DB file — immediate in the normal case, reclaiming disk
even offline; on a partial sweep failure the storage file is kept so
the next boot retries), and
kicks a background reconciler that sends the signed
`coordinator.SpaceDelete` now (if online) or on a later tick. Owner-only
on the network side: deleting a non-owned space offloads locally and the
reconciler no-ops the coordinator call. The reconciler also runs the
inbound direction — spaces the coordinator reports gone (deleted on
another device, or an owner deleted a space you joined) are offloaded
locally on the next poll. **Derived spaces are refused** with
`409 space.derived_undeletable` (see § Derived spaces), and an id the
account doesn't know returns `404 space.not_found` instead of a silent
204.

**`GET /v1/spaces` defaults to active spaces only.** The tech-space row
is never physically removed — it stays in `Service.List` with
`status:"deleted"` as a **sticky tombstone** — so the raw list otherwise
accumulates dead rows even though their storage is reclaimed. Pass
`?status=all` to get the full list (every status), or `?status=<value>`
to filter to a specific status (e.g. `deleted`). The
`POST /v1/spaces/query[/subscribe]` primitive is unaffected — it still
returns the raw tech-index rows. Consumers that keep derived per-space
state (search index, UI caches) drop it by watching
`POST /v1/spaces/query/subscribe` and purging on the `removed` frame;
the server's own search indexer already does this.

**`GET /v1/spaces/:id` materializes only active spaces.** A non-active
row (joining / one_to_one_pending / one_to_one_declined / invite
statuses / deleted) is served straight from the tech-space index —
plain row `SpaceInfo`, no `spaceIndexObjectId`,
nothing loaded. Materializing a pending row would download the space
before it was accepted: the SDK's load path falls back to a network
SpacePull when local storage is missing, so a single read on a
pending-join or incoming-1-1 id used to pull the whole space
ciphertext ahead of acceptance (and a 1-1's derivation-time read key
would even decrypt it). The SDK enforces the same guard in
`Service.Get`; the handler's status check keeps non-active reads
serving row info instead of surfacing that error.

`SpaceInfo` carries a `spaceIndexObjectId` field: the deterministic id
of the in-space `spaceIndex` derived object that owns this space's
metadata. Stable across peers and across SDK reboots — clients
attach a `POST /v1/spaces/:id/objects/query/subscribe` stream filtered
on this id to live-update name / description / icon. Single-space responses
(`POST /v1/spaces`, `GET /v1/spaces/:id`, `PATCH /v1/spaces/:id`)
always populate the field. `GET /v1/spaces` fills it on a best-effort
basis; rows whose Space handle the SDK can't resolve (e.g. tombstoned
entries) omit it.


`SpaceInfo.createdAt` (RFC3339) is the **added-to-account** time,
stamped when the tech-space row is created — at create for the author,
at join for a joiner. Immutable once stamped. Rows from before the
stamp existed report the zero time (`0001-01-01T00:00:00Z`) — treat it
as "unknown"; there is no backfill. The stamp is per-device, so the
account's devices can disagree by a few seconds (or zero vs real on
mixed SDK versions) — good for ordering, not for equality checks.

`SpaceInfo` also carries `spaceType` and `author`. `spaceType` is the
**app-level classification** tag (read from the in-space `spaceIndex`),
distinct from the on-wire header `type`: a 1-1 space reports
`spaceType:"any.onetoone"`, a created space `"any.space"` — use it to
tell direct chats from regular spaces client-side. `author` is the
space owner's account identity, resolved best-effort from the ACL (empty
when the ACL isn't loadable) — except on a 1-1, where the owner slot is a
synthetic shared key nobody holds, so `author` carries the **other
participant** instead (each side sees its counterpart — use it for "chat
with X", not for "who created this"). Both are omitted when empty.

`SpaceInfo.ownRole` is the caller's own role in the space — `owner` /
`admin` / `writer` / `reader` / `guest` / `none` — mirrored from ACL
state by the SDK onto the tech-space row: one mirror pass when the
space loads, then one per applied ACL record (grants, permission
changes, removals), so a demotion by another peer lands as a row
update. A plain row field like `push`: `GET /v1/spaces` list rows
carry it, and the raw rows on `POST /v1/spaces/query[/subscribe]`
stream role changes live. Use it to gate role-dependent UI straight
from the space list — no per-space `GET …/members/me` fan-out.
Two caveats: `"none"` doubles as "not mirrored yet" (a space this
device hasn't loaded since the field shipped, a pending join, a
tombstoned row) — treat it as "unknown / no access", with
`GET /v1/spaces/:spaceId/members/me` as the authoritative per-space
read when it matters. And on a 1-1 space both participants report
`writer` (the ACL owner slot is a synthetic shared key nobody holds),
so don't gate owner-only actions on `ownRole == "owner"` for
`spaceType:"any.onetoone"` rows.

`SpaceInfo.settings` is the **account-private, client-owned** per-space
settings object (free-form single-level keys, scalar values) — written
per key via `PATCH /v1/spaces/:spaceId/settings` (§ Per-space
settings), synced across the account's own devices through the tech
space, never visible to other members. Omitted when never written.

`SpaceInfo.push` is the space's push-notification key material —
`{spaceKey, encKey, encKeyId}`, mirrored from ACL state by the SDK so
mobile clients can cache it and decrypt push payloads while `any` is
not running. A plain row field, so `GET /v1/spaces` list rows carry it
too, and the raw rows on `POST /v1/spaces/query[/subscribe]` stream
rotations live (`encKey`/`encKeyId` change when the ACL read key
rotates). Omitted until the SDK's per-space mirror has run — e.g. a
joiner whose access is still pending. Full receiver contract — cache
rules, keystore placement, decrypt steps — in docs/20-push.md
§ Receiver-side keys.

#### Derived spaces

Well-known per-account spaces derived deterministically from the
account keys and a fixed seed: the same account resolves the same
spaceId on every device, so all clients and devices converge on *the*
space without a create/find handshake (no check-then-create races, no
duplicate "agent space" per client). The vocabulary is a small
**embedded registry** compiled into `any`
(`internal/server/derivedspaces.go`; seeds follow the
`any/space/<name>/v1` convention) — v1 entry: `bao`, the account's
agent space.

```
GET  /v1/spaces/derived        → 200 {spaces: [{name, spaceId, created, status?}]}
POST /v1/spaces/derived/:name  → 201 SpaceInfo   (404 space.derived_unknown,
                                                  409 space.deleted)
```

- **GET resolves, never creates.** Ids are computed once at engine boot
  (`Service.DeriveId` — pure computation over the account keys);
  `created` reports whether a usable tech-space row exists —
  materialized here or on any of the account's devices (rows sync).
  `status` is the raw row status when a row exists; a `deleted` row
  (wedged before the permanence guard existed) reports `created:false`.
- **POST materializes lazily and idempotently** (`Service.Derive`) and
  returns the full single-space `SpaceInfo`. On first materialization the registry's display name is
  written as the space name (`DeriveRequest.Name` — not part of the
  id derivation; a later rename via `PATCH /v1/spaces/:id` wins).
  Repeat calls land on the same space; a tombstoned row is refused
  with `409 space.deleted` rather than reported as success. Typical
  consumer flow: one POST at boot, then use the id like any other
  space.
- **Derived spaces are permanent.** `DELETE /v1/spaces/:spaceId`
  refuses them with `409 space.derived_undeletable` — the
  deterministic id means delete + re-derive would replace history, and
  the sticky deleted tombstone would wedge the well-known id for the
  account's lifetime. Enforcement is layered: the server pre-checks the
  boot-resolved registry ids (covers not-yet-materialized entries), the
  SDK refuses rows carrying the synced `derived` flag
  (`space.ErrIsDerivedSpace`), its space-index handler drops
  `remoteStatus=deleted` writes on flagged rows from any peer, and its
  deletion reconciler exempts them. `SpaceInfo.derived` surfaces the
  flag; joiners of someone else's derived space never carry it, so
  their removal stays allowed. Rollout caveat: a device still running a
  pre-guard binary can locally delete the space it materialized —
  upgrade all of an account's devices before relying on permanence.
- `spaceType` is `any.space` — derived spaces are ordinary spaces in
  every other respect (members, invites, datasets, search).
- **Migrating from an ad-hoc agent space**: accounts that already carry
  a client-created agent space (e.g. a space named "bao" minted by an
  older agent runtime) get a SECOND, derived space from the registry —
  the registry id is the convergence point going forward; move or
  re-import content from the legacy space, don't alternate between
  them.

#### One-to-one (direct) spaces

A **1-1 (direct) space** is shared by exactly two identities, derived
deterministically from both account keys: both peers compute the *same*
spaceId (order-independent), the same immutable ACL (both as writers), the
same read key — there is **no owner/invite handshake** at the crypto
layer. The peer's account identity is the `id` from their `GET
/v1/account`, exchanged out-of-band. Authoritative SDK contract:
`any-sync-sdk/docs/13-one-to-one-spaces.md`.

```
POST /v1/spaces/one-to-one                  { otherIdentity }              → 201 SpaceInfo
POST /v1/spaces/one-to-one/register-incoming { peerIdentity, displayHint? } → 204
POST /v1/spaces/:spaceId/one-to-one/accept                                  → 200 SpaceInfo
POST /v1/spaces/:spaceId/one-to-one/decline                                 → 204
```

Because the ACL is immutable (nothing to accept *cryptographically*),
"approve incoming" is a **local SDK gate** governing whether *this device*
materializes and syncs the derived space — surfaced as space `status`
values, not ACL operations:

- **Initiate / accept-by-peer** — `POST /v1/spaces/one-to-one`
  (`Service.OneToOne`). Derives the space and activates it immediately
  (implicit self-approval → `status:"active"`). Idempotent; overrides a
  prior local decline (un-decline). Returns 201 with the `SpaceInfo`
  (`type`/`spaceType` = `any.onetoone`). `400 request.missing_field`
  when `otherIdentity` is empty; `400 request.invalid_field` for an
  undecodable identity or self-pairing (`details.reason:"self"`).
- **Incoming → pending.** When a peer reaches out, the other side learns
  of it one of two ways: (a) automatically, via the SDK's coordinator
  **inbox notifier** (auto-started in `sdk.Open`, see `docs/02-server.md`),
  or (b) out-of-band, by the app calling `POST
  /v1/spaces/one-to-one/register-incoming` with the peer's identity (+ an
  optional `displayHint` `{name, description, iconCid}` for the UI). Either
  way a **device-local** row appears with `status:"one_to_one_pending"`
  and **no storage materialized**. `register-incoming` is idempotent
  (no-op if a row already exists) and returns 204.
- **Accept** — `POST /v1/spaces/:spaceId/one-to-one/accept`
  (`Service.AcceptOneToOne`). Approves a pending row by space id (the peer
  identity is read off the row, so the caller needn't re-derive it),
  materializes + activates it. Equivalent to re-running `OneToOne(peer)`;
  idempotent. Returns 200 with the activated `SpaceInfo`.
- **Decline** — `POST /v1/spaces/:spaceId/one-to-one/decline`
  (`Service.DeclineOneToOne`). Writes a **synced sticky** marker
  (`status:"one_to_one_declined"`) suppressing the request on every device;
  it never auto-resurfaces. A later explicit `POST /v1/spaces/one-to-one`
  overrides it. Returns 204.

**Discovery has no bespoke endpoint** — incoming requests are the space
list filtered on the new status: `GET
/v1/spaces?status=one_to_one_pending` (pending and declined rows are
non-active, so they're hidden from the active-only default list, like
`deleted`), or `POST /v1/spaces/query[/subscribe]` over the `spaces`
dataset for a live view.

**Deletion is local-only.** A 1-1 is derived and not node-owned, so
`DELETE /v1/spaces/:spaceId` offloads it locally and propagates the
offload to the account's other devices, but never removes it from the
nodes — a later `POST /v1/spaces/one-to-one` re-derives and re-materializes
it from scratch.

#### Direct-add invites (added to a space by identity)

The counterpart of `POST /v1/spaces/:spaceId/acl/add`: when another
account adds this account to a regular space **by identity** (one ACL
record per batch — the SDK notifies every added account through the
coordinator inbox, durably retried), the space surfaces here as a
**synced** pending row. The account is already a full ACL member; like
the 1-1 gate, approval only governs whether the space is materialized —
nothing is downloaded until accepted. Authoritative SDK contract:
`any-sync-sdk/docs/15-direct-add-invites.md`.

```
POST /v1/spaces/:spaceId/invite/accept    → 200 SpaceInfo | 202 SpaceInfo
POST /v1/spaces/:spaceId/invite/decline   → 204
```

- **Incoming → pending.** The SDK's inbox notifier registers the row
  autonomously with `status:"invite_pending"` — synced account-wide
  (unlike the device-local 1-1 pending), carrying the sender-supplied
  name hint until the real metadata syncs after accept. Discover via
  `GET /v1/spaces?status=invite_pending` — no bespoke endpoint,
  mirroring the 1-1 pattern.
- **Accept** — `POST /v1/spaces/:spaceId/invite/accept`
  (`Service.AcceptInvite`). Flips the synced status to active (every
  device converges) and loads the space. `200` with the loaded
  `SpaceInfo` when content is pullable now; `202` when the accept is
  recorded but loading continues in the background (crash-safe — poll
  `GET /v1/spaces/:spaceId` for the flip). Idempotent; also overrides a
  prior decline. `404 space.not_found` for unknown ids,
  `409 space.not_invite_pending` when the row isn't awaiting approval,
  `400 request.invalid_field` for 1-1 rows (use the one-to-one
  endpoints).
- **Decline** — `POST /v1/spaces/:spaceId/invite/decline`
  (`Service.DeclineInvite`). Writes a **synced sticky, non-terminal**
  marker (`status:"invite_declined"`) suppressing the invite on every
  device; a later accept overrides it. **No ACL change** — the account
  remains a member on the space's ACL (self-remove is a follow-up).
  Returns 204.

#### Query / subscribe the space list

`GET /v1/spaces` (`Service.List`) stays the mapped convenience — it
returns the public `SpaceInfo` shape (status / ownRole projected from the
raw tech-index rows — the raw rows carry the same `ownRole` string
label, device-local). For a **filterable / sortable / live** view, the
generic windowed primitive reads the tech-space `spaces` dataset
directly:

```
POST /v1/spaces/query              snapshot   → { records, total?, hasNext? }
POST /v1/spaces/query/subscribe    SSE        → ready → snapshot → changes → closed
```

Both wrap `Service.Query(SpaceIndexObjectId(), "spaces")` and take the
same body as the per-object `…/query` endpoints (`filter` / `sort` /
`limit` / `offset` / `includeTotal` / `mailboxCapacity` /
`driftBudgetPercent`), plus an optional `dataset` override. `objectId` is
fixed server-side to the tech-space index object. `dataset` is restricted
to the closed allowlist `{spaces, profile}` (defaults to `spaces`) —
anything else returns `400 request.invalid_field`. The tech-space index
object also hosts the `identities` directory, whose rows carry a synced
decryption key; it is deliberately **not** reachable here — read it
through `GET /v1/identities`. Records are the **raw**
tech-index rows (not the mapped `SpaceInfo`) — use `GET /v1/spaces` when
you want the projected status/role. Rows carry `createdAt` as an
instant — `{"$date": "<RFC 3339>"}`, the handler-derived added-to-account
time, absent on pre-stamp rows — so newest-first creation ordering is
`{"sort": ["-createdAt"]}` and a range filter takes the same shape
(`{"$gte": {"$date": "…"}}`). The mapped `SpaceInfo.createdAt` on
`GET /v1/spaces` stays a plain RFC 3339 string.
The subscribe frame set and `closed`
reasons are identical to the per-object `…/query/subscribe` (see the Data
plane § Subscribe and `docs/04-events.md`); a space joined on another
device or head-synced in arrives as an `added` change.

#### Dataset schema discovery

```
GET /v1/spaces/:spaceId/datasets   → { datasets: [ { name, schema, owners?, module, shared? } ] }   Space.Datasets
GET /v1/datasets                   → { datasets: [ { name, schema, module } ] }                    Service.Datasets
```

`schema` is a standard **JSON Schema** object per dataset
(`{type:"object", properties:{…}, additionalProperties:<dynamic>}`).
`name` is the collection — what `dataset` names on every read and
write. `module` is the serving module (`records` for schema-enforced
runtime datasets, `chat` / `editor` for the compiled-in ones, absent
only on the SDK's own system datasets). `owners` lists the types whose
parts declare the collection: the one declaring type of a namespaced
`<typeId>_<key>` instance, every type sharing a module's canonical
collection (`shared: true` — `editor_blocks`, `chat_messages`). A
collection lives on an object only while the object carries one of its
owners, so consumers gate indexing/eviction on that set; a canonical
collection nothing declares yet is listed without `owners`, and no
object can hold it until a type declares the module (§ Parts and
modules). Each property carries an `x-scope` extension keyword
classifying the field:

- `synced` — user/DAG-written, synced to everyone in the space;
- `derived` — handler-computed, read-only to writers (e.g. chat
  `creator` / `createdAt`);
- `local` — device-local, never synced (e.g. chat `unread` /
  `unreadMention` / `unreadReactions` — written via the local-scope
  `POST …/modify` route, § Modify records);
- `account` — synced across this account's devices only, invisible to
  other members (declarable on property definitions today; dataset
  record fields await the SDK's record-level account transport).

`additionalProperties:true` marks a dynamic dataset (free-form keys
allowed, defaulting to synced — e.g. the per-type `objects` namespace and
the chat/editor collections, which declare their known fields while
staying open). The per-space form lists every collection the space
hosts (`objects`, the module canonicals `chat_messages` /
`editor_blocks`, every namespaced instance and runtime definition the
space's types declare); the account-wide form lists the tech-space
system datasets (`spaces`, `profile`) behind the space-list
query/subscribe above.

Datasets with behavioral schema declarations (§ Runtime dataset schemas)
carry further extension keywords in the document:

- per-field `x-mutable-by` (`author` / `any`; absent = write-once) and
  `x-stamp` (`creator` / `createTime` / `modifyTime` — derived at apply
  time, client writes rejected);
- doc-level standard `required` (fields that must be present on
  create), `x-delete-by` (`author`; absent = anyone may delete),
  `x-id` (`user`, with `x-id-pattern` / `x-id-max-length`; absent =
  auto-derived record ids), and `x-search` (`{title, text, scope}` —
  the record fields the search indexer extracts and the index scope
  the entries land under, § docs/13-index.md).

#### Update space metadata

`PATCH /v1/spaces/:spaceId`

```json
{ "name":        "Project Phoenix",
  "description": "shared notes + chat",
  "iconCid":     "bafy..." }
// → 204
```

All three fields are optional but at least one must be present —
an all-empty body returns `400 request.missing_field`. Field semantics
mirror the SDK's pointer-to-string contract: a key absent from the JSON
body leaves the field unchanged; a key present with an empty string
clears the field. `spaceType` is intentionally not patchable; it's
pinned by the initial Create.

The write lands on the in-space `spaceIndex` object's `properties`
dataset and CRDT-replicates to every member. Each peer's indexer hook
mirrors the converged state into its own local tech-space row.
Because the mirror runs asynchronously (subscription delivery, not
in-line with the local write), an immediate follow-up `GET
/v1/spaces/:id` may briefly return the pre-patch values. Callers that
need the converged state poll, or attach a `…/objects/query/subscribe`
stream filtered on `spaceIndexObjectId`.

#### Per-space settings (account-private)

`PATCH /v1/spaces/:spaceId/settings` → `Spaces().SetSettings`

```json
{ "set":   { "notifyMode": "mentions" },
  "unset": [ "someOldKey" ] }
// → 204
```

A per-key patch of the `settings` object on the space's **tech-space
row** — deliberately separate from `PATCH /v1/spaces/:spaceId`, which
writes the *member-replicated* spaceIndex (name / description / icon).
Mixing account-private and member-visible writes on one endpoint is a
trap; these are different scopes with different audiences.

- **Account-private by construction**: the tech space is per-account
  (owner-only ACL), so settings sync across the account's own devices
  and are invisible to other space members.
- **Keys** are the caller's vocabulary — non-empty, single-level (no
  dots; a dotted key would silently become a deeper CRDT path). Push
  claims `notifyMode` (`all | mentions | none`, `docs/20-push.md`);
  other client settings are welcome to live alongside.
- **Values** are scalars only: string, number, or bool.
- At least one `set` or `unset` entry is required
  (`400 request.missing_field`); a key may not appear in both
  (`400 request.invalid_field`).
- Works on **any row the account knows** — deleted tombstones and
  pending 1-1s included (mute a pending 1-1 before accepting). Unknown
  ids return `404 space.not_found`.

Reads are passthrough — no bespoke read endpoint: `SpaceInfo.settings`
on `GET /v1/spaces[/:id]`, or the raw rows from
`POST /v1/spaces/query[/subscribe]` for live cross-device updates.

#### Force a head-sync round (sync now)

`POST /v1/spaces/:spaceId/sync`

```
// → 204 (no body)
```

Wraps `Space.SyncHeads`: forces an immediate head-sync (diff) round
against the space's responsible nodes instead of waiting for the
periodic headsync timer. The call **blocks** server-side until the
round completes, then returns `204`. Normal operation never needs this
— periodic + reactive sync keep a space current on their own — it
exists for on-demand convergence: a manual "sync now" button, or
collapsing the multi-peer convergence wait in tests from "next periodic
headsync (~30s)" to "as fast as the diff round settles." A single round
exchanges heads with the node; for a writer→reader handoff, sync the
writer first (push to the node) then the reader (pull back).

#### POST /v1/spaces/:spaceId/search — local search index

The **one sanctioned endpoint that does not map 1:1 onto an SDK
method**: it queries the server's local search index (FTS + vector over
the chunker feed — contract, scopes, and indexing pipeline in
`docs/13-index.md`). Requires `index.enabled` (default true); `409
index.disabled` otherwise.

Body:

```json
{
  "query":   "zeppelin disaster",     // required; supports "phrases" and prefix* on the FTS leg
  "scopes":  ["chat", "basic"],       // optional scope slugs (open set — see docs/13-index.md); empty = all
  "limit":   10,                      // optional: max records (default 10, max 100)
  "mode":    "hybrid",                // optional: hybrid (default) | fts | vector
  "require": ["1937"],                // optional must-have terms (phrase/prefix ok); enforced in every mode
  "exclude": ["fiction"],             // optional must-not terms
  "maxData": 512,                     // optional: runes of `data` per hit around the first match (default 512; -1 = whole chunk)
  "passages": 3                       // optional: further matching chunks per record on hit.passages (default 0, max 10)
}
```

User property values index by default under the dedicated scope
`props` (self-describing `"<prop name>: <value>"` entries; opt-out per
property via `meta.index: "none"`). Props docs are FTS-only — they
surface through the FTS leg (hybrid included) but never through
vector. A content-only search passes `scopes` without `props`.

Reply:

```json
{
  "hits": [
    { "scope": "chat", "objectId": "…", "dataset": "chat_messages",
      "recordId": "…", "data": "the zeppelin disaster of 1937",
      "dataTotal": 29, "score": 0.0328 },
    { "scope": "email", "objectId": "…", "dataset": "email_messages",
      "recordId": "…", "chunk": 2, "data": "…the Hindenburg burned at…",
      "dataOffset": 1210, "dataTotal": 1984, "score": 0.0161 }
  ],
  "mode": "hybrid",
  "vectorStatus": "used"
}
```

**One hit per record; `limit` counts records.** Long records are
indexed as several chunks (~2000 runes each, `docs/13-index.md`
§ Chunking long records); the hit shows the record's best-ranked chunk
— `chunk` (omitted when 0) says which — and `limit` distinct
`(objectId, dataset, recordId)` come back whenever the index holds
that many matches, however many chunks one record contributes. The
other matching chunks of a record are available on request:
`passages: N` (max 10, `400 request.invalid_field` above) adds up to N
further chunks per hit as `passages: [{chunk, data, dataOffset,
dataTotal, score}]`, best first — the chunks that ranked within the
search window, not every chunk of the record. Each leg reads until its
window covers enough records (at most 1000 chunks deep), so the reply
can still hold fewer than `limit` records when one record dominates
that whole window (`docs/13-index.md` § Search).

**`data` is a bounded window, not the record.** Within a hit (and each
passage), `data` is at most `maxData` runes (default 512) cut around
the first query / `require` term, the head when nothing matches
literally; `dataOffset` (omitted when 0) is the window's rune offset
into the chunk's indexed text and `dataTotal` that text's rune length
— `data` is the whole chunk iff `dataOffset` is 0 and its rune length
equals `dataTotal`. `maxData: -1` returns the whole chunk; `maxData <
-1` is `400 request.invalid_field`. A reply is bounded by `limit ×
(1 + passages) × maxData` runes of text (chunk size, ~2000 runes, in
place of `maxData` when it is -1). The full record is one
dataset query away (`docs/08-clients.md` § 6).

`require` / `exclude` are a contract on every returned hit, whatever
the mode: the FTS leg matches on them, and vector hits (hybrid and pure
`vector`) are post-filtered against the FTS index before fusion, so
fusion can never re-admit a hit the lexical leg would have refused.
Terms are matched by the index analyzer — a `"phrase"` or `prefix*`
term behaves as it does in `query`. Both legs enforce them through the
full-text index, so a build compiled without it (`fts` build tag,
`docs/13-index.md` § build tags) refuses a request carrying them with
`409 index.terms_unsupported` rather than answering with the constraint
ignored.

`mode` in the reply is the mode that actually ran: `hybrid` degrades to
`fts` when no embedder is configured, it is unreachable, or the query
embedding does not finish within the server's budget
(`index.search.queryEmbedTimeout`, default 5 s); `mode:
"vector"` requests get `400 index.no_embedder` (none configured) or
`503 index.embedder_unavailable` (configured but down or too slow —
retryable).

`vectorStatus` tells the consumer — typically an agent deciding how
much to trust recall — whether semantic search took part, and why not:

| Value | Meaning |
|-------|---------|
| `used` | the vector leg ran and contributed to ranking |
| `unavailable` | embedder configured but unreachable or over budget for this query — results are lexical-only; retrying later may differ |
| `disabled` | no embedder configured on this server — vector can never run until config changes |
| `skipped` | the caller asked for `mode: "fts"`; vector was not attempted |

Scores are comparable only within one response
(BM25 for fts, cosine similarity for vector, RRF for hybrid). The index
covers content written while indexing is on — "index from the next
change" (`docs/13-index.md`).

### Bundles

A **bundle** is one thing installed into a space — a chat, a
marketplace bundle, an app's setup. It is one root object registered in
the space's registry (the `bundles` dataset on the spaceIndex object;
design in the SDK's `docs/bundles.md`), with every setup object derived
from that root, so one converged id names the whole install.

Clients register their own; the server installs nothing on a client's
behalf. What it does own is the registry mechanics — picking the winner
when two devices install concurrently, and refusing to delete a losing
root before it has stopped arriving — and one id namespace: **ids under
`system:` are the server's**, installed only through its embedded
catalog (§ Catalog, `docs/28-well-known-bundles.md`), so a client
ensure with such an id is `409 bundle.reserved`, before any wait.
Reads, resolve and children on a `system:` id work like on any other.

```
POST   /v1/spaces/:spaceId/bundles                        → 200 {bundle, installed}
GET    /v1/spaces/:spaceId/bundles                        → 200 {bundles: [...]}
GET    /v1/spaces/:spaceId/bundles/:bundleId              → 200 Bundle
POST   /v1/spaces/:spaceId/bundles/:bundleId/resolve      → 204
POST   /v1/spaces/:spaceId/bundles/:bundleId/children     → 200 {objectId}
```

**Bundle ids carry a slash** (`general-chat/v1` — the version suffix is
part of the id, and ids are permanent: a successor install takes a new
one, since record deletes are refused and a reused id could never be
reclaimed). In a path segment the slash is percent-encoded:
`/bundles/general-chat%2Fv1`. Request bodies take the id verbatim.

**Ensure** (`POST …/bundles`) is adopt-or-install:
`{id, name?, rootTypes?, rootProperties?, derived?, parts?, properties?,
xKey?, layout?, weight?, hidden?}`. With a winner already
registered it is a pure read — nothing is written, so a reader or guest
member can resolve an install they could not create — and the reply is
`installed: false`. That flag means "this call registered the install":
a derived adopt can still materialize the root's tree locally (the id
is this device's to mint) and reports `false`, because it registered
nothing. Otherwise the server creates the root object with
the requested types and initial properties, registers it in one change,
and replies `installed: true`; that path is a write, so a member
without write permission gets `403` (use `GET …/bundles/:bundleId`
instead). `name` is stamped as `any.name` on the root, which is also
what puts the root's tree in the head-sync diff. The `id` is the whole
identity — a marketplace id, an app slug, a versioned convention like
`general-chat/v1` — so there is no separate provenance field.

Installing waits for the registry to converge first (bounded, 30s —
cut to 3s when no peer is connected, since a head-sync round against
nobody answers the same way every time). It rides the space's index
tree, and a member that ensures against state it has not synced yet
reads "nothing installed" and mints a root competing with the one
already out there. Adopting never waits — a read cannot fork anything. A space that has never been set up
converges to an empty registry, which is a valid answer, not a stall.

When the wait cannot complete, who is asking decides: the space's
**owner** installs anyway (nobody else could have installed into a
space only this account has, and its own devices converge through the
registry), so an offline owner is never blocked. Any other member —
a joiner, either side of a 1-1 — is refused with
`409 bundle.not_ready` rather than left to fork, and retries when the
network is back. A **derived** root (below) is never refused *for lack
of convergence* — there is no competing id it could mint — though it
still reports `409 bundle.not_ready` when the live winner is a created
root whose tree has not arrived.

Two devices that install while genuinely apart still each register a
root; the registry converges on one winner and the other appears in
`losers` — so **`rootId` is provisional until the space syncs**, and
clients re-read after. A winner whose tree has not reached this device
yet is likewise `409 bundle.not_ready` rather than handed out: its id
would reject every write. Retry.

**Derived roots.** `"derived": true` installs the bundle on the root
**derived from its id** instead of a created one. That id is a pure
function of (space, bundle id) — the derived root change carries no
identity, signature or timestamp — so every device and every member
computes it offline, with zero communication. Nothing can fork: each
side registers the same id, the claim set converges to one element and
`losers` stays empty. The reply and every read report `derived: true`.

This is the answer for a space's chat, and the only workable one for a
**1-1**: its ACL owner is a synthetic key nobody holds, so both
participants are writers, neither can ever claim the owner escape, and
a created root leaves both refused until they converge — which never
happens while they are apart. With a derived root each side installs
immediately and they meet on the one object; the two copies merge like
any other CRDT tree.

The price is permanence, in two directions:

- **No uninstall.** any-sync refuses to delete a derived tree, so the
  bundle id stays bound to that root for the space's lifetime. There is
  no reinstall-after-delete escape. Ask for it for setups that must
  exist on both sides of a partition; not for anything a user may
  remove.
- **No migration.** A bundle already installed on a created root is
  ADOPTED, not moved: the reply is `installed: false`, `derived: false`,
  and the created `rootId`. Moving content between roots is the client's
  decision, never a side effect of a flag.

If a created and a derived root are ever both claimed for one id, the
derived one wins on every device and the created one becomes an
ordinary resolvable loser. The verdict itself reads only the add-only
claim set, so every device reaches the same one — but **the claim can
still be made blind**. A derived install runs the same convergence wait and, unlike a created
one, installs anyway when it expires; if the space already carried a
created install this device had not seen, that claim demotes it,
irreversibly. Nothing is destroyed — the demoted root
keeps its content and stays deletable — but the app's pointer moves,
which for content that cannot be merged across objects (chat) amounts
to the same thing. The wait is what narrows that window; proceeding
past it is the deliberate trade that lets an offline 1-1 have a chat at
all — and with no peer connected there is nothing to narrow, so the
wait collapses to its offline bound and the chat appears in seconds.

**Bundle-declared types.** A bundle may declare a full type on its
root — `parts`, `properties` or an `xKey` make the root implement
itself as a type (`any.types = ["__type__", "<rootId>"]`, `typeId =
rootId`, readable through `GET …/types/:rootId` and its `parts` /
`properties` / `datasets` routes); `layout`, `weight` and `hidden`
describe that type and ride along (alone they are
`400 request.invalid_field`).

- `parts: [...]` (the same draft shape as `POST …/types/:typeId/parts`,
  ≤32 entries) declares parts and the datasets under them. A records
  dataset the bundle declares is namespaced to the root: its
  collection is `<rootId>_<key>` (read it off `collection` in the
  parts list), and records go through `POST …/upsert` / `…/modify` /
  `…/query[/subscribe]` with `objectId = rootId` and that collection as
  `dataset`. A part naming a module (`{"module": "chat", "shared":
  true}`) makes the root hold that module's canonical collection —
  this is how the well-known chat bundle gives a space its chat. A part
  naming a module reserved to the server (§ Parts and modules) is
  `400 dataset.module_reserved`.
- `properties: [...]` (the same draft shape as `POST
  …/types/:typeId/properties`, ≤64 entries, ≤64 KiB) declares property
  definitions, so the root is a type **objects carry** — a wiki's
  `parentId` / `pos`. Every draft carries an `xKey`, unique in the body
  (`400 request.missing_field` / `400 request.invalid_field`), because
  the **property id is derived from (rootId, xKey)**: two devices that
  install while apart mint ONE column per handle, not the two the
  descriptor model otherwise allows (docs/27-descriptors.md § Handles)
  — a forked tree is not a repairable outcome. Resolve `xKey → propId`
  through `GET …/types/:rootId/properties`; a property added later
  through `POST …/types/:rootId/properties` gets an ordinary id. Each
  draft passes the property gate (kind required, descriptor against
  kind, `meta` narrowed to `index`).
- `xKey` is the type's handle (the same meaning as on `POST …/types`):
  what a client resolves the type by, and what `relation.targetTypes`
  in other declarations name. An xKey **alone** declares a marker type
  — no columns, no parts, a flag objects carry. Unique among the
  space's types: an install whose xKey a type in the space already
  holds (as its xKey or its id, hidden or not) is
  `409 type.xkey_conflict` (`details: {xKey, existingTypeId,
  bundleId}`), checked on the install path only — an adopted root
  carries the handle by design and never conflicts with itself.
  Written on install. A writer's adopt fills in a handle the root
  lacks (an install that predates it); an existing handle is never
  changed.
- `layout` / `weight` (the type's rendering slice, § Types) and
  `hidden` are written with the root's name on install. **`hidden` is
  explicit**: a root that only hosts its bundle's records (favourites,
  an app's setup) should ask for it — a listed type is one a picker
  offers for attachment elsewhere, which would grant that object the
  bundle's collections — while a root that is a type objects carry (a
  page, a wiki) stays listed.

An install writes the root as **root + up to 3 changes**: one `objects`
change carrying the types (`__type__`, the root's own id, `rootTypes`),
`any.name`, the type metadata (`type.xkey` / `layout` / `weight` /
`hidden`) and the seeded `rootProperties` values; then, after the
registry row, one `datasets` change when the bundle declares parts and
one `properties` change when it declares properties. Each dataset
lands atomically; a peer may briefly see the parts before the property
definitions. A bundle with no declaration (a bare miniapp) mints its
root through the ordinary object create plus the name stamp. Declared
once on install. An
adopt heals what is **absent** and never patches — parts only on a
root carrying no part declaration at all, properties per handle (a
definition the root lacks is written; one it carries under any id, or
removed through `DELETE …/types/:rootId/properties/:propId` — the
tombstone keeps the id — is left alone, so nothing is doubled or
resurrected). Later evolution is `POST/PATCH/DELETE …/types/:rootId/parts…`,
`…/datasets…` and `…/properties…` — `Ensure` never patches, adds or
resurrects a declaration, and never touches the root's name, layout,
weight or hidden flag once stamped. A malformed declaration (unknown
module, a field on a module dataset, a duplicate key, a property
without an xKey) fails before the permanent root is derived. The
declaration combines with `derived: true` or stands alone (a created
root the server mints and self-types). `rootTypes` / `rootProperties`
ride every root: a created one with no declaration, a derived one, and
the created root of a request that declares a type — where they land
in the root's first change next to its own type, so one object can be
both a type and a carrier of another (the wiki root: the type its
pages carry and a `miniapp`).

Input is bounded and pre-flighted: `id` ≤256 B, `xKey` ≤256 B, `name` ≤1024 B,
`rootTypes` ≤32 entries, `rootProperties` ≤64 KiB, `parts` ≤32
entries / 64 KiB, `properties` ≤64 entries / 64 KiB. Type ids must exist
in the space (`400 type.not_found` — the create path would otherwise
drop an unknown type and report success) and property values must fit
their descriptor slug (`400 property.format_violation`); both are
checked BEFORE the root is created, so a rejected request never leaves
an orphan object. Bundle records are **permanent** — the registry
refuses record deletes, so an id is spent for the space's lifetime, and
`roots` only ever grows: deleting a root and re-ensuring appends
another claim rather than replacing one. The registry rides the
eagerly-loaded spaceIndex on every device, so treat ids as a small
fixed vocabulary, not a scratch namespace.

#### Tech-space bundles

Account-level product data — favourites, pinned items, personal
settings — lives in bundles on the account's **tech space**, whose id
`GET /v1/account` returns as `techSpaceId`. The tech space is a valid
`:spaceId` for:

- `bundles` ensure / get / list / resolve — a type declaration
  required (`parts`, `properties` or an `xKey`), roots minted by Ensure
  (`rootTypes` / `rootProperties` / `children` refused). The normal shape is the default CREATED root — deletable
  (`DELETE …/objects/:rootId` = uninstall; the id then reads as not
  installed and a fresh install works), forking on concurrent offline
  installs and resolving like in any space. `derived: true` is the
  EXCEPTION, not a peer option: a permanent, uninstallable root,
  justified only when a fork would be unmergeable (chat-like content —
  the general-chat convention, above all in a 1-1, where the
  convergence gate cannot work). Records-shaped bundles merge, so they
  are created;
- `types` reads and `types/:rootId/parts…` / `types/:rootId/datasets…`
  / `types/:rootId/properties…` on bundle roots;
- records on bundle roots: `query[/subscribe]`, `modify`, `upsert`,
  `delete-records`, `aggregate`; `GET …/objects/:objectId`;
- `GET` the space (a synthetic row: `spaceType: any.techspace`,
  owner, derived), `sync-status`, `debug`, `sync`.

Everything else — `POST …/objects`, `DELETE …/objects/:id`,
`POST …/types`, properties, members, invites, guest key, ACL, files,
chat, editor, history, search, `PATCH`/`DELETE` the space, settings —
returns `405 space.unsupported`. On the tech index object
(`spaceIndexObjectId` of the tech row) reads are limited to the
`spaces`, `profile` and `bundles` datasets (guest keys stripped from
`spaces` rows; `identities` stays behind `GET /v1/identities`) and
generic writes are refused.

**Locked reads.** `GET …/bundles` and `GET …/bundles/:bundleId` answer
only after the space's registry convergence wait (local fast path when
already synced; fast expiry with no reachable peer), and the reply
carries `synced`: true means an absent bundle is definitively not
installed; false (cold offline device) means absence is provisional.
`GET …/bundles/:bundleId` returns `{bundle, synced}`. The ensure POST
already runs the same wait as its convergence gate. The raw `bundles`
dataset via `POST …/query[/subscribe]` stays the live local view.

**Favourites** is a client-registered bundle (`favorites/v1`, created
root, an `entries` part) — a documented convention like
`general-chat/v1`, no server code. Model and client contract:
`docs/25-favorites.md`.

**Reads.** `GET …/bundles` lists the live rows as of local state;
`GET …/bundles/:bundleId` reads one (`404 bundle.not_found`). Rows are
also readable through the ordinary dataset surface —
`POST /v1/spaces/:spaceId/query` with `{"objectId":
"<spaceIndexObjectId>", "dataset": "bundles"}` — which is how a client
subscribes to live conflict updates. That path is read-only: the SDK
fences the dataset off the generic modify surface so no client can
forge a claim. Raw rows carry the stored `rootId` register and no
`derived` field — the derived-root verdict is applied by
`GET …/bundles[/:bundleId]`, so read those when a bundle may be
derived.

**Children** (`POST …/bundles/:bundleId/children`, `{seed, types?}`)
derive a setup object under the bundle's current winner. Same semantics
as the objects derive: deterministic per (space, root, seed),
materialized on the first call, the same id on every device — a
restored device reaches the whole install from the winner alone — and
cascade-deleted with the root. Seeds are permanent. A child binds to
its parent's tree, so on a member whose copy of the winner has not
landed yet the call is `409 bundle.not_ready` — the same retryable
state Ensure reports. Under a **derived** root the child binds by seed
instead (any-sync rejects a derived object as a parent): same
determinism, same ids everywhere, and the cascade the parent binding
buys is moot on a root that can never be deleted.

**Conflicts.** `losers` is the live conflict set: claimed roots that
are neither the winner nor already deleted. Non-empty means two devices
installed concurrently and the loser may hold real content, so cleanup
is the client's call: merge what matters out of the losing root and its
children, then `POST …/bundles/:bundleId/resolve` with
`{loserRootId}`, which cascade-deletes it. The server never merges —
only the client knows what the content means.

What the server does enforce is timing. A losing root arrives change by
change, so a merge made from a half-arrived tree is a half-merge:
resolve is refused with `409 bundle.loser_not_ready` until the SDK
reports the root fully **synced** — an unknown or still-syncing tree
never qualifies — and it has been observed as a loser for a grace
period (5 min). The clock starts when the conflict first became
visible on this device (any `GET …/bundles[/:id]` or the boot pass
counts), not at the first resolve call, so a client that showed the
user a conflict and got an answer is not made to wait again. A restart
restarts the clock, which only ever delays a deletion.

After a timing refusal the server keeps retrying in the background (the
merge decision is already made; only the timing was missing) — one loop
per losing root however often you poll, in-memory and dropped on
restart, so clients retry too. Resolving the winner, or a root never
claimed for the bundle, is `409 bundle.not_loser`; a root already
resolved returns 204 — the call is idempotent.

**Restore.** On boot the server converges the space list and projects
the space index for the well-known derived spaces, so a client ensuring
right after a restore meets the account's converged registry instead of
an empty one and does not mint a competing root. It installs nothing
and deletes nothing itself.

**Agreeing who installs.** Nothing stops two members from ensuring the
same bundle; the registry just converges and reports a loser. Clients
that want to avoid the conflict entirely either agree on one installer
out of band, or ask for a `derived` root — the id both would compute
anyway, which is what the per-space chat convention does.

### Catalog

The server's own well-known bundles: a yaml catalog embedded in the
binary, grouped into **usecases** — a set of bundles installed together
plus `requires`, the usecases that must be present first. Every bundle
is one created root under a permanent `system:<name>/v<n>` id,
declaring a `type` objects carry, a `miniapp` the client opens (the
root carries the built-in `miniapp` with `bundle` = its id), `parts`
(records on the root), or several of those; the general chat is the
one `derived` root. The catalog is read-only over HTTP, validated at
build time (`make catalog-validate`, CI, boot refusal), and installs
nothing unless a client asks. Full client contract — model, setup
semantics, handles, rendering, forks, evolution, the shipped entries —
in `docs/28-well-known-bundles.md`.

Account-scoped, behind the auth guard, outside the space group:

```
GET  /v1/catalog                    → 200 {usecases: [CatalogUsecase]}
GET  /v1/catalog/:usecaseId         → 200 CatalogUsecase            404 catalog.not_found
POST /v1/catalog/:usecaseId/setup   → 200 CatalogSetupResponse
     {spaceId}
```

`CatalogUsecase` is the entry as the catalog declares it — `{id, name,
description?, requires?, bundles: [{id, name, description?, derived?,
hidden?, type?: {xKey, weight?, layout?, properties?}, miniapp?,
parts?}]}` — property and part entries in the `POST …/types/:typeId/
properties` / `…/parts` draft shapes. Usecase ids are slugs and need
no encoding in the path.

**Setup** resolves the usecase's transitive dependency closure
(dependencies first, deterministic), runs ONE registry-convergence
wait for the whole list, then per bundle the § Bundles
adopt-or-install: a live winner is adopted (a pure read; a writer's
adopt also heals a property the root lacks by handle and a `miniapp`
value the root lacks, attaching the built-in first when the root
predates it), otherwise the handle check runs (no type in the space
may already hold the bundle's xKey — `409 type.xkey_conflict`, install
path only) and the root is minted with everything the bundle declares
(root + up to 3 changes). Idempotent: a second call adopts everything. A
heal that fails (a permission or sync race) is not an error: the setup
still answers 200 and the next setup retries it. A
failure mid-walk leaves the dependencies it installed, names the step
in `details.usecase` / `details.bundleId`, and the next call resumes.
Readers adopt, writers install; when the wait expires the owner
installs anyway and any other member is `409 bundle.not_ready`.

```jsonc
// CatalogSetupResponse — every bundle the call touched, dependencies
// first, the requested usecase's bundles last
{ "usecase": "contact",
  "bundles": [ { "usecase": "people", "id": "system:person/v1",
                 "bundle": { "id": "system:person/v1", "rootId": "…", "roots": ["…"] },
                 "installed": true, "typeId": "<rootId>", "properties": { "email": "<propId>", "…": "…" } },
               { "usecase": "people", "id": "system:organization/v1", "…": "…" },
               { "usecase": "contact", "id": "system:contact/v1", "…": "…" } ] }
```

`typeId` (the root id) and `properties` (every property on the root
with an xKey, xKey → propId) are present when the bundle declares a
type — `type`, or `parts`; `miniapp` echoes the values a miniapp
bundle declares, `bundle` filled in; `installed` reports whether THIS
call registered the root. Which usecases a space has is read off
`GET …/bundles` — every member row is there under its `system:` id.

Errors: `404 catalog.not_found` (`details.usecaseId`);
`400 request.missing_field` (no `spaceId`); `405 space.unsupported`
(the tech space is not a setup target); the space errors; per bundle
the § Bundles set — `409 bundle.not_ready`, `403` for a member without
write permission on an install, `409 type.xkey_conflict`
(`details.xKey`, `details.existingTypeId`). Every error of the walk
carries `details.spaceId`, `details.usecaseId`, and for a failing
step `details.usecase` + `details.bundleId`. A client `POST …/bundles`
with a `system:` id stays `409 bundle.reserved`. CLI: `any catalog
list | get | setup`.

### Objects

| Method | Path                                                      | Purpose                            |
|--------|-----------------------------------------------------------|------------------------------------|
| POST   | `/v1/spaces/:spaceId/objects`                             | `Objects.Create`                   |
| POST   | `/v1/spaces/:spaceId/objects/query`                       | `Space.QueryObjects.Snapshot`      |
| POST   | `/v1/spaces/:spaceId/objects/query/subscribe`             | `Space.QueryObjects.Subscribe` (SSE) |
| POST   | `/v1/spaces/:spaceId/objects/aggregate`                   | `Space.AggregateObjects` (pipeline) |
| GET    | `/v1/spaces/:spaceId/objects/:objectId`                   | `Objects.Get` — the objects row; `404 object.not_found` / `410 object.deleted` |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId`                   | `Objects.Delete`                   |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/backlinks`         | reverse reference lookup (no SDK method) |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/editor/:collection/markdown`        | render blocks as markdown |
| PUT    | `/v1/spaces/:spaceId/objects/:objectId/editor/:collection/markdown`        | bulk parse markdown → blocks |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/editor/:collection/markdown`        | targeted oldText → newText replacements |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/editor/:collection/markdown/append` | append markdown at tail (no read/diff) |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/editor/:collection/blocks`          | create one block         |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/editor/:collection/blocks/:blockId` | $set / $unset one block  |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId/editor/:collection/blocks/:blockId` | tombstone one block      |

Object bodies are stored as a tree of atomic blocks in an **editor
collection** (one record per block) served by the compiled-in `editor`
module, and exposed through the `…/editor/:collection/**` route
namespace. `:collection` is the collection a type's part declared with
`{"module": "editor"}` (§ Parts and modules): the canonical
`editor_blocks` for a shared part — the body every document type
shares, so an object carrying two such types has one body — or a
namespaced `<typeId>_<key>` instance for a part that wants its own
editor (a meeting type's `notes` next to its body). An object holds a
collection only while it carries a type whose part declares it: a
write into a collection none of the object's types declare is `400
dataset.not_declared` (attach the type first — the write never attaches
one); a `:collection` no editor part in the space declares is `404
dataset.not_found`. The built-in `page` type (§ Built-in hidden types)
is the plain document — hidden, one part sharing this collection; a
client with its own document types declares them with an editor part,
registered as a bundle so every peer lands on one, and an object
carrying both has one body. The atomic surface is the three
`…/blocks` endpoints; the `…/markdown` routes are a lossless
import/export layer over the same collection for LLM tools, "Export as
.md" / "Import .md" flows, and programmatic API users that don't want
to walk the block tree. Liveness goes through the per-object
query/subscribe endpoint with `dataset=<collection>`.

The `editor/markdown` routes are aggregating endpoints (each one
bundles several SDK calls) and are a deliberate exception to the
"endpoints map 1:1 onto SDK methods" rule. `GET` reads every
top-level block, renders each to its canonical markdown bytes, and
joins with `\n\n` (see *Empty paragraphs* below for the blank-line
rule). `PUT` parses the incoming markdown, diffs against
the current block tree by (type + position + text), and emits
per-block create / update / delete ops through the same write path a
PATCH /editor/blocks call would, so the same `editor_blocks` SSE events
fire under the hood. `PUT` replies with `{"inserted": [...],
"updated": [...], "deleted": [...], "unchanged": N}` where the slices
contain block ids.

`PATCH …/editor/:collection/markdown` is the surgical variant of `PUT` — for
callers (LLM agents above all) that know the *text* they want changed
but not the block ids. The body is a batch of exact-match
replacements against the rendered document:

```json
{ "edits": [
    { "oldText": "- [ ] Children of Time",
      "newText": "- [x] Children of Time" },
    { "oldText": "typo", "newText": "fixed", "replaceAll": true }
] }
```

The server renders the current canonical markdown (the exact bytes
`GET` returns), resolves every edit against it, splices the
replacements, and feeds the result through `PUT`'s diff pipeline — so
a checkbox tick lands as a single `$set style.checked` on the matched
block, ids and untouched blocks stay stable, and the reply is `PUT`'s
shape. Matching rules:

- Every `oldText` matches against the ORIGINAL document,
  independently of the other edits; matched regions must not overlap.
- Without `replaceAll` the match must be unique. `newText` may be
  empty (deletes the matched text). When the match is a whole block,
  the deletion takes one blank-line separator with it, so removing a
  block leaves its neighbours adjacent rather than leaving empty
  paragraphs behind; empty paragraphs that were already there stay.
- Exact match first; on zero hits a whole-line fuzzy fallback
  retries with unicode punctuation folded to ASCII (curly quotes,
  dash family, NBSP; NFKC) and trailing whitespace ignored. A
  mid-line fragment is never fuzzy-matched — re-`GET` and quote
  exactly instead.
- All-or-nothing: any failing edit rejects the whole request with a
  `markdown.*` error (400) and nothing is written. Edits that produce
  byte-identical content are a 200 no-op — ticking an already-ticked
  box is idempotent.

| Error code | Meaning / recovery |
|------------|--------------------|
| `markdown.no_match` | `edits[i].oldText` not in the current rendering — `GET` and quote the exact text (details: `editIndex`) |
| `markdown.ambiguous_match` | occurs more than once without `replaceAll` — add surrounding context or set `replaceAll` (details: `editIndex`, `occurrences`) |
| `markdown.overlapping_edits` | two edits matched intersecting text — merge them into one edit (details: `editIndices`) |

Because the match runs server-side against the current state, `PATCH`
is what replaces the client-side `GET → string-replace → PUT`
read-modify-write: a stale quote fails loudly instead of silently
reverting concurrent edits elsewhere in the document, and the caller
ships O(edit) bytes instead of O(document).

`POST …/editor/:collection/markdown/append` is the append-only fast path. It
parses the supplied `{"content": "..."}`, looks up only the tail
position (one indexed `-nav.pos` query, never the existing block
bodies), allocates lexids past the last block, and creates every
parsed block in a single ModifyBatch. Cost is O(appended content),
independent of how large the document already is — unlike `PUT`, which
renders and diffs the whole document on every call. The reply uses the
same shape as `PUT` with only `inserted` populated (`updated` and
`deleted` are always empty). Trade-offs the caller accepts: it is
purely additive (no update/delete, and it will create a block
identical to an existing one), and it inserts no leading separator —
`content` is appended structurally after the current last block.
Empty/blank content is a 200 no-op. Use this for grow-by-append pages
(e.g. agent debug logs that append every turn); a run of N appends is
O(N) here versus O(N²) through `PUT`.

##### Empty paragraphs

An empty paragraph is a real block — a `paragraph` record with
`text: ""` — and blank lines are how the markdown routes carry it.
The rule, applied by both `PUT` (parse) and `GET` (render), so the
two are exact inverses:

- **Between two content blocks**: one blank line is the plain
  separator; **every blank line beyond it is one empty paragraph**.
  `alpha\n\nbeta` is two blocks; `alpha\n\n\nbeta` is two blocks with
  one empty paragraph between them.
- **At either edge**: a leading or trailing run has no separator to
  build on, so **every one of its blank lines is an empty paragraph**
  — with one exception below. The same holds for a document that is
  blank throughout.
- **A single trailing newline is a terminator, not content.**
  `alpha\n` is one block, byte-identical on read-back to `alpha`.
  A trailing empty paragraph therefore renders as `alpha\n\n`.

Consequences worth designing against:

- `GET` after `PUT` returns the same bytes for any document expressed
  in this form, and re-`PUT`ting a `GET` writes nothing (`unchanged`
  equals the block count). A client that hydrates from `GET` will not
  see its own save come back reshaped.
- The encoding is **ours, not CommonMark's**: every other markdown
  renderer collapses blank runs. Content that round-trips through an
  external tool, a paste, or a client that does not implement this
  rule loses its empty paragraphs. Editors that want them preserved
  must both emit and parse blank runs this way.
- `POST …/editor/:collection/markdown/append` is the exception: a fragment is
  positioned by the append itself, so blank lines wrapping it are
  framing and are dropped. Empty paragraphs *between* the fragment's
  own blocks are kept, and blank-only content stays a 200 no-op.

#### Blocks

One record per block, stored in the object's editor collection
(`editor_blocks`, or a namespaced instance — `:collection` in every
route below; the examples use the canonical name). Nest via
`nav.parentId`; order siblings via `nav.pos` (lexid). Wire shape:

```json
{
  "id":    "<auto-derived from changeId>",
  "_ver":  { "id": "<VersionId of last change>", ... },
  "type":  "paragraph" | "heading" | "list_item" |
           "check_list_item" | "code" | "quote" |
           "divider" | "html" | "table" | "image",
  "style": { "level": 1..6,         /* heading */
             "ordered": true|false, /* list_item */
             "checked": true|false, /* check_list_item */
             "lang":    "go" },     /* code */
  "text":  "**bold** inline markdown",
  "nav":   { "parentId": "<blockId>" | "",
             "pos":      "<lexid>" }
}
```

`text` is INLINE markdown only — bold, italic, inline code, links,
strikethrough. Block-level syntax (heading hashes, list bullets,
fences, quote `>` prefixes) lives in `type` + `style` instead so
clients render blocks structurally without re-parsing.

##### Read blocks

The bespoke list endpoint is gone — reads go through the per-object
query primitive with `dataset=editor_blocks`:

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<oid>",
  "dataset":  "editor_blocks",
  "sort":     ["nav.pos"] }
```

Each record carries `id`, `_ver`, `type`, `style`, `text`, `nav`.
Records sort by `nav.pos` ascending (flat order, not DFS — clients
that want DFS reconstruct the tree by grouping children under each
`nav.parentId`). Empty `records` array when the object has no body
blocks yet.

##### Create

`POST /v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/blocks`

```json
{ "type":  "paragraph",
  "style": {"level": 2},
  "text":  "hello",
  "nav":   {"parentId": "<blockId>", "pos": "<lexid>"} }
```

`type` is required (≤ 64 bytes, non-empty). `style` is an open-ended
object — the handler accepts any sub-keys. `text` is inline markdown.
`nav.parentId` defaults to `""` (top-level); `nav.pos` defaults to
the next lexid past the parent's current max (queried server-side at
create time). Returns 201 with the shared write result
`{versionId, changeId, recordIds}` — `recordIds[0]` is the
server-allocated block id. Read the block back via `POST /query` with
`dataset=editor_blocks` (see § Write responses).

##### Patch

`PATCH /v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/blocks/:blockId`

```json
{ "set":   { "text": "...", "style.level": 2 },
  "unset": ["style.checked"] }
```

Each key in `set` is a dotted field path applied as one `$set` op.
Each entry in `unset` is a dotted path applied as one `$unset`. Both
fields are optional; an empty patch is a no-op — no change is produced,
so the result carries `recordIds=[blockId]` with an empty `versionId`.
All ops land in a single any-sync change (one VersionId).

Required fields cannot be `$unset`-ed (`type`, `nav.parentId`,
`nav.pos`) — the handler rejects those ops while still applying the
rest of the batch. Per-op rejections do not fail the whole change.

Note on path syntax: dotted-string keys (`"style.level": 2`) are
parsed as one anyenc field path, NOT as nested objects. Use
`"style.level"` to touch a single sub-field; use `"style": {"level":2}`
only when you want to replace the entire `style` object whole-cloth.

Response: the shared write result `{versionId, changeId, recordIds}`
(`recordIds=[blockId]`). Clients running the
subscribe-then-query-then-apply recipe stamp `_ver.<op.path> = versionId`
on the affected paths to pre-seed dedup against the matching live event.

##### Delete

`DELETE /v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/blocks/:blockId`
→ 200 with the shared write result `{versionId, changeId, recordIds}`
(`recordIds=[blockId]`). Tombstones the record (sticky — re-creating
the same id is rejected). Children of the deleted block are NOT
cascaded; the client either deletes the descendants explicitly or
rewrites the document via `PUT …/editor/:collection/markdown`, which
diffs the whole body.

##### Subscribe

Liveness goes through the per-object windowed query/subscribe endpoint
with `dataset=editor_blocks`:

```
POST /v1/spaces/:spaceId/query/subscribe
{ "objectId": "<oid>", "dataset": "editor_blocks", "sort": ["nav.pos"] }
```

`changes` frames carry `added` / `updated` / `removed` for blocks
entering, mutating, or leaving the visible window. `added` records
include the full block as `doc` plus its per-field ops; `updated`
records carry the post-apply doc and the ops that triggered the
change; `removed` carries just the id. The same events fire whether
the change originated from a PATCH `…/blocks` call or a PUT
`…/markdown` bulk rewrite. See `04-events.md`.

#### Create an object

`POST /v1/spaces/:spaceId/objects` takes two keys:

```json
{
  "types": ["..."],
  "initialProperties": { "<typeId>": { "<propId>": "..." } }
}
```

These two keys are the **whole** create vocabulary. Any other
top-level key — a bare type group like `"any"`, a top-level `"name"`,
a typo — is `400 request.unknown_field` naming the accepted set and
where the value belongs: object properties always ride
`initialProperties` keyed by type
(`{"initialProperties": {"any": {"name": "Dune"}}}`). Shape is
enforced per field too (`types` an array, `initialProperties` an
object, every `initialProperties` group an object of
`{propertyId: value}`) → `400 request.schema`. Nothing in this body
is silently dropped, and nothing is added to it server-side: the
object carries exactly the types it names.

#### The wiki tree

The space's tree is the catalog usecase `wiki` (§ Catalog,
`docs/28-well-known-bundles.md`) — no built-in type, nothing stamped
on create, no tree endpoint. A client sets it up once per space and
keeps the reply:

```
POST /v1/catalog/wiki/setup   {"spaceId": "<spaceId>"}
→ the bundles[] entry with id "system:wiki/v1":
    typeId                # <wikiTypeId>
    properties.parentId   # <parentIdPropId> — string; "" = top level
    properties.pos        # <posPropId>      — lexid string; orders siblings
    properties.folder     # <folderPropId>   — boolean
```

An object is in the tree only when it carries the wiki type; its
placement is three ordinary property values at
`<wikiTypeId>.<propId>`, written like any other property. A page in
the tree carries `page` for its body and the wiki type for its place:

```
POST /v1/spaces/:spaceId/objects
{
  "types": ["page", "<wikiTypeId>"],
  "initialProperties": {
    "<wikiTypeId>": { "<parentIdPropId>": "", "<posPropId>": "a0", "<folderPropId>": false }
  }
}
```

A folder is the same create with `<folderPropId>` `true` and no
`page`. Which other types a tree object carries is the client's
choice — the wiki type only places it.

Children of a node, in order (`""` as the parent lists the top level):

```
POST /v1/spaces/:spaceId/objects/query
{
  "filter": { "<wikiTypeId>.<parentIdPropId>": "<parentObjectId>" },
  "sort":   [ "<wikiTypeId>.<posPropId>" ]
}
```

The columns are ordinary properties: unindexed on the `objects`
collection (a scan, `09-query.md` § Indexes); `parentId` and `pos` are
kept out of search with `meta.index: none` and are plain strings
rather than relations, so `/backlinks` never reports a parent link
(`folder`, a boolean, is never indexed at all).

**`pos` is the client's.** The server allocates nothing: the client
computes every position with the lexid allocator the editor's blocks
use (alphabet `CharsAllNoEscape`, block size 4, step 100 — match the
Go side byte-for-byte): past the last sibling on create, between two
siblings on a drop. No write needs a server round-trip to pick one.

#### Moves (drag-and-drop)

A move is one property write on the wiki type — no dedicated route.
To relocate `oid` under `newParent` at lexid `p`:

```
POST /v1/spaces/:spaceId/properties/:oid/set/<wikiTypeId>
{ "patch": { "<parentIdPropId>": "<newParent>", "<posPropId>": "<p>" } }
```

Both fields land in one DAG change; a reorder inside the same parent
patches `pos` alone.

#### Object deletion

`DELETE /v1/spaces/:spaceId/objects/:objectId` is a single
`Objects.Delete` call. The SDK writes a record-level tombstone on the
per-space `objects` row before tearing down the any-sync tree, so the
row disappears from `QueryObjects` and a `deleted: true` event fires
on the per-space firehose (`dataset=objects`) with the change's
`versionId` — the canonical signal subscribers use to drop the id
from local state. See `04-events.md`.

#### Backlinks

`GET /v1/spaces/:spaceId/objects/:objectId/backlinks` answers "which
objects reference X?" — the reverse direction of relation property
values. The SDK exposes no reverse index, so like `/search` this is a
consumer-side exception to the 1:1 rule: object references are
properties whose descriptor slug is `relation` (`xFormat.type`, arrays
of `"any://<objectId>"` URIs), stored at `record[typeId][propId]`; the
handler resolves the space's relation property catalog (top-level
definitions only) and queries the `objects` collection for rows whose
arrays contain `"any://<X>"`.

```json
{"backlinks": [{"objectId": "…", "typeId": "…", "propId": "…"}]}
```

One entry per (referencing object, property) pair — an object linking
X through two different links properties appears twice. Only live
references count: values under a currently detached type are skipped
(same convention as the search index's prop chunker). No existence
check on `:objectId` — an unknown or unreferenced id returns
`{"backlinks": []}`, not 404. Link values carry no index, so this is a
scan over the objects collection; fine at v1 scale, a reverse index is
a follow-up. The wiki tree's `parentId` is a plain string, not a
relation — query children directly (§ The wiki tree).

### Data plane

| Method | Path                                                      | Purpose                              |
|--------|-----------------------------------------------------------|--------------------------------------|
| POST   | `/v1/spaces/:spaceId/query`                               | `Space.Query.Snapshot`               |
| POST   | `/v1/spaces/:spaceId/query/subscribe`                     | `Space.Query.Subscribe` (SSE)        |
| POST   | `/v1/spaces/:spaceId/aggregate`                           | `Space.Aggregate` (pipeline)         |
| POST   | `/v1/spaces/:spaceId/modify`                              | `Space.Modify`                       |
| POST   | `/v1/spaces/:spaceId/upsert`                              | `Space.Upsert` (§ Upsert records)    |
| POST   | `/v1/spaces/:spaceId/delete-records`                      | `Space.Delete`                       |

Two query scopes:

- `POST /v1/spaces/:spaceId/objects/query` (+ `/subscribe`) —
  **cross-object**. Reads the per-space `objects` collection (one row
  per object's computed property values). Use this to find objects by
  property, e.g. `{"filter":{"<typeId>.<propId>":"Casablanca"}}`.
- `POST /v1/spaces/:spaceId/query` (+ `/subscribe`) — **per-object**.
  Reads one of an object's own datasets (`objectId` and `dataset`
  required). Used for a type object's `properties` definitions
  dataset, `editor_blocks`, `chat_messages`, runtime datasets such
  as `program_source` / `mini_app`, etc.

Every row in the per-space `objects` collection carries SDK-stamped
row-root fields alongside `id`, all derived/read-only (client writes
addressing them are rejected):

- `author` — identity that created the object (root-change signer);
- `createdAt` — object creation time (root-change time), an instant:
  `{"$date": "2026-08-05T17:00:00.000Z"}`;
- `spaceId`;
- `modifiedAt` — the instant of the latest synced change that touched
  the object, whatever dataset it landed on. Any property write bumps
  it; peers converge on the same value (LWW on the change's DAG
  order). It is the **author's clock** — sort/display quality, never a
  fencing token. Local- and account-scope writes (e.g. chat read
  flags) deliberately don't bump it.
- `modifiedBy` — the account identity that signed that same change:
  the object's last writer, `author` on an object nobody edited since
  it was created.

"Recently modified first" is `{"sort": ["-modifiedAt"]}`.

All four take POST (filter/sort body doesn't fit a query string).
Reads always go through these — the bare `…/query` returns a
point-in-time snapshot; `…/query/subscribe` returns the same
snapshot plus a live SSE stream of windowed transitions. See
`04-events.md` for the subscribe contract.

`modifiedAt` and `modifiedBy` are stamped from **one** change — the
object's latest by DAG order, whatever dataset it landed on (a
property write, an editor block, a chat message, a runtime-dataset
record, a record delete). They share that change's version, so they
move as a unit and never pair one change's time with another's
signer; a late arrival regresses neither, and concurrent writers are
resolved by DAG order, not by clock, so the winning identity may be
the one whose wall clock reads earlier. Deleting the object removes
the row outright, stamps included. `modifiedBy` is in the same
identity encoding as `author`, as chat `creator`, as `identity` in
`GET /v1/spaces/:spaceId/members` and as `id` from `GET /v1/account`:
clients resolve name and icon through the members list, falling back
to `GET /v1/identities/:identity` for a writer who has since left the
space. `modifiedAt` is indexed, `modifiedBy` is not — a filter on it
scans the collection. A missing `modifiedBy` means the row has yet to
be rebuilt (on first load of the object, or by the background sweep)
or the latest change has no known signer, never "nobody modified it".

A `filter` naming an operator outside the grammar is a caller fault:
`400 filter.unknown_operator`, with the offending token in
`details.operator` and the supported set spelled out in the message.
Note there is no `$contains` — a scalar already compares against array
elements, so `{"any.types": "chat"}` is the contains spelling. Filter
grammar and the array rules: `09-query.md`.

A per-object read (`objectId` in the body, and likewise the editor /
markdown / history routes) that names an object this space doesn't
have — never created here, or deleted — answers `404 object.not_found`.
The cross-object `objects/query` has no such failure mode: a filter on
a dead id just returns zero rows. Search hits can briefly outlive their
object (the index evicts asynchronously), so a client following a hit
into `/query` must treat 404 as "stale hit", not an error.

#### Snapshot request body (shared by both `…/query` and `…/query/subscribe`)

```json
{
  "objectId":   "obj_abc",            // per-object variant only
  "dataset":    "notes",              // per-object variant only
  "filter":     { "tags": "idea" },
  "sort":       ["-_ver.id"],         // required when limit > 0 on subscribe
  "limit":      100,
  "offset":     0,
  "includeTotal":       true,         // populate `total` + `hasNext` in the snapshot
  "includeDeleted":     false,        // per-object `…/query` only — tombstones too, see below
  "mailboxCapacity":    256,          // subscribe only — default 256, min 16
  "driftBudgetPercent": 30,           // subscribe only — default 30
  "projection": { "any": 1, "<typeId>": 1, "_ver": -1 }   // field paths → 1 include / -1 exclude
}
```

This field set is **closed**: an unrecognized top-level key answers
`400 request.unknown_field` listing the accepted vocabulary (so a
`filters` typo fails loudly instead of silently querying the whole
space), and a body that isn't a JSON object is `400 request.schema`.
An `objectId` that is a serialized nil (`"None"`, `"null"`,
`"undefined"`, …) is `400 object.id_required` — the caller's id
variable was unset. The same closed set guards the space-list and
files query/subscribe bodies (plus their own `dataset` / `objectId`
extras where documented).

**`projection`** shapes the records that come back — mongo's grammar,
a flat object of dotted field paths to `1` (include) or `-1`
(exclude). Omit it and records ship their full form, byte for byte as
before. It applies to snapshot frames AND to every `added` / `updated`
record in `changes` events, docs and per-field ops alike, so a
projected subscription cannot silently widen after the first update.
Full grammar, the `_ver` rule, and the divergences from mongo:
[`docs/09-query.md` § Projection](09-query.md).

**`includeDeleted`** (per-object `…/query` only) returns the dataset's
**record-level tombstones** next to the live rows: a deleted record
comes back as `{id, _deletedAt, _ver, _traces?}` with its content
wiped — `_deletedAt` is the discriminator, and a filter on a content
field never matches one. It exists for writers of `id: user` datasets:
a deleted id is burned forever (`upsert.record_deleted`), so the live
maximum is not the next free id — `{"includeDeleted": true, "sort":
["-id"], "limit": 1}` is the probe that finds the highest id ever
used. With it the snapshot reads through the SDK's find path rather
than the windowed live view (same filter / sort / limit / offset;
`total` is the full match count including tombstones). Refused on
`…/query/subscribe` (`400 request.invalid_field` — the live window
never carries tombstones) and unknown on `objects/query` (a deleted
OBJECT is purged, not tombstoned — there is nothing to include; see
`Objects.Delete` above).

Snapshot response (bare `…/query`):

```json
{ "records": [ /* *anyenc.Value rendered as JSON */ ],
  "total":   17,                      // omitted when includeTotal=false
  "hasNext": true }                   // more matches past this page; omitted when includeTotal=false
```

#### Aggregate

The same two scopes take MongoDB-style aggregation pipelines — the
aggregation siblings of the query endpoints, snapshot-only (no
subscribe variant):

```
POST /v1/spaces/:spaceId/objects/aggregate     Space.AggregateObjects
POST /v1/spaces/:spaceId/aggregate             Space.Aggregate (objectId + dataset required)
```

Body: `{objectId?, dataset?, pipeline: [...], groupLimit?,
accumArrayLimit?, memoryLimitBytes?, explain?}`. Response
`{records: [...]}` — pipeline result documents (a `$group` doc carries
the group key as `id`, never `_id`) — or `{plan: "..."}` with
`explain: true`. Tombstones are excluded server-side, same as `/query`.
Stage set, examples, limits, and the catalog of deliberate MongoDB
divergences live in [`docs/14-aggregation.md`](14-aggregation.md).
Errors: `aggregate.bad_pipeline` / `aggregate.limit_exceeded`
(`docs/06-errors.md`).

#### Subscribe (Server-Sent Events)

Two endpoints — POST, body as documented above:

```
POST /v1/spaces/:spaceId/objects/query/subscribe       (cross-object)
POST /v1/spaces/:spaceId/query/subscribe               (per-object)
```

Response is `Content-Type: text/event-stream`. Errors before the
stream opens use the canonical JSON envelope (`request.missing_field`,
`space.not_found`, ...). Once the response status is 200, problems
become SSE `event: closed` frames.

Wire format:

```
event: ready
data: {}

event: snapshot
data: {"records":[{"id":"obj_a", "...": "..."}, ...], "total": 17, "hasNext": true}

event: changes
data: [{"versionId":"!!%>",
        "added":  [{"id":"obj_c","doc":{...},
                    "ops":[{"type":"$set","path":[],
                            "payload":{"title":"hello"}}]}],
        "updated":[{"id":"obj_a","doc":{...},
                    "ops":[{"type":"$set","path":["title"],
                            "payload":"renamed"}]}],
        "removed":["obj_b"]}]

: keepalive

event: closed
data: {"reason": "overflow"}
```

- **`ready`** — sent once after the SDK `Subscribe` call returns. Wait
  for it before treating the stream as live.
- **`snapshot`** — sent once, right after `ready`. `records` is the
  materialised window (bounded by `limit`/`offset`); `total` is the
  unbounded filter-matching count and `hasNext` reports whether more
  matches exist past this page (`offset+len(records) < total`). Both
  are present only when `includeTotal` was set in the request body.
- **`changes`** — JSON array of zero-or-more windowed events. Each
  event has `versionId` (per-change DAG order, locally-scoped — don't
  compare across peers) plus three buckets:
  - `added` — records that entered the visible window. Each carries
    the full `doc` plus the per-field `$set`/`$unset` ops that
    triggered the entry (a brand-new record's ops collapse to one
    multi-field `$set` at path `[]`).
  - `updated` — records already in the window whose state changed.
    Same `doc`+`ops` shape as `added`.
  - `removed` — array of ids that left the window. The wire does NOT
    distinguish *deleted* / *filter-rejected* / *displaced* (pushed
    past `limit`); all three look the same. From the consumer's view
    the action is the same: drop the id from local state. If you need
    to know which it was, query `…/query` with that id.

  An op's `path` is always a JSON array of dotted segments — never
  `null`. An empty array `[]` means the record root: on `$set`, the
  payload is then an object whose top-level keys are themselves
  dot-separated paths to assign at. `Wait` coalesces every event
  accumulated during the previous write into a single frame, so a
  slow client / network produces fewer, larger frames rather than
  head-of-line stalls.
- **`: keepalive`** — comment frame every 25s during silence; defeats
  idle middlebox timeouts.
- **`closed`** — terminal frame. Reasons:
  - `server_shutdown` — server got a signal or `POST /v1/shutdown`.
  - `deauthorized` — the account was torn down in place
    (`DELETE /v1/auth`, or a `replace` switch) while the server stays
    up; re-read `GET /v1/auth` before resubscribing.
  - `sdk_closed` — the SDK released the subscription channel (space
    or SDK closed).
  - `overflow` — the per-subscriber mailbox filled before the consumer
    drained it. The SDK closes the sub rather than dropping events —
    resubscribe to get a fresh snapshot.
  - `drifted` — more than `driftBudgetPercent` of the held window left
    without replacements, and the engine refuses to re-query on the
    hot path. Resubscribe.

  Every reason means "the stream is over; if you want live state,
  open a new POST." Recovery is identical for `overflow` and `drifted`
  — the reason is split only so clients can log/backoff sensibly.

Subscriptions deliver events from registration onward only — there is
no replay. The bundled `snapshot` frame is the only point-in-time read.
There is no SSE `id:` — clients fence-and-replay on `versionId` if
they want at-least-once semantics across reconnects.

### Version history

| Method | Path                                                                                        | Purpose                       |
|--------|---------------------------------------------------------------------------------------------|-------------------------------|
| GET    | `/v1/spaces/:spaceId/objects/:objectId/history`                                             | `Space.History().ListChanges` |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/history/diff`                                        | `Space.History().Diff`        |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/history/:version`                                    | `Space.History().ViewAt`      |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/history/:version/datasets/:dataset/records/:recordId`| `Space.History().RecordAt`    |

Read-only, per-object, and **snapshot-only** — there is no subscribe
variant. A **version is a ChangeId**: the content-hash CID of a DAG
change, which every write already returns as `changeId` in
[`ModifyResult`](#write-responses). It's stable across peers and
restarts, so a version handed out by one device resolves on another.
"State at version X" is the projection of exactly X's causal past —
not "the object at wall-clock time T". Concurrent branches mean two
peers can hold versions neither of which precedes the other; there is
no total order to page through, which is why listing is DAG order plus
a cursor rather than a timestamp range.

`timestamp` is the **author's clock**, Unix seconds — display-only.
Never sort or fence on it: it comes from whichever device wrote the
change, and nothing forces those clocks to agree.

The static `diff` segment is registered before `:version` so it isn't
swallowed by the wildcard.

**Excluded datasets.** `chat_messages` opts out of history
(`handler.Dataset.SkipHistory`): chat clients render live records
only (edits show current text, deletes tombstone), so nothing reads a
per-message timeline and the index rows would be dead weight at chat
write volume. Writes to the dataset succeed as usual but are invisible
to every history endpoint, filtered or not. The DAG retains
everything regardless — re-enabling a dataset later only costs a
backfill.

#### List changes

`GET …/history` pages an object's changes newest-first. Filters:
`dataset`, `recordId` (requires `dataset` → else `400
request.invalid_field`), `traceId`, `author`. Paging: `limit`
(default 50, capped at 200 — a larger value is clamped, not
rejected) + the opaque `cursor` echoed from the previous page. An
empty `cursor` in the response means history is exhausted.

`coalesce=true` groups consecutive same-author changes into one entry
whose `version` is the group's **newest** ChangeId and whose
`groupSize` is the member count (1 = ungrouped); `coalesceWindow`
bounds the gap in seconds (default 300, max 86400 — outside that →
`400 request.invalid_field`). Grouping follows the DAG: a linear
same-author chain coalesces, a branch does not.

```json
{
  "changes": [
    { "version":   "bafy…9c",
      "author":    "A5k…",
      "timestamp": 1763040000,
      "dataset":   "editor_blocks",
      "traceIds":  ["trace_…"],
      "touched":   [{ "dataset": "editor_blocks", "recordId": "blk_…", "ops": ["$set"] }],
      "groupSize": 3 }
  ],
  "cursor": "eyJ…"
}
```

`truncated` is **reserved and always false today** — the SDK keeps
full local history. It becomes meaningful only with the future
snapshot-horizon contract.

#### View at a version

`GET …/history/:version` materializes the object's live records at
that cut, grouped by dataset; records are raw dataset rows, the same
shape [`/query`](#data-plane) returns. Optional `dataset` narrows to
one. Empty datasets are omitted unless explicitly requested.

**Synced scope only.** Local and account-scoped values have no
history (they never entered the DAG) and are excluded — a history
view is not a substitute for a `/query` read.

The view is request-scoped: the server opens it, serializes, and
closes it within the request. There are no long-lived view handles
over HTTP in v1. A version whose materialization exceeds the SDK's
bound returns `413 history.view_too_large` — narrow with `dataset`,
or use the record fast path.

#### One record at a version

`GET …/history/:version/datasets/:dataset/records/:recordId` is the
record-scope fast path: one record, no full-view materialization, so
it can't hit `view_too_large`. `exists: false` means the record wasn't
present at that cut; `deleted: true` means it was tombstoned and
`record` carries the tombstone row.

#### Diff

`GET …/history/diff` takes a required `version` and an optional
`base`. **Omit `base`** and you get the per-change *effect* diff —
`version` against its own DAG parents, i.e. "what did this change
do". Pass `base` and you get the cumulative `base..version` diff.
Scope with `dataset` and `recordIds` (comma-separated; requires
`dataset`).

```json
{
  "base":    "",
  "version": "bafy…9c",
  "datasets": [
    { "dataset": "editor_blocks",
      "records": [
        { "id":   "blk_…",
          "kind": "changed",
          "fields": [
            { "path": ["text"], "before": "old", "after": "new" }
          ] } ] } ]
}
```

`kind` is one of `added` / `removed` / `changed` / `deleted`. Field
diffs are leaf-level; an absent side is omitted (`added` has no
`before`). Peer-local bookkeeping (`_ver` and friends) never appears
— consistent with [`_ver` staying off the event stream](04-events.md).

### Types

| Method | Path                                                          | Purpose                |
|--------|---------------------------------------------------------------|------------------------|
| GET    | `/v1/spaces/:spaceId/types`                                   | `TypesAPI.List`        |
| POST   | `/v1/spaces/:spaceId/types`                                   | `TypesAPI.Create`      |
| GET    | `/v1/spaces/:spaceId/types/:typeId`                           | `TypesAPI.Get`         |
| PATCH  | `/v1/spaces/:spaceId/types/:typeId`                           | `TypesAPI.Patch` — name / description / icon / weight / layout |
| DELETE | `/v1/spaces/:spaceId/types/:typeId`                           | `TypesAPI.Delete`      |
| GET    | `/v1/spaces/:spaceId/types/:typeId/properties`                | `TypesAPI.Properties`  |
| POST   | `/v1/spaces/:spaceId/types/:typeId/properties`                | `TypesAPI.AddProperty` |
| DELETE | `/v1/spaces/:spaceId/types/:typeId/properties/:propId`        | `TypesAPI.RemoveProperty` |
| PATCH  | `/v1/spaces/:spaceId/types/:typeId/properties/:propId`        | `TypesAPI.PatchProperty` |
| GET    | `/v1/spaces/:spaceId/types/:typeId/parts`                     | `TypesAPI.Parts` — parts with their datasets |
| POST   | `/v1/spaces/:spaceId/types/:typeId/parts`                     | `TypesAPI.AddPart` — a part and its datasets, one change |
| PATCH  | `/v1/spaces/:spaceId/types/:typeId/parts/:partId`             | `TypesAPI.PatchPart`   |
| DELETE | `/v1/spaces/:spaceId/types/:typeId/parts/:partId`             | `TypesAPI.RemovePart` — the part and every dataset under it |
| POST   | `/v1/spaces/:spaceId/types/:typeId/parts/:partId/datasets`    | `TypesAPI.AddDataset`  |
| GET    | `/v1/spaces/:spaceId/types/:typeId/datasets`                  | `TypesAPI.Datasets` — the flat compiled view |
| PATCH  | `/v1/spaces/:spaceId/types/:typeId/datasets/:defId`           | `TypesAPI.PatchDataset` |
| DELETE | `/v1/spaces/:spaceId/types/:typeId/datasets/:defId`           | `TypesAPI.RemoveDataset` |
| POST   | `/v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields`    | `TypesAPI.AddDatasetField` |
| PATCH  | `/v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields/:fieldId` | `TypesAPI.PatchDatasetField` |
| DELETE | `/v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields/:fieldId` | `TypesAPI.RemoveDatasetField` |

`POST …/types` **requires** a non-empty **`xKey`** — the stable
programmatic handle a type is resolved by (the display `name` is not a
resolution key; a type without an xKey is reachable only by its CID).
The SDK treats xKey as non-unique display metadata, so the server
enforces it: empty → `400 type.xkey_required`; collision with an
existing type's `xKey` **or** id in the same space → `409
type.xkey_conflict` (`details: {xKey, existingTypeId}`). Clients derive
the xKey as a slug of the name (`"Pages"` → `pages`); it must survive
display-name renames. Built-in types (the hidden
`dataview` / `page` / `miniapp` / `bin`) are registered, not created here,
and resolve by their literal id; a registered type's parts are static —
`GET …/types/:typeId/parts` reads them compiled (keys as ids, a static
dataset's collection is its name, `module: records` on a schema-only
dataset), every write on them is `400 type.registered`, and a
registered type may be `hidden` like a user one. There is no built-in
`editor` or `chat` type: a chat is a user type whose part declares the
`chat` module (§ Parts and modules), registered through a well-known
bundle so every client lands on one type; a document is the built-in
`page` or a user type with an editor part (§ Built-in hidden types).

`GET …/types` returns the synthetic built-ins first — `any`,
`spaceIndex` and `type` (the meta-type: the shape of type objects
themselves: `xkey`, `weight`, `layout`, `hidden`, `meta`) — then every
registered type, then the
space's user types. Built-ins and registered types report `builtIn:
true` with `xKey` equal to their id, which is what reserves those ids
against user types (`409 type.xkey_conflict`); user types report
`builtIn: false` and their caller-set `xKey`. The three synthetic ids
are not attachable to an object — a client offering "filter by type" or
"add a type" should skip them.

Storage note for anyone reading raw rows (`GET …/properties/:objectId`,
`/query`): a type's own row keeps `any.name` / `any.description` /
`any.icon` where every object keeps them, but its xKey sits at
`type.xkey` — the meta-type's namespace, writable only on rows carrying
the `__type__` marker in `any.types`. `TypeInfo.xKey` is the supported
read; the raw path is for debugging.

The create body is `{name?, description?, iconCid?, xKey, weight?,
layout?, hidden?, meta?}` — **inline property definitions are not part
of type create** (no SDK surface accepts them). A `properties` key, or any
other unknown top-level key, answers `400 request.unknown_field`
pointing at the per-field route: create the type, then add each
property via `POST …/types/:typeId/properties` (each add materializes
the schema immediately) and each part via `POST …/types/:typeId/parts`.

`weight` and `layout` are the type's rendering slice, stored on the
meta-type (`type.weight`, `type.layout` on the raw row, next to
`type.xkey`). An object carries several types; the one with the
highest `weight` is its **primary** type — the one whose `layout` a
client renders (`any` and the built-ins carry no weight and never
win; ties break on type id). `layout` is an opaque descriptor object
in the x-format shape — `{"type": "<slug>", "config": {…}}`, e.g.
`{"type": "page"}` or `{"type": "tabs"}` — the client's vocabulary,
checked only for being an object. **`PATCH …/types/:typeId`** takes
`{name?, description?, iconCid?, weight?, layout?, hidden?, meta?}`:
absent keeps, an empty string clears a text field, `"layout": null`
clears the layout; `204`, `400 type.registered` on a built-in, `404
type.not_found`.

`hidden` (bool, `type.hidden`) keeps the type out of `GET …/types` —
the picker view — unless the request carries `?includeHidden=true`;
`GET …/types/:typeId` resolves a hidden type always, so an object
carrying one still renders. A bundle root is hidden when its install
asks for it (`hidden` in § Bundles): a root that only hosts its
bundle's records should be, since attaching it elsewhere would grant
that object the bundle's collections; a root that is a type objects
carry stays listed.

`meta` (`type.meta`) is the open bag of consumer flags on a type — one
string, bool or number per single-level key (no `.`, no `$`, ≤64
bytes; `400 request.invalid_field` otherwise), opaque to the server.
Create takes it whole; PATCH patches it **per key** — a scalar sets
the key, `null` unsets it, keys not named are untouched — so two
devices writing different keys merge instead of clobbering each
other. Consumers read the keys they own (an indexer flag, a client's
tags); the server interprets none of them today.

`GET …/types/:typeId` and `GET …/types/:typeId/properties` answer `404
type.not_found` for an unknown typeId (deleted, never existed, or an id
that resolves to a non-type object). The properties list is
existence-checked server-side — the SDK's `Properties` returns an empty
slice for unknown ids — so a `200 []` always means "the type exists and
has no property definitions yet", never "no such type".

`POST …/properties` — the definition. `kind` is **required** and
pinned; nothing is defaulted from the descriptor. Body:

```json
{ "name": "Stage", "xKey": "stage", "kind": "array",
  "meta": { "index": "basic" },
  "xFormat": { "type": "choice", "pos": "a0",
               "config": { "multiple": false },
               "options": { "lead": { "name": "Lead", "color": "grey", "pos": "a0" } } } }
```

- **`xKey`** — the property's handle: an alias, not a storage key
  (values live under the content-addressed `propId`; keying a write by
  xKey is `property.not_found`). Unique **within the type** — a
  read-then-create preflight, `409 property.xkey_conflict`
  (`details: {xKey, existingPropId}`); two devices working apart can
  still both land it, and then both columns persist (docs/27-descriptors.md
  § Handles). Mutable via PATCH, same check.
- **`meta`** — the consumer flags the server interprets: only
  **`meta.index`**, which controls how the search indexer treats the
  property's value (absent ⇒ indexed under the default scope `props`;
  `"<scope>"` ⇒ that scope; `"none"` ⇒ excluded — `docs/13-index.md`
  § prop chunker). Any other key → `400 request.invalid_field`;
  descriptive metadata (display order, icon, …) lives under `xFormat`.
- **`xFormat`** — the descriptor: everything descriptive beyond the
  kind, one object the SDK stores opaquely and **this server is the
  semantics boundary for**. The full contract — the six interpreted keys
  (`type`, `icon`, `pos`, `options`, `relation`, `config`), the v1 slug
  vocabulary and the kind each requires, the merge model, the client
  rules — is `docs/27-descriptors.md`. On create the interpreted keys are
  typed, the slug is checked against `kind`, `tags` / `validate` /
  `compute` are refused, vendor-namespaced keys (`acme`) pass verbatim,
  and every key in the bag is non-empty, dot-free and not `$`-prefixed:
  `400 request.invalid_field` for a shape problem, `400
  property.format_invalid` for a vocabulary one. `null` reads as absent.
- **`scope`** — the property's write/sync class: `"synced"` (default —
  everyone in the space), `"account"` (this account's devices only, via
  the private tech space), or `"local"` (this device only, never synced).
  `"derived"` is reserved for built-ins → `400 request.schema`. Like
  `kind`, scope is pinned by the first write — changing it means defining
  a new property. `GET …/properties` returns each definition's `scope`
  (pre-scope definitions read back as `"synced"`). Value writes need no
  scope parameter: `/set/:typeId` auto-routes by the declared scope
  (below).

```json
{ "name": "pin", "kind": "boolean", "xKey": "pin", "scope": "local" }
```

`GET …/properties` reads every definition back as
`{id, name, description?, xKey, kind, scope, meta?, xFormat?}` —
`xFormat` verbatim as stored, absent for a property that never declared
one (it renders structurally from `kind`).

**Values are validated against the current slug** on every property
write — `POST …/set/:typeId`, `initialProperties` on object create,
bundle `rootProperties`: a `date` is an instant at midnight UTC, a
`relation` an array of plain `any://<objectId>` URIs (no spaceId
segment, no fragment), a `choice` an array of option keys (one unless
`config.multiple`), a `period` / `money` / `geo` its exact shape, and so
on per the vocabulary table → `400 property.format_violation`
(`details: {propId, format, reason}`; `format` is the slug). Option
membership is **not** enforced (dangling-tolerant), nor is object
existence or type. An unknown slug gets no value checks. Known gap: raw
`POST /v1/spaces/:spaceId/modify` against the `properties` dataset
bypasses value validation.

**`PATCH …/properties/:propId`** — a generic per-path patch to a property
definition (`TypesAPI.PatchProperty`): rename, the handle, the index
flag, and every descriptor path — slug, icon, order, option CRUD,
relation targets, config. Body:

```json
{ "set":   { "xFormat.options.high.name": "High",
             "xFormat.options.high.color": "red",
             "xFormat.options.high.pos": "a0",
             "xFormat.config.multiple": true },
  "unset": [ "xFormat.options.low" ] }
```

`set` maps a dotted path to its new value; `unset` lists dotted paths to
remove (naming a whole option key, e.g. `xFormat.options.high`, deletes
that option). All ops apply in one CRDT change (atomic); each leaf
merges per-path, so concurrent edits to different options/leaves
converge. Deleting then re-adding the same option key works (it's a
field unset, not a record tombstone); unsetting the last option leaves
an empty `options` object behind.

Mutable paths: `name`, `description`, `xKey`, `meta.index`, and every
path under `xFormat`. **A `set` targets a leaf and never carries an
object**: `xFormat` itself, `xFormat.options`, `xFormat.options.<key>`,
`xFormat.options.<key>.meta`, `xFormat.relation`, `xFormat.config` and
`meta` can be **`unset`** but not set (a set would replace the whole
container and drop what other clients wrote) → `400
request.invalid_field`. Interpreted leaves are typed —
`type` / `icon` / `pos` / option `name` / `color` / `pos` / `meta.<k>`
strings, `relation.targetTypes` an array of strings, `relation.filter`
a string that parses as a query condition, `config.<k>` a scalar —
`400 request.invalid_field` on a wrong shape, `400
property.format_invalid` on an unparseable filter, a set of a reserved
key (`validate`, `compute` — unset stays allowed as the repair path), or
a `xFormat.type` that does not fit the pinned kind (a slug only moves
within one kind). Vendor subtrees take any non-object value at any
depth, with the same key rules as create. Outside `xFormat` a set value
is a JSON string — `null` is refused, a clear is an unset. Pinned paths
(`kind`, `scope`, `items`, `properties`) → `400 property.immutable`;
unknown paths (including the retired `format.*` and `xKind`) → `400
request.invalid_field`; a `xKey` another property holds → `409
property.xkey_conflict`. PATCH/DELETE on a registered built-in type →
`400 type.registered`. Returns `204`; `404 sdk.not_found` for an
unknown type/propId. At least one `set`/`unset` entry is required.

Examples: rename `{ "set": { "name": "Priority" } }`; recolor
`{ "set": { "xFormat.options.high.color": "blue" } }`; delete an option
`{ "unset": [ "xFormat.options.high" ] }`; grow a descriptor onto a bare
property `{ "set": { "xFormat.type": "email", "xFormat.icon": "envelope" } }`.

**`DELETE …/properties/:propId`** (`TypesAPI.RemoveProperty`) tombstones
the definition and returns `204`. Existing instance values are **not**
cleaned up — subsequent writes to that propId are dropped op-by-op
(dangling-tolerant). Unknown/already-removed propId → `404 sdk.not_found`.

#### Parts and modules

A type is properties (the columns on its objects) plus **parts**: the
display units a client renders for an object of the type — a body, a
transcript, a task list — each owning one or more **datasets**. Every
dataset is served by a **module**: `records`, the built-in generic
module (a schema-enforced dataset, § Runtime dataset schemas below),
or a compiled-in module with its own handler and write surface —
`editor` (block bodies, § Objects) and `chat` (messages, § Chat). The
part is what the client renders; the module is what the server
enforces.

Where a dataset's records live is the **collection** — the `dataset`
value on every read and write:

- **namespaced** (the default): `<typeId>_<key>`. Owned by this type
  alone; two types each declaring a `notes` editor part have two
  bodies. `records` datasets are always namespaced.
- **shared** (`"shared": true`): the module's canonical collection —
  `editor_blocks`, `chat_messages`. Every type declaring a shared
  editor part contributes to the same body, so an object carrying two
  document-ish types has one body, not two. The key is the canonical
  name (omit it or spell it exactly; anything else is `400
  dataset.shared_conflict`); one shared dataset per module per type
  (a second is `409 dataset.key_conflict` on the canonical key); only
  modules with a canonical collection share (`records` never does —
  `400 dataset.shared_conflict`). `chat` is shared-only in v1.

A module may be **reserved** to the server's own installs: a part or
dataset draft naming it — on a type, or in a bundle body — is `400
dataset.module_reserved`, decided from the compiled-in catalog before
any wait. Only the server's own catalog install and a registered
type's static part may declare it. No shipped module is reserved yet;
the mechanism is what lets the server's catalog own the one install
of a module (the general chat under `chat`) without a client racing
it.

**An object holds a collection while it carries a declaring type.**
The write gate is on the object's `any.types`: a write into a
collection none of the object's types declare — the SDK's local
write-time check, on every module's write path — is `400
dataset.not_declared`. Attach the type first (object create `types`,
`POST …/properties/:objectId/attach/:typeId`); no write attaches one.
Inbound changes are read-tolerant, so a peer that removed a part still
applies data from before the removal. Removing a part withdraws the
declaration: the collection stops accepting writes on objects that
carry no other declaring type; existing records are not cleaned up.

`POST …/types/:typeId/parts` → `201 {partId}` — the part and every
dataset under it land in one change:

```json
{ "key": "body", "name": "Description", "pos": "a0",
  "ui": { "type": "document" },
  "datasets": [ { "module": "editor", "shared": true } ] }
```

```json
{ "key": "transcript", "name": "Transcript", "ui": { "type": "table" },
  "uses": ["speakers"],
  "datasets": [ { "key": "segments", "idRule": "user",
      "fields": [ { "key": "time", "kind": "number" },
                  { "key": "speaker", "kind": "string" },
                  { "key": "text", "kind": "string", "mutableBy": "any" } ] } ] }
```

- `key` — a slug (`[a-z][a-z0-9_]*`, ≤64), unique among the type's
  parts, pinned (`409 dataset.key_conflict`; `400 dataset.decl_invalid`
  for a non-slug).
- `name` / `icon` / `pos` / `hidden` — the display slice; clients sort
  parts by `pos` (lexid) and hide `hidden` ones by default.
- `ui` — the widget descriptor, an object in the x-format shape
  (`{type, config}`) with a client-owned vocabulary (`document`,
  `table`, `board`, `chat`, …); replaced whole; absent = the first
  dataset's module default.
- `uses` — keys of other datasets **of this type** the part renders
  without owning (a transcript part reading the `speakers` dataset).
- `datasets` — the initial declarations, each `{key?, module?, shared?,
  …}` plus the records-module schema fields below. `module` defaults to
  `records`; an unknown module is `400 dataset.module_unknown`. A
  module-served dataset (`editor`, `chat`) carries **no** `fields` —
  the module owns the schema — so any field declaration on it is `409
  dataset.module_owned`.

`GET …/types/:typeId/parts` → `{parts: [{id, key, name?, icon?, pos?,
hidden?, ui?, uses?, datasets: [<DatasetDef>…]}]}` — the compiled
view, each dataset carrying its `collection`, `module`, `shared` and
`partId` (the same `DatasetDef` shape `GET …/datasets` lists flat).
Parts and datasets fold by key across replicas (two devices declaring
the same key while apart converge on one definition; a disagreement on
a pinned leaf marks it `invalid`).

**`PATCH …/parts/:partId`** takes `{set, unset}` over the mutable
leaves `name`, `icon`, `pos` (strings), `hidden` (boolean), `ui` (an
object, replaced whole), `uses` (an array of keys); `key` is pinned
(`400 dataset.immutable`). **`DELETE …/parts/:partId`** removes the
part and every dataset under it (`204`; `404 sdk.not_found`). Datasets
are added later with `POST …/parts/:partId/datasets` → `201
{datasetDefId, collection}` and evolve through the dataset routes.
Every write on a registered built-in type is `400 type.registered`.

Rendering rule for clients: take the object's primary type (highest
`weight`), render its `layout` with the parts of **every** carried
type, ordered by `pos`; a shared collection appears once however many
types share it. Types remain plain user types — a client that wants
every peer to agree on "the page type" registers it as a bundle
(§ Bundles) rather than minting one per device.

#### Runtime dataset schemas

A declarative dataset schema, defined at runtime under a part of a
**user type**, enforced generically by the SDK apply path on every
peer as the definition syncs — a schema alone expresses what
previously took a compiled-in handler: required fields, write-once vs
author-mutable fields, author-only delete, derived creator/time
stamps, user-supplied record ids, search extraction. This is the
`records` module — the default when a dataset names none. Registered
built-in types (`dataview`, `page`, …) refuse (`400 type.registered`) —
their datasets are statically declared. SDK contract (vocabulary,
convergence rules, storage model, runtime registration): the SDK's
`docs/17-user-datasets.md`.

`POST …/types/:typeId/parts/:partId/datasets` → `201 {datasetDefId,
collection}` (or inline in the part's `datasets` on `POST …/parts`):

```json
{ "key": "articles", "displayName": "Articles",
  "idRule": "user", "deleteBy": "author",
  "search": { "title": "title", "text": "body" },
  "fields": [
    { "key": "title", "kind": "string", "required": true, "mutableBy": "author",
      "description": "Headline", "xFormat": { "type": "text", "icon": "heading" } },
    { "key": "body",  "kind": "string", "mutableBy": "author" },
    { "key": "author",    "stamp": "creator" },
    { "key": "createdAt", "stamp": "createTime" },
    { "key": "updatedAt", "stamp": "modifyTime" } ] }
```

- `key` — the dataset's slug inside the type; pinned, unique among the
  type's parts and datasets (`409 dataset.key_conflict`). The records
  live in the namespaced collection **`<typeId>_<key>`** — the
  `collection` the reply and every listing carry, and the `dataset`
  value on reads and writes. Keys are never space-unique: two types
  can each declare `entries`, and the search indexer's virtual names
  cannot collide with a namespaced collection. Definitions racing in
  from other members fold by key SDK-side (smallest definition id
  wins; a pinned-leaf disagreement marks the fold `invalid`).
- `idRule` — `auto` (default; record ids derived from the change,
  explicit client ids rejected) or `user` (caller-supplied ids under
  `idPattern` / `idMaxLen`, defaults `[A-Za-z0-9._:-]+` / 128; the id
  doubles as the upsert idempotency key).
- `deleteBy` — `anyone` (default) or `author` (requires a
  `stamp: creator` field; deletes by anyone else are dropped at apply).
- per-field `mutableBy` — default write-once (writable only in the
  creating change); `author` (requires a `stamp: creator` field) or
  `any` opt into post-create edits. Every allowed edit bumps the
  `modifyTime` stamp if declared.
- per-field `stamp` — `creator` / `createTime` / `modifyTime`: derived
  at apply time, client writes rejected; forces derived scope; `kind`
  may be omitted (creator ⇒ string, times ⇒ number).
- `required` — must be present on create; declarable only at
  AddDataset (an additive required field would reject the dataset's own
  history on fresh devices) and incompatible with `stamp`.
- `search` — the x-search extraction mapping (docs/13-index.md
  § Schema chunker); `title`/`text` either optional. `text` is a bare
  field key **or a non-empty array of field keys** (`["body",
  "notes"]`) — the indexer renders each mapped field and joins the
  non-empty values into one body, in mapping order. A single key is
  canonicalized to the bare string on every read-back (definition list
  and discovery), so single-field declarations keep the scalar shape.
  An array must name at least one key, none empty, no duplicates
  (`400 dataset.decl_invalid`). Mapped keys are not required to be
  declared fields (dynamic datasets may map undeclared ones). The
  optional `scope` slug (`index.ValidScope`; `400
  request.invalid_field` otherwise) picks the index scope the
  dataset's entries land under — absent = `basic`. Scopes are the open
  slug set `/search` filters on; `props` inherits that scope's
  FTS-only rule (never embedded).
- `dynamic` / `skipHistory` / per-field `scope` and `shape` — as in
  compiled-in declarations. (`skipHistory` declared after the history
  index opened applies from the next index open — SDK limitation.)
- per-field `description` and **`xFormat`** — the descriptive slice: the
  same descriptor a property carries (`docs/27-descriptors.md`),
  validated the same way against the field's kind (the wire `kind`, the
  shape's top-level kind, or the kind a stamp implies — creator ⇒
  string, times ⇒ datetime) and stored opaquely. Neither enters the
  schema — a display edit never re-registers the dataset.

`GET …/datasets` reads each field back whole: `{id, key, name?,
description?, kind, shape?, scope, required?, mutableBy, stamp?,
xFormat?}` — `shape` (`{kind, items?, properties?}`) only when one was
declared beyond the bare kind. Space-level discovery
(`GET /v1/spaces/:id/datasets`) renders `description` and `x-format` on
the field nodes of the JSON Schema document.

**`PATCH …/datasets/:defId/fields/:fieldId`** (`TypesAPI.PatchDatasetField`)
edits one field's mutable leaves under the property PATCH rules: `name`,
`description` (strings), and every path under `xFormat` (a `set` targets
a leaf, containers are unset-only, interpreted leaves are typed, a slug
move is checked against the field's kind). The behavioral declaration —
`key`, `kind`, `shape`, `scope`, `required`, `mutableBy`, `stamp` — is
pinned → `400 dataset.immutable`. `404 sdk.not_found` when the field is
not on this dataset. Returns `204`.

A malformed declaration (unknown enum labels, `mutableBy: author`
without a creator stamp, duplicate stamp kinds, …) → `400
request.invalid_field` or `400 dataset.decl_invalid`.

**Semantics of the pinning model:** behavioral parts — `key` (and with
it the collection name), `module`, `shared`, `dynamic`,
`idRule`/`idPattern`/`idMaxLen`, `deleteBy`, `skipHistory`, field
`key`/`kind`/`shape`/`scope`/`required`/`mutableBy`/`stamp` — are
pinned for the definition's life; remove and re-add under a new
definition to change them. Display parts patch:
**`PATCH …/datasets/:defId`** takes the same `{set, unset}` shape as
property patch over the mutable leaves `description`, `displayName`,
`search.title`, `search.text`, `search.scope` (a whole `search`
replace is pinned; a scope value must pass `index.ValidScope`). Every
leaf is a plain string except `search.text`, which also accepts a
non-empty array of unique field keys — same forms and validation as
the declaration (`400 request.invalid_field` on an invalid array; a
single-element array is stored as the bare string). A search-mapping
patch applies to records as they (re-)index — already-indexed docs
keep their stored scope and extracted text until their object next
goes dirty.
Pinned path → `400 dataset.immutable`; unknown
`defId` → `404 sdk.not_found` (existence-preflighted — the SDK itself
would silently no-op).

**Evolution is additive**: `POST …/datasets/:defId/fields` → `201
{fieldDefId}` appends a field (never `required`);
`DELETE …/datasets/:defId/fields/:fieldId` drops one field definition
(values stay stored; subsequent writes to the field are rejected as
undeclared on non-dynamic datasets; a removal that would invalidate
the remaining declaration — e.g. the creator stamp of an author-gated
dataset — is refused). The SDK keys field definitions by (typeId,
fieldId) — `:defId` rides the URI for hierarchy only.

`GET …/types/:typeId/datasets` returns the compiled view:
`{datasets: [{id, key, collection, module, shared?, partId,
displayName?, description?, dynamic?, idRule, idPattern?, idMaxLen?,
deleteBy, skipHistory?, search?, fields: [{id, key, name?, kind,
scope, required?, mutableBy, stamp?}], invalid?, invalidReason?}]}`.
`invalid` marks a definition whose folded declaration fails validation
— it never registers or accepts data but stays listed so it can be
repaired (add the missing field) or removed. Field read-back drops
`description` and nested `shape` (leaf kind only). Runtime datasets
also appear in the space's discovery document (§ Dataset schema
discovery) under their collection name with `owners`, `module` and the
behavioral `x-*` keywords.

`DELETE …/datasets/:defId` tombstones the definition (unknown defId →
`404 sdk.not_found`, existence-preflighted). Existing record data is
**not** cleaned up (the property-removal stance); subsequent writes
drop once peers apply the removal; the search index evicts the
collection's docs lazily (docs/13-index.md § Removal semantics).

**Data path:** the existing dataset-parameterized surface works as-is —
`POST /v1/spaces/:spaceId/modify` / `/delete-records` write,
`POST …/query[/subscribe]` read (dataset = the definition's
`collection`; records live on objects carrying the owning type — the
first write needs the type attached, e.g. via object create `types`;
`400 dataset.not_declared` otherwise). `id: user` datasets additionally
get the batch upsert below.

#### Upsert records

`POST /v1/spaces/:spaceId/upsert` (`Space.Upsert`) — schema-driven
batch ingest into an `id: user` dataset:

```json
{ "objectId": "bafy…", "dataset": "articles",
  "records": [
    { "id": "a1", "fields": { "title": "Hello", "body": "…" } } ],
  "pageSize": 500, "traceIds": ["import-42"] }
```

Per record, keyed by the caller-supplied id (the idempotency key):
absent → created (one multi-field set); present → only declared-mutable
fields are diffed against stored values, each changed field lands as a
single-path set, identical records are skipped. Re-running an identical
batch is a no-op — **the first genuinely idempotent write** on the
surface. One CRDT change per page (`pageSize` default 500). Not
transactional against concurrent writers; the intended deployment is a
single ingest writer per dataset (concurrent creates of the same id by
different members are outside the convergence contract — the SDK's
`docs/17-user-datasets.md` § The IdRule: user contract).

Response (200 even with rejections — the `/modify` partial-success
stance):

```json
{ "pages": [ { "versionId": "…", "changeId": "…", "recordIds": [] } ],
  "created": 1, "updated": 0, "skipped": 0,
  "rejections": [ { "index": 3, "id": "a4",
                    "code": "upsert.immutable_field",
                    "reason": "…" } ] }
```

Rejection codes: `upsert.immutable_field` (payload would change a
write-once field), `upsert.not_author` (author-mutable field on another
author's record), `upsert.record_deleted` (stored tombstone — ids never
reuse), `upsert.rejected` (creation screening: missing required field,
id pattern/length violation, undeclared field on a non-dynamic dataset,
write to a stamped field — the specific cause in `reason`). Whole-call
errors: `400 upsert.requires_user_ids` (dataset not declared
`idRule: user`), `400 dataset.unknown` (no such records collection in
the space — a module collection such as `chat_messages` is never
upsertable), `400 dataset.not_declared` (the object carries no type
declaring it).

#### Documents and chats

There is no built-in `editor` or `chat` type. "This object is a
document" is a type whose part shares the editor module — the built-in
`page` (§ Built-in hidden types) or a user type; "this object is a
chat" is a user type whose part shares the chat module (§ Parts and
modules). What used to be the reason for a built-in — every client
minting its own type and racing into parallel definitions — is solved
by registering the type through a bundle (§ Bundles), which converges
on one type per space: a client's document type is a bundle-declared
type with an editor part, a space's chat the `general-chat/v1`
bundle with a chat part (the catalog's `general-chat` usecase declares
the same under `system:general-chat/v1` — a different derived root;
`docs/28-well-known-bundles.md` § What clients delete). Listing a
space's documents is a filter on
the type ids that declare the editor (`owners` of `editor_blocks` in
§ Dataset schema discovery — `page` is always among them):
`{"filter": {"any.types": {"$in": [<owners>]}}}` on
`…/objects/query[/subscribe]`. Being user types, the declared ones
carry properties, a `weight` and a `layout` like any other; `page`
carries none — a client that needs them declares its own document
type.

#### Built-in `dataview` type

`dataview` is the built-in for **saved views** — a named, shareable way
of looking at a set of objects — in two levels: a host object carries
many **dataviews** (named, ordered tables), each with its own **views**.
It attaches to a host object (including a **type object**, which is how
"views on a type" works) and owns two records datasets under one part
`views` (`ui: {"type": "table"}`): `dataviews`, one record per dataview,
and `views`, one record per view. Hidden, like the three types below.

```json
// dataviews
{ "id": "default", "name": "Tasks", "icon": "✅", "pos": "a0",
  "creator": "<identity>", "createdAt": { "$date": "…" }, "modifiedAt": { "$date": "…" } }

// views
{
  "id": "default", "dataview": "default",
  "name": "All", "icon": "📋", "pos": "a0", "layout": "table",
  "query":          { "type": "plain", "filter": {…}, "sort": […], "groupBy": {…} },
  "layoutSettings": { "visible": […], "order": […], "widths": {…} },
  "localSettings":  { "widths": {…} },
  "creator": "<identity>",
  "createdAt": { "$date": "2026-08-20T17:06:40Z" },
  "modifiedAt": { "$date": "2026-08-20T17:06:40Z" }
}
```

`filter` and `sort` are the `/query` body shapes verbatim, so a view
feeds straight into `…/objects/query[/subscribe]`. `query`,
`layoutSettings` and `localSettings` are **opaque** — the server checks
only that each is an object; clients own the vocabulary and decide what
a rule naming a deleted property means. A view's `dataview` is required
but **not validated** against the collection, and deleting a dataview
does not cascade — orphan views stay readable and writable for the
client to delete or re-parent (`$set dataview`).

No bespoke endpoints and no `dataview` module: write through
`POST /v1/spaces/:spaceId/modify` with `dataset: "dataviews"` /
`"views"`, read through `…/query[/subscribe]` — the dataview list sorted
by `pos`, one dataview's views with `{"filter": {"dataview": "<id>"},
"sort": ["pos"]}` (indexed). `name` + `pos` are required on a dataview,
`dataview` + `name` + `pos` + `layout` on a view; `creator` / `createdAt`
/ `modifiedAt` are server-stamped and reject client writes; every synced
field is `mutableBy: any` and any writer may delete a record.

Record ids are **client-supplied** (`idRule: user`) in both datasets —
ensure the default dataview and its default view with fixed ids plus
`upsert`, never create-on-open, or two devices mint two "All" views.
View ids are one namespace per host, so a second dataview's views take
`<dataviewId>.<key>` ids. A deleted id is **burned permanently**:
re-upserting it returns `200` with a `rejections` entry and creates
nothing, so an ensure must inspect `rejections` and fall through to the
next id in a deterministic sequence (`default`, `default-2`, …). See
`24-data-views.md` § Ensuring the defaults.

`localSettings` is `scope: local` — this device's override of
`layoutSettings`, written with `"scope": "local"` on `/modify` (explicit
id, no upsert) and never synced. Column-drag autosave belongs there so
it does not push a change to every member.

Only the **shared** tier ships; account- and device-private views need
scoped datasets (SYN-174). Full model, the client grouping recipe, and
the tier roadmap: `24-data-views.md`.

#### Built-in hidden types: `page`, `miniapp`, `bin`

Three more registered types an object **opts into** rather than a class
a user picks, so all three are `hidden`: out of `GET …/types` unless
`?includeHidden=true`, resolvable by `GET …/types/:typeId` always,
`builtIn: true` with `xKey` equal to the id (which reserves `page`,
`miniapp` and `bin` against user types — `409 type.xkey_conflict`),
static (`400 type.registered` on every write), and present in every
space by construction — nothing installs them and nothing stamps them
onto an object: a client decides which types its objects carry
(`types` on `POST …/objects`, or `…/properties/:objectId/attach/:typeId`).

**`page`** — the plain document. No properties; one part `body`
(`ui: {"type": "document"}`) whose dataset is the editor module's
shared collection, so an object carrying `page` holds `editor_blocks`
and every `…/editor/editor_blocks/**` route works on it. Optional: a
client that wants a plain body uses it; one with its own document types
declares them with an editor part (§ Parts and modules) — both share
the collection, and `page` is always among the `owners` of
`editor_blocks`. Being registered, `page` carries no `weight` and no
`layout`: an object carrying only `page` has no primary type and renders
by the client's default.

**`miniapp`** — the marker of an object that runs an installed bundle.
One property, `bundle` (string): the id of the installed bundle
(§ Bundles — `system:wiki/v1`, a marketplace id, …), which is what a
client needs to know what to open. No parts. Written through the
generic `POST …/properties/:objectId/set/miniapp`
(`{"patch": {"bundle": "<bundleId>"}}`); the object must carry the type.
Catalog miniapp roots (§ Catalog) carry it from their first change
with `bundle` set to the bundle id (`system:wiki/v1`,
`system:collections/v1`, …) plus any other `miniapp` value the catalog
declares; a value the catalog gains later is healed onto existing
roots at their next setup.

**`bin`** — the marker of an object moved to the bin. Move to bin is
`POST …/properties/:objectId/attach/bin`, restore is
`…/detach/bin` — the plain type-binding routes, no wire surface of
their own. The server stamps two properties on the move and clears them
on restore: `movedAt` (datetime — `{"$date": …}` on the wire, the
server clock) and `movedBy` (string — the account identity, the same
encoding as `author` / `modifiedBy`). The membership op and the stamps
ride **one** synced change, so the `changeId` the call returns names
the move, a bin carrier never lacks its stamps and a restored object
never keeps stale ones; a second move re-stamps. Clients filter carriers
out of ordinary lists — `{"any.types": {"$nin": ["bin"]}}` — and list
the bin with `{"any.types": "bin"}` sorted `-bin.movedAt` (not indexed;
the bin is small). Restore brings the object back as it was: detaching
is not a delete, nothing else on the row changes. Permanent deletion
stays `DELETE …/objects/:objectId`. The stamps are ordinary synced
properties: the server writes them, but a peer can write the namespace
directly, so a reader treats an absent stamp as unknown, never as "not
in the bin".

### Properties (values on objects)

| Method | Path                                                          | Purpose                          |
|--------|---------------------------------------------------------------|----------------------------------|
| GET    | `/v1/spaces/:spaceId/properties/:objectId`                    | `PropertiesAPI.Get`              |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/set/:typeId`        | `PropertiesAPI.Set`              |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/attach/:typeId`     | `PropertiesAPI.AttachType`       |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/detach/:typeId`     | `PropertiesAPI.DetachType`       |

Scoped properties (v0.0.11) replaced the former per-scope set endpoints
(`/base`, `/account`, `/device` → `SetBase`/`SetAccount`/`SetDevice`) with
a single scope-aware `/set/:typeId` → `PropertiesAPI.Set`: every propId in
the patch must resolve to the SAME declared scope (the SDK rejects
mixed-scope or unknown-key patches; scope is inferred from the props).

Runtime type binding: `attach` adds a type to the object's `any.types`,
admitting writes to that type's membership-gated datasets; `detach`
removes it. Both take no body, return `ModifyResult`, and are idempotent
(`$addToSet` / `$pull`). Bind at creation instead via the `types` array
on `POST /v1/spaces/:spaceId/objects` when the object is new.

`attach` pre-flights both ids — `404 object.not_found` for an unknown
object, `404 type.not_found` for a type the space doesn't have — because
`any.types` is a synced DAG write with no validation behind it, so a
typo would replicate permanently. `detach` deliberately checks neither:
it is the repair path for a row that already carries a bogus id.

Detaching is **not** a delete: values in that namespace and records in
the type's datasets stay as orphan data, read-tolerant by design, and
re-attaching brings them back into view. See `08-clients.md`
§ "Preflight-validate writes against the bound types".

The built-in `bin` is the one type these routes treat specially:
`attach/bin` also stamps `bin.movedAt` / `bin.movedBy` and `detach/bin`
clears them, in the same change as the membership op (§ Types →
Built-in hidden types).

### Chat (the `chat` module)

| Method | Path                                                                     | Purpose                  |
|--------|--------------------------------------------------------------------------|--------------------------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages`                         | send a message           |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId`                  | edit own message text    |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId`                  | delete own message       |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions/:emoji` | toggle own reaction      |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/read-all`                         | mark everything read     |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/read`             | mark msg + all above read |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions-read`   | mark msg's reactions read |

A chat is an object holding the `chat_messages` collection — served by
the compiled-in `chat` module, which an object holds while it carries
a type whose part declares `{"module": "chat", "shared": true}`
(§ Parts and modules; chat is shared-only, one collection per object).
The routes below write into it; a write on an object with no such
type is `400 dataset.not_declared`.

**Finding the chat object.** A space's chats are not server-owned:
register one through the bundles API (§ Bundles) and use its `rootId`
as the `<objectId>` below.

```
POST /v1/spaces/:spaceId/bundles
{ "id": "general-chat/v1", "name": "General", "derived": true, "hidden": true,
  "layout": { "type": "chat" },
  "parts": [ { "key": "chat", "datasets": [ { "module": "chat", "shared": true } ] } ] }
→ 200 { "bundle": { "rootId": "<chat object>", "derived": true, ... }, "installed": true|false }
```

Ensure is adopt-or-install, so every client that runs it lands on the
same object instead of each minting a chat of its own — the failure
mode this replaces, most visible in 1-1 direct spaces. The `parts`
declaration makes the root its own type with a chat part, so it
accepts `chat/messages` writes immediately. `id` is yours to choose;
`general-chat/v1` is the convention for "the chat of this space", and a
space can carry as many purpose-specific chat bundles as you want.

`"derived": true` is part of the convention: the chat's root id is
computed from the bundle id, so every member and device lands on it
offline, two sides of a 1-1 included, and the chat can never fork into
two parallel conversations. It also makes the chat permanent — a
derived root cannot be deleted (§ Bundles → Derived roots), which is
what you want for "the chat of this space" and not what you want for a
bundle a user may uninstall.

One caveat carries over from § Bundles for a **created** chat root
(`derived` absent): `rootId` is provisional until the space syncs
(re-read after), and two members ensuring concurrently produce
`losers` to handle. A derived root has neither problem.

Read tracking: `…/:msgId/read` marks the message and everything
ordered before it (`_ver.id` order) read; `…/read-all` clears the
whole chat. `…/:msgId/reactions-read` clears the unread **reaction(s)**
on that message — a reaction is a change ordered *after* its target
message, so `…/:msgId/read` (which cuts at the message's own `_ver.id`)
never covers it; this route lets a client that has shown the reaction
to the user clear it. **Scope caveat:** the underlying `MarkRead` covers
the reaction **and its causal ancestry**, so it also marks read any
unread *message* the reactor had already seen when they reacted
(everything causally before the reaction); messages that arrived *after*
the reaction stay unread — that surviving tail is what still
distinguishes it from `read-all`. In the target case (a reaction on an
already-read message) the ancestry holds nothing unread, so only the
reaction clears; when unread messages coexist, mark the visible ones
read first so the only extra thing this clears is messages the user has
already seen. It is a no-op (still `204`, never `404`) on a message with
no unread reactions. All three return `204`, are idempotent and
forward-only (no mark-unread), work offline, and sync across the
account's devices.
Read state is private — no read receipts. The SDK materializes
per-message `unread` / `unreadMention` / `unreadReactions` flags
(filterable) and per-chat `unreadCount` / `unreadMentions` /
`unreadReactionsCount` row properties (nested under the type
container on the row: `chat.unreadCount`). When to call what — including
the viewport rule and the unread divider — is covered in
`16-chat.md`.

Liveness goes through the per-object query/subscribe endpoint with
`dataset=chat_messages`:

```
POST /v1/spaces/:spaceId/query/subscribe
{ "objectId": "<chatObjectId>",
  "dataset":  "chat_messages",
  "sort":     ["-_ver.id"],
  "limit":    50 }
```

Subscribe descending (`-_ver.id`) with a `limit`: the window holds the
*newest* `limit` messages, so new arrivals enter it (oldest drops out as
`removed`). Ascending would pin the oldest `limit` and new messages would
never appear. Always set a `limit` — an unbounded subscribe risks
overflowing the mailbox.

New incoming messages arrive in `added`; edits in `updated`; deletes
and reactions toggling off in `removed`. `added.doc` carries the full
message body — no follow-up GET needed. See `04-events.md`.

#### Message wire shape (read path)

This is what `POST /query` and `/query/subscribe` return for a
`chat_messages` record. The write endpoints (send / edit / delete /
react) do NOT return this — they return the shared write result
`{versionId, changeId, recordIds}` (see § Write responses); the message
body is always read back through the query path.

```json
{
  "id":               "<change-derived id>",
  "creator":          "<accountId>",
  "createdAt":        {"$date": "2026-05-01T21:00:00.000Z"},
  "modifiedAt":       {"$date": "2026-05-01T21:00:00.000Z"},
  "replyToMessageId": "<msgId>",
  "agent": {
    "name": "bao", "debugLink": "any://<spaceId>/<debugObjId>#turn_3", "done": true
  },
  "text":             "**hi** _there_",
  "mentions":         ["<identity1>", "<identity2>"],
  "attachments": {
    "a1": { "type": "link",  "link": "any://abc/def" },
    "a2": { "type": "image", "link": "https://example.com/x.png" }
  },
  "context":          { "spaceId": "<spaceId>", "objectId": "<objectId>", "view": "object" },
  "control":          { "kind": "break", "hard": false },
  "reactions":        { "👍": { "<id1>": {"$date": "2026-05-01T21:00:00.000Z"},
                                "<id2>": {"$date": "2026-05-01T21:00:05.000Z"} } }
}
```

A record may additionally carry the device-local read-tracking flags
`unread` / `unreadMention` / `unreadReactions` (booleans, `x-scope`
local). They are not part of the synced message — each device
materializes its own values via `POST …/modify` with
`{"scope":"local"}` (§ Modify records) and they never appear on other
devices. Filterable like any field: `{"filter":{"unread":true}}`.

`createdAt` and `modifiedAt` are server-stamped instants
(`{"$date": "<RFC 3339>"}`). They are equal on a never-edited message —
clients detect edits by comparing them. `text` is markdown; rendering is the client's
problem (`internal/markdown` exists if anyone wants to round-trip).

`mentions` is server-DERIVED (`x-scope` derived) — never accepted from
a client: the send route has no such field and a direct `$set` via
`POST …/modify` is rejected (400 `dataset.validation`). At change
materialization the handler extracts every mention link
(`any://m/<spaceId>/<identity>`, docs/19-links.md) from `text`
(deduped, first-occurrence order, capped at 64 — with the reply
fold-in always retained: when the cap is hit, the last text mention
yields the slot, so link-stuffing can't squeeze the replied-to author
out) and, when
`replyToMessageId` is set, folds in the replied-to message's creator —
a reply is a ping to the original author, and folding it in at write
time keeps every consumer (badge, push, "mentions of me") a single
indexed field check (`{"filter":{"mentions":"<identity>"}}`,
sparse multikey index with `_ver.id` tiebreak). Edits re-derive the
array from the new text. Omitted when the message mentions nobody.
Reply-derived entries are not distinguished from text mentions.
Client recipes: docs/16-chat.md § Mentions.

`agent` is an optional, create-only group the sender sets to mark the
message as written by an agent acting on the signer's behalf (vs typed
by the signer directly). It is NOT cryptographically verified —
`creator` is still the change signer; the group is a UI hint. Fields:

- `name` — required, non-empty, ≤ 256 bytes. Display label.
- `debugLink` — optional, non-empty when present, ≤ 2 KiB. Opaque to
  the server; by convention `any://<spaceId>/<debugLogObjectId>` with
  an optional `#turn_<n>` fragment (1-based LLM-turn ordinal) so a UI
  can deep-link "go to debug" from the message to the turn that
  produced it.
- `done` — required boolean. Liveness: `false` means the run that
  produced this message is still going; clients cycle a typing
  indicator while the *last* message in a chat is an agent message
  with `done: false`. Every run must end with a `done: true` message.

No unknown sub-fields. Immutable post-create as a group. Typical use:
an agent subscribed to `chat_messages` ignores its own messages
(`agent` present) and only responds to human ones (`agent` absent).
Omitted from responses when unset.

`attachments` is an optional, create-only map keyed by short opaque
ids (1–64 chars, `[A-Za-z0-9_-]+`); each entry is `{type, link}`.
`type` is an open enum — known values are `"link"` and `"image"`, but
clients should fall back to rendering `link` as a plain anchor for
unknown types rather than dropping the entry. `link` is ≤ 2 KiB. Up
to 32 attachments per message. Immutable post-create — the handler
rejects $set on the attachments path.

`context` is the sender's view at send time — the page on screen when
they hit send: `spaceId` (required), `objectId` (the open object /
collection / record, when there is one), `view` (the client's view
kind — an open string such as `object`, `collection`, `mail`, `files`).
Optional, create-only, immutable. It is how an agent reading the chat
resolves "here" / "this page"; there is no timestamp on it because the
message's `createdAt` is when the user was there. Ids ≤ 256 bytes,
`view` ≤ 64; an unknown sub-key or an empty `spaceId` rejects 400
`chat.context_invalid` (HTTP) / `field_not_allowed` (handler).

`control` is a client's signal to the agent serving the chat, carried
on a message of its own — the one case where `text` may be empty:
`kind` (required, an open string ≤ 64 bytes the agent interprets —
`break` asks the run in flight to stop), `hard` (optional boolean:
stop now, vs. wrap up at the next turn). Optional, create-only,
immutable; the server stores it opaquely. A client renders it as a
marker in the thread, never as a bubble, and the agent never reads it
as content. An empty `kind` or an unknown sub-key rejects 400
`chat.control_invalid` (HTTP) / `field_not_allowed` (handler).

`reactions` ships on the wire in the same shape it has in storage:
emoji → `{accountId: <changeTimestamp>}`, where the leaf timestamp is
when that identity added the emoji. This is identical to what `/query`
and `/query/subscribe` return for the record, so a client parses
`reactions` exactly one way regardless of which endpoint produced it
(clients sort by the leaf timestamp themselves if they want arrival
order). Authorization on writes is a single path-segment compare
against `ctx.Change.Creator` in the handler: only the change's signer
can write into `reactions.<emoji>.<their-identity>`. The leaf timestamp
is server-derived (`sink.Derive` overrides whatever the client sent).
See `internal/chat/handler.go`.

#### Send

`POST /v1/spaces/:spaceId/objects/:objectId/chat/messages`

```json
{ "text": "hello", "replyToMessageId": "abc",
  "agent": { "name": "bao", "debugLink": "any://sp/dbg#turn_2", "done": false },
  "context": { "spaceId": "<spaceId>", "objectId": "<objectId>", "view": "object" } }
```

`text` is required unless `attachments` is non-empty — a photo sent
with no caption is an ordinary message, so an attachment-only send is
valid and `text` may be `""` or omitted. A message with neither text
nor attachments carries nothing and is rejected 400
`chat.text_required`. `text` is ≤ 32 KiB. `replyToMessageId` is optional, ≤ 256
bytes, and a soft reference — the server doesn't validate that the
target exists (when it does exist, its creator is folded into the
derived `mentions` array; when it doesn't, the fold-in is silently
skipped). `agent` is optional (see § Message wire shape for the
sub-field rules; 400 `chat.agent_invalid` on violations); immutable
post-create. Returns 201 with the shared write
result `{versionId, changeId, recordIds}` — `recordIds[0]` is the
server-derived message id. Read the message back via the query path
above.

#### Read

The bespoke list endpoint is gone — reads go through the per-object
query primitive with `dataset=chat_messages`:

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<chatObjectId>",
  "dataset":  "chat_messages",
  "sort":     ["-_ver.id"],
  "limit":    50 }
```

Sort descending (`-_ver.id`) with a `limit` — that loads the *newest*
page first, the usual chat default. `_ver.id` is the record's `VersionId`
at creation — its position in the any-sync DAG (lex-monotonic, per-change
DAG order) — so sorting by it is logical DAG order, not wall-clock time.
It's stamped once at creation and never bumped by edits, so that order is
stable across edits; only the read direction flips.
Page backward into history as a client-side two-step: take the oldest
`_ver.id` from the page you have, then chain a second query with the same
sort and `filter: {"_ver.id": {"$lt": <id>}}` (older messages). Always
set a `limit` so a long history can't produce a huge response; reverse
each page client-side for oldest-at-top display. The bespoke endpoint's
`before` / `after` / `limit` flags moved off the API surface; the recipe
replaces them. See `08-clients.md` for the full read/write recommendations.

Reactions on queried records ship as
`reactions.<emoji>.<accountId> = {"$date": "<RFC 3339>"}` (the instant
the identity reacted, server-derived) — the same shape the bespoke send
/ edit / react responses return, so there is nothing to transpose
between the read and write paths.

#### Edit / delete (own only)

`PATCH .../chat/messages/:msgId` body `{ "text": "..." }` replaces the
text and bumps `modifiedAt`. `DELETE .../chat/messages/:msgId` tombstones
the record. Both return `200` with the shared write result
`{versionId, changeId, recordIds}` (`recordIds=[msgId]`), `403
chat.not_author` for non-authors, and `404 chat.not_found` for unknown
ids. The handler enforces the same rules for peer-originated changes.

#### React (toggle)

`POST .../chat/messages/:msgId/reactions/:emoji` (no body) toggles the
caller's reaction. The CRDT op is `$set` (add) or `$unset` (remove)
on the leaf `reactions.<emoji>.<callerId>`; the value on add is the
triggering change's instant, server-derived. Because the leaf is
unique per (emoji, identity), two clients toggling at the same time
can't corrupt each other. Returns `200` with the shared write result
`{versionId, changeId, recordIds}` (`recordIds=[msgId]`); read the
updated `reactions` back via the query path.

## Enrichment (moved userspace)

The former built-in `enriched_data` / `enrich_proposal` types, their
bespoke endpoints (`POST …/enriched-data`, `POST …/enrich/apply`) and
the compiled-in enrichment chunker are **gone** — nothing
enrichment-specific belongs in core (the same principle that kept the
email type out). Enrichment is now a userspace convention owned by the
agent: user types discovered by xKey (`enrichments` hub +
`enrich_proposal`), runtime dataset schemas (§ Runtime dataset
schemas) with an `x-search` mapping for indexing, records written
through the generic `/modify` / `/query`, and a deterministic apply
implemented client-side. The convention's contract lives with its
producer (anybao `enrich@v1`); provenance `source` links follow
[docs/19-links.md](19-links.md) § Fragments.
## Agent data (moved userspace)

The agent's operational data — turns, chunks, memory items, triggers,
config, secrets — is a userspace convention owned by the harness
(anybao ADR-017), on the same machinery as enrichment: user types,
runtime dataset schemas with `search.scope` mappings (`agent` /
`history`), records through the generic `/modify` / `/upsert` /
`/query`, homed on children of the `bao/v1` bundle (§ Bundles) and of
each chat's bundle. The server carries nothing agent-specific: no
agent types, endpoints, chunkers, or `SpaceInfo` fields.


### Files (files v2)

Full model — storage tiers, durability states, cache/offload, variants
— in [`docs/17-files.md`](17-files.md). Files always bind to an
existing object; the SDK stores one `payloads` row per file on a
derived per-object child. The file **bytes ride plain HTTP** — upload
is a raw POST body, download a raw GET response — the two deliberate
non-JSON bodies in the API. Everything else is the usual JSON.

| Method | Path | Purpose |
|--------|------|---------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/files`                 | attach a file (raw body upload) → 201 `FileInfo` |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/files/query`           | snapshot one object's payload rows |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/files/query/subscribe` | live windowed view of the same (SSE) |
| GET    | `/v1/spaces/:spaceId/files`                                   | list files (`?objectId=`, `?limit=`) |
| GET    | `/v1/spaces/:spaceId/files/stats`                             | aggregate durability counts |
| GET    | `/v1/spaces/:spaceId/files/subscribe`                         | file durability transitions (SSE) |
| GET    | `/v1/spaces/:spaceId/files/:fileId`                           | one file's info |
| GET    | `/v1/spaces/:spaceId/files/:fileId/content`                   | download the bytes (Range/206 supported) |
| GET    | `/v1/spaces/:spaceId/files/:fileId/status`                    | one file's durability status |
| POST   | `/v1/spaces/:spaceId/files/:fileId/pin`                       | schedule a full background fetch → 204 |
| POST   | `/v1/spaces/:spaceId/files/:fileId/retry`                     | make pending background work due now → 204 |
| POST   | `/v1/spaces/:spaceId/files/:fileId/offload`                   | drop local bytes (keep the file) → 204 |
| DELETE | `/v1/spaces/:spaceId/files/:fileId`                           | delete the file for every member → 204 |
| GET    | `/v1/files/cache`                                             | local cache size, all spaces |
| POST   | `/v1/files/cache/free`                                        | LRU-reclaim `{bytes}` → `{freed}` |
| POST   | `/v1/files/cache/sweep`                                       | one manual safety sweep → 204 |

Errors use the `file.*` namespace (`docs/06-errors.md`): unknown
fileId/objectId → `404 file.not_found`, offload of the only copy →
`409 file.not_durable`, content not fetchable yet →
`409 file.not_available` (retry later), broken variant pairing →
`400 file.variant_invalid`.

#### Upload (attach)

The **raw request body is the file** — no JSON envelope, no multipart.
Metadata rides outside the body:

- `Content-Type` header + `?name=` → stored mime, resolved by the
  **precedence below**. Attach is the only moment a type can be
  attached, and the `curl -T` / typeless-`Blob` default would otherwise
  pin the file to octet-stream for every downstream reader (browser
  tags, model input),
- `?name=` → stored user-facing name. A name without an extension
  gains the one the resolved mime implies, **for binary content only**
  (`?name=pasted` + PNG bytes → `pasted.png`), so a download lands on
  disk as something the OS can open. Text keeps its name — `Makefile`,
  `Dockerfile`, `LICENSE` come back unchanged — and an extension the
  caller supplied is never rewritten ("has an extension" means a short
  alphanumeric suffix with a letter in it, so `Screenshot at 10.32.11`
  still gains `.png` and `v2.0` gains `.pdf`),
- `?variant=` + `?variantOf=` → attach the content as an alternate
  representation (e.g. a thumbnail the client rendered) of an existing
  file **on the same object**. Both or neither.

**Mime precedence** — three signals, strongest first:

1. **The `Content-Type` header**, taken at its word (parameters
   stripped; a malformed parameter does not void the type before it).
   Only the tool defaults that cannot be a file's type —
   `application/octet-stream`, `binary/octet-stream`,
   `application/unknown`, `application/x-www-form-urlencoded` (what
   `curl --data-binary` sends) — plus an absent header and an
   unparseable type mean "the caller didn't say" and fall through.
   `text/plain` is honoured as text but refined by the name as in
   step 3: it is what `fetch()` sends for a string body, and it says
   "text", not which text.
2. **The content** — magic numbers over the first 3072 bytes. Covers
   png/jpeg/gif/webp/heic/avif/tiff, pdf, mp4/quicktime/webm,
   mp3/m4a/flac/ogg, zip/OOXML and more. A binary signature beats the
   name: a file *named* `.png` whose bytes are a PDF stores
   `application/pdf`.
3. **The name's extension, within text.** Text formats carry no
   signature, only conventions the sniffer guesses at (a markdown
   README opening with a badge `<p>` matches the HTML tag list; two
   lines with a comma each match the CSV rule), so for text the name
   decides: `.txt .md .markdown .csv .tsv .html .htm .css .js .mjs
   .json .xml .svg .yaml .yml` map to their types whatever the
   sniffer's sub-verdict. Without a known extension the sniffer's
   signature-backed text verdicts stand (json, xml, svg, shebang
   scripts, …) and its heuristic ones (html, csv, tsv) collapse to
   `text/plain`. Other extensions are not consulted: `main.go` is
   `text/plain`.

Content that cannot be placed leaves the mime **unset** rather than
asserting `application/octet-stream`: absent and "unknown" are
different claims, and the download route already falls back. The body
still streams — at most the 3072-byte sniff window is buffered, and
only when the header left the type open (a typed upload is never
read before the SDK's own checks run).

```
curl -X POST -T photo.jpg -H 'Content-Type: image/jpeg' \
  'localhost:7001/v1/spaces/SP/objects/OBJ/files?name=photo.jpg'

// 201
{ "fileId": "…", "objectId": "OBJ", "rootCid": "bafy…", "size": 482113,
  "inline": false, "durable": false, "cached": true,
  "name": "photo.jpg", "mime": "image/jpeg" }
```

This is the one route exempt from the global 1 MB body limit — the
body streams straight into the SDK. Files < 4096 bytes take the
**inline tier** (`inline: true`, no `rootCid`, durable by
construction, riding the CRDT row itself); larger files are encrypted
and content-addressed locally, then backed up to the network's fileV2
broker. The backup is **attempted synchronously inside the attach
request** (best-effort): with a reachable broker the 201 usually
already says `durable: true`, and attach latency for large files is
dominated by the object-store upload (~upload time for a 10 MB file).
When the broker is unreachable or refuses, attach still succeeds —
`durable: false`, and a persistent background queue retries; watch
`/files/subscribe` or poll `/files/:fileId/status` for the
`inflight → durable` flip.

#### Download (content)

`GET /v1/spaces/:spaceId/files/:fileId/content[?variant=]` serves the
file's verified plaintext as a **regular HTTP resource**: stored mime
as `Content-Type` (octet-stream fallback — sniffing happens at attach,
never here),
`Content-Disposition: inline; filename=…` from the stored name,
`Content-Length`, and full **`Range` / 206** support (the underlying
reader is seekable). Browser tags work directly:

```html
<img src="http://127.0.0.1:7001/v1/spaces/SP/files/FILE/content">
```

Content not yet local streams in from the network on demand; every
fetched block persists, so repeated reads accrete toward a complete
local copy. A file whose bytes are not local and not yet fetchable —
not durable yet, or the network advertises no public read base —
returns `409 file.not_available`: a **retry-later resource state**,
not a fault. The signal that it became fetchable is the row's
`networkSign` appearing (a row-update event on
`…/files/query/subscribe`, or `durable: true` on a re-GET).

#### Payload-row query / subscribe

`POST …/objects/:objectId/files/query[/subscribe]` is the windowed
query/subscribe primitive over ONE object's payload rows — needed
because the `payloads` dataset lives on a derived child object whose
id clients don't know, so the generic `/query` can't reach it. Body
and SSE frames are identical to the generic per-object query
(`filter / sort / limit / offset / includeTotal` + subscribe opts).
Rows expose the **cleartext fields only** (`id`, `rootCid`, `size`,
`networkSign`, `objectId`) — the sealed member meta (name, mime, key)
never appears here; use `GET /files` for typed access. Returns
`404 file.not_found` until the object's first file is attached (the
backing dataset materializes on first Attach) — fall back to
`GET /files?objectId=` until then.

#### File cache (account-wide)

The three `/v1/files/cache*` routes are SDK-level (bytes held across
ALL spaces), so like `/sync-status/subscribe` they sit outside the
space group. `free` drops least-recently-used content that is **safe
to drop** (backed up or unreferenced — never the only copy) and
returns the bytes actually freed; `sweep` is the manual trigger of the
safety pass that otherwise runs only when `files.gcInterval` is
configured (`docs/05-config.md`).

### Members

| Method | Path                                                 | Purpose                            |
|--------|------------------------------------------------------|------------------------------------|
| GET    | `/v1/spaces/:spaceId/members`                        | `MembersAPI.List`                  |
| GET    | `/v1/spaces/:spaceId/members/me`                     | `MembersAPI.Me`                    |
| GET    | `/v1/spaces/:spaceId/members/requests`               | `MembersAPI.JoinRequests`          |
| GET    | `/v1/spaces/:spaceId/members/subscribe`              | `MembersAPI.Subscribe` (SSE)       |
| GET    | `/v1/spaces/:spaceId/members/:identity`              | `MembersAPI.Get`                   |

Static path segments (`/me`, `/requests`, `/subscribe`) are registered
before the `:identity` wildcard so they don't get swallowed. The `Member` wire
shape mirrors `space.Member` 1:1; both `permission` and `status` are
strings (see "Permission / status strings" below). `requestRecordId`
is non-empty only on a pending-request entry — pass it to
`POST /v1/spaces/:id/acl/accept`.

This is the **authoritative per-space roster with rights**: each row
carries the member's `permission` (the role) alongside the profile
(`name` / `iconCid`, resolved from the same identityRepo cache that feeds
the [identities directory](#identities-account-global-directory), with
the join-time metadata as the always-present baseline). For a roster with
roles, this one call is all a client needs — don't reach for the
directory, which is account-global and carries no rights.

```json
// GET /v1/spaces/:id/members
{
  "members": [
    { "identity":"A6ux…",
      "permission":"owner",
      "status":"active",
      "name":"Alice",
      "iconCid":"…" }
  ]
}
```

### Invites

| Method | Path                                                 | Purpose                            |
|--------|------------------------------------------------------|------------------------------------|
| POST   | `/v1/spaces/:spaceId/invites`                        | `ACL.CreateInvite` — replaces any prior invite |
| GET    | `/v1/spaces/:spaceId/invites`                        | `MembersAPI.Invites`               |
| GET    | `/v1/spaces/:spaceId/invites/:recordId`              | `MembersAPI.Invites` — one record; `404 invite.not_found` |
| DELETE | `/v1/spaces/:spaceId/invites`                        | `ACL.RevokeAllInvites`             |
| DELETE | `/v1/spaces/:spaceId/invites/:recordId`              | `ACL.RevokeInvite`                 |
| POST   | `/v1/spaces/join`                                    | `Service.Join` / `Service.JoinGuest` — body carries the share token; guest tokens are auto-detected |
| POST   | `/v1/spaces/:spaceId/guest-key`                      | `ACL.CreateGuestKey` — public read-only access; idempotent, owner only |
| DELETE | `/v1/spaces/:spaceId/guest-key`                      | `ACL.RevokeGuestKey` — rotates the read key; old tokens die |

Mint:

```json
// POST /v1/spaces/:id/invites
// → 201
{ "spaceId":"bafyrei…", "inviteToken":"5ZHbdx…" }
```

`inviteToken` is a base58-packed `(spaceId, invitePrivKey)` produced
by `space.EncodeInvite`. Owners share this string out-of-band; joiners
pass it back verbatim:

```json
// POST /v1/spaces/join
{ "inviteToken":"5ZHbdx…",
  "metadata":{ "name":"Bob","iconCid":"…" } }
// → 202 {SpaceInfo}      (RequestToJoin: status="joining" until owner accepts)
// → 201 {SpaceInfo}      (AnyoneCanJoin: deferred — never returned in v1)
```

A malformed or unrecognized `inviteToken` returns `400 invite.invalid`. A
token for a space this account deleted returns `409 space.deleted` before
anything reaches the network — the tombstone is sticky. A join that merely
ended (the owner declined, or the joiner withdrew it with `cancel-join`) is
not a tombstone: the same call re-requests and the row returns to `joining`.

In the v1 RequestToJoin flow `Service.Join` returns 202: the SDK has
posted the join request, written a `joining` index entry, and the
joiner now polls `GET /v1/spaces/:id/members/me` for the status flip
to `active` after the owner accepts.

The pending join is **account-wide**: the `joining` row syncs to every
device of the joiner's account, each of them reads the space as
`joining` (`GET /v1/spaces?status=joining` — the default list is
active-only; `GET /v1/spaces/:id` serves the row) and none
materializes it (`space.not_accepted` on anything that would load it).
The device that observes the owner's verdict settles the row for all of
them — acceptance flips it to `active` once that device has loaded the
space (the others load lazily), a decline or a `cancel-join` moves it
to `deleted`. Every device of the account may `cancel-join`, not only
the one that requested. `DELETE /v1/spaces/:id` on a `joining` row is
a withdrawal too, never a tombstone: the row reads `deleted` and stays
re-joinable.

Listing returns one entry per active invite record — pass `recordId`
to the DELETE path to revoke a single invite, or DELETE the parent
collection to revoke all in one batch.

```json
// GET /v1/spaces/:id/invites
// → 200
{ "invites": [
  { "recordId":"bafyrei…", "permission":"none", "inviteToken":"5ZHbdx…" }
] }
```

`inviteToken` on a read is the SAME string the mint returned, recovered
from the minting account's synced custody (the ACL record itself
carries only the invite public key). It is present only on the devices
of the account that minted the invite — every other member, whatever
their role, gets the row without it. Two more absence cases: invites
minted before custody shipped (regenerate once to make the token
durable across devices), and custody gone stale because the invite was
replaced or revoked on another device. Clients must treat the field as
optional and fall back to "regenerate to get a shareable code".

#### Guest key (public read-only access)

`POST /v1/spaces/:spaceId/guest-key` mints a shared read-only guest
identity (one per space, idempotent — repeated calls return the same
token; owner only) and returns the same `{spaceId, inviteToken}` shape.
Anyone holding the token joins via the regular `POST /v1/spaces/join` —
the guest kind is encoded in the token and auto-detected. No join
request, no approval, no per-user ACL entry: the space loads read-only
(`ownRole:"guest"`); writes return `403 space.read_only`.

`DELETE /v1/spaces/:spaceId/guest-key` revokes: the guest identity is
removed from the ACL and the read key rotates, so every guest copy
stops receiving new content and flips to `status:"guest_revoked"`
(local copy stays readable). A later create mints a fresh key — old
tokens die permanently.

Guests drop the space with the regular `DELETE /v1/spaces/:spaceId` —
for guest spaces the delete marker is non-terminal: a later
`POST /v1/spaces/join` with a valid guest token re-adds the space
(state re-pulls from the network).

### ACL operations

| Method | Path                                                 | Purpose                                    |
|--------|------------------------------------------------------|--------------------------------------------|
| POST   | `/v1/spaces/:spaceId/acl/accept`                     | `ACL.AcceptRequest` — grants permission    |
| POST   | `/v1/spaces/:spaceId/acl/decline`                    | `ACL.DeclineRequest`                       |
| POST   | `/v1/spaces/:spaceId/acl/permissions`                | `ACL.ChangePermissions` — batched          |
| POST   | `/v1/spaces/:spaceId/acl/remove`                     | `ACL.RemoveAccounts` — rotates read key    |
| POST   | `/v1/spaces/:spaceId/acl/add`                        | `ACL.AddAccounts` — server-side flow       |
| POST   | `/v1/spaces/:spaceId/acl/ownership`                  | `ACL.OwnershipChange`                      |
| POST   | `/v1/spaces/:spaceId/acl/self-remove`                | `ACL.RequestSelfRemove`                    |
| POST   | `/v1/spaces/:spaceId/acl/cancel-join`                | `Service.CancelJoin` — account-level, see below |
| POST   | `/v1/spaces/:spaceId/acl/stop-sharing`               | `ACL.StopSharing` — drops everyone, rotates|

Bodies (every successful op returns `204 No Content`):

```json
// POST /v1/spaces/:id/acl/accept
{ "requestRecordId":"bafy…", "permission":"writer" }

// POST /v1/spaces/:id/acl/decline
{ "identity":"A6ux…" }

// POST /v1/spaces/:id/acl/permissions
{ "changes":[
  { "identity":"A6ux…", "permission":"reader" },
  { "identity":"BcdE…", "permission":"admin"  }
] }

// POST /v1/spaces/:id/acl/remove
{ "identities":[ "A6ux…", "BcdE…" ] }

// POST /v1/spaces/:id/acl/add
{ "accounts":[
  { "identity":"A6ux…", "permission":"writer",
    "metadata":{"name":"Alice"} }
] }

// POST /v1/spaces/:id/acl/ownership
{ "newOwner":"A6ux…", "oldOwnerPerm":"admin" }
```

`self-remove`, `cancel-join`, and `stop-sharing` take no body.

`cancel-join` is the one ACL op that never resolves the space. It
applies only to a pending join, and a pending join is never
materialized (`Service.Get` refuses it with `space.not_accepted`), so
the server posts the withdrawal through the SDK's account-level
`Service.CancelJoin`: the joining client writes the cancel record
straight to the ACL chain the nodes serve, the same way the request
was posted — from any device of the account, the request is
identity-based. Afterwards the joiner's row reads `status: "deleted"`
account-wide — the end state an owner decline leaves — and drops out
of the default space list on every device; `POST /v1/spaces/join` with
a valid token re-requests and returns the row to `joining` (a fresh ACL
request, fresh `requestRecordId` on the owner's side), and a direct add
by the owner surfaces it as `invite_pending`. Errors: `404
space.not_found` for an id this account has no row for; `409
space.join_not_pending` when the row is not `joining`, or when the
owner accepted before the cancel landed — consensus is linear, so
exactly one side wins, and the row settles to `active` on its own
within the join controller's poll; re-read `GET /v1/spaces/:id` rather
than retrying. A request that is already gone from the chain with no
membership behind it (declined, or withdrawn from another device
before the marker synced) is settled by the call itself: 204, row
`deleted`.

#### Permission / status strings

| Wire string | `space.Permission` |
|-------------|--------------------|
| `none`      | `PermissionNone`   |
| `reader`    | `PermissionReader` |
| `guest`     | `PermissionGuest`  |
| `writer`    | `PermissionWriter` |
| `admin`     | `PermissionAdmin`  |
| `owner`     | `PermissionOwner`  |

| Wire string | `space.MemberStatus`     |
|-------------|--------------------------|
| `unknown`   | `MemberStatusUnknown`    |
| `joining`   | `MemberStatusJoining`    |
| `active`    | `MemberStatusActive`     |
| `removed`   | `MemberStatusRemoved`    |
| `declined`  | `MemberStatusDeclined`   |
| `removing`  | `MemberStatusRemoving`   |
| `canceled`  | `MemberStatusCanceled`   |

Unknown values on the wire return `400 request.schema`. Member-event
SSE is **not** wired in v1 — clients refresh by re-`GET`-ing the
collection after a write.

### Sync status

| Method | Path                                                              | Purpose                                    |
|--------|-------------------------------------------------------------------|--------------------------------------------|
| GET    | `/v1/spaces/:spaceId/sync-status`                                 | `Space.SyncStatus().Space()`               |
| GET    | `/v1/spaces/:spaceId/sync-status/objects/:objectId`               | `Space.SyncStatus().Object`                |
| GET    | `/v1/spaces/:spaceId/sync-status/objects/:objectId/subscribe`     | per-object SSE (state-flip stream)         |
| GET    | `/v1/sync-status/subscribe`                                       | account-wide SSE — every space's rollup    |
| GET    | `/v1/spaces/:spaceId/sync-status/peers`                           | **501** until the SDK lands per-space peer list (use `/debug` for diagnostic equivalent) |

The two GETs are cheap; safe to call on a render tick. `state` is one
of `unknown` / `offline` / `syncing` / `synced` / `error`. Unknown
object ids return `{state: "unknown"}` rather than 404 — the SDK is
forgiving here, callers that need existence checks should use the
object catalog.

```json
// GET /v1/spaces/:spaceId/sync-status
{ "spaceId":      "spc_…",
  "state":        "syncing",
  "synced":       1,
  "total":        3,
  "networkPeers": 0,
  "localPeers":   1,
  "p2p":          "connected",
  "lastSyncedAt": "0001-01-01T00:00:00Z" }

// GET /v1/spaces/:spaceId/sync-status/objects/:objectId
{ "objectId":   "obj_…",
  "state":      "synced",
  "lastSyncAt": "2026-05-15T12:00:00Z" }
```

`networkPeers` counts responsible sync nodes with a live connection;
`localPeers` counts local-network (LAN) peers sharing this space that
are connected right now. `p2p` summarizes the local-network state:
`unknown` / `notpossible` (disabled or no usable interface) /
`notconnected` / `connected` / `restricted` (OS denied local-network
access). A space can be `synced` with `networkPeers: 0` when it
converged entirely over the LAN.

The two `/subscribe` endpoints are SSE streams. Wire shape and
lifecycle are documented in `04-events.md` § Sync-status streams —
short version: `event: ready`, then one `event: status` per state
transition (carrying the GET body), terminating with `event: closed`
on server shutdown. Account-wide subscribe lives outside the space
group because the SDK call is account-scoped — one stream covers
every known space.

### Events

Account-wide, **ephemeral** event bus (the generalized successor of the
retired `/v1/ui/commands` channel — UI navigation is now the `ui.*` type
family). Not space data — no SDK/dataset backing, nothing stored. Full
contract in `docs/21-events.md`.

| Method | Path                      | Purpose                                              |
|--------|---------------------------|------------------------------------------------------|
| POST   | `/v1/events`              | publish an event → `{subscribers: n}` (local matches; 0 = nobody listening) |
| GET    | `/v1/events/subscribe`    | filtered SSE — `ready` → `event` per publish → `closed` |

Body: `{type, scope, spaceId?, target?, data?}` — `type` is an open
dotted slug set (`ui.open_space`, `process.progress`, …); `scope` is
`device` / `account` / `space` (`spaceId` required iff `space`);
`sender` is server-stamped and rejected in the body; `data` ≤ 64 KiB.
Subscribe filters via repeatable query params `scope` / `spaceId` /
`type` (exact or `x.*` prefix) / `target` — AND across dimensions, OR
within one. **At-most-once, no snapshot** — a subscriber receives only
events published after it connects (no stale replay on reconnect).
`closed` reasons: `server_shutdown`, `overflow`. Both routes sit
outside the space group like `/sync-status/subscribe`.
`scope: account | space` ride the SDK pub/sub (tech space / target
space) with refcounted subscribe-side interests; an explicit
`scope=space` subscription must name at least one `spaceId` filter.
Network errors: `events.no_read_key`, `events.too_many_patterns`,
`events.topic_not_owned` (docs/06-errors.md).

### Processes

The process helper over the event bus: long-running operations (agent
runs, index passes) broadcast `process.*` events; the server keeps an
in-memory last-event-wins view with staleness expiry — nothing
persisted, a restart forgets everything. Keyed `(sender.identity,
id)`; cancel is an event addressed at the owner, who emits the
terminal event. Full contract in `docs/22-processes.md`.

| Method | Path                          | Purpose                                                    |
|--------|-------------------------------|------------------------------------------------------------|
| GET    | `/v1/processes`               | live view — `{processes: [...]}`, expired entries swept    |
| POST   | `/v1/processes`               | register `{id, kind, title, scope, spaceId?, target?}` → emits `process.started` |
| POST   | `/v1/processes/:id/progress`  | `{done?, total?, message?}` → `process.progress` (heartbeat: ≤ every 15s; absent fields keep their values, `{}` = pure heartbeat) |
| POST   | `/v1/processes/:id/finish`    | `{status: done\|failed\|cancelled, error?}` → terminal event (`error` required iff failed) |
| POST   | `/v1/processes/:id/cancel`    | `{identity?}` → `process.cancel` toward the owner (no state change) |

Every POST answers the bus publish reply `{subscribers: n}`.
Progress/finish require the process live under this account's
identity (`404 process.not_found` — re-register after restart or
expiry: running rows expire 45s after the last frame, terminal rows
60s after finishing). Cancel resolves against the whole view; several
same-id publishers answer `409 process.ambiguous`
(`details.identities`). Remote coverage: account-scope processes of
this account's other devices always (standing `ev/process/>`
interest); space-scope processes only while some local subscriber
holds an interest on that space **covering `process.*`** (a stream
filtered to other types doesn't count). No `/processes/subscribe` — watch
raw frames via `GET /v1/events/subscribe?type=process.*`. Routes sit
outside the space group like `/v1/events`.

### Local store

Device-local, non-CRDT any-store collections — query / modifiers /
indexes / aggregation for state that must never sync (scratch sets,
ingest staging, per-device caches). They live inside the SDK's own
`sdk.db` under a name tag (`l_a_<name>` / `l_s_<spaceId>_<name>`),
which is what makes local↔synced `$lookup` and `$out`/`$merge`
possible later; today both are gated upstream and sinks/lookups are
local-only. Not a dataset: no type, no schema, no `_ver`, no
subscribe, not search-indexed, and **a space-scoped collection
outlives its space**. Full model + trade-offs: `docs/26-local-store.md`.
Account-scoped routes outside the space group; `409 local.disabled`
when `local.enabled: false`.

| Method | Path                     | Purpose                                                       |
|--------|--------------------------|---------------------------------------------------------------|
| GET    | `/v1/local/meta`         | `{stages, accumulators}` — the pipeline vocabulary any-store accepts |
| GET    | `/v1/local/collections`  | list `?scope=account\|space&spaceId=` → `{collections: [{scope, spaceId?, name, storageName, count, indexes}]}` |
| PUT    | `/v1/local/collections`  | ensure `{scope, spaceId?, name, indexes?}` → `{collection, created}` (201 created / 200 existed) |
| DELETE | `/v1/local/collections`  | drop `?scope=&spaceId=&name=` → 204 (no space pre-flight: the cleanup path for a gone space) |
| POST   | `/v1/local/insert`       | `{coll, docs: [..]}` → `{ids}` — a missing `id` is minted; existing `id` → 409 |
| POST   | `/v1/local/upsert`       | `{coll, docs: [..]}` → `{ids}` — whole-document replace-or-insert |
| POST   | `/v1/local/update`       | `{coll, id, modifier, upsert?}` → `{modified, record}` — mongo-style `$set`/`$unset`/`$inc`… |
| POST   | `/v1/local/delete`       | `{coll, ids: [..]}` or `{coll, filter}` → `{deleted}` |
| POST   | `/v1/local/get`          | `{coll, id}` → `{record}` |
| POST   | `/v1/local/query`        | `{coll, filter?, sort?, limit?, offset?, includeTotal?, projection?}` → `{records, total?, hasNext?}` |
| POST   | `/v1/local/aggregate`    | `{coll, pipeline, groupLimit?, accumArrayLimit?, memoryLimitBytes?, explain?}` → `{records}` \| `{plan}` \| `{written}` |
| POST   | `/v1/local/indexes`      | `{coll, ensure?: [{name?, fields, unique?, sparse?}], drop?: [name]}` → `{indexes}` |

`coll` is `{scope: "account" | "space", spaceId?, name}`; `name`
matches `^[a-z0-9][a-z0-9_-]{0,63}$`. A space-scoped op pre-flights
the space (`404 space.not_found` unknown, `409 space.deleted`
tombstoned), except drop. Every op on an
un-ensured collection is `404 local.collection_not_found`.
`filter` / `sort` / `modifier` / `pipeline` are the raw any-store
shapes `/query` and `/aggregate` take. Query `limit` defaults to 100
(cap 1000).

**Sinks and lookups name collections by `storageName`** — `$out
"l_a_rollup"`, `$merge {into: "l_s_<spaceId>_x"}`, `$lookup {from:
…}` — and every such name is fenced before any-store parses: anything
outside the local store is `400 local.bad_sink_target`; a sink target
must already exist and passes the space pre-flight (`404
local.collection_not_found` / `space.*` otherwise — a sink never
creates a collection); `$facet` sub-pipelines are walked. A sink
pipeline answers `{written: n}`. `$out`/`$merge` into the aggregated
collection itself, results lacking `id`, and `$lookup from` naming any
collection but the aggregated one are `400 local.bad_pipeline`. A
rejected index on ensure is `400 local.bad_index` and leaves no
collection behind (create + index are one transaction).

**Writes are chunked, 256 docs per transaction** (any-store has one
writer per DB and the CRDT apply path shares it): insert/upsert take
≤ 1000 docs per request (`400 local.too_many_docs`) and a mid-way
failure leaves earlier chunks committed; delete-by-filter collects ids
under one read and removes them in chunks — **not atomic per call**.

### Push notifications

Mobile push for chat (heart-interoperable; full contract —
model, topic vocabulary, payload shape, settings, config — in
`docs/20-push.md`). Account-scoped routes outside the `:spaceId` group,
behind the `/v1` auth guard. Every route returns `409 push.disabled`
when the server has no push node configured (`push.peerId` /
`push.addrs`).

| Method | Path                        | Purpose                                              |
|--------|-----------------------------|------------------------------------------------------|
| POST   | `/v1/push/token`            | register this device's mobile push token             |
| GET    | `/v1/push/token`            | local registration state (no push-node round trip)   |
| DELETE | `/v1/push/token`            | revoke this device's token                           |
| GET    | `/v1/push/subscriptions`    | the account's server-held topic set                  |

```json
// POST /v1/push/token → 204
{ "platform": "android", "token": "<opaque FCM/APNs token>" }

// GET /v1/push/token
{ "registered": true, "platform": "android" }

// GET /v1/push/subscriptions
{ "subscriptions": [
    { "spaceKey": "<base58 space push pubkey>", "topic": "chats" },
    { "spaceKey": "<base58 space push pubkey>", "topic": "<identity>" } ] }
```

- `platform` is `ios | android` — the push server's platform enum has
  no desktop entry, so desktop/headless servers are **send-only**; the
  token endpoints back the mobile shells embedding `any.aar` / the
  xcframework. Re-POST on token rotation; DELETE on logout.
- The token persist is durable and local
  (`<account-dir>/push-token.json`); a transient forward failure is
  retried in the background, so a slow push node never fails the POST.
  DELETE removes the local file even when the node is unreachable.
- `spaceKey` is the base58 space push **public key** (the push server's
  space identifier), not a spaceId; rows come back unsigned (signatures
  can't round-trip — the server never returns them).

Notification *sending* has no endpoint: it's a sender-scoped side
effect of the chat write handlers (send / mention-adding edit / read),
async and best-effort — the consumer-side exception category `/search`
established. Who-gets-what is controlled by the two `notifyMode` knobs
(§ Per-space settings + the `chat.notifyMode` property,
`docs/20-push.md` § Settings).

### Debug (diagnostic)

| Method | Path                                                 | Purpose                                |
|--------|------------------------------------------------------|----------------------------------------|
| GET    | `/v1/spaces/:spaceId/debug`                          | `Space.Debug().Space()`                |
| GET    | `/v1/spaces/:spaceId/debug/objects/:objectId`        | `Space.Debug().Object`                 |
| GET    | `/v1/debug/p2p`                                       | `SDK.P2PStatus()` — account-wide local-network snapshot |

**Diagnostic only — not a stable interface.** The SDK's `DebugAPI` is
explicitly tagged as "fields and methods may grow or move"; this
mirror inherits the same churn. Production UI should use
`/sync-status` instead (501 until the SDK lands it).

`GET /v1/debug/p2p` returns the account-wide local-network layer: this
device's own peer id, listener state, discovery possibility, and every
discovered LAN peer with the spaces it shares with this account and
whether a connection is live. Account-scoped (no `:spaceId`), so it
sits outside the space group. `spaceIds` is the SHARED set only — the
space exchange proves membership per space and reveals nothing else, so
a stranger on the LAN shows up (if it runs any-sync p2p) with an empty
list. A freshly joined space appears once the joiner's ACL read key
has synced in — normally within seconds of the join being approved.

```json
{
  "peerId":          "12D3Koo…",
  "enabled":         true,
  "listenerStarted": true,
  "port":            56187,
  "possibility":     "possible",
  "state":           "connected",
  "peers": [
    { "peerId":    "12D3Koo…",
      "spaceIds":  ["spc_…"],
      "connected": true }
  ]
}
```

`GET /v1/spaces/:spaceId/debug` returns the per-space outbound
headsync counters since boot (in-memory; resets on every server
restart). `peers` is `[]` until at least one diff round has run
against a responsible node.

```json
{
  "spaceId": "spc_…",
  "peers": [
    { "peerId":     "12D3Koo…",
      "lastSyncAt": "2026-05-15T12:00:00Z",
      "new":        2,
      "changed":    5,
      "lastErr":    "" }
  ]
}
```

`GET /v1/spaces/:spaceId/debug/objects/:objectId` returns a joint-
consistent snapshot of one object's tree + sync state. The read
locks the object tree and walks every change — on a million-change
tree this can block local writes for a noticeable pause. **Not for
high-frequency polling.** First touch of a never-loaded object also
triggers a cold-restore.

```json
{
  "objectId":        "obj_…",
  "syncState":       "syncing",
  "pending":         ["head_a"],
  "lastSyncAt":      "2026-05-15T12:00:00Z",
  "heads":           ["head_a"],
  "headsCount":      1,
  "branchCount":     0,
  "treeLen":         3,
  "snapshots":       1,
  "latestVersionId": "01HX…",
  "maxAddSeq":       17
}
```

`syncState` is one of `unknown` / `offline` / `syncing` / `synced` /
`error`. `latestVersionId` is empty for root-only / transient cold-
restore states and is local to this peer — `VersionIds` are not
comparable across peers. `maxAddSeq` is the controller's
delivery-order watermark, surfaced as a sanity check against tree
length — it is not a cross-peer primitive.

## Body shapes (examples)

Query body / response are documented in § Data plane above.

**POST /v1/spaces/:spaceId/modify**

```json
{
  "objectId": "obj_abc",
  "dataset":  "notes",
  "records": [
    {
      "id":     "",
      "upsert": true,
      "ops": [
        { "type": "$set",       "path": "",     "value": { "title": "x" } },
        { "type": "$addToSet",  "path": "tags", "value": "idea" }
      ]
    }
  ],
  "traceIds": ["demo"]
}
```

Response: the shared write result `{versionId, changeId, recordIds,
rejections?}` — see § Write responses. `recordIds` mirrors the input
record order (`recordIds[0]` is the derived id for the empty-id upsert
above).

The body takes an optional **`scope`** selecting the write route:
`"synced"` (default — the object's own DAG change, synced to every
member) or `"local"` (device-only materialization: no DAG change,
never syncs, still flows through query/subscribe with a locally-minted
`versionId` and an empty `changeId`). A local write may only target
fields the dataset schema declares `local` (`x-scope` in
`GET …/datasets`) — e.g. chat's `unread` / `unreadMention` /
`unreadReactions` read-tracking flags on `chat_messages`. Constraints,
enforced with `400 request.schema`: explicit record `id`s, no
`upsert` (local fields annotate records the synced route created —
they never create records), no `traceIds`, and not the shared
`objects` dataset (its fields are per-property scoped — local property
values go through `POST …/properties/:objectId/set/:typeId`, which
validates per-prop scope and kind). Ops that target a
non-local field come back in `rejections` (the write itself succeeds);
the reverse direction — a synced write touching a local field — fails
whole with `400 dataset.validation`. `"account"` is not writable here
yet (the SDK's account transport covers property values only).

```json
{
  "objectId": "obj_abc",
  "dataset":  "chat_messages",
  "scope":    "local",
  "records": [
    { "id": "msg_1", "ops": [ { "type": "$set", "path": "unread", "value": true } ] }
  ]
}
```

## Middleware

Minimal in v1:

- `middleware.Recover` — catch panics, return 500.
- `middleware.RequestID` — generate an id per request for the logs.
- **Logger middleware** wired to `any-sync/app/logger` — one line per
  request at info level (path, status, duration).
- `middleware.BodyLimit("1M")` — reject anything larger; prevents
  accidental uploads before the file API lands.

No rate limiting in v1, and no caller authentication (loopback is the
trust boundary). The only auth-shaped middleware is the unauthorized
guard (§ Auth): `401 auth.required` on SDK-backed routes until an
account is booted — it gates server STATE, not the caller — and the ownership gates of `02-server.md` § Modes (`X-Any-Control-Token` on a managed server's auth and shutdown verbs). CORS: one named exception — a fixed allowlist for the desktop-shell webview origins (`tauri://localhost`, `http://tauri.localhost`, the Vite dev origins; see `internal/server/routes.go`), with `X-Any-Control-Token` among the allowed headers so the webview can log a managed server in; requests without an Origin header are untouched, and the loopback-only listen stays the trust boundary.

## Pagination

Offset-based, mirroring the SDK. Cursor pagination is a future add.

## Idempotency

POST endpoints are **not** idempotent in v1 — each POST produces a new
DAG change. An `Idempotency-Key` header is a future add. The one
exception is `POST /v1/spaces/:spaceId/upsert` (§ Upsert records):
the caller-supplied record id is the idempotency key, and an identical
re-run diffs to nothing and emits no change.
