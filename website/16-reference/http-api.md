---
title: HTTP API
description: Every endpoint of the any server, grouped as the server registers them, with bodies, return shapes and the error codes each one can answer.
order: 10
---
# HTTP API

The any server listens on `127.0.0.1:7001` and exposes one JSON API under `/v1/`. Every endpoint below maps onto one SDK call (the two exceptions — `/search` and `/backlinks` — are marked), reads always go through `/query`, and every write returns the same result shape.

## Conventions

- **Base path** `/v1/`. Media type `application/json; charset=utf-8` for every request with a body and every response, except the two raw file routes.
- **Status codes**: `200` reads, `201` creates, `204` side-effect-only endpoints. Errors always use the envelope in [Errors](errors.html).
- **Ids in paths** are URL-safe strings; segments are URL-encoded (a bundle id like `general-chat/v1` becomes `general-chat%2Fv1`).
- **Body limit** 1 MB on every route except file attach.
- **Strict bodies**: endpoints whose OpenAPI schema carries `additionalProperties: false` answer `400 request.unknown_field` for any unknown top-level key. `GET /v1/openapi.json` is the authoritative list.
- **Unauthorized server**: until an account is booted, every route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json` and `/v1/auth` answers `401 auth.required`.

### Write result

Every dataset write — `/modify`, `/delete-records`, and the chat and editor handlers — returns `api.ModifyResult`:

```json
{ "versionId": "<change VersionId>", "changeId": "<changeId>",
  "recordIds": ["<id>"], "rejections": [] }
```

`recordIds` mirrors the input order (`recordIds[0]` is the server-derived id on a create); `rejections` appears only when a handler dropped an op. Writes never return the record body — read it back through `/query` or live through `/query/subscribe` (see [Reading data](../database/reading-data.html)).

## Meta

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/health` | — | `{status, version, account, bootstrapping, crdtVersion?}` | works unauthorized (`account: ""`); `bootstrapping` is true while the background catch-up pass runs; `crdtVersion {supported, stored, newer}` — `newer` means the account was written by a newer release and this server is read-only |
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

Errors: `400 auth.bad_mnemonic`, `400 request.invalid_field` (mnemonic + accountId together, `index` without `mnemonic`, `replace` without a credential, `accountId` on a managed server), `403 control.forbidden`, `403 auth.not_managed`, `404 auth.account_not_found`, `409 auth.account_in_use`, `409 auth.account_mismatch`, `409 auth.mnemonic_mismatch`, `409 auth.already_authorized`, `400 auth.passkey_required`, `500 auth.device_key_corrupt`. Details and the decision table: [Accounts](../auth/accounts.html).

```bash
curl -X POST http://127.0.0.1:7001/v1/auth -d '{}'      # any auth login
```

