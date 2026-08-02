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
  - [Objects](#objects)
    - [Blocks](#blocks)
      - [Read blocks](#read-blocks)
      - [Create](#create)
      - [Patch](#patch)
      - [Delete](#delete)
      - [Subscribe](#subscribe)
    - [`nav` auto-stamping on `Objects.Create`](#nav-auto-stamping-on-objectscreate)
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
  - [Properties (values on objects)](#properties-values-on-objects)
  - [Chat (built-in `chat` type)](#chat-built-in-chat-type)
    - [Message wire shape (read path)](#message-wire-shape-read-path)
    - [Send](#send)
    - [Read](#read)
    - [Edit / delete (own only)](#edit--delete-own-only)
    - [React (toggle)](#react-toggle)
  - [Agent data layer (built-in `agent_log` + `agent_memory` types)](#agent-data-layer-built-in-agent_log--agent_memory-types)
  - [Enrichment (built-in `enriched_data` + `enrich_proposal` types)](#enrichment-built-in-enriched_data--enrich_proposal-types)
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
  Built-in types (chat, editor) keep bespoke handlers for *writes*
  only — POST/PATCH/DELETE and reactions. Reads always go through
  the per-object query primitive with the matching `dataset` value
  (`chat_messages`, `editor_blocks`, etc.). One read path for every
  dataset, one wire shape for every snapshot. The lone exception is
  `GET /editor/markdown`, which renders blocks to markdown bytes —
  a transform, not a dataset read.

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

| Method | Path            | Purpose                                |
|--------|-----------------|----------------------------------------|
| GET    | `/v1/health`    | server health, version, account id     |
| POST   | `/v1/shutdown`  | graceful shutdown                      |

`/v1/health` works on an unauthorized server too — `account` is then
`""`.

`bootstrapping` (bool): `true` while a booted engine's SDK background
boot pass (eager space loading + offline catch-up) is still running —
serving, offline catch-up in background; per-space convergence stays
on `/sync-status`. `false` when unauthorized and after the pass
completes. See `02-server.md` § Startup / § Health.

### Auth

| Method | Path        | Purpose                                          |
|--------|-------------|--------------------------------------------------|
| GET    | `/v1/auth`  | authorization state + locally available accounts |
| POST   | `/v1/auth`  | generate / restore / select an account, boot SDK |

A server started without a resolvable account (fresh data dir, or
several accounts and no selector — see `02-server.md` § Startup) is
**unauthorized**: every `/v1` route except `/v1/health`,
`/v1/shutdown`, `/v1/openapi.json` and `/v1/auth` returns
`401 auth.required`. `POST /v1/auth` boots the account in place; no
restart, and the server stays on that account for its lifetime
(switching = restart, a second POST returns
`409 auth.already_authorized`).

```json
// GET /v1/auth
{ "authorized": false,
  "accounts": [
    {"id":"A8tR…","default":true},   // legacy root wallet.key
    {"id":"A8g1…"} ] }               // <root>/<id>/ dirs

// POST /v1/auth — mnemonic and accountId are mutually exclusive:
{}                                    // generate a fresh account
{ "mnemonic":"w1 … w12", "index":0 }  // restore: same phrase ⇒ same account,
                                      // device key freshly generated
{ "accountId":"A8g1…" }               // select an existing local wallet

// → 200
{ "accountId":"A8g1…",
  "created": true,        // a new wallet file was written
  "mnemonic":"w1 … w12" } // ONLY when generated — shown once, back it up
```

`index` is the account-derivation index and is valid **only with
`mnemonic`** (a selected account's index is baked into its wallet; a
generated one is always 0) — a non-zero `index` without `mnemonic` is
`400 request.invalid_field`. If the engine fails to boot after a fresh
wallet was created this call (e.g. SDK init error), the half-created
per-account dir is removed, so a retry — or `generate` getting a new
phrase — starts clean rather than auto-selecting an un-backed account.

Errors: `400 auth.bad_mnemonic` (BIP-39 validation),
`400 request.invalid_field` (mnemonic+accountId together, or index
without mnemonic), `404 auth.account_not_found` (accountId without a
local wallet), `409 auth.account_in_use` (another process holds that
account's pid lock), `409 auth.mnemonic_mismatch` (existing wallet file
disagrees with the supplied phrase/index), `400 auth.passkey_required`
(encrypted wallet — the passkey still comes from the configured env
var, never the request body).

### Account

| Method | Path                         | Purpose                                |
|--------|------------------------------|----------------------------------------|
| GET    | `/v1/account`                | own id + metadata                      |
| PUT    | `/v1/account/metadata`       | `Account.UpdateMetadata`               |

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

**`Service.Derive` / `DeriveId` are deliberately not exposed.** A
client-supplied seed derives a deterministic space id, so a reused seed
re-creates an existing space's identity — too dangerous for clients.
Derivation stays an in-process SDK surface (used internally for e.g.
the tech-space); there is no `/v1/spaces/derive` route by design.

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
locally on the next poll.

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
plain row `SpaceInfo`, no `spaceIndexObjectId` / `generalChatObjectId`,
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

`SpaceInfo` also carries `generalChatObjectId`: the deterministic id of
the space's single general chat object (see § Chat → General chat).
Same single-space-only surfacing as `spaceIndexObjectId` — populated on
create / get / one-to-one / join responses (deriving, i.e.
materializing, the chat on first sight), omitted on `GET /v1/spaces`
list rows so listing stays a cheap read.

`SpaceInfo` also carries `agentConfigObjectId` and
`agentSecretsObjectId`: the deterministic ids of the space's single
agent config object (dataset `agent_config`, seed
`any/agent-config/v1`) and agent secrets object (dataset
`agent_secrets`, seed `any/agent-secrets/v1`). Same surfacing policy as
`generalChatObjectId` — populated (materializing on first sight, type
attached) on single-space responses, omitted on list rows. Both
datasets are raw harness-owned keyspaces, one record per dotted key /
secret ref; both declare a device-local field the scoped-modify path
admits local writes to (`agent_config.localValue`,
`agent_secrets.value` — see § Modify records). Secrets live on their
own object, split out of `agent_config`, so the agent runtime can gate
reads on the whole secrets dataset by object id/name with one
meaningful authorization error, while `agent_config` stays
guest-readable.

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
`spaceType:"anytype.onetoone"`, a regular space `"anytype.space"` — use it
to tell direct chats from regular spaces client-side. `author` is the
space owner's account identity, resolved best-effort from the ACL (empty
when the ACL isn't loadable). Both are omitted when empty.

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
`spaceType:"anytype.onetoone"` rows.

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
  (`type`/`spaceType` = `anytype.onetoone`). `400 request.missing_field`
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
you want the projected status/role. Rows carry `createdAt` as unix
seconds (handler-derived added-to-account time, absent on pre-stamp
rows), so newest-first creation ordering is `{"sort": ["-createdAt"]}`.
The subscribe frame set and `closed`
reasons are identical to the per-object `…/query/subscribe` (see the Data
plane § Subscribe and `docs/04-events.md`); a space joined on another
device or head-synced in arrives as an `added` change.

#### Dataset schema discovery

```
GET /v1/spaces/:spaceId/datasets   → { datasets: [ { name, schema } ] }   Space.Datasets
GET /v1/datasets                   → { datasets: [ { name, schema } ] }   Service.Datasets
```

`schema` is a standard **JSON Schema** object per dataset
(`{type:"object", properties:{…}, additionalProperties:<dynamic>}`). Each
property carries an `x-scope` extension keyword classifying the field:

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
the chat/editor datasets, which declare their known fields while staying
open). The per-space form lists every dataset the space hosts (`objects`,
`chat_messages`, `editor_blocks`, …); the account-wide form lists the
tech-space system datasets (`spaces`, `profile`) behind the space-list
query/subscribe above.

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
  "limit":   10,                      // optional: default 10, max 100
  "mode":    "hybrid",                // optional: hybrid (default) | fts | vector
  "require": ["1937"],                // optional FTS must-have terms (phrase/prefix ok); ignored in vector mode
  "exclude": ["fiction"]              // optional FTS must-not terms
}
```

Reply:

```json
{
  "hits": [
    { "scope": "chat", "objectId": "…", "dataset": "chat_messages",
      "recordId": "…", "data": "the zeppelin disaster of 1937",
      "score": 0.0328 }
  ],
  "mode": "hybrid",
  "vectorStatus": "used"
}
```

`mode` in the reply is the mode that actually ran: `hybrid` degrades to
`fts` when no embedder is configured or it is unreachable; `mode:
"vector"` requests get `400 index.no_embedder` (none configured) or
`503 index.embedder_unavailable` (configured but down — retryable).

`vectorStatus` tells the consumer — typically an agent deciding how
much to trust recall — whether semantic search took part, and why not:

| Value | Meaning |
|-------|---------|
| `used` | the vector leg ran and contributed to ranking |
| `unavailable` | embedder configured but unreachable for this query — results are lexical-only; retrying later may differ |
| `disabled` | no embedder configured on this server — vector can never run until config changes |
| `skipped` | the caller asked for `mode: "fts"`; vector was not attempted |

Scores are comparable only within one response
(BM25 for fts, cosine similarity for vector, RRF for hybrid). The index
covers content written while indexing is on — "index from the next
change" (`docs/13-index.md`).

### Objects

| Method | Path                                                      | Purpose                            |
|--------|-----------------------------------------------------------|------------------------------------|
| POST   | `/v1/spaces/:spaceId/objects`                             | `Objects.Create`                   |
| POST   | `/v1/spaces/:spaceId/objects/query`                       | `Space.QueryObjects.Snapshot`      |
| POST   | `/v1/spaces/:spaceId/objects/query/subscribe`             | `Space.QueryObjects.Subscribe` (SSE) |
| POST   | `/v1/spaces/:spaceId/objects/aggregate`                   | `Space.AggregateObjects` (pipeline) |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId`                   | `Objects.Delete`                   |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/backlinks`         | reverse reference lookup (no SDK method) |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/editor/markdown`              | render blocks as markdown |
| PUT    | `/v1/spaces/:spaceId/objects/:objectId/editor/markdown`              | bulk parse markdown → blocks |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/editor/markdown/append`       | append markdown at tail (no read/diff) |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/editor/blocks`                | create one block         |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`       | $set / $unset one block  |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`       | tombstone one block      |

Object bodies are stored as a tree of atomic blocks on a per-object
`editor_blocks` dataset (one record per block) and exposed through the
`…/editor/**` route namespace. The atomic surface is the four
`…/editor/blocks` endpoints; the two `…/editor/markdown` routes are
a lossless import/export layer over the same dataset for LLM tools,
"Export as .md" / "Import .md" flows, and programmatic API users that
don't want to walk the block tree. Liveness goes through the
per-object query/subscribe endpoint with `dataset=editor_blocks`.

The `editor/markdown` routes are aggregating endpoints (each one
bundles several SDK calls) and are a deliberate exception to the
"endpoints map 1:1 onto SDK methods" rule. `GET` reads every
top-level block, renders each to its canonical markdown bytes, and
joins with `\n\n`. `PUT` parses the incoming markdown, diffs against
the current block tree by (type + position + text), and emits
per-block create / update / delete ops through the same write path a
PATCH /editor/blocks call would, so the same `editor_blocks` SSE events
fire under the hood. `PUT` replies with `{"inserted": [...],
"updated": [...], "deleted": [...], "unchanged": N}` where the slices
contain block ids.

`POST …/editor/markdown/append` is the append-only fast path. It
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

#### Blocks

One record per block, stored on a per-object `editor_blocks` dataset.
Nest via `nav.parentId`; order siblings via `nav.pos` (lexid). Wire
shape:

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

`POST /v1/spaces/:spaceId/objects/:objectId/editor/blocks`

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

`PATCH /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`

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

`DELETE /v1/spaces/:spaceId/objects/:objectId/editor/blocks/:blockId`
→ 200 with the shared write result `{versionId, changeId, recordIds}`
(`recordIds=[blockId]`). Tombstones the record (sticky — re-creating
the same id is rejected). Children of the deleted block are NOT
cascaded; the client either deletes the descendants explicitly or
rewrites the document via `PUT /editor/markdown`, which diffs the whole
body.

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
the change originated from a PATCH /editor/blocks call or a PUT
/editor/markdown bulk rewrite. See `04-events.md`.

#### `nav` auto-stamping on `Objects.Create`

Every new object gets a `nav` row stamped on it server-side: `nav` is
appended to `any.types` and three property values land on the
per-space `objects` collection — `nav.type` (1 = item, 2 = folder),
`nav.parentId` (string id of the parent folder; `""` = root) and
`nav.pos` (lexid for ordering inside a parent). See `internal/nav` for
the constants. The body accepts an optional `"nav"` block to override
defaults:

```json
{
  "types": ["..."],
  "initialProperties": { "...": { "...": "..." } },
  "nav": {
    "type":     2,
    "parentId": "obj_parent_id",
    "pos":      "PPQY"
  }
}
```

`nav.pos` defaults to the next lexid after the current max pos in the
target folder (queried server-side at create time); `Middle()` when
the folder is empty. Mirrors anytype-heart's `LexId.Next(prev)`
pattern. Trees are built by querying the per-space `objects`
collection — no dedicated tree endpoint:

```bash
# children of folder X, in order:
curl -X POST /v1/spaces/$SPID/objects/query -d '{
  "filter": { "nav.parentId": "obj_X" },
  "sort":   [ "nav.pos" ]
}'
```

`nav` is a **virtual built-in type** — surfaced through
`GET /v1/spaces/:spaceId/types` (BuiltIn=true) and
`GET /v1/spaces/:spaceId/types/nav/properties`, but not registered
through the SDK's `handler.Type` machinery (no separate dataset, no
custom validator). Property paths use literal string keys
(`nav.parentId` etc.), not content-addressable propIds.

#### Moves (drag-and-drop)

Tree moves use the existing property `set` endpoint — no dedicated move
route. To relocate object `oid` under `newParent` at lexid pos `p`:

```
POST /v1/spaces/:spaceId/properties/:oid/set/nav
{ "patch": { "parentId": "<newParent>", "pos": "<p>" } }
```

Both fields land in one DAG change. The web UI ports the lexid
allocator to JavaScript (alphabet `CharsAllNoEscape`, blockSize=4,
stepSize=100 — match the Go side byte-for-byte) so the client can
compute drop-target positions without a server round-trip.

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
objects reference X?" — the reverse direction of links-format property
values. The SDK exposes no reverse index, so like `/search` this is a
consumer-side exception to the 1:1 rule: object references are
properties with `format.type: "links"` (arrays of `"any://<objectId>"`
URIs), stored at `record[typeId][propId]`; the handler resolves the
space's links-format property catalog and queries the `objects`
collection for rows whose arrays contain `"any://<X>"`.

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
a follow-up. `nav.parentId` (the tree) is not a links property — query
children directly with `{"filter":{"nav.parentId":"<X>"}}`.

### Data plane

| Method | Path                                                      | Purpose                              |
|--------|-----------------------------------------------------------|--------------------------------------|
| POST   | `/v1/spaces/:spaceId/query`                               | `Space.Query.Snapshot`               |
| POST   | `/v1/spaces/:spaceId/query/subscribe`                     | `Space.Query.Subscribe` (SSE)        |
| POST   | `/v1/spaces/:spaceId/aggregate`                           | `Space.Aggregate` (pipeline)         |
| POST   | `/v1/spaces/:spaceId/modify`                              | `Space.Modify`                       |
| POST   | `/v1/spaces/:spaceId/delete-records`                      | `Space.Delete`                       |

Two query scopes:

- `POST /v1/spaces/:spaceId/objects/query` (+ `/subscribe`) —
  **cross-object**. Reads the per-space `objects` collection (one row
  per object's computed property values). Use this to find objects by
  property, e.g. `{"filter":{"<typeId>.<propId>":"Casablanca"}}`.
- `POST /v1/spaces/:spaceId/query` (+ `/subscribe`) — **per-object**.
  Reads one of an object's own datasets (`objectId` and `dataset`
  required). Used for a type object's `properties` definitions
  dataset, `editor_blocks`, `chat_messages`, `program_source`,
  `mini_app`, etc.

Every row in the per-space `objects` collection carries SDK-stamped
row-root fields alongside `id`, all derived/read-only (client writes
addressing them are rejected):

- `author` — identity that created the object (root-change signer);
- `createdAt` — object creation time, unix seconds (root-change time);
- `spaceId`;
- `modifiedAt` — unix seconds of the latest synced change that touched
  the row. Any property write bumps it; peers converge on the same
  value (LWW on the change's DAG order). It is the **author's clock** —
  sort/display quality, never a fencing token. Local-scope writes
  (e.g. chat read flags) deliberately don't bump it.

"Recently modified first" is `{"sort": ["-modifiedAt"]}`.

All four take POST (filter/sort body doesn't fit a query string).
Reads always go through these — the bare `…/query` returns a
point-in-time snapshot; `…/query/subscribe` returns the same
snapshot plus a live SSE stream of windowed transitions. See
`04-events.md` for the subscribe contract.

A `filter` naming an operator outside the grammar is a caller fault:
`400 filter.unknown_operator`, with the offending token in
`details.operator` and the supported set spelled out in the message.
Note there is no `$contains` — a scalar already compares against array
elements, so `{"any.types": "chat"}` is the contains spelling. Filter
grammar and the array rules: `09-query.md`.

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
  "mailboxCapacity":    256,          // subscribe only — default 256, min 16
  "driftBudgetPercent": 30,           // subscribe only — default 30
  "projection": { "includeVariants": false, "includeMeta": false }   // NOT IMPLEMENTED
}
```

**`projection` is not implemented yet.** The field is accepted in the
body but the server doesn't thread it to `Query.Projection`, and the
SDK's `Projection(opts)` is itself a no-op in MVP. Every record on
snapshot frames and every `added` / `updated` record in `changes`
events ships its full anyenc form — `_ver` (creation marker + per-
field high-water), and `_traces` / `_deletedAt` if present. Clients
that need a leaner shape strip those fields locally for now. See
`docs/07-roadmap.md` § "Query `Projection`".

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
  - `sdk_closed` — the SDK released the subscription channel (space
    or SDK closed).
  - `overflow` — the per-subscriber mailbox filled before the consumer
    drained it. The SDK closes the sub rather than dropping events —
    resubscribe to get a fresh snapshot.
  - `drifted` — more than `driftBudgetPercent` of the held window left
    without replacements, and the engine refuses to re-query on the
    hot path. Resubscribe.

  All four reasons mean "the stream is over; if you want live state,
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

**Excluded datasets.** `chat_messages` and the agent data datasets —
`agent_turns`, `agent_chunks` — opt out of history
(`handler.Dataset.SkipHistory`). Turns and chunks are write-once
(edits rejected, author-only deletes — every record has exactly one
live version), so a history index
would only
duplicate them; chat clients render live records only (edits show
current text, deletes tombstone), so nothing reads a per-message
timeline and the index rows would be dead weight at chat write
volume. Writes to these datasets succeed as usual but are invisible
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
| DELETE | `/v1/spaces/:spaceId/types/:typeId`                           | `TypesAPI.Delete`      |
| GET    | `/v1/spaces/:spaceId/types/:typeId/properties`                | `TypesAPI.Properties`  |
| POST   | `/v1/spaces/:spaceId/types/:typeId/properties`                | `TypesAPI.AddProperty` |
| DELETE | `/v1/spaces/:spaceId/types/:typeId/properties/:propId`        | `TypesAPI.RemoveProperty` |
| PATCH  | `/v1/spaces/:spaceId/types/:typeId/properties/:propId`        | `TypesAPI.PatchProperty` |

`POST …/types` **requires** a non-empty **`xKey`** — the stable
programmatic handle a type is resolved by (the display `name` is not a
resolution key; a type without an xKey is reachable only by its CID).
The SDK treats xKey as non-unique display metadata, so the server
enforces it: empty → `400 type.xkey_required`; collision with an
existing type's `xKey` **or** id in the same space → `409
type.xkey_conflict` (`details: {xKey, existingTypeId}`). Clients derive
the xKey as a slug of the name (`"Pages"` → `pages`); it must survive
display-name renames. Built-in types (`chat`, `nav`, …) are registered,
not created here, and resolve by their literal id.

`GET …/types/:typeId` and `GET …/types/:typeId/properties` answer `404
type.not_found` for an unknown typeId (deleted, never existed, or an id
that resolves to a non-type object). The properties list is
existence-checked server-side — the SDK's `Properties` returns an empty
slice for unknown ids — so a `200 []` always means "the type exists and
has no property definitions yet", never "no such type".

`POST …/properties` accepts an optional **`meta`** object (string →
string) stored verbatim on the property definition and returned by
`GET …/properties`. It is opaque consumer metadata; the one convention
today is `meta.index = "<scope>"`, which marks the property for the
search indexer (its value is indexed under that scope — see
`docs/13-index.md` § prop chunker). Only string / array kinds index.

```json
{ "name": "context", "kind": "string", "xKey": "context",
  "meta": { "index": "agent" } }
```

`POST …/properties` also accepts an optional **`format`** object — the
property's value convention beyond its structural kind:

```json
{ "name": "related",
  "format": { "type": "links", "ui": "multiselect",
              "filter": { "type": { "$in": ["page"] } } } }
```

- `format.type` — `links` (array of `any://<objectId>` URI strings),
  `date` (`2006-01-02` string), `datetime` (RFC 3339 string), `select`
  (a single option key — string), `multiselect` (an array of option
  keys). `tags` is reserved until the space-level tag table lands.
  Pinned for the property's life and coupled to `kind` (`links` /
  `multiselect` ⇒ `array`, `date`/`datetime`/`select` ⇒ `string`);
  **`kind` may be omitted** when a format is set — it defaults from the
  format type.
- `format.ui` — presentation hint: `select` / `multiselect` / `link` /
  `links`. `date`/`datetime` take no ui.
- `format.filter` — mongo-style condition over candidate objects
  (`links` only); must parse as a query condition.
- `format.options` — the enumerated choice set for `select` /
  `multiselect`, a map keyed by each option's **stable key** (the key IS
  the value a select/multiselect value stores). Each entry is
  `{name, color, pos, meta?}` (all strings; `pos` is a lexid display-
  order key). Usually populated via PATCH (below), not at create.
  Membership is **not** enforced on value writes (an option may be
  deleted while values still reference its key — dangling-tolerant).
- `format.meta` — an opaque format-level string→string config bag.

The SDK stores formats opaquely (structure-only checks); **this server
is the semantics boundary**. Definition-time violations → `400
property.format_invalid`. Value writes through `POST …/set/:typeId` and
`initialProperties` on object create are shape-checked against the
format (datetime must parse, links must be plain `any://<objectId>`
URIs — no spaceId segment, no fragment) → `400
property.format_violation` (`details: {propId, format, reason}`). No
object-existence or object-type checks. Known gap: raw `POST
/v1/spaces/:spaceId/modify` against the `properties` dataset bypasses
format value validation.

`POST …/properties` also accepts an optional **`scope`** — the
property's write/sync class: `"synced"` (default — everyone in the
space), `"account"` (this account's devices only, via the private tech
space), or `"local"` (this device only, never synced). `"derived"` is
reserved for built-ins → `400 request.schema`. Like `kind`, scope is
pinned by the first write — changing it means defining a new property.
`GET …/properties` returns each definition's `scope` (pre-scope
definitions read back as `"synced"`). Value writes need no scope
parameter: `/set/:typeId` auto-routes by the declared scope (below).

```json
{ "name": "pin", "kind": "boolean", "xKey": "pin", "scope": "local" }
```

**`PATCH …/properties/:propId`** — a generic per-path patch to a property
definition (`TypesAPI.PatchProperty`). This is the write half of a
property rename and of select/multiselect option CRUD (create / rename /
recolor / reorder / delete an option). Body:

```json
{ "set":   { "format.options.high.name": "High",
             "format.options.high.color": "red",
             "format.options.high.pos": "a0" },
  "unset": [ "format.options.low" ] }
```

`set` maps a dotted path to its new value; `unset` lists dotted paths to
remove (naming a whole option key, e.g. `format.options.high`, deletes
that option). Every value is a JSON **string** except `format.filter`
(a condition object stored as its JSON text). All ops apply in one CRDT
change (atomic); each leaf merges per-path, so concurrent edits to
different options/leaves converge. Deleting then re-adding the same
option key works (it's a field unset, not a record tombstone).

Mutable paths: `name`, `description`, `xKey`, `xKind`, `meta.<k>`,
`format.ui`, `format.filter`, `format.meta.<k>`,
`format.options.<key>.{name,color,pos}`, `format.options.<key>.meta.<k>`.
A **`set`** must target a scalar leaf; a bare container
(`meta`, `format.meta`, `format.options`, `format.options.<key>`) is
rejected on `set` (it would clobber the whole map) but may be **`unset`**
to clear it (e.g. unset `format.options.<key>` deletes an option).
Pinned paths (`kind`, `scope`, `items`, `properties`, the whole `format`
object, `format.type`) → `400 property.immutable`; an unknown/malformed
path or a non-string value on a non-format leaf → `400
request.invalid_field`; a format-specific value error (unknown
`format.ui`, unparseable `format.filter`, `format.*` on a format-less
property) → `400 property.format_invalid`. PATCH/DELETE on a registered
built-in type → `400 type.registered`. Returns `204`; `404 sdk.not_found`
for an unknown type/propId. At least one `set`/`unset` entry is required.

Examples: rename `{ "set": { "name": "Priority" } }`; recolor
`{ "set": { "format.options.high.color": "blue" } }`; delete an option
`{ "unset": [ "format.options.high" ] }`.

**`DELETE …/properties/:propId`** (`TypesAPI.RemoveProperty`) tombstones
the definition and returns `204`. Existing instance values are **not**
cleaned up — subsequent writes to that propId are dropped op-by-op
(dangling-tolerant). Unknown/already-removed propId → `404 sdk.not_found`.

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

Runtime type binding (`attach` / `detach`) is still `501
sdk.not_implemented` — bind types at object-create time via the `types`
array on `POST /v1/spaces/:spaceId/objects`. See `08-clients.md`
§ "Preflight-validate writes against the bound types".

### Chat (built-in `chat` type)

| Method | Path                                                                     | Purpose                  |
|--------|--------------------------------------------------------------------------|--------------------------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages`                         | send a message           |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId`                  | edit own message text    |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId`                  | delete own message       |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions/:emoji` | toggle own reaction      |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/read-all`                         | mark everything read     |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/read`             | mark msg + all above read |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions-read`   | mark msg's reactions read |

**General chat.** Every space has one deterministic "general" chat
object, derived from a fixed seed (`chat.GeneralChatSeed`,
`any/general-chat/v1`) — the same objects/derive primitive the brain
(`/agent/brain`) uses. There is no bespoke resolver endpoint: the id is
delivered as `generalChatObjectId` on every single-space `SpaceInfo`
response (create / get / one-to-one / join) — the same common point
that carries `spaceIndexObjectId` (§ Spaces). The first single-space
response materializes the object (the `chat` type is attached then, so
the id accepts `chat/messages` writes immediately); it is omitted from
`GET /v1/spaces` list rows, which stay a cheap read that never
materializes chats. Clients should write and read this shared chat
instead of creating their own chat object per client — otherwise a
space accumulates two or three parallel chats depending on which client
spoke first, most visibly in 1-1 direct spaces. Deterministic
derivation means a joiner computes the same id the creator did, so the
locally derived object and the CRDT-replicated one converge.

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
  "createdAt":        1714597200,
  "modifiedAt":       1714597200,
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
  "reactions":        { "👍": { "<id1>": 1714597200, "<id2>": 1714597205 } }
}
```

A record may additionally carry the device-local read-tracking flags
`unread` / `unreadMention` / `unreadReactions` (booleans, `x-scope`
local). They are not part of the synced message — each device
materializes its own values via `POST …/modify` with
`{"scope":"local"}` (§ Modify records) and they never appear on other
devices. Filterable like any field: `{"filter":{"unread":true}}`.

`createdAt` and `modifiedAt` are unix-seconds, server-stamped. They
are equal on a never-edited message — clients detect edits by
comparing them. `text` is markdown; rendering is the client's
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
  "agent": { "name": "bao", "debugLink": "any://sp/dbg#turn_2", "done": false } }
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
`reactions.<emoji>.<accountId> = <timestamp>` (server-derived) — the
same shape the bespoke send / edit / react responses return, so there
is nothing to transpose between the read and write paths.

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
triggering change's timestamp, server-derived. Because the leaf is
unique per (emoji, identity), two clients toggling at the same time
can't corrupt each other. Returns `200` with the shared write result
`{versionId, changeId, recordIds}` (`recordIds=[msgId]`); read the
updated `reactions` back via the query path.

### Agent data layer (built-in `agent_log` + `agent_memory` types)

Full model, record shapes, and query recipes in
[`docs/11-agent-memory.md`](11-agent-memory.md). Writes only here —
reads + liveness go through `/query` and `/query/subscribe` with
`dataset` ∈ `{agent_turns, agent_chunks, agent_memory_items}`.

| Method | Path | Purpose |
|--------|------|---------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/agent/turns`  | append one write-once turn record |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/agent/chunks` | create one write-once summary chunk |
| GET    | `/v1/spaces/:spaceId/agent/brain`                    | deterministic brain object id |
| POST   | `/v1/spaces/:spaceId/agent/memory`                   | create a memory item |
| PATCH  | `/v1/spaces/:spaceId/agent/memory/:itemId`           | evolve mutable fields (author only) |
| DELETE | `/v1/spaces/:spaceId/agent/memory/:itemId`           | delete a memory item (author only) |

Turns and chunks attach to the chat object itself (the `agent_log`
type is attached on first write, making the object multitype chat +
agent_log); a chunk's `fromSeq`/`toSeq` are explicit pointers to the
raw `agent_turns` range it summarizes. Memory items live on the
per-space brain object — the server resolves it internally on writes;
clients call `GET /agent/brain` once to learn the objectId for reads.
All writes return the shared write result `{versionId, changeId,
recordIds}`. Errors use the `agent.*` code namespace
(`docs/06-errors.md`).

Turn and chunk records are write-once — edits are rejected by the
handler (`agent_log.turn: append_only`), but deletes are allowed
**author-only** (rejection reason `not_author` otherwise) so agent
history can be wiped. There is no bespoke delete endpoint: deletes go
through the generic `POST /v1/spaces/:spaceId/delete-records` with
`dataset` = `agent_turns` / `agent_chunks`. A wiped turn range leaves
chunk `fromSeq`/`toSeq` pointers dangling — readers tolerate sparse
seq ranges (docs/11-agent-memory.md).

Deleted seqs are never reused: server-assigned seq allocation reads
the max record id **including tombstones** (a tombstone keeps its
zero-padded-seq id after its content is wiped), so appends after a
history wipe continue where the counter left off. A client-provided
`seq` that points at a deleted record returns `409 agent.seq_deleted`
— the store would otherwise absorb the write without an error
(CRDT delete-wins) while storing nothing. Omit `seq` to let the
server pick the next free one.

### Enrichment (built-in `enriched_data` + `enrich_proposal` types)

Structured, sourced, reviewable enrichment. Two built-in types:

- **`enriched_data`** — a durable, sourced enrichment collection on a
  target object (multitype-attaches on first write, same pattern as
  chat/agent_log). One record per fact: `{text, source, target, value,
  createdBy, createdAt}` — `text` required; `source` is the provenance
  link (`any://<space>/<transcript>#<blockId>,…`); `target`/`value` are
  set only for property enrichments (which real property was set, and
  to what), so the UI can show a property value's source.
  `createdBy`/`createdAt` are server-stamped (derived; client writes
  rejected). Records are searchable (indexed under scope `basic`).
- **`enrich_proposal`** — an ephemeral, reviewable enrichment plan; its
  `enrich_proposal_items` dataset holds one loose record per proposed
  item (`text`, `source`, `outcome` enrich|new, `targetObjectId`,
  `targetKind` collection|property, `targetProperty`
  `<typeXKey>.<propXKey>`, `value`, `newType`, `newName`). Items are
  written/edited through the generic `/modify` and read through
  `/query` — no bespoke item endpoint. Proposals are scaffolding:
  deleted on apply and excluded from the search index.

| Method | Path | Purpose |
|--------|------|---------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/enriched-data` | write one sourced enrichment record (attaches the type first) |
| POST   | `/v1/spaces/:spaceId/enrich/apply`                    | deterministically apply a reviewed proposal, then delete it |

`POST …/enriched-data` body: `{text, source?, target?, value?}`;
returns the shared write result (`recordIds[0]` is the derived record
id). Reads go through `POST /v1/spaces/:id/query` with
`dataset=enriched_data`.

`POST …/enrich/apply` body: `{proposalId}`. Deterministic (no LLM): per
item it creates the target object for `new` items (items sharing
`newType`+`newName` land on ONE object), sets the real property for
`property` items, and always writes an `enriched_data` record onto the
target; then deletes the proposal object. Returns `{created,
propertiesSet, enrichedDataWritten, proposalDeleted, failures[]}` —
`failures` is per-item; a non-empty list still means the rest applied.
Failure strings are short stable descriptions naming the item/target
only — raw SDK error text goes to the server log, never the body.
`404 enrich.empty_proposal` when the proposal has no items (already
applied, deleted, or empty).

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

- `Content-Type` header → stored mime (parameters stripped;
  `application/octet-stream` or absent = "unset"),
- `?name=` → stored user-facing name,
- `?variant=` + `?variantOf=` → attach the content as an alternate
  representation (e.g. a thumbnail the client rendered) of an existing
  file **on the same object**. Both or neither.

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
as `Content-Type` (octet-stream fallback — never sniffed),
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

A malformed or unrecognized `inviteToken` returns `400 invite.invalid`.

In the v1 RequestToJoin flow `Service.Join` returns 202: the SDK has
posted the join request, written a `joining` index entry, and the
joiner now polls `GET /v1/spaces/:id/members/me` for the status flip
to `active` after the owner accepts.

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
| POST   | `/v1/spaces/:spaceId/acl/cancel-join`                | `ACL.CancelJoinRequest`                    |
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

### UI commands

Account-wide, **in-memory** agent→any-ui control channel. Not space
data — no `:spaceId` scope, no SDK/dataset backing, nothing stored.
Full contract in `docs/15-ui-commands.md`.

| Method | Path                          | Purpose                                              |
|--------|-------------------------------|-----------------------------------------------------|
| POST   | `/v1/ui/commands`             | publish a command → `{subscribers: n}` (0 = nobody listening) |
| GET    | `/v1/ui/commands/subscribe`   | SSE — `ready` → `command` per publish → `closed`     |

Body: `{action, spaceId, objectId?, source?}` — `action` is an open
slug set (`open_space` / `open_object`); `objectId` required iff
`action == "open_object"`. **At-most-once, no snapshot** — a subscriber
receives only commands published after it connects (no stale replay on
reconnect). `closed` reasons: `server_shutdown`, `overflow`. Both routes
sit outside the space group like `/sync-status/subscribe`.

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
account is booted — it gates server STATE, not the caller. CORS: one named exception — a fixed allowlist for the desktop-shell webview origins (`tauri://localhost`, `http://tauri.localhost`, the Vite dev origins; see `internal/server/routes.go`); requests without an Origin header are untouched, and the loopback-only listen stays the trust boundary.

## Pagination

Offset-based, mirroring the SDK. Cursor pagination is a future add.

## Idempotency

POST endpoints are **not** idempotent in v1 — each POST produces a new
DAG change. An `Idempotency-Key` header is a future add.
