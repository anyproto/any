---
title: Errors
description: The one error envelope every non-2xx response uses, the HTTP status rules, and the full dotted error-code namespace.
order: 40
---
# Errors

Every error response from the any server — whatever the status code — has the same body, and every code is a dotted identifier you can switch on. Messages are safe to display; `details` carries only the identifiers you asked about, never secrets or internal paths.

## Shape

```json
{
  "error": {
    "code":    "space.not_found",
    "message": "space spc_xyz not found",
    "details": { "spaceId": "spc_xyz" }
  }
}
```

| Field | Meaning |
|---|---|
| `code` | machine-readable handle, `<section>.<fault>` |
| `message` | human-readable, display-safe |
| `details` | optional structured context — field names, ids, counts |

## HTTP status codes

| Status | When |
|---|---|
| 400 | validation error on body, path params or query string |
| 401 | `auth.required` — the server has no account booted yet; `access.signature_rejected` — the invite service refused this account's signature |
| 403 | ownership gates (`auth.not_managed`, `shutdown.not_managed`, `control.forbidden`), author-only data rules (`chat.not_author`), guest and reader writes (`space.read_only`), ACL roles (`acl.forbidden`), foreign topic namespaces |
| 404 | target not found (space, object, type, collection, record, version, route) |
| 405 | the surface is not available on the tech space (`space.unsupported`) |
| 409 | conflict — duplicate, precondition failed, not converged yet, feature disabled |
| 410 | the target existed and is gone for good (`object.deleted`, `record.deleted`) |
| 413 | history view too large; request body over the 1 MB limit |
| 429 | the invite service is throttling this client (`access.rate_limited`) |
| 500 | unexpected engine or server failure (`internal`); the stack goes to the server log only |
| 501 | route registered for an SDK surface that is not implemented yet |
| 502 | the invite service is unreachable or answered unexpectedly (`access.unavailable`) |
| 503 | not ready — request cancelled, shutting down, or the embedder is unreachable |

Panics are converted to `500 internal` with a generic message.

## CLI exit codes

`0` success · `1` user error or 4xx · `2` server error (5xx) · `3` cannot reach the server. See [CLI](cli.html).

## Code namespace

### Request, auth, account

| Code | Status | Meaning |
|---|---|---|
| `request.bad_json` | 400 | body is not valid JSON |
| `request.schema` | 400 | JSON shape does not match the endpoint schema |
| `request.missing_field` | 400 | a required field is absent — object create without `type`, a bundle body that declares nothing and has no `rootType` |
| `request.unknown_field` | 400 | a top-level key outside the accepted set (`details.fields`, `details.accepted`) |
| `request.invalid_field` | 400 | a field value the endpoint refuses (bad identity, dataset outside an allowlist, …; `details.field`) |
| `request.bad` | 400 | a malformed request the framework or a query parameter check rejected |
| `request.not_found` | 404 | no route matches the path |
| `request.method_not_allowed` | 405 | the path exists, the method does not |
| `request.too_large` | 413 | body over the 1 MB limit |
| `auth.required` | 401 | server unauthorized — `POST /v1/auth` first |
| `auth.already_authorized` | 409 | `{}` while an account runs — a fresh account is never minted in place |
| `auth.not_managed` | 403 | standalone server: a different account on `POST`, or `DELETE` — switching means a restart |
| `auth.account_mismatch` | 409 | managed server runs another account; pass `replace: true` to switch |
| `auth.bad_mnemonic` | 400 | BIP-39 validation failed |
| `auth.mnemonic_mismatch` | 409 | wallet on disk disagrees with the phrase/index |
| `auth.account_not_found` | 404 | `accountId` has no local wallet |
| `auth.account_in_use` | 409 | another process holds the account's instance lock (`details.pid` when known) |
| `auth.passkey_required` | 400 | encrypted wallet; no or wrong passkey in the configured env var |
| `auth.device_key_corrupt` | 500 | managed: the account's cached `device.key` is unreadable; never re-minted silently — remove it to mint a new device identity |
| `control.forbidden` | 403 | managed server: control token (`X-Any-Control-Token`) missing or wrong |
| `shutdown.not_managed` | 403 | standalone server refuses `POST /v1/shutdown`; use `any stop` or a signal |
| `identity.not_found` | 404 | the identities directory has never seen this identity |
| `access.disabled` | 409 | `POST /v1/account/access-code` with no `access.redeemUrl` configured |
| `access.request_rejected` | 400 | the invite service refused the request (`details.code`) |
| `access.signature_rejected` | 401 | the invite service could not verify this account's signature |
| `access.code_not_found` | 404 | unknown invite code |
| `access.code_unusable` | 409 | disabled, expired or exhausted code (`details.code`) |
| `access.rate_limited` | 429 | the invite service is throttling this client |
| `access.unavailable` | 502 | the invite service is unreachable or answered unexpectedly |

