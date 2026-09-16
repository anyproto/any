---
title: HTTP API
description: Every endpoint of the any server, grouped as the server registers them, with bodies, return shapes and the error codes each one can answer.
order: 10
---
# HTTP API

The any server listens on `127.0.0.1:7001` and exposes its API under `/v1/`. The tables below give each route’s request and response shape. Dataset reads use `/query`; dataset writes share a write-result envelope. Consumer-side features built next to the SDK — search, links and backlinks, the event bus, processes and the local store — have their own routes and responses.

## Conventions

- **Base path** `/v1/`. JSON requests and responses use `application/json; charset=utf-8`. File attach and content routes transfer raw file bytes; local-store export returns `application/gzip` and import accepts that file as its raw body. Subscription responses use `text/event-stream`.
- **Status codes**: `200` reads, `201` creates, `204` side-effect-only endpoints. Errors always use the envelope in [Errors](errors.html).
- **Ids in paths** are URL-safe strings; segments are URL-encoded (a bundle id like `favorites/v1` becomes `favorites%2Fv1`).
- **Body limit** 1 MiB on every route except file attach and local-store import, whose raw bodies stream without this cap.
- **Strict bodies**: endpoints whose OpenAPI schema carries `additionalProperties: false` answer `400 request.unknown_field` for any unknown top-level key. `GET /v1/openapi.json` is the authoritative list.
- **Unauthorized server**: until an account is booted, every route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json` and `/v1/auth` answers `401 auth.required`.
- **Tech space**: `GET /v1/account` returns its id as `techSpaceId`. It is a valid `:spaceId` for bundles, for types, collections and records on bundle roots, and for the space read, sync-status, debug and sync routes; every other space-scoped route there answers `405 space.unsupported`.

### Write result

Every dataset write — `/modify`, `/delete-records`, and the chat and editor handlers — returns `api.ModifyResult`:

```json
{ "versionId": "<change VersionId>", "changeId": "<changeId>",
  "recordIds": ["<id>"], "rejections": [] }
```

`recordIds` mirrors the input order (`recordIds[0]` is the server-derived id on a create); `rejections` appears only when a handler dropped an op. These dataset writes do not return the record body — read it back through `/query` or live through `/query/subscribe` (see [Reading data](../database/reading-data.html)).

## Meta

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/health` | — | `{status, version, networkId, account, bootstrapping, crdtVersion?}` | works unauthorized (`account: ""`); `networkId` is the joined any-sync network, set from startup; `bootstrapping` is true while the background catch-up pass runs; `crdtVersion {supported, stored, newer}` — `newer` means the account was written by a newer release and this server is read-only |
| POST | `/v1/shutdown` | header `X-Any-Control-Token` | 204 | managed servers only (`403 shutdown.not_managed` on standalone; `403 control.forbidden` without the token); drains in-flight streams for up to 10 s |
| GET | `/v1/openapi.json` | — | OpenAPI 3.1 document | 404 on the mobile build |

```bash
curl http://127.0.0.1:7001/v1/health        # any status
```

## Auth

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/auth` | — | `{authorized, accountId?, mode, capabilities: {deauthorize, switchAccount, shutdown}, accounts: [{id, default?}]}` | `mode` is `standalone` or `managed`; clients branch on the capability bits; `accounts` lists wallets on disk (standalone only, `[]` on managed) |
| POST | `/v1/auth` | `{}` \| `{mnemonic, index?, replace?}` \| `{accountId}` (+ header `X-Any-Control-Token` on managed) | `{accountId, created, mnemonic?, alreadyAuthorized?}` | generates / restores / selects an account and boots the engine in place; the running account answers `200 {alreadyAuthorized: true}`; `replace: true` switches a managed server in place; `mnemonic` returned once, only when generated |
| DELETE | `/v1/auth` | header `X-Any-Control-Token` | 204 | managed only: tears the account down in place, the server stays up unauthorized (`403 auth.not_managed` on standalone) |

Errors: `400 auth.bad_mnemonic`, `400 request.invalid_field` (mnemonic + accountId together, `index` without `mnemonic`, `replace` without a credential, `accountId` on a managed server), `403 control.forbidden`, `403 auth.not_managed`, `404 auth.account_not_found`, `409 auth.account_in_use`, `409 auth.account_mismatch`, `409 auth.mnemonic_mismatch`, `409 auth.already_authorized`, `409 auth.network_mismatch`, `400 auth.passkey_required`, `409 sdk.crdt_version_newer`, `500 auth.device_key_corrupt`, `500 auth.network_pin_corrupt`. Details and the decision table: [Accounts](../auth/accounts.html).

```bash
curl -X POST http://127.0.0.1:7001/v1/auth -d '{}'      # any auth login
```

## Account and identities

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/account` | — | `{id, metadata?, techSpaceId}` | |
| PUT | `/v1/account/metadata` | `{name?, description?, iconCid?}` | 204 | at least one field (`400 request.missing_field`); the profile is encrypted — contacts see it only after a shared-space or 1-1 key exchange |
| POST | `/v1/account/access-code` | `{code}` | `{status: accepted\|already_redeemed, redemptionId?}` | signs the code with the account key and relays it to the invite service at `access.redeemUrl`; `409 access.disabled` without one; refusals are `access.*` with the service's code in `details.code` |
| GET | `/v1/identities` | — | `{identities: [IdentityInfo]}` | account-global directory of every identity encountered; carries no rights |
| GET | `/v1/identities/:identity` | — | `IdentityInfo` | `404 identity.not_found` |
| GET | `/v1/identities/subscribe` | — | SSE `identities` frames | see [Events](events.html) |

