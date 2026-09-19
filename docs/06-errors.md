# Errors

## Shape

Every error response — regardless of status code — has the same body:

```json
{
  "error": {
    "code":    "space.not_found",
    "message": "space spc_xyz not found",
    "details": { /* optional, structured key-value context */ }
  }
}
```

- `code` — machine-readable handle.
- `message` — human-readable. Safe to display directly.
- `details` — optional, structured context (field names, ids, counts).
  Never secrets or internal paths.

## HTTP status codes

| Status | When                                                           |
|--------|----------------------------------------------------------------|
| 400    | Validation error on request body / path params / query string  |
| 401    | `auth.required` — no account booted; `access.signature_rejected` from the invite service |
| 403    | Ownership gates and permission rules (below)                   |
| 404    | Target not found (space, object, type, record, route)          |
| 405    | Route not available on the tech space (`space.unsupported`), or wrong method for the route |
| 409    | Conflict (duplicate, precondition failed, state not ready)     |
| 410    | Deleted for good (`object.deleted`, `record.deleted`)          |
| 413    | Body over the 1 MiB cap (`request.too_large`; file attach is exempt), or `history.view_too_large` |
| 429    | `access.rate_limited`                                          |
| 500    | Internal error — unexpected SDK or server failure              |
| 501    | `sdk.not_implemented` — `DELETE …/types/:typeId`, `DELETE …/collections/:collectionId` and `GET …/sync-status/peers` |
| 502    | `access.unavailable`                                           |
| 503    | Request cancelled or the engine tearing down (`server.unavailable`); embedder unreachable (`index.embedder_unavailable`) |

No caller authentication in v1 (loopback is the trust boundary). The
single 401 the server raises itself is `auth.required` — no account is
booted yet (see `03-api.md` § Auth). 403 covers permission rules
(`chat.not_author`, `acl.forbidden`, `space.read_only`,
`events.topic_not_owned`) and the **ownership gates** of `02-server.md`
§ Modes: an operation the server's mode refuses (`auth.not_managed`,
`shutdown.not_managed`) or a managed control operation without the
host's token (`control.forbidden`). Those express ownership, not
authentication.

Handler errors and recovered panics log at `error` on the server
(panics with their stack). Clients receive the sanitized body only.

## Error code namespace

Dotted identifiers grouped by area:

```
request.bad_json                 # 400 — request body is not valid JSON; message names what failed to parse and the expected field set
request.schema                   # 400 — JSON shape doesn't match endpoint schema
request.missing_field            # 400 — required field absent
request.invalid_field            # 400 — a field is present but out of range / malformed (details.field names it)
request.unknown_field            # 400 — a top-level body key outside the endpoint's accepted set (details.fields, details.accepted); message enumerates the accepted fields and, where one exists, the right home for the value (e.g. object properties → initialProperties, type properties → POST …/types/:typeId/properties). The strict endpoints are the ones whose request schemas carry additionalProperties: false in /v1/openapi.json.
request.bad                      # 400 — rejected by the HTTP layer before a handler ran
request.not_found                # 404 — no such route
request.method_not_allowed       # 405 — the route exists, not with this method
request.too_large                # 413 — body over the 1 MiB cap

auth.required                    # 401 — server unauthorized; POST /v1/auth first
auth.already_authorized          # 409 — `{}` while an account runs; a fresh account is never minted in place
auth.not_managed                 # 403 — standalone server: a different account on POST, or DELETE (switching = restart)
auth.account_mismatch            # 409 — managed server runs another account; pass replace:true to switch
auth.bad_mnemonic                # 400 — BIP-39 validation failed
auth.mnemonic_mismatch           # 409 — wallet on disk disagrees with the phrase/index
auth.account_not_found           # 404 — accountId has no local wallet
auth.account_in_use              # 409 — another process holds the account's instance lock (details.pid)
auth.passkey_required            # 400 — wallet encrypted; no or wrong passkey in the configured env
auth.device_key_corrupt          # 500 — managed: the account's cached device.key is unreadable; never re-minted silently — remove it to mint a new device identity
auth.network_mismatch            # 409 — the account's data belongs to another any-sync network than the server's config (details.pinned, details.configured); one data root per network
auth.network_pin_corrupt         # 500 — the account's network.json is unreadable; never rewritten from config — remove it and start on the account's network
control.forbidden                # 403 — managed server: control token (X-Any-Control-Token) missing or wrong
shutdown.not_managed             # 403 — standalone server refuses POST /v1/shutdown; use `any stop` or a signal

space.not_found                  # 404
space.not_accepted               # 409 — join pending approval; space not materialized yet
space.deleted                    # 409 — space is deleted (row is a tombstone); 1-1s re-creatable via one-to-one start; a declined or withdrawn join re-requestable via POST /v1/spaces/join
space.already_member             # 409 — POST /v1/spaces/join for a space this account already tracks as a member
space.not_invite_pending         # 409 — invite accept/decline on a space not awaiting direct-add approval
space.join_not_pending           # 409 — POST …/acl/cancel-join with nothing to withdraw: the row is not joining, or the owner accepted first (the row settles to active on its own); a request gone with no membership behind it is settled by the call itself (204, row deleted)
space.read_only                  # 403 — synced write into a space this account cannot write (guest access or reader role)
space.derived_unknown            # 404 — POST /v1/spaces/derived/:name outside the embedded registry
space.derived_undeletable        # 409 — DELETE on a derived space; derived spaces are permanent
space.unsupported                # 405 — the route is not available on the tech space (allowed there: bundles, type reads and dataset declarations on bundle roots, records on bundle roots, GET space, sync-status, debug)

members.not_found                # 404 — the member a members / ACL operation names is unknown (e.g. GET …/members/:identity)
acl.forbidden                    # 403 — the ACL refuses the operation for this account's permissions
acl.record_not_found             # 404 — the ACL record the operation names does not exist
invite.invalid                   # 400 — invite token malformed or unrecognized
invite.duplicate                 # 409 — POST …/invites while an invite already exists
invite.not_found                 # 404 — GET …/invites/:recordId for an unknown record
guest_key.not_found              # 404 — revoke with no active guest key
identity.not_found               # 404 — GET /v1/identities/:identity for an identity not in the directory

bundle.not_found                 # 404 — no live install for the bundle id in this space
bundle.not_ready                 # 409 — the winner's tree (or the registry) has not reached this device yet, incl. a tree the SDK does not hold at all right after a join; retry
bundle.not_loser                 # 409 — resolve target is the current winner or was never claimed
bundle.loser_not_ready           # 409 — the losing root is still syncing / inside the grace window; the server keeps retrying
bundle.reserved                  # 409 — a client ensure with an id under the server's `system:` prefix (the embedded catalog's ids)
catalog.not_found                # 404 — GET /v1/catalog/:usecaseId or POST …/setup naming no usecase of the embedded catalog (details.usecaseId)

object.not_found                 # 404 — objectId unknown or deleted in this space (per-object query, editor, markdown, history …)
object.deleted                   # 410 — GET …/objects/:objectId on a deleted object (distinct from never-existed)
record.deleted                   # 410 — a write addressed a tombstoned record (a block, a message, a runtime record): the id is burned for good, never reused
object.derived_undeletable       # 409 — DELETE on a derived object (a bundle root installed with derived:true, e.g. the general chat); derived objects are permanent
object.id_required               # 400 — the object id in the path or body is a serialized nil ("None", "null", "undefined", …): the caller's id variable was unset; never a store lookup failure

dataset.unknown                  # 400 — a record write (modify / delete-records / upsert) names a storage collection the space does not serve as a records dataset (details.dataset): no part declares it, or it is a module's storage collection (chat_messages, editor_blocks — written through the module's routes, never upsertable). A read of an unknown dataset answers 200 {"records": []}
dataset.not_declared             # 400 — a write into a storage collection the object's type does not declare (details.dataset, details.objectId): the space serves it, but no part of the object's type owns it — set that type first (a definition object owns its own datasets); no write sets one; also a property value written under an owner the object does not have — neither its type nor a collection it is filed under (the message names the owner and the object's current type and collections)
dataset.not_found                # 404 — the :collection segment of an editor route names no editor dataset in this space (details.collection): neither editor_blocks nor a namespaced <typeId>_<key> instance a part declares with module "editor"
dataset.validation               # 400 — schema or handler rejected ops
dataset.key_conflict             # 409 — a part or dataset with this key already exists on the type (details.key); a second shared dataset of one module collides on the canonical key
dataset.shared_conflict          # 400 — shared on a module with no canonical storage collection (records), a shared key that is not the canonical name, or a namespaced dataset on a shared-only module (chat)
dataset.module_unknown           # 400 — the dataset names a module this server does not compile in (records, editor, chat)
dataset.module_owned             # 409 — a field declaration on a module-served dataset (editor, chat): the module owns the schema, the dataset declares no fields
dataset.module_reserved          # 400 — a part or dataset draft (on a type, or in a bundle body) names a module reserved to the server's own installs (`chat` — the catalog's general-chat usecase is its one declaration)
dataset.decl_invalid             # 400 — malformed part or dataset declaration (non-slug key, author mutability without a creator stamp, duplicate stamp kind, required additive field, …)
dataset.immutable                # 400 — PATCH a pinned part or dataset-def path (part: name, icon, pos, hidden, ui, uses are mutable; head: description, displayName, search.title/text/scope; field: name, description, xFormat.*); details.path

upsert.requires_user_ids         # 400 — upsert on a dataset not declared idRule "user"
# per-record rejection codes inside the 200 body's rejections[] (never HTTP errors):
#   upsert.immutable_field / upsert.not_author / upsert.record_deleted / upsert.rejected

filter.unknown_operator          # 400 — filter names an operator outside the grammar (details.operator, details.path); message lists the supported set
filter.invalid                   # 400 — any other filter-grammar violation (wrong operand type, malformed $and/$or array, bad $regex, …); message carries the parser's path + reason (details.path, details.operator). Filters parse at the request boundary, so these never surface mid-subscribe. Both codes also answer a bad `filter` on POST /v1/spaces/:id/search

type.not_found                   # 404 — unknown typeId on GET …/types/:typeId, GET …/types/:typeId/properties (existence-checked: a real type with no properties answers 200 [], an unknown id never does) POST …/properties/:objectId/type/:typeId, POST …/objects and POST …/bundles/:bundleId/children (`type` names a type the space does not have); 400 when a bundle ensure's rootType does — an unknown rootProperties owner is collection.not_found
membership.wrong_slot            # 400 — a write put a known collection id in any.type or a known type id in any.collections through a raw route (POST …/modify on the objects row, a bundle child); the membership routes and POST …/objects answer type.not_a_type / collection.not_a_collection instead
membership.type_required         # 400 — a raw write cleared any.type; every object has exactly one type (POST …/objects without `type` and a bare bundle body without `rootType` answer request.missing_field)
type.not_a_type                  # 400 — a …/types route, or POST …/properties/:objectId/type/:typeId, names a user COLLECTION (details.collectionId): use the …/collections routes, or file the object with POST …/properties/:objectId/collections/:collectionId. A registered collection's id (miniapp, bin) has no type row at all and answers type.not_found
type.xkey_required               # 400 — a type or a collection created without an xKey (both need a stable handle)
type.reserved_carrier            # 400 — object create `type`, POST …/properties/:objectId/type/:typeId, a bundle's rootType or a bundle child's type, or an `any.type` op through …/modify names a type whose part declares a reserved module (the general-chat root): that type is carried only by its own root (details.typeId)
type.xkey_conflict               # 409 — the xKey is already a handle in this space, on EITHER surface: a type's xKey or id (details.xKey, details.existingTypeId), or a collection's (details.xKey, details.existingCollectionId) — a collection never takes a type's handle and vice versa; also raised by POST …/bundles and POST /v1/catalog/:usecaseId/setup when an install's xKey is already held — install path only, never on adopt (details.bundleId; a setup adds details.usecase, details.usecaseId)
type.registered                  # 400 — add/patch/remove a property, part or dataset, or PATCH the type itself, on a registered built-in type; also a column write on a registered built-in collection (declarations are static)
collection.not_found             # 404 — unknown collectionId on GET/PATCH …/collections/:collectionId, GET …/collections/:collectionId/properties and POST …/properties/:objectId/collections/:collectionId; 400 when a bundle ensure's rootCollections names a collection the space does not have
collection.not_a_collection      # 400 — a …/collections route, or POST …/properties/:objectId/collections/:collectionId, names a user TYPE (details.typeId): use the …/types routes, or set it with POST …/properties/:objectId/type/:typeId. A registered type's id (page, dataview) answers collection.not_found
collection.registered            # 400 — PATCH the metadata of a registered built-in collection (miniapp, bin): it is statically declared (a column write on one is type.registered)
property.not_found               # 400 — a value write names a property the owner — the object's type or one of its collections — does not declare
property.kind_mismatch           # 400 — a value write violates the property's pinned kind
property.xkey_conflict           # 409 — add/rename a property to an xKey another property of the same type or collection holds (details.xKey, details.existingPropId); a preflight, not a guarantee
property.immutable               # 400 — PATCH a pinned path (kind/scope/items/properties); details.path
property.format_invalid          # 400 — descriptor vocabulary problem on create/PATCH, property or dataset field: slug does not fit the pinned kind, reserved slug/key (tags, validate, compute), unparseable relation.filter, empty slug
property.format_violation        # 400 — a property VALUE write does not fit its descriptor's current slug (details.propId, format = the slug, reason)

device.not_found                 # 404 — unknown peer id in the devices registry (or already pruned; tombstones are sticky)
device.self_delete               # 400 — DELETE of this server's own row refused (the sticky tombstone would lock the installation out; prune from another device)
device.pruned                    # 409 — self-row write (PUT /me, activate) absorbed by the row's tombstone; the peer id can never re-register (fresh `any init` to re-derive keys)

file.not_found                   # 404 — unknown fileId / objectId, or files query before the first attach
file.not_durable                 # 409 — offload refused: local bytes are the only copy (not backed up yet)
file.not_available               # 409 — content not local and not fetchable yet (not durable, or no public read base); retry after the row gains networkSign
file.variant_invalid             # 400 — variant/variantOf pairing broken, or original on a different object

history.version_not_found        # 404 — unknown version (ChangeId), or not in this object's DAG
history.view_too_large           # 413 — materializing that version blew the SDK's view bound; narrow with dataset/recordId
history.truncated                # 404 — the causal past needed is not on this device (reserved; the SDK keeps full history)

chat.text_required               # 400 — send with no text, attachments or control; edit with empty text
chat.text_too_long               # 400 — text over the byte cap (details.max_bytes, details.got_bytes)
chat.reply_id_invalid            # 400 — replyToMessageId too long
chat.agent_invalid               # 400 — malformed agent group
chat.attachments_invalid         # 400 — malformed attachments
chat.context_invalid             # 400 — malformed context
chat.control_invalid             # 400 — malformed control
chat.emoji_invalid               # 400 — reaction emoji empty or over 64 bytes
chat.not_found                   # 404 — unknown message id
chat.not_author                  # 403 — edit / delete of another member's message

blocks.type_required             # 400 — block create without a type
blocks.not_found                 # 404 — unknown block id

markdown.no_match                # 400 — an edits[i].oldText not found in the current rendering (details.editIndex); GET .../editor/:collection/markdown and quote exactly
markdown.ambiguous_match         # 400 — oldText occurs >1 times without replaceAll (details.editIndex, details.occurrences); add context or set replaceAll
markdown.overlapping_edits       # 400 — two edits matched intersecting text (details.editIndices); merge them into one edit

aggregate.bad_pipeline           # 400 — unparseable pipeline, unknown stage, or $text/vector outside the pushdown prefix
aggregate.limit_exceeded         # 400 — a blocking-stage bound blew (details.limit: group | accumArray | memory)

local.disabled                   # 409 — local store disabled (local.enabled: false); existing collections untouched on disk
local.collection_not_found       # 404 — collection not ensured yet (PUT /v1/local/collections), or already dropped
local.bad_index                  # 400 — an index in ensure/indexes was rejected (same name, different definition; invalid name); ensure leaves no collection behind
local.bad_name                   # 400 — scope / spaceId / name failed validation (name: ^[a-z0-9][a-z0-9_-]{0,63}$)
local.bad_sink_target            # 400 — a pipeline $out / $merge into / $lookup from names a collection outside the local store
local.doc_not_found              # 404 — get / update (without upsert) of an unknown id
local.duplicate_id               # 409 — insert of an id that already exists
local.unique_violation           # 409 — a unique index rejected the write
local.too_many_docs              # 400 — insert/upsert over 1000 docs in one request (details.max / got)
local.bad_filter                 # 400 — filter failed to parse (filter.* codes cover the boundary check; this is the store-side fallback)
local.bad_sort                   # 400 — unparseable sort key
local.bad_modifier               # 400 — unparseable or unknown-operator modifier
local.bad_pipeline               # 400 — unparseable pipeline, sink into the aggregated collection, sink result without id, $lookup from another collection
local.limit_exceeded             # 400 — a blocking-stage bound blew (details.limit: group | accumArray | memory)
local.bad_export                 # 400 — import of a file that is not an export this server reads: not gzip, foreign format/version, a section shorter than its count, a non-object or id-less document, bytes past the last section, an untagged collection name (details.imported = collections completed before it)

access.disabled                  # 409 — no access.redeemUrl configured (a named nodeconf gets no default)
access.request_rejected          # 400 — the invite service refused the request (details.code)
access.signature_rejected        # 401 — the invite service could not verify this account's signature
access.code_not_found            # 404 — unknown invite code
access.code_unusable             # 409 — disabled, expired or exhausted code (details.code)
access.rate_limited              # 429 — the invite service is throttling this client
access.unavailable               # 502 — the invite service is unreachable or answered unexpectedly

push.disabled                    # 409 — push notifications not configured (push.enabled / push.peerId), or the SDK has no push node

events.payload_too_large         # 400 — event data exceeds the 64 KiB per-message cap
events.no_read_key               # 409 — network-scope event in a space this identity has no read key for (keyless/guest access)
events.too_many_patterns         # 409 — the space's pub/sub subscription pattern budget (100) is exhausted; narrow or share filters
events.topic_not_owned           # 403 — the type maps into another account's self-owned topic namespace (defensive; server mapping never produces it)

process.not_found                # 404 — progress/finish on a process not live under this account, or cancel with no live match; re-register after restart/expiry
process.ambiguous                # 409 — cancel matched several publishers running the same id; details.identities lists them, pass identity to pick one

index.disabled                   # 409 — search index turned off (index.enabled: false)
index.no_embedder                # 400 — mode=vector without an embedder configured
index.embedder_unavailable       # 503 — mode=vector while the embedder is unreachable (retryable)
index.terms_unsupported          # 409 — require/exclude on a build with no full-text index (they cannot be enforced)
search.bad_mode                  # 400 — mode not hybrid | fts | vector
search.bad_scope                 # 400 — scope not a valid slug ([a-z0-9_-], max 64; scopes are an open set)

sdk.not_implemented              # 501 — DELETE …/types/:typeId, DELETE …/collections/:collectionId, GET …/sync-status/peers
sdk.not_found                    # 404 — the SDK reports the target is gone (object, type, property, part or dataset definition)
sdk.crdt_version_newer           # 409 — the account's data was written by a newer release (details.stored > details.supported): POST /v1/auth refuses to boot it; a running server whose account is raised by another device turns read-only — every synced write answers this until the server is upgraded (GET /v1/health.crdtVersion)

server.unavailable               # 503 — request cancelled / engine tearing down
server.internal                  # 500 — the embedded usecase catalog failed to compile (catalog routes)
internal                         # 500 — catch-all
```

Rule: **never leak SDK internal types or file paths in `message` or
`details`.** The code says enough; `details` carries only the
caller-provided identifiers it asked about.

## Panic handler

Echo's recover middleware converts panics to `500 internal` with a
generic message. Stack traces go to the server log, not the response
body.

## Exit codes (CLI)

- `0` — success
- `1` — user error (bad args, 4xx from server)
- `2` — server error (5xx)
- `3` — transport error (can't reach server)