### Spaces, members, invites

| Code | Status | Meaning |
|---|---|---|
| `space.not_found` | 404 | unknown space id |
| `space.not_accepted` | 409 | join pending approval; space not materialized |
| `space.deleted` | 409 | the row is a tombstone; a declined or withdrawn join is re-requestable via `POST /v1/spaces/join` |
| `space.join_not_pending` | 409 | `POST …/acl/cancel-join` with nothing to withdraw: the row is not `joining`, or the owner already accepted (the row settles to `active` on its own) |
| `space.derived_unknown` | 404 | name outside the derived-spaces registry |
| `space.derived_undeletable` | 409 | derived spaces are permanent |
| `space.not_invite_pending` | 409 | invite accept on a row not awaiting approval |
| `space.already_member` | 409 | a guest token for a space this account already tracks as a member |
| `space.read_only` | 403 | a guest or reader tried to write |
| `space.unsupported` | 405 | the surface is not available on the tech space (object and type lifecycle, members, files, chat, editor, history, search, metadata, settings, non-bundle writes, catalog setup) |
| `members.not_found` | 404 | unknown member identity in this space |
| `acl.forbidden` | 403 | the caller's role does not allow this ACL operation |
| `acl.record_not_found` | 404 | the ACL record named (a join request) does not exist |
| `invite.invalid` | 400 | invite token malformed or unrecognized |
| `invite.not_found` | 404 | unknown invite record |
| `invite.duplicate` | 409 | an invite already exists for this space |
| `guest_key.not_found` | 404 | revoke with no active guest key |

### Objects, bundles, catalog

| Code | Status | Meaning |
|---|---|---|
| `object.not_found` | 404 | object unknown or deleted in this space |
| `object.deleted` | 410 | `GET …/objects/:objectId` on a deleted object — distinct from never-existed |
| `object.derived_undeletable` | 409 | `DELETE` on a derived object (a bundle root installed with `derived: true`, such as the general chat) |
| `object.id_required` | 400 | the object id is a serialized nil (`"None"`, `"null"`, `"undefined"`) |
| `record.deleted` | 410 | a write addressed a tombstoned record; the id is burned for good |
| `bundle.not_found` | 404 | no live install for the bundle id in this space |
| `bundle.not_ready` | 409 | registry not converged, or the winner's tree is not local yet — retry |
| `bundle.loser_not_ready` | 409 | losing root not fully synced / grace period not elapsed — retry |
| `bundle.not_loser` | 409 | resolve targeted the winner or an unclaimed root |
| `bundle.reserved` | 409 | a client ensure with an id under the server's `system:` prefix |
| `catalog.not_found` | 404 | no usecase with that id in the catalog (`details.usecaseId`) |

### Datasets, filters, upsert