`IdentityInfo` = `{identity, name?, description?, iconCid?, spaceIds}`. Details: [Identities](../auth/identities.html).

## Spaces

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces` | `{name?, description?, iconCid?, spaceType?}` | 201 `SpaceInfo` | |
| GET | `/v1/spaces` | `?status=all\|<status>` | `{spaces: [SpaceInfo]}` | active-only by default; deleted rows are sticky tombstones |
| POST | `/v1/spaces/query` | snapshot body + `dataset?` (`spaces` \| `profile`) | `{records, total?, hasNext?}` | raw tech-space rows; other datasets `400 request.invalid_field` |
| POST | `/v1/spaces/query/subscribe` | same | SSE | see [Space list](../realtime/space-list.html) |
| GET | `/v1/spaces/:spaceId` | — | `SpaceInfo` | non-active rows are served from the index without loading the space; a deleted id reads `200` with `status: "deleted"` |
| PATCH | `/v1/spaces/:spaceId` | `{name?, description?, iconCid?}` | 204 | absent = keep, `""` = clear; at least one field; mirror is async |
| PATCH | `/v1/spaces/:spaceId/settings` | `{set: {k: scalar}, unset: [k]}` | 204 | account-private per-space settings; `notifyMode` = `all\|mentions\|none` |
| POST | `/v1/spaces/:spaceId/sync` | — | 204 | forces one head-sync round; blocks until done |
| DELETE | `/v1/spaces/:spaceId` | — | 204 | real offline-first deletion; on a `joining` row it withdraws the request; `409 space.derived_undeletable`, `404 space.not_found` |
| POST | `/v1/spaces/join` | `{inviteToken, metadata?}` | 201 \| 202 `SpaceInfo` | 202 while the request awaits approval (status `joining`); guest tokens auto-detected (201 once loaded, 202 while loading); `400 invite.invalid`; `409 space.deleted` on a space this account deleted; `409 space.already_member` for a guest token of a space already tracked |
| GET | `/v1/spaces/derived` | — | `{spaces: [{name, spaceId, created, status?}]}` | resolves, never creates |
| POST | `/v1/spaces/derived/:name` | — | 201 `SpaceInfo` | idempotent; `404 space.derived_unknown`, `409 space.deleted` |
| GET | `/v1/spaces/:spaceId/datasets` | — | `{datasets: [{name, schema, owners?, module, shared?}]}` | JSON Schema with `x-scope` per field; `owners` = the types whose parts declare the storage collection |
| GET | `/v1/datasets` | — | `{datasets: [{name, schema}]}` | tech-space system datasets |
| POST | `/v1/spaces/:spaceId/search` | `{query, scopes?, limit?, mode?, require?, exclude?, maxData?, passages?, filter?}` | `{hits, mode, vectorStatus, truncated?}` | local index, not an SDK method; `limit` counts records (default 10), `passages` ≤ 10; `filter` is an objects-query filter on the host object's row, `truncated` marks a short filtered page that may not be exhaustive; `400 filter.invalid`, `400 filter.unknown_operator`, `409 index.disabled`, `400 index.no_embedder`, `503 index.embedder_unavailable`, `409 index.terms_unsupported`, `400 search.bad_mode`, `400 search.bad_scope` |

`SpaceInfo` fields worth knowing: `spaceIndexObjectId`, `createdAt`, `spaceType` (`any.space` \| `any.onetoone`), `author`, `ownRole` (`owner\|admin\|writer\|reader\|guest\|none`), `settings`, `push`, `derived`, `status`.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces -d '{"name":"Notes"}'
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SP -d '{"name":"Project Phoenix"}'   # any space update $SP --name "Project Phoenix"
```

### One-to-one and direct-add invites

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces/one-to-one` | `{otherIdentity}` | 201 `SpaceInfo` | initiate / accept-by-peer / un-decline; `400 request.missing_field`, `400 request.invalid_field` (self-pairing) |
| POST | `/v1/spaces/one-to-one/register-incoming` | `{peerIdentity, displayHint?}` | 204 | out-of-band discovery, idempotent |
| POST | `/v1/spaces/:spaceId/one-to-one/accept` | — | 200 `SpaceInfo` | |
| POST | `/v1/spaces/:spaceId/one-to-one/decline` | — | 204 | synced, sticky |
| POST | `/v1/spaces/:spaceId/invite/accept` | — | 200 \| 202 `SpaceInfo` | `409 space.not_invite_pending`, `404 space.not_found` |
| POST | `/v1/spaces/:spaceId/invite/decline` | — | 204 | synced, sticky, non-terminal |

Pending rows are discovered through `GET /v1/spaces?status=one_to_one_pending` / `?status=invite_pending`. See [One-to-one](../collaboration/one-to-one.html).

## Bundles

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces/:spaceId/bundles` | `{id, name?, rootType?, rootCollections?, rootProperties?, derived?, parts?, properties?, xKey?, collection?, layout?, hidden?}` | `{bundle, installed}` | adopt-or-install; a bundle may declare a definition on its root — a type (`parts`, `properties`, `xKey`, `layout`, `hidden`) or, with `collection: true`, a collection (`properties`, `xKey`, `hidden`; `parts` / `layout` refused). A declaring root hosts its own values and records; `rootType` is required when the body declares nothing and refused next to a declaration. `409 bundle.not_ready`, `400 request.missing_field`, `400 type.not_found`, `400 collection.not_found`, `400 property.format_violation`, `409 type.xkey_conflict`, `400 dataset.module_reserved`, `409 bundle.reserved` for a `system:` id |
| GET | `/v1/spaces/:spaceId/bundles` | — | `{bundles: [Bundle], synced}` | answers after the registry convergence wait; `synced: true` means an absent bundle is definitively not installed |
| GET | `/v1/spaces/:spaceId/bundles/:bundleId` | — | `{bundle, synced}` | `404 bundle.not_found` |
| POST | `/v1/spaces/:spaceId/bundles/:bundleId/resolve` | `{loserRootId}` | 204 | `409 bundle.loser_not_ready`, `409 bundle.not_loser` |
| POST | `/v1/spaces/:spaceId/bundles/:bundleId/children` | `{seed, type, collections?}` | `{objectId}` | deterministic child; `type` set on first materialization, missing collections added on every call; `409 bundle.not_ready` |

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/bundles \
  -d '{"id":"notes/v1","name":"Notes","xKey":"notes","hidden":true,"parts":[{"key":"body","datasets":[{"module":"editor","shared":true}]}]}'