## Account and identities

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/account` | — | own id + metadata | |
| PUT | `/v1/account/metadata` | `{name?, description?, iconCid?}` | 204 | at least one field (`400 request.missing_field`); the profile is encrypted — contacts see it only after a shared-space or 1-1 key exchange |
| GET | `/v1/identities` | — | `{identities: [IdentityInfo]}` | account-global directory of every identity encountered; carries no rights |
| GET | `/v1/identities/:identity` | — | `IdentityInfo` | `404 identity.not_found` |
| GET | `/v1/identities/subscribe` | — | SSE `identities` frames | see [Events](events.html) |

`IdentityInfo` = `{identity, name?, description?, iconCid?, spaceIds}`. Details: [Identities](../auth/identities.html).

## Spaces

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces` | `{name, …}` | 201 `SpaceInfo` | |
| GET | `/v1/spaces` | `?status=all\|<status>` | `[]SpaceInfo` | active-only by default; deleted rows are sticky tombstones |
| POST | `/v1/spaces/query` | snapshot body + `dataset?` (`spaces` \| `profile`) | `{records, total?, hasNext?}` | raw tech-space rows; other datasets `400 request.invalid_field` |
| POST | `/v1/spaces/query/subscribe` | same | SSE | see [Space list](../realtime/space-list.html) |
| GET | `/v1/spaces/:spaceId` | — | `SpaceInfo` | non-active rows are served from the index without loading the space |
| PATCH | `/v1/spaces/:spaceId` | `{name?, description?, iconCid?}` | 204 | absent = keep, `""` = clear; at least one field; mirror is async |
| PATCH | `/v1/spaces/:spaceId/settings` | `{set: {k: scalar}, unset: [k]}` | 204 | account-private per-space settings; `notifyMode` = `all\|mentions\|none` |
| POST | `/v1/spaces/:spaceId/sync` | — | 204 | forces one head-sync round; blocks until done |
| DELETE | `/v1/spaces/:spaceId` | — | 204 | real offline-first deletion; `409 space.derived_undeletable`, `404 space.not_found` |
| POST | `/v1/spaces/join` | `{inviteToken, metadata?}` | 202 `SpaceInfo` | request-to-join (status `joining`); guest tokens auto-detected; `400 invite.invalid` |
| GET | `/v1/spaces/derived` | — | `{spaces: [{name, spaceId, created, status?}]}` | resolves, never creates |
| POST | `/v1/spaces/derived/:name` | — | 201 `SpaceInfo` | idempotent; `404 space.derived_unknown`, `409 space.deleted` |
| GET | `/v1/spaces/:spaceId/datasets` | — | `{datasets: [{name, schema, owners?, module, shared?}]}` | JSON Schema with `x-scope` per field; `owners` = the types whose parts declare the collection |
| GET | `/v1/datasets` | — | `{datasets: [{name, schema}]}` | tech-space system datasets |
| POST | `/v1/spaces/:spaceId/search` | `{query, scopes?, limit?, mode?, require?, exclude?}` | `{hits, mode, vectorStatus}` | local index, not an SDK method; `409 index.disabled`, `400 index.no_embedder`, `503 index.embedder_unavailable`, `400 search.bad_mode`, `400 search.bad_scope` |

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
| POST | `/v1/spaces/:spaceId/bundles` | `{id, name?, rootTypes?, rootProperties?, derived?, parts?}` | `{bundle, installed}` | adopt-or-install; `parts` declares the root's modules and datasets; `409 bundle.not_ready`, `400 type.not_found`, `400 property.format_violation` |
| GET | `/v1/spaces/:spaceId/bundles` | — | `{bundles: [Bundle]}` | |
| GET | `/v1/spaces/:spaceId/bundles/:bundleId` | — | `Bundle` | `404 bundle.not_found` |
| POST | `/v1/spaces/:spaceId/bundles/:bundleId/resolve` | `{loserRootId}` | 204 | `409 bundle.loser_not_ready`, `409 bundle.not_loser` |
| POST | `/v1/spaces/:spaceId/bundles/:bundleId/children` | `{seed, types?}` | `{objectId}` | deterministic child; `409 bundle.not_ready` |

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/bundles \
  -d '{"id":"general-chat/v1","name":"General","derived":true,"parts":[{"key":"chat","datasets":[{"module":"chat","shared":true}]}]}'
```

Full semantics in [Bundles](../collaboration/bundles.html).

## Objects

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces/:spaceId/objects` | `{types?, initialProperties?, nav?}` | 201 `{id, …}` | closed vocabulary (`400 request.unknown_field`); `nav.*` auto-stamped |
| POST | `/v1/spaces/:spaceId/objects/query` | snapshot body | `{records, total?, hasNext?}` | cross-object `objects` collection |
| POST | `/v1/spaces/:spaceId/objects/query/subscribe` | snapshot body | SSE | |
| POST | `/v1/spaces/:spaceId/objects/aggregate` | `{pipeline, groupLimit?, accumArrayLimit?, memoryLimitBytes?, explain?}` | `{records}` \| `{plan}` | `400 aggregate.bad_pipeline`, `400 aggregate.limit_exceeded` |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId` | — | 204 | tombstones the row, then tears down the tree; `404 sdk.not_found` |
| GET | `/v1/spaces/:spaceId/objects/:objectId/backlinks` | — | `{backlinks: [{objectId, typeId, propId}]}` | reverse lookup over `links` properties; not an SDK method; unknown id ⇒ `[]` |

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects \
  -d '{"types":["'$PAGE'"],"initialProperties":{"any":{"name":"Dune"}}}'
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/query \
  -d '{"filter":{"any.types":"'$PAGE'"},"sort":["-modifiedAt"],"limit":20}'
```

