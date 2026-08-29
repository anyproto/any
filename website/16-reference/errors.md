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
| 401 | `auth.required` only — the server itself has no account booted yet |
| 403 | author-only data rules (`chat.not_author`), guest writes (`space.read_only`), foreign topic namespaces |
| 404 | target not found (space, object, type, record, version) |
| 409 | conflict — duplicate, precondition failed, feature disabled |
| 413 | history view too large |
| 500 | unexpected engine or server failure (`internal`); the stack goes to the server log only |
| 501 | route registered for an SDK surface that is not implemented yet |
| 503 | not ready — shutting down, or the embedder is unreachable |

Panics are converted to `500 internal` with a generic message.

## CLI exit codes

`0` success · `1` user error or 4xx · `2` server error (5xx) · `3` cannot reach the server. See [CLI](cli.html).

## Code namespace

### Request and auth

| Code | Status | Meaning |
|---|---|---|
| `request.bad_json` | 400 | body is not valid JSON |
| `request.schema` | 400 | JSON shape does not match the endpoint schema |
| `request.missing_field` | 400 | a required field is absent |
| `request.unknown_field` | 400 | a top-level key outside the accepted set (`details.fields`, `details.accepted`) |
| `request.invalid_field` | 400 | a field value the endpoint refuses (bad identity, dataset outside an allowlist, …) |
| `auth.required` | 401 | server unauthorized — `POST /v1/auth` first |
| `auth.already_authorized` | 409 | engine already booted; restart to switch accounts |
| `auth.bad_mnemonic` | 400 | BIP-39 validation failed |
| `auth.mnemonic_mismatch` | 409 | wallet on disk disagrees with the phrase/index |
| `auth.account_not_found` | 404 | `accountId` has no local wallet |
| `auth.account_in_use` | 409 | another process holds the account's instance lock |
| `auth.passkey_required` | 400 | encrypted wallet, no passkey provided |
| `auth.passkey_wrong` | 400 | passkey rejected |

### Spaces, invites, objects

| Code | Status | Meaning |
|---|---|---|
| `space.not_found` | 404 | unknown space id |
| `space.exists` | 409 | create conflict |
| `space.not_joined` | — | operation requires membership |
| `space.not_accepted` | 409 | join pending approval; space not materialized |
| `space.deleted` | 409 | the row is a tombstone |
| `space.derived_unknown` | 404 | name outside the derived-spaces registry |
| `space.derived_undeletable` | 409 | derived spaces are permanent |
| `space.not_invite_pending` | 409 | invite accept on a row not awaiting approval |
| `space.read_only` | 403 | a guest tried to write |
| `invite.invalid` | 400 | invite token malformed or unrecognized |
| `invite.not_found` | 404 | unknown invite record |
| `object.not_found` | 404 | object unknown or deleted in this space |
| `object.id_required` | 400 | the object id is a serialized nil (`"None"`, `"null"`, `"undefined"`) |
| `object.type_required` | 400 | a type is required |
| `bundle.not_found` | 404 | unknown bundle id |
| `bundle.not_ready` | 409 | registry not converged, or the winner's tree is not local yet — retry |
| `bundle.loser_not_ready` | 409 | losing root not fully synced / grace period not elapsed — retry |
| `bundle.not_loser` | 409 | resolve targeted the winner or an unclaimed root |

### Datasets, filters, upsert

| Code | Status | Meaning |
|---|---|---|
| `dataset.unknown` | 400 | no handler or runtime definition registered |
| `dataset.validation` | 400 | schema or handler rejected the ops |
| `dataset.name_conflict` | 409 | name already used in the space (`details.name`) |
| `dataset.decl_invalid` | 400 | malformed dataset declaration |
| `dataset.immutable` | 400 | PATCH of a pinned definition path (`details.path`) |
| `upsert.requires_user_ids` | 400 | dataset not declared `idRule: user` |
| `upsert.immutable_field` / `upsert.not_author` / `upsert.record_deleted` / `upsert.rejected` | 200 | per-record codes inside `rejections[]` — never HTTP errors |
| `filter.unknown_operator` | 400 | operator outside the grammar (`details.operator`, `details.path`) |
| `filter.invalid` | 400 | any other filter-grammar violation |
| `aggregate.bad_pipeline` | 400 | unparseable pipeline, unknown stage, or `$text`/vector outside the pushdown prefix |
| `aggregate.limit_exceeded` | 400 | a blocking-stage bound blew (`details.limit`: `group` \| `accumArray` \| `memory`) |

### Types and properties

| Code | Status | Meaning |
|---|---|---|
| `type.not_found` | 404 | unknown typeId |
| `type.xkey_required` | 400 | create without an `xKey` |
| `type.xkey_conflict` | 409 | `xKey` collides with an existing type's xKey or id (`details.xKey`, `details.existingTypeId`) |
| `type.registered` | 400 | add/patch/remove on a registered built-in type |
| `property.not_found` | 404 | unknown propId |
| `property.kind_mismatch` | 400 | write violated the immutable kind |
| `property.immutable` | 400 | PATCH of a pinned path (`details.path`) |
| `property.format_invalid` | 400 | bad format leaf on create/PATCH |
| `property.format_violation` | 400 | a value violated its declared format (`details.propId`, `format`, `reason`) |

### Chat, editor, history

| Code | Status | Meaning |
|---|---|---|
| `chat.text_required` | 400 | neither text nor attachments |
| `chat.agent_invalid` | 400 | malformed `agent` group |
| `chat.not_author` | 403 | edit/delete of someone else's message |
| `chat.not_found` | 404 | unknown message id |
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

### Events, processes, search

| Code | Status | Meaning |
|---|---|---|
| `events.payload_too_large` | 400 | `data` exceeds 64 KiB |
| `events.no_read_key` | 409 | network-scope event in a space without a read key |
| `events.too_many_patterns` | 409 | the space's 100-pattern pub/sub budget is exhausted |
| `events.topic_not_owned` | 403 | type maps into another account's self-owned topic namespace |
| `process.not_found` | 404 | progress/finish/cancel on a process not live under this account |
| `process.ambiguous` | 409 | several publishers run the same id (`details.identities`) |
| `index.disabled` | 409 | search index turned off (`index.enabled: false`) |
| `index.no_embedder` | 400 | `mode: vector` without an embedder |
| `index.embedder_unavailable` | 503 | embedder configured but unreachable — retryable |
| `search.bad_mode` | 400 | mode not `hybrid` \| `fts` \| `vector` |
| `search.bad_scope` | 400 | scope not a valid slug (`[a-z0-9_-]`, max 64) |

### Engine and server

| Code | Status | Meaning |
|---|---|---|
| `sdk.not_implemented` | 501 | placeholder route (sync-status peers, attach/detach type, type delete) |
| `sdk.not_found` | 404 | the engine reports the target gone (deleted object, unknown property or definition) |
| `server.unavailable` | 503 | request cancelled / server shutting down |
| `internal` | 500 | catch-all |

> **Note.** `/modify` and `/aggregate` bodies hand their op and stage vocabularies straight to the engine, so unknown top-level keys there are dropped rather than rejected. Every other strict endpoint is listed with `additionalProperties: false` in `GET /v1/openapi.json`.