```

Full semantics in [Bundles](../collaboration/bundles.html).

## Catalog

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/catalog` | — | `{usecases}` | the server's well-known `system:` bundles, grouped into usecases |
| GET | `/v1/catalog/:usecaseId` | — | usecase | `404 catalog.not_found` |
| POST | `/v1/catalog/:usecaseId/setup` | `{spaceId}` | `{usecase, bundles: [{usecase, id, bundle, installed, typeId?, collectionId?, properties?, miniapp?}]}` | idempotent adopt-or-install, dependencies first; `typeId` for a bundle declaring a type, `collectionId` for one declaring a collection; `properties` maps xKey → propId; `409 type.xkey_conflict`, `409 bundle.not_ready`, `405 space.unsupported` on the tech space |

`wiki` is the usecase that gives a space its tree — a **collection** pages are filed under ([Objects](../database/objects.html)).

## Objects

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces/:spaceId/objects` | `{type, collections?, initialProperties?}` | 201 `{objectId}` | closed vocabulary (`400 request.unknown_field`); `type` required (`400 request.missing_field`; `page` is the plain document), `initialProperties` keyed by owner; nothing appended server-side — the tree is the wiki usecase's collection ([Objects](../database/objects.html)); `400 type.not_a_type` for a collection id in `type`, `400 collection.not_a_collection` for a type id in `collections`, `400 type.reserved_carrier` for a type only its own root may carry |
| POST | `/v1/spaces/:spaceId/objects/query` | snapshot body | `{records, total?, hasNext?}` | cross-object `objects` storage collection |
| POST | `/v1/spaces/:spaceId/objects/query/subscribe` | snapshot body | SSE | |
| POST | `/v1/spaces/:spaceId/objects/aggregate` | `{pipeline, groupLimit?, accumArrayLimit?, memoryLimitBytes?, explain?}` | `{records}` \| `{plan}` | `400 aggregate.bad_pipeline`, `400 aggregate.limit_exceeded` |
| GET | `/v1/spaces/:spaceId/objects/:objectId` | — | `{objectId, record}` | the raw `objects` row; `404 object.not_found`, `410 object.deleted` |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId` | — | 204 | tombstones the row, then tears down the tree; `404 sdk.not_found`, `409 object.derived_undeletable`, `403 space.read_only` |

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects \
  -d '{"type":"page","initialProperties":{"any":{"name":"Dune"}}}'
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/query \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt"],"limit":20}'
```

Every `objects` row carries derived `author`, `createdAt`, `spaceId`, `modifiedAt`, `modifiedBy` (instants as `{"$date": …}`; `modifiedBy` is the identity that signed the change `modifiedAt` names). See [Objects](../database/objects.html) and [System fields](../database/system-fields.html).

### Links and backlinks

Reads over the link index the search indexer maintains — not SDK methods. All answer `409 index.disabled` when the index is off and reflect a write after the indexer's debounce.

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `…/objects/:objectId/backlinks` | `?record&dataset` \| `?prop`, `?kind` (repeatable), `?limit` | `{object: [Link], parts: [Link], truncated?}` | edges pointing at the object itself vs one of its records or property values; `limit` default and max 500; unknown ids answer empty lists |
| GET | `…/objects/:objectId/links` | same narrowing, `?dataset` alone | `{links: [Link], truncated?}` | edges whose source is the object |
| GET | `/v1/backlinks` | `?target=<global any:// uri>`, `?kind`, `?limit` | `{spaces: [{spaceId, object, parts, truncated?}]}` | across every indexed space; a bare in-space URI is `400 request.invalid_field` |

`Link` = `{source: {spaceId, objectId, dataset, recordId, typeId?}, kind, target: {uri, kind, spaceId, objectId?, dataset?, recordId?, propId?, identity?, fileId?}}`; `kind` is `mention`, `link`, `card`, `embed` or `relation`. A change publishes a device-scope `links.updated` event naming the moved targets. See [Links](../types/links.html).