Every `objects` row carries derived `author`, `createdAt`, `spaceId`, `modifiedAt`, `modifiedBy` (instants as `{"$date": …}`; `modifiedBy` is the identity that signed the change `modifiedAt` names). See [Objects](../database/objects.html) and [System fields](../database/system-fields.html).

### Editor (blocks and markdown)

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `…/objects/:objectId/editor/:collection/markdown` | — | markdown text | render transform over the editor collection (`editor_blocks` or `<typeId>_<key>`); `404 dataset.not_found` off-catalog, `400 dataset.not_declared` when no carried type declares it |
| PUT | `…/objects/:objectId/editor/:collection/markdown` | `{content}` | `{inserted, updated, deleted, unchanged}` | parse → diff → per-block ops |
| PATCH | `…/objects/:objectId/editor/:collection/markdown` | `{edits: [{oldText, newText, replaceAll?}]}` | PUT shape | all-or-nothing; `400 markdown.no_match`, `markdown.ambiguous_match`, `markdown.overlapping_edits` |
| POST | `…/objects/:objectId/editor/:collection/markdown/append` | `{content}` | PUT shape (`inserted` only) | O(fragment), no diff |
| POST | `…/objects/:objectId/editor/:collection/blocks` | `{type, style?, text?, nav?}` | 201 write result | `recordIds[0]` is the block id |
| PATCH | `…/objects/:objectId/editor/:collection/blocks/:blockId` | `{set: {"dotted.path": v}, unset: [path]}` | write result | required fields cannot be unset |
| DELETE | `…/objects/:objectId/editor/:collection/blocks/:blockId` | — | write result | no cascade to children |

Reads: `POST /v1/spaces/:spaceId/query` with `{"objectId", "dataset": "editor_blocks", "sort": ["nav.pos"]}`. See [Editor](../types/editor.html) and [Markdown import/export](../database/markdown-import-export.html).

## Data plane

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `/v1/spaces/:spaceId/query` | snapshot body + `objectId`, `dataset` | `{records, total?, hasNext?}` | per-object dataset; `404 object.not_found` |
| POST | `/v1/spaces/:spaceId/query/subscribe` | same + `mailboxCapacity?`, `driftBudgetPercent?` | SSE | frames in [Events](events.html) |
| POST | `/v1/spaces/:spaceId/aggregate` | `{objectId, dataset, pipeline, …}` | `{records}` \| `{plan}` | snapshot-only |
| POST | `/v1/spaces/:spaceId/modify` | `{objectId, dataset, records: [{id, upsert?, ops}], traceIds?, scope?}` | write result | `scope: "local"` for device-local fields only |
| POST | `/v1/spaces/:spaceId/upsert` | `{objectId, dataset, records: [{id, fields}], pageSize?, traceIds?}` | `{pages, created, updated, skipped, rejections}` | idempotent; `400 upsert.requires_user_ids`, `400 dataset.unknown`, `400 dataset.not_declared` |
| POST | `/v1/spaces/:spaceId/delete-records` | `{objectId, dataset, recordIds}` | write result | |

Snapshot body (closed field set — unknown keys `400 request.unknown_field`):

```json
{ "objectId": "obj", "dataset": "chat_messages",
  "filter": {"unread": true}, "sort": ["-_ver.id"],
  "limit": 50, "offset": 0, "includeTotal": true }
```

Filter faults: `400 filter.unknown_operator`, `400 filter.invalid`. A serialized-nil `objectId` is `400 object.id_required`. Grammar in [Reading data](../database/reading-data.html); pipelines in [Aggregation](../database/aggregation.html).

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