| Code | Status | Meaning |
|---|---|---|
| `dataset.unknown` | 400 | a record write names a storage collection the space does not serve as a records dataset (a module's own is never upsertable); a read of an unknown dataset answers `200 {"records": []}` |
| `dataset.not_declared` | 400 | a write into a storage collection the object's type does not declare — set the declaring type first (collections declare no datasets) |
| `dataset.not_found` | 404 | the editor route's `:collection` is not an editor storage collection in this space |
| `dataset.validation` | 400 | schema or handler rejected the ops |
| `dataset.key_conflict` | 409 | a part or dataset with this key already exists on the type (`details.key`) |
| `dataset.shared_conflict` | 400 | `shared` on a module without a canonical storage collection, a shared key that is not the canonical name, or a namespaced dataset on a shared-only module |
| `dataset.module_unknown` | 400 | the dataset names a module the server does not compile in |
| `dataset.module_owned` | 409 | fields declared on a module-served dataset |
| `dataset.module_reserved` | 400 | a part, dataset or bundle draft names a module reserved to the server's own installs (`chat`) |
| `dataset.decl_invalid` | 400 | malformed part or dataset declaration |
| `dataset.immutable` | 400 | PATCH of a pinned part, definition or field path (`details.path`) |
| `upsert.requires_user_ids` | 400 | dataset not declared `idRule: user` |
| `upsert.immutable_field` / `upsert.not_author` / `upsert.record_deleted` / `upsert.rejected` | 200 | per-record codes inside `rejections[]` — never HTTP errors |
| `filter.unknown_operator` | 400 | operator outside the grammar (`details.operator`, `details.path`) |
| `filter.invalid` | 400 | any other filter-grammar violation |
| `aggregate.bad_pipeline` | 400 | unparseable pipeline, unknown stage, or `$text`/vector outside the pushdown prefix |
| `aggregate.limit_exceeded` | 400 | a blocking-stage bound blew (`details.limit`: `group` \| `accumArray` \| `memory`) |

### Types, collections and properties

| Code | Status | Meaning |
|---|---|---|
| `type.not_found` | 404 | unknown typeId (400 inside a bundle ensure) |
| `type.not_a_type` | 400 | a `…/types` route, or `POST …/properties/:objectId/type/:typeId`, names a user **collection** (`details.collectionId`) — use the `…/collections` routes |
| `type.xkey_required` | 400 | a type or a collection created without an `xKey` |
| `type.xkey_conflict` | 409 | `xKey` collides with an existing type's or collection's xKey or id (`details.xKey`, `details.existingTypeId` or `details.existingCollectionId`) — the two surfaces share one handle namespace; also raised by a bundle or catalog install |
| `type.registered` | 400 | add/patch/remove a property, part or dataset, or PATCH the type, on a registered built-in type; also a column write on a registered built-in collection |
| `type.reserved_carrier` | 400 | object create, `POST …/type/:typeId` or an `any.type` op names a type only its own root may carry (the general-chat root; `details.typeId`) |
| `collection.not_found` | 404 | unknown collectionId (400 inside a bundle ensure) |
| `collection.not_a_collection` | 400 | a `…/collections` route, or `POST …/properties/:objectId/collections/:collectionId`, names a user **type** (`details.typeId`) |
| `collection.registered` | 400 | PATCH the metadata of a registered built-in collection (`miniapp`, `bin`) |
| `membership.wrong_slot` | 400 | a raw `…/modify` write put a known collection id in `any.type` or a known type id in `any.collections` |
| `membership.type_required` | 400 | a raw write cleared `any.type`; every object has exactly one type |
| `property.not_found` | 404 | unknown propId, or a value write names a property the owner does not declare |
| `property.kind_mismatch` | 400 | write violated the immutable kind |
| `property.xkey_conflict` | 409 | another property of the same type or collection holds this `xKey` (`details.xKey`, `details.existingPropId`) |
| `property.immutable` | 400 | PATCH of a pinned path — `kind`, `scope`, `items`, `properties` (`details.path`) |
| `property.format_invalid` | 400 | descriptor vocabulary problem on create/PATCH: slug does not fit the kind, reserved slug or key (`tags`, `validate`, `compute`), unparseable `relation.filter` |
| `property.format_violation` | 400 | a value violated its descriptor's current slug (`details.propId`, `format` = the slug, `reason`) |

### Chat, editor, history

| Code | Status | Meaning |
|---|---|---|
| `chat.text_required` | 400 | no text, attachments or control |
| `chat.text_too_long` | 400 | text over 32 KiB (`details.max_bytes`, `got_bytes`) |
| `chat.reply_id_invalid` | 400 | `replyToMessageId` over 256 bytes |
| `chat.agent_invalid` | 400 | malformed `agent` group |
| `chat.attachments_invalid` | 400 | malformed `attachments` map |
| `chat.context_invalid` | 400 | malformed `context` |
| `chat.control_invalid` | 400 | malformed `control` |
| `chat.emoji_invalid` | 400 | reaction emoji empty or over 64 bytes |
| `chat.not_author` | 403 | edit/delete of someone else's message |
| `chat.not_found` | 404 | unknown message id |
| `blocks.type_required` | 400 | block create without a `type` |
| `blocks.not_found` | 404 | unknown block id |
| `markdown.no_match` | 400 | `edits[i].oldText` not in the current rendering (`details.editIndex`) |
| `markdown.ambiguous_match` | 400 | `oldText` occurs more than once without `replaceAll` (`details.occurrences`) |
| `markdown.overlapping_edits` | 400 | two edits matched intersecting text (`details.editIndices`) |
| `history.version_not_found` | 404 | unknown version, or not in this object's DAG |
| `history.view_too_large` | 413 | narrow with `dataset` / `recordId` |
| `history.truncated` | 404 | reserved — the causal past is not on this device |

### Files, devices, push

| Code | Status | Meaning |
|---|---|---|
| `file.not_found` | 404 | unknown fileId/objectId, or files query before the first attach |
| `file.not_durable` | 409 | offload refused: local bytes are the only copy |
| `file.not_available` | 409 | content not local and not fetchable yet — retry |
| `file.variant_invalid` | 400 | variant/variantOf pairing broken |
| `device.not_found` | 404 | unknown peer id (or already pruned) |
| `device.self_delete` | 400 | refusing to prune this server's own row |
| `device.pruned` | 409 | self-row write absorbed by a sticky tombstone |
| `push.disabled` | 409 | no push node configured |

### Local store

| Code | Status | Meaning |
|---|---|---|
| `local.disabled` | 409 | `local.enabled: false`; existing collections stay on disk |
| `local.collection_not_found` | 404 | local storage collection not ensured yet, or already dropped |
| `local.doc_not_found` | 404 | get, or update without `upsert`, of an unknown id |
| `local.duplicate_id` | 409 | insert of an id that already exists |
| `local.unique_violation` | 409 | a unique index rejected the write |
| `local.too_many_docs` | 400 | insert/upsert over 1000 docs in one request (`details.max`, `got`) |
| `local.bad_name` | 400 | scope, spaceId or name failed validation |
| `local.bad_index` | 400 | an index was rejected (same name, different definition; invalid name) |
| `local.bad_filter` / `local.bad_sort` / `local.bad_modifier` | 400 | the filter, sort key or modifier did not parse |
| `local.bad_pipeline` | 400 | unparseable pipeline, a sink into the aggregated storage collection, a sink result without `id`, `$lookup` from another one |
| `local.bad_sink_target` | 400 | `$out` / `$merge into` / `$lookup from` names a storage collection outside the local store |
| `local.limit_exceeded` | 400 | a blocking-stage bound blew (`details.limit`) |

### Events, processes, search

| Code | Status | Meaning |
|---|---|---|
| `events.payload_too_large` | 400 | `data` exceeds 64 KiB |
| `events.no_read_key` | 409 | network-scope event in a space without a read key |
| `events.too_many_patterns` | 409 | the space's 100-pattern pub/sub budget is exhausted |
| `events.topic_not_owned` | 403 | type maps into another account's self-owned topic namespace |
| `process.not_found` | 404 | progress/finish/cancel on a process not live under this account |
| `process.ambiguous` | 409 | several publishers run the same id (`details.identities`) |
| `index.disabled` | 409 | search index turned off (`index.enabled: false`); also the link and backlink reads |
| `index.no_embedder` | 400 | `mode: vector` without an embedder |
| `index.embedder_unavailable` | 503 | embedder configured but unreachable or over the query budget — retryable |
| `index.terms_unsupported` | 409 | `require` / `exclude` on a build without the full-text index |
| `search.bad_mode` | 400 | mode not `hybrid` \| `fts` \| `vector` |
| `search.bad_scope` | 400 | scope not a valid slug (`[a-z0-9_-]`, max 64) |

### Engine and server

| Code | Status | Meaning |
|---|---|---|
| `sdk.not_implemented` | 501 | placeholder route (sync-status peers, type delete, collection delete) |
| `sdk.not_found` | 404 | the engine reports the target gone (deleted object, unknown property or definition) |
| `sdk.crdt_version_newer` | 409 | the account's data was written by a newer release (`details.stored` > `details.supported`): booting it refuses, a running server turns read-only until upgraded (`GET /v1/health` → `crdtVersion`) |
| `server.unavailable` | 503 | request cancelled / server shutting down |
| `server.internal` | 500 | the embedded usecase catalog failed to compile |
| `internal` | 500 | catch-all |

> **Note.** `/modify` and `/aggregate` bodies hand their op and stage vocabularies straight to the engine, so unknown top-level keys there are dropped rather than rejected. Every other strict endpoint is listed with `additionalProperties: false` in `GET /v1/openapi.json`.

`make catalog-validate` reports catalog problems offline with their own codes — `catalog.bad_yaml`, `unknown_field`, `bad_id`, `duplicate`, `missing`, `bad_field`, `unknown_usecase`, `cycle`, `broken_link`, `bad_miniapp` — one `<source>: <path>: <code>: <message>` line each; they never appear on the wire.