### Editor (blocks and markdown)

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `…/objects/:objectId/editor/:collection/markdown` | — | `{content}` | render transform over the editor storage collection (`editor_blocks` or `<typeId>_<key>`); `404 dataset.not_found` when no editor part in the space declares `:collection` |
| PUT | `…/objects/:objectId/editor/:collection/markdown` | `{content}` | `{inserted, updated, deleted, unchanged}` | parse → diff → per-block ops; every editor write answers `400 dataset.not_declared` when the object's type does not declare that storage collection |
| PATCH | `…/objects/:objectId/editor/:collection/markdown` | `{edits: [{oldText, newText, replaceAll?}]}` | PUT shape | all-or-nothing; `400 markdown.no_match`, `markdown.ambiguous_match`, `markdown.overlapping_edits` |
| POST | `…/objects/:objectId/editor/:collection/markdown/append` | `{content}` | PUT shape (`inserted` only) | O(fragment), no diff |
| POST | `…/objects/:objectId/editor/:collection/blocks` | `{type, style?, text?, nav?}` | 201 write result | `recordIds[0]` is the block id; `400 blocks.type_required` |
| PATCH | `…/objects/:objectId/editor/:collection/blocks/:blockId` | `{set: {"dotted.path": v}, unset: [path]}` | write result | required fields cannot be unset; `404 blocks.not_found` |
| DELETE | `…/objects/:objectId/editor/:collection/blocks/:blockId` | — | write result | no cascade to children; `404 blocks.not_found` |

Reads: `POST /v1/spaces/:spaceId/query` with `{"objectId", "dataset": "editor_blocks", "sort": ["nav.pos"]}`. See [Editor](../types/editor.html) and [Markdown import/export](../database/markdown-import-export.html).

## Data plane

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces/:spaceId/query` | snapshot body + `objectId`, `dataset` | `{records, total?, hasNext?}` | per-object dataset; `404 object.not_found` |
| POST | `/v1/spaces/:spaceId/query/subscribe` | same + `mailboxCapacity?`, `driftBudgetPercent?` | SSE | frames in [Events](events.html) |
| POST | `/v1/spaces/:spaceId/aggregate` | `{objectId, dataset, pipeline, …}` | `{records}` \| `{plan}` | snapshot-only |
| POST | `/v1/spaces/:spaceId/modify` | `{objectId, dataset, records: [{id, upsert?, ops}], traceIds?, scope?}` | write result | `scope: "local"` for device-local fields only; a write onto a tombstoned record comes back as a `rejections` entry; `400 dataset.unknown`, `400 dataset.not_declared` |
| POST | `/v1/spaces/:spaceId/upsert` | `{objectId, dataset, records: [{id, fields}], pageSize?, traceIds?}` | `{pages, created, updated, skipped, rejections}` | idempotent; `400 upsert.requires_user_ids`, `400 dataset.unknown`, `400 dataset.not_declared` |
| POST | `/v1/spaces/:spaceId/delete-records` | `{objectId, dataset, recordIds}` | write result | |

Snapshot body (closed field set — unknown keys `400 request.unknown_field`):

```json
{ "objectId": "obj", "dataset": "chat_messages",
  "filter": {"unread": true}, "sort": ["-_ver.id"],
  "limit": 50, "offset": 0, "includeTotal": true,
  "projection": {"text": 1, "creator": 1} }
```

`projection` (dotted paths → `1` include / `-1` exclude; `id` always ships, `_ver` narrows to match) shapes snapshot rows and every record in `changes` frames. `includeDeleted: true` (snapshot `…/query` only) returns record tombstones next to live rows. Filter faults: `400 filter.unknown_operator`, `400 filter.invalid`. A serialized-nil `objectId` is `400 object.id_required`. Grammar in [Reading data](../database/reading-data.html); pipelines in [Aggregation](../database/aggregation.html).

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/modify -d '{
  "objectId":"'$OBJ'","dataset":"notes",
  "records":[{"id":"","upsert":true,"ops":[{"type":"$set","path":"","value":{"title":"x"}}]}]}'
```

## Version history

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `…/objects/:objectId/history` | `?dataset&recordId&traceId&author&limit&cursor&coalesce&coalesceWindow` | `{changes, cursor}` | newest-first DAG order; `limit` default 50, cap 200 |
| GET | `…/objects/:objectId/history/diff` | `?version&base?&dataset?&recordIds?` | `{base, version, datasets}` | no `base` = effect diff against parents |
| GET | `…/objects/:objectId/history/:version` | `?dataset?` | records grouped by dataset | `413 history.view_too_large` |
| GET | `…/objects/:objectId/history/:version/datasets/:dataset/records/:recordId` | — | `{exists, deleted?, record?}` | record fast path |

A version is a `changeId`. Errors: `404 history.version_not_found`, `404 history.truncated` (reserved). `chat_messages` opts out of history. See [Version history](../database/version-history.html).