## Types and properties

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| GET | `/v1/spaces/:spaceId/types` | `includeHidden?` | `{types}` | built-ins `any`, `spaceIndex`, `type` first, then registered (`nav`, and the hidden `dataview` / `page` / `miniapp` / `bin`), then user types; hidden types (bundle roots and hidden built-ins) only with `includeHidden=true` |
| POST | `/v1/spaces/:spaceId/types` | `{name?, description?, iconCid?, xKey}` | 201 `TypeInfo` | `400 type.xkey_required`, `409 type.xkey_conflict` |
| GET | `/v1/spaces/:spaceId/types/:typeId` | — | `TypeInfo` | `404 type.not_found` |
| DELETE | `/v1/spaces/:spaceId/types/:typeId` | — | — | `501 sdk.not_implemented` |
| GET | `…/types/:typeId/properties` | — | `{properties: [PropertyDef]}` | `404 type.not_found`; `200 []` means "no properties yet" |
| POST | `…/types/:typeId/properties` | `{name, kind?, xKey?, format?, meta?, scope?}` | 201 | `400 property.format_invalid`, `400 type.registered` |
| PATCH | `…/types/:typeId/properties/:propId` | `{set, unset}` | 204 | `400 property.immutable`, `400 property.format_invalid`, `404 sdk.not_found` |
| DELETE | `…/types/:typeId/properties/:propId` | — | 204 | tombstone; values not cleaned up |
| PATCH | `…/types/:typeId` | `{name?, description?, iconCid?, weight?, layout?, hidden?, meta?}` | 204 | rendering slice, the hidden flag and the per-key meta bag (`null` unsets a key); `400 type.registered` |
| GET | `…/types/:typeId/parts` | — | `{parts: [{id, key, name?, icon?, pos?, hidden?, ui?, uses?, datasets: [DatasetDef]}]}` | |
| POST | `…/types/:typeId/parts` | `{key, name?, icon?, pos?, hidden?, ui?, uses?, datasets?: [dataset draft]}` | 201 `{partId}` | one change; `409 dataset.key_conflict`, `400 dataset.module_unknown`, `400 dataset.shared_conflict`, `409 dataset.module_owned` |
| PATCH | `…/types/:typeId/parts/:partId` | `{set, unset}` | 204 | `name`, `icon`, `pos`, `hidden`, `ui`, `uses`; `400 dataset.immutable` |
| DELETE | `…/types/:typeId/parts/:partId` | — | 204 | removes the part and its datasets |
| POST | `…/types/:typeId/parts/:partId/datasets` | `{key?, module?, shared?, displayName?, idRule?, deleteBy?, search?, fields, …}` | 201 `{datasetDefId, collection}` | collection = `<typeId>_<key>` (or the module's canonical when shared); `409 dataset.key_conflict`, `400 dataset.decl_invalid` |
| GET | `…/types/:typeId/datasets` | — | `{datasets: [DatasetDef]}` | flat compiled view; each carries `collection`, `module`, `shared`, `partId` |
| PATCH | `…/types/:typeId/datasets/:defId` | `{set, unset}` | 204 | display leaves only; `400 dataset.immutable` |
| DELETE | `…/types/:typeId/datasets/:defId` | — | 204 | |
| POST | `…/types/:typeId/datasets/:defId/fields` | field def | 201 `{fieldDefId}` | never `required` |
| DELETE | `…/types/:typeId/datasets/:defId/fields/:fieldId` | — | 204 | |
| GET | `/v1/spaces/:spaceId/properties/:objectId` | — | `{record}` | raw row |
| POST | `/v1/spaces/:spaceId/properties/:objectId/set/:typeId` | `{patch: {propId: value}}` | write result | routes by the props' declared scope; `400 property.format_violation`, `property.kind_mismatch`, `property.not_found` |
| POST | `…/properties/:objectId/attach/:typeId` | — | — | `501 sdk.not_implemented` — bind types at create |
| POST | `…/properties/:objectId/detach/:typeId` | — | — | `501 sdk.not_implemented` |

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types -d '{"name":"Book","xKey":"book"}'
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types/$T/properties \
  -d '{"name":"Rating","kind":"number","xKey":"rating"}'
```

Values are keyed by `propId`, never `xKey`. See [Types and properties](../database/types-and-properties.html) and [Runtime datasets](../database/runtime-datasets.html).

## Chat

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `…/objects/:objectId/chat/messages` | `{text?, replyToMessageId?, agent?, attachments?}` | 201 write result | `400 chat.text_required`, `400 chat.agent_invalid`; text ≤ 32 KiB |
| PATCH | `…/chat/messages/:msgId` | `{text}` | write result | `403 chat.not_author`, `404 chat.not_found` |
| DELETE | `…/chat/messages/:msgId` | — | write result | own only |
| POST | `…/chat/messages/:msgId/reactions/:emoji` | — | write result | toggle |
| POST | `…/chat/read-all` | — | 204 | |
| POST | `…/chat/messages/:msgId/read` | — | 204 | marks this and everything before it |
| POST | `…/chat/messages/:msgId/reactions-read` | — | 204 | |

Reads: `POST /v1/spaces/:spaceId/query` with `dataset: "chat_messages"`, `sort: ["-_ver.id"]`, a `limit`. See [Chat](../types/chat.html).

## Files

| Method | Path | Body/params | Returns | Notes |
|---|---|---|---|---|
| POST | `…/objects/:objectId/files` | raw body; `Content-Type`; `?name&variant&variantOf` | 201 `FileInfo` | body-limit exempt; `400 file.variant_invalid` |
| POST | `…/objects/:objectId/files/query` | snapshot body | `{records}` | cleartext payload rows; `404 file.not_found` before first attach |
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
| GET | `/v1/files/cache` | — | size | all spaces |
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
| GET | `/v1/spaces/:spaceId/members/requests` | — | pending join requests | rows carry `requestRecordId` |
| GET | `/v1/spaces/:spaceId/members/subscribe` | — | SSE `member` frames | |
| GET | `/v1/spaces/:spaceId/members/:identity` | — | `Member` | |
| POST | `/v1/spaces/:spaceId/invites` | — | 201 `{spaceId, inviteToken}` | replaces any prior invite |
| GET | `/v1/spaces/:spaceId/invites` | — | `{invites: [{recordId, permission, inviteToken?}]}` | token only on the minting account's devices |
| GET | `/v1/spaces/:spaceId/invites/:recordId` | — | one invite | `404 invite.not_found` |
| DELETE | `/v1/spaces/:spaceId/invites` | — | 204 | revoke all |
| DELETE | `/v1/spaces/:spaceId/invites/:recordId` | — | 204 | |
| POST | `/v1/spaces/:spaceId/guest-key` | — | `{spaceId, inviteToken}` | public read-only token; owner only, idempotent |
| DELETE | `/v1/spaces/:spaceId/guest-key` | — | 204 | rotates the read key |
| POST | `/v1/spaces/:spaceId/acl/accept` | `{requestRecordId, permission}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/decline` | `{identity}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/permissions` | `{changes: [{identity, permission}]}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/remove` | `{identities}` | 204 | rotates the read key |
| POST | `/v1/spaces/:spaceId/acl/add` | `{accounts: [{identity, permission, metadata?}]}` | 204 | added accounts see an `invite_pending` row |
| POST | `/v1/spaces/:spaceId/acl/ownership` | `{newOwner, oldOwnerPerm}` | 204 | |
| POST | `/v1/spaces/:spaceId/acl/self-remove` | — | 204 | |
| POST | `/v1/spaces/:spaceId/acl/cancel-join` | — | 204 | |
| POST | `/v1/spaces/:spaceId/acl/stop-sharing` | — | 204 | drops everyone |

Permissions: `none`, `reader`, `guest`, `writer`, `admin`, `owner`. Member statuses: `unknown`, `joining`, `active`, `removed`, `declined`, `removing`, `canceled`. Guests writing get `403 space.read_only`. See [Collaboration](../collaboration/index.html).

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
| PUT | `/v1/devices/me` | `{name?, apps?}` | — | `"apps": {"slug": null}` uninstalls; `409 device.pruned` |
| POST | `/v1/devices/activate` | `{app}` | — | claim the active role on this device |
| DELETE | `/v1/devices/:peerId` | — | 204 | sticky tombstone; `404 device.not_found`, `400 device.self_delete` |
| POST | `/v1/push/token` | `{platform: ios\|android, token}` | 204 | `409 push.disabled` without a push node |
| GET | `/v1/push/token` | — | `{registered, platform}` | local state |
| DELETE | `/v1/push/token` | — | 204 | |
| GET | `/v1/push/subscriptions` | — | `{subscriptions: [{spaceKey, topic}]}` | |

See [Devices](../auth/devices.html) and [Push](../notifications/push.html).

> **Note.** POSTs are not idempotent — each one produces a new DAG change. The single exception is `/upsert`, where the caller-supplied record id is the idempotency key. Pagination is offset-based on every list.