## Types, collections and properties

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/spaces/:spaceId/types` | `includeHidden?` | `{types}` | built-ins `any`, `spaceIndex`, `type`, `collection` first, then the registered hidden `page` / `dataview`, then user types; hidden types (the hidden built-ins, and any type created, patched or installed with `hidden: true`) only with `includeHidden=true` |
| POST | `/v1/spaces/:spaceId/types` | `{name?, description?, iconCid?, xKey, layout?, hidden?, meta?}` | 201 `{typeId}` | `400 type.xkey_required`, `409 type.xkey_conflict` (one handle namespace with collections); properties and parts are added through their own routes |
| GET | `/v1/spaces/:spaceId/types/:typeId` | — | `TypeInfo` | `404 type.not_found`; `400 type.not_a_type` for a user collection id |
| DELETE | `/v1/spaces/:spaceId/types/:typeId` | — | — | `501 sdk.not_implemented` |
| GET | `…/types/:typeId/properties` | — | `{properties: [PropertyDef]}` | `404 type.not_found`; `200 []` means "no properties yet" |
| POST | `…/types/:typeId/properties` | `{name?, description?, kind, xKey?, xFormat?, meta?, scope?}` | 201 `{propId}` | `kind` required and pinned (`string`, `number`, `boolean`, `array`, `object`, `datetime`); `xFormat` is the descriptor ([Types and properties](../database/types-and-properties.html)); `meta` takes only `index`; `400 property.format_invalid`, `409 property.xkey_conflict`, `400 type.registered` |
| PATCH | `…/types/:typeId/properties/:propId` | `{set, unset}` | 204 | `name`, `description`, `xKey`, `meta.index`, `xFormat.*` leaves; `400 property.immutable`, `400 property.format_invalid`, `409 property.xkey_conflict`, `404 sdk.not_found` |
| DELETE | `…/types/:typeId/properties/:propId` | — | 204 | tombstone; values not cleaned up |
| PATCH | `…/types/:typeId` | `{name?, description?, iconCid?, layout?, hidden?, meta?}` | 204 | rendering slice, the hidden flag and the per-key meta bag (`null` unsets a key); `400 type.registered`, `404 type.not_found` |
| GET | `…/types/:typeId/parts` | — | `{parts: [{id, key, name?, icon?, pos?, hidden?, ui?, uses?, datasets: [DatasetDef]}]}` | |
| POST | `…/types/:typeId/parts` | `{key, name?, icon?, pos?, hidden?, ui?, uses?, datasets?: [dataset draft]}` | 201 `{partId}` | one change; `409 dataset.key_conflict`, `400 dataset.module_unknown`, `400 dataset.module_reserved`, `400 dataset.shared_conflict`, `409 dataset.module_owned` |
| PATCH | `…/types/:typeId/parts/:partId` | `{set, unset}` | 204 | `name`, `icon`, `pos`, `hidden`, `ui`, `uses`; `400 dataset.immutable` |
| DELETE | `…/types/:typeId/parts/:partId` | — | 204 | removes the part and its datasets |
| POST | `…/types/:typeId/parts/:partId/datasets` | `{key?, module?, shared?, displayName?, idRule?, deleteBy?, search?, fields, …}` | 201 `{datasetDefId, collection}` | the storage collection is `<typeId>_<key>` (or the module's canonical one when shared); `409 dataset.key_conflict`, `400 dataset.decl_invalid` |
| GET | `…/types/:typeId/datasets` | — | `{datasets: [DatasetDef]}` | flat compiled view; each carries its storage `collection`, `module`, `shared`, `partId` |
| PATCH | `…/types/:typeId/datasets/:defId` | `{set, unset}` | 204 | display leaves only; `400 dataset.immutable` |
| DELETE | `…/types/:typeId/datasets/:defId` | — | 204 | |
| POST | `…/types/:typeId/datasets/:defId/fields` | field def | 201 `{fieldDefId}` | never `required` |
| PATCH | `…/types/:typeId/datasets/:defId/fields/:fieldId` | `{set, unset}` | 204 | `name`, `description`, `xFormat.*`; `400 dataset.immutable` |
| DELETE | `…/types/:typeId/datasets/:defId/fields/:fieldId` | — | 204 | |
| GET | `/v1/spaces/:spaceId/collections` | `includeHidden?` | `{collections}` | the meta row `collection` first, then the registered hidden `miniapp` / `bin`, then user collections |
| POST | `/v1/spaces/:spaceId/collections` | `{name?, description?, iconCid?, xKey, hidden?, meta?}` | 201 `{collectionId}` | the type body minus `layout`; `400 type.xkey_required`, `409 type.xkey_conflict` |
| GET | `/v1/spaces/:spaceId/collections/:collectionId` | — | `CollectionInfo` | `404 collection.not_found`; `400 collection.not_a_collection` for a user type id |
| PATCH | `…/collections/:collectionId` | `{name?, description?, iconCid?, hidden?, meta?}` | 204 | `400 collection.registered` on a built-in, `404 collection.not_found` |
| DELETE | `…/collections/:collectionId` | — | — | `501 sdk.not_implemented` |
| GET/POST/PATCH/DELETE | `…/collections/:collectionId/properties[/:propId]` | as the `…/types` twins | as the `…/types` twins | one property surface: same bodies, same codes; a column write on a built-in is `400 type.registered` |
| GET | `/v1/spaces/:spaceId/properties/:objectId` | — | `{record}` | raw `objects` row |
| POST | `/v1/spaces/:spaceId/properties/:objectId/set/:ownerId` | `{patch: {propId: value}}` | write result | `ownerId` is the object's type, one of its collections, or a module namespace its type grants (`chat` for `chat.notifyMode`); routes by the props' declared scope; `400 property.format_violation`, `property.kind_mismatch`, `property.not_found` |
| POST | `…/properties/:objectId/type/:typeId` | — | write result | `$set any.type`, replacing the previous one (no unset; a raw `$unset` is `400 membership.type_required`); checks both ids (`404 object.not_found`, `404 type.not_found`, `400 type.not_a_type`); `400 type.reserved_carrier` |
| POST | `…/properties/:objectId/collections/:collectionId` | — | write result | idempotent `$addToSet any.collections`; checks both ids (`404 object.not_found`, `404 collection.not_found`, `400 collection.not_a_collection`); `collections/bin` also stamps `bin.movedAt` / `bin.movedBy` |
| DELETE | `…/properties/:objectId/collections/:collectionId` | — | write result | idempotent `$pull`; checks neither id (the repair path); values and records stay as orphan data; `collections/bin` clears the stamps |

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types -d '{"name":"Book","xKey":"book"}'
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types/$T/properties \
  -d '{"name":"Rating","kind":"number","xKey":"rating"}'
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/collections -d '{"name":"Reading list","xKey":"reading_list"}'
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/properties/$OBJ/collections/$C
```

Values are keyed by `propId`, never `xKey`. See [Types and properties](../database/types-and-properties.html), [Collections](../database/collections.html) and [Runtime datasets](../database/runtime-datasets.html).

## Chat

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `…/objects/:objectId/chat/messages` | `{text?, replyToMessageId?, agent?, attachments?, context?, control?}` | 201 write result | text required unless `attachments` or `control` is set (`400 chat.text_required`); text ≤ 32 KiB (`400 chat.text_too_long`); `400 chat.reply_id_invalid`, `chat.agent_invalid`, `chat.attachments_invalid`, `chat.context_invalid`, `chat.control_invalid` |
| PATCH | `…/chat/messages/:msgId` | `{text}` | write result | `403 chat.not_author`, `404 chat.not_found` |
| DELETE | `…/chat/messages/:msgId` | — | write result | own only |
| POST | `…/chat/messages/:msgId/reactions/:emoji` | — | write result | toggle; `400 chat.emoji_invalid` (empty or > 64 bytes) |
| POST | `…/chat/read-all` | — | 204 | |
| POST | `…/chat/messages/:msgId/read` | — | 204 | marks this and everything before it |
| POST | `…/chat/messages/:msgId/reactions-read` | — | 204 | |

Reads: `POST /v1/spaces/:spaceId/query` with `dataset: "chat_messages"`, `sort: ["-_ver.id"]`, a `limit`. See [Chat](../types/chat.html).

## Files

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `…/objects/:objectId/files` | raw body; `Content-Type`; `?name&variant&variantOf` | 201 `FileInfo` | body-limit exempt; `400 file.variant_invalid` |
| POST | `…/objects/:objectId/files/query` | snapshot body | `{records, total?, hasNext?}` | cleartext payload rows; `404 file.not_found` before first attach |
| POST | `…/objects/:objectId/files/query/subscribe` | snapshot body | SSE | |
| GET | `/v1/spaces/:spaceId/files` | `?objectId&limit` | `{files}` | |
| GET | `/v1/spaces/:spaceId/files/stats` | — | durability counts | |
| GET | `/v1/spaces/:spaceId/files/subscribe` | — | SSE `status` frames | local transitions only |
| GET | `/v1/spaces/:spaceId/files/:fileId` | — | `FileInfo` | `404 file.not_found` |
| GET | `/v1/spaces/:spaceId/files/:fileId/content` | `?variant` | bytes, Range/206 | `409 file.not_available` |
| GET | `/v1/spaces/:spaceId/files/:fileId/status` | — | status | |
| POST | `/v1/spaces/:spaceId/files/:fileId/pin` | — | 204 | |
| POST | `/v1/spaces/:spaceId/files/:fileId/retry` | — | 204 | |
| POST | `/v1/spaces/:spaceId/files/:fileId/offload` | — | 204 | `409 file.not_durable` |
| DELETE | `/v1/spaces/:spaceId/files/:fileId` | — | 204 | variants cascade |
| GET | `/v1/files/cache` | — | `{size}` | all spaces |
| POST | `/v1/files/cache/free` | `{bytes}` | `{freed}` | LRU, never the only copy |
| POST | `/v1/files/cache/sweep` | — | 204 | |

```bash
curl -X POST -T photo.jpg -H 'Content-Type: image/jpeg' \
  "http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/files?name=photo.jpg"   # any file attach $SP $OBJ photo.jpg
```

See [Files](../files/index.html).

## Members, invites, ACL

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/spaces/:spaceId/members` | — | `{members: [Member]}` | authoritative roster with `permission` |
| GET | `/v1/spaces/:spaceId/members/me` | — | `Member` | |
| GET | `/v1/spaces/:spaceId/members/requests` | — | `{requests: [{recordId, identity, name?, description?, iconCid?}]}` | pass `recordId` as `requestRecordId` to `acl/accept` |
| GET | `/v1/spaces/:spaceId/members/subscribe` | — | SSE `member` frames | |
| GET | `/v1/spaces/:spaceId/members/:identity` | — | `Member` | |
| POST | `/v1/spaces/:spaceId/invites` | — | 201 `{spaceId, inviteToken}` | replaces any prior invite; `409 invite.duplicate` when the engine refuses a second one |
| GET | `/v1/spaces/:spaceId/invites` | — | `{invites: [{recordId, permission, inviteToken?}]}` | token only on the minting account's devices |
| GET | `/v1/spaces/:spaceId/invites/:recordId` | — | one invite | `404 invite.not_found` |
| DELETE | `/v1/spaces/:spaceId/invites` | — | 204 | revoke all |
| DELETE | `/v1/spaces/:spaceId/invites/:recordId` | — | 204 | |
| POST | `/v1/spaces/:spaceId/guest-key` | — | 201 `{spaceId, inviteToken}` | public read-only token; owner only, idempotent |
| DELETE | `/v1/spaces/:spaceId/guest-key` | — | 204 | rotates the read key; `404 guest_key.not_found` |
| POST | `/v1/spaces/:spaceId/acl/accept` | `{requestRecordId, permission}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/decline` | `{identity}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/permissions` | `{changes: [{identity, permission}]}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/remove` | `{identities}` | 204 | rotates the read key |
| POST | `/v1/spaces/:spaceId/acl/add` | `{accounts: [{identity, permission, metadata?}]}` | 204 | added accounts see an `invite_pending` row |
| POST | `/v1/spaces/:spaceId/acl/ownership` | `{newOwner, oldOwnerPerm}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/self-remove` | — | 204 | |
| POST | `/v1/spaces/:spaceId/acl/cancel-join` | — | 204 | joiner row reads `deleted`, re-request via `/v1/spaces/join`; `404 space.not_found`, `409 space.join_not_pending` |
| POST | `/v1/spaces/:spaceId/acl/stop-sharing` | — | 204 | drops everyone |

Permissions: `none`, `reader`, `guest`, `writer`, `admin`, `owner`. Member statuses: `unknown`, `joining`, `active`, `removed`, `declined`, `removing`, `canceled`. Guests writing get `403 space.read_only`; an ACL operation the caller's role does not allow is `403 acl.forbidden`, one naming an unknown request record `404 acl.record_not_found`. See [Collaboration](../collaboration/index.html).

## Sync status and debug

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/spaces/:spaceId/sync-status` | — | `{spaceId, state, synced, total, networkPeers, localPeers, p2p, lastSyncedAt}` | `state`: `unknown\|offline\|syncing\|synced\|error` |
| GET | `…/sync-status/objects/:objectId` | — | `{objectId, state, lastSyncAt}` | unknown ids answer `state: "unknown"` |
| GET | `…/sync-status/objects/:objectId/subscribe` | — | SSE `status` frames | |
| GET | `/v1/sync-status/subscribe` | — | SSE, every space | account-scoped |
| GET | `…/sync-status/peers` | — | — | `501 sdk.not_implemented` |
| GET | `/v1/spaces/:spaceId/debug` | — | per-peer headsync counters | diagnostic, unstable |
| GET | `…/debug/objects/:objectId` | — | tree + sync snapshot | locks the tree; don't poll |
| GET | `/v1/debug/p2p` | — | LAN discovery snapshot | |

See [Sync status](../realtime/sync-status.html) and [Debugging](../operations/debugging.html).

## Events and processes

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/events` | `{type, scope, spaceId?, target?, data?}` | `{subscribers}` | `sender` server-stamped; `data` ≤ 64 KiB (`400 events.payload_too_large`); `409 events.no_read_key`, `409 events.too_many_patterns`, `403 events.topic_not_owned` |
| GET | `/v1/events/subscribe` | `?scope&spaceId&type&target` (repeatable) | SSE `event` frames | at-most-once, no replay |
| GET | `/v1/processes` | — | `{processes}` | in-memory view, staleness-swept |
| POST | `/v1/processes` | `{id, kind, title, scope, spaceId?, target?}` | `{subscribers}` | emits `process.started` |
| POST | `/v1/processes/:id/progress` | `{done?, total?, message?}` | `{subscribers}` | `{}` = heartbeat; `404 process.not_found` |
| POST | `/v1/processes/:id/finish` | `{status: done\|failed\|cancelled, error?}` | `{subscribers}` | `error` iff failed |
| POST | `/v1/processes/:id/cancel` | `{identity?}` | `{subscribers}` | `409 process.ambiguous` |

```bash
curl -X POST http://127.0.0.1:7001/v1/events \
  -d '{"type":"ui.open_object","scope":"device","data":{"spaceId":"'$SP'","objectId":"'$OBJ'"}}'
```

See [Event bus](../realtime/event-bus.html) and [Processes](../notifications/processes.html).

## Devices and push

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/devices` | — | `{devices, active: {slug: peerId}, self}` | election pre-resolved |
| POST | `/v1/devices/query` | snapshot body | `{records}` | raw `devices` rows |
| POST | `/v1/devices/query/subscribe` | snapshot body | SSE | |
| PUT | `/v1/devices/me` | `{name?, apps?}` | 204 | `"apps": {"slug": null}` uninstalls; `409 device.pruned` |
| POST | `/v1/devices/activate` | `{app}` | 204 | claim the active role on this device |
| DELETE | `/v1/devices/:peerId` | — | 204 | sticky tombstone; `404 device.not_found`, `400 device.self_delete` |
| POST | `/v1/push/token` | `{platform: ios\|android, token}` | 204 | `409 push.disabled` without a push node |
| GET | `/v1/push/token` | — | `{registered, platform}` | local state |
| DELETE | `/v1/push/token` | — | 204 | |
| GET | `/v1/push/subscriptions` | — | `{subscriptions: [{spaceKey, topic}]}` | |

See [Devices](../auth/devices.html) and [Push](../notifications/push.html).

## Local store

Device-local, non-CRDT collections that never sync — query, modifiers, indexes and aggregation for scratch sets, ingest staging and per-device caches. Not an SDK dataset: no type, no `_ver`, no subscribe, not search-indexed, and a space-scoped collection outlives its space. Every route answers `409 local.disabled` when `local.enabled` is false.

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/local/meta` | — | `{stages, accumulators}` | the pipeline vocabulary |
| GET | `/v1/local/collections` | `?scope=account\|space&spaceId` | `{collections: [{scope, spaceId?, name, storageName, count, indexes}]}` | |
| PUT | `/v1/local/collections` | `{scope, spaceId?, name, indexes?}` | 201 \| 200 `{collection, created}` | ensure; `400 local.bad_index` leaves nothing behind |
| DELETE | `/v1/local/collections` | `?scope&spaceId&name` | 204 | drop; no space pre-flight |
| POST | `/v1/local/insert` | `{coll, docs}` | `{ids}` | missing `id` minted; `409 local.duplicate_id`; ≤ 1000 docs (`400 local.too_many_docs`) |
| POST | `/v1/local/upsert` | `{coll, docs}` | `{ids}` | whole-document replace-or-insert |
| POST | `/v1/local/update` | `{coll, id, modifier, upsert?}` | `{modified, record}` | `404 local.doc_not_found` without `upsert` |
| POST | `/v1/local/delete` | `{coll, ids}` \| `{coll, filter}` | `{deleted}` | by filter is not atomic per call |
| POST | `/v1/local/get` | `{coll, id}` | `{record}` | `404 local.doc_not_found` |
| POST | `/v1/local/query` | `{coll, filter?, sort?, limit?, offset?, includeTotal?, projection?}` | `{records, total?, hasNext?}` | `limit` default 100, cap 1000 |
| POST | `/v1/local/aggregate` | `{coll, pipeline, groupLimit?, accumArrayLimit?, memoryLimitBytes?, explain?}` | `{records}` \| `{plan}` \| `{written}` | `$out` / `$merge` / `$lookup` name local collections by `storageName` only (`400 local.bad_sink_target`) |
| POST | `/v1/local/indexes` | `{coll, ensure?: [{name?, fields, unique?, sparse?}], drop?: [name]}` | `{indexes}` | |
| GET | `/v1/local/export` | `?scope=account\|space&spaceId&names=a,b` | `application/gzip` file | one consistent snapshot; `names` needs a scope (`spaceId` implies `space`); omit `names` for all collections in the selection |
| POST | `/v1/local/import` | raw exported file | `{collections: [{scope, spaceId?, name, storageName, count, indexes}]}` | ensure indexes and upsert documents; body-limit exempt; `400 local.bad_export`, `400 local.bad_index` |

`coll` is `{scope: "account" | "space", spaceId?, name}`, `name` matching `^[a-z0-9][a-z0-9_-]{0,63}$` (`400 local.bad_name`). An op on a local storage collection never ensured is `404 local.collection_not_found`. Space-scoped ensure, insert, upsert, update, index changes and pipeline sink writes check the space (`404 space.not_found` / `409 space.deleted`). Get, query, read-only aggregation, delete, drop, list, export and import do not: imported collections remain readable even if their space has never existed on this server.

### Export and import local collections

Export is a manual copy of selected local collections, including their scope, space ID, names, indexes and documents. It does not export the account's CRDT data or file bytes. The file is a gzip-compressed anyenc value stream, with a manifest followed by each collection's documents; use `.anyenc.gz` as its extension. An empty selection or missing named collection returns `404 local.collection_not_found` before the file response starts.

**Before you start:** use an authorized server with the local store enabled and an existing account-scoped `scratch` collection; the [CLI example](cli.html#local-store) creates one. This example exports it and imports the same file back. Change `DEST` to another authorized server's address to copy the collection there.

```bash
SOURCE=http://127.0.0.1:7001
DEST=http://127.0.0.1:7001
curl --fail --get "$SOURCE/v1/local/export" \
  --data-urlencode 'scope=account' --data-urlencode 'names=scratch' \
  --output scratch.anyenc.gz &&
curl --fail -X POST "$DEST/v1/local/import" \
  -H 'Content-Type: application/gzip' --data-binary @scratch.anyenc.gz
```

The import response lists the resulting collections. For space-scoped traces, export with `scope=space`, `spaceId=<sourceSpaceId>` and `names=trace_runs,trace_records,trace_blobs`; the destination does not need that space to read the imported records.

Import overlays existing collections: matching IDs are replaced, new IDs are inserted, and documents absent from the file remain. Indexes are ensured; a same-name index with a different definition is `400 local.bad_index`. Re-importing an unchanged file leaves the same stored data. Import commits in chunks of 256 documents, so a failure can leave earlier collections and chunks committed. Invalid gzip, format/version, document sections or storage names return `400 local.bad_export`; `details.imported` is the number of fully completed collections, not a rollback guarantee.

> **Note.** Synced writes are not idempotent — each POST produces a new DAG change. The exceptions are `/upsert`, where the caller-supplied record id is the idempotency key, and the adopt-or-install routes (`…/bundles`, `/v1/catalog/:usecaseId/setup`), where a second call adopts. Pagination is offset-based on every list.
