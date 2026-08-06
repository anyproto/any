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
| 404    | Target not found (space, object, type, record)                 |
| 409    | Conflict (duplicate, precondition failed)                      |
| 500    | Internal error — unexpected SDK or server failure               |
| 501    | Not implemented — routes for SDK placeholder APIs (sync-status, some Properties/Types subroutes) return this in v1 |
| 503    | Server not ready (shutdown, reindex in progress)               |

No caller authentication in v1 (loopback is the trust boundary). The
single 401 is `auth.required` — the server itself has no account
booted yet (see `03-api.md` § Auth); 403 appears only for author-only
data rules (`chat.not_author`, `agent.not_author`).

5xx responses log at `error` level on the server with the full stack.
Clients receive the sanitized body only.

## Error code namespace

Dotted identifiers grouped by SDK section. Concrete codes are added as
handlers are implemented; examples:

```
request.bad_json                 # request body is not valid JSON; message names what failed to parse and the expected field set
request.schema                   # JSON shape doesn't match endpoint schema
request.missing_field            # required field absent
request.unknown_field            # 400 — a top-level body key outside the endpoint's accepted set (details.fields, details.accepted); message enumerates the accepted fields and, where one exists, the right home for the value (e.g. object properties → initialProperties, type properties → POST …/types/:typeId/properties). The strict endpoints are the ones whose request schemas carry additionalProperties: false in /v1/openapi.json.

auth.required                    # 401 — server unauthorized; POST /v1/auth first
auth.already_authorized          # 409 — engine already booted; restart to switch
auth.bad_mnemonic                # 400 — BIP-39 validation failed
auth.mnemonic_mismatch           # 409 — wallet on disk disagrees with the phrase/index
auth.account_not_found           # 404 — accountId has no local wallet
auth.account_in_use              # 409 — another process holds the account's pid lock
auth.passkey_required            # wallet encrypted, no passkey provided
auth.passkey_wrong

space.not_found
space.exists                     # create conflict
space.not_joined                 # operation requires membership
space.not_accepted               # 409 — join pending approval; space not materialized yet
space.deleted                    # 409 — space is deleted (row is a tombstone); 1-1s re-creatable via one-to-one start

invite.invalid                   # invite token malformed or unrecognized

object.not_found                 # 404 — objectId unknown or deleted in this space (per-object query, editor, markdown, history …)
object.id_required               # 400 — the object id in the path or body is a serialized nil ("None", "null", "undefined", …): the caller's id variable was unset; never a store lookup failure
object.type_required

dataset.unknown                  # no handler registered
dataset.validation               # schema or handler rejected ops

filter.unknown_operator          # 400 — filter names an operator outside the grammar (details.operator, details.path); message lists the supported set
filter.invalid                   # 400 — any other filter-grammar violation (wrong operand type, malformed $and/$or array, bad $regex, …); message carries the parser's path + reason (details.path, details.operator). Filters parse at the request boundary, so these never surface mid-subscribe

enrich.empty_proposal            # 404 — proposal has no items (already applied, deleted, or empty)
enricheddata.text_too_long       # 400 — enrichment text exceeds the byte cap (details.max_bytes, got_bytes)

agent.seq_required               # turn/chunk append missing the seq ordering key
agent.turn_invalid               # turn record shape violation (caps, types)
agent.chunk_invalid              # chunk shape violation (missing/inverted pointers or period)
agent.memory_invalid             # memory item shape violation (category/context/caps)
agent.memory_not_found           # 404 — unknown itemId on evolve/delete
agent.not_author                 # 403 — evolve/delete by non-creator
agent.seq_deleted                # 409 — client-provided seq points at a tombstoned (wiped) record; deleted seqs are never reused

type.not_found                   # 404 — unknown typeId on GET …/types/:typeId and GET …/types/:typeId/properties (existence-checked: a real type with no properties answers 200 [], an unknown id never does)
type.xkey_required               # 400 — create without an xKey (a type needs a stable handle)
type.xkey_conflict               # 409 — xKey collides with an existing type's xKey or id in the space (details.xKey, details.existingTypeId)
type.registered                  # 400 — add/patch/remove a property on a registered built-in type (properties are static)
property.not_found
property.kind_mismatch           # write violated the immutable kind
property.immutable               # 400 — PATCH a pinned path (kind/scope/items/properties, whole `format`, format.type); details.path
property.format_invalid          # 400 — bad format leaf on create/PATCH (unknown ui, unparseable filter, format.* on a format-less property)
property.format_violation        # 400 — a property VALUE write violated its declared format (details.propId, format, reason)

file.not_found                   # 404 — unknown fileId / objectId, or files query before the first attach
file.not_durable                 # 409 — offload refused: local bytes are the only copy (not backed up yet)
file.not_available               # 409 — content not local and not fetchable yet (not durable, or no public read base); retry after the row gains networkSign
file.variant_invalid             # 400 — variant/variantOf pairing broken, or original on a different object

history.version_not_found        # 404 — unknown version (ChangeId), or not in this object's DAG
history.view_too_large           # 413 — materializing that version blew the SDK's view bound; narrow with dataset/recordId
history.truncated                # 404 — the causal past needed is not on this device (reserved; the SDK keeps full history today)

markdown.no_match                # 400 — an edits[i].oldText not found in the current rendering (details.editIndex); GET .../editor/markdown and quote exactly
markdown.ambiguous_match         # 400 — oldText occurs >1 times without replaceAll (details.editIndex, details.occurrences); add context or set replaceAll
markdown.overlapping_edits       # 400 — two edits matched intersecting text (details.editIndices); merge them into one edit

aggregate.bad_pipeline           # 400 — unparseable pipeline, unknown stage, or $text/vector outside the pushdown prefix
aggregate.limit_exceeded         # 400 — a blocking-stage bound blew (details.limit: group | accumArray | memory)

push.disabled                    # 409 — push notifications not configured (push.enabled / push.peerId), or the SDK has no push node

index.disabled                   # 409 — search index turned off (index.enabled: false)
index.no_embedder                # 400 — mode=vector without an embedder configured
index.embedder_unavailable       # 503 — mode=vector while the embedder is unreachable (retryable)
search.bad_mode                  # 400 — mode not hybrid | fts | vector
search.bad_scope                 # 400 — scope not a valid slug ([a-z0-9_-], max 64; scopes are an open set)

sdk.not_implemented              # 501 — SDK placeholder (sync-status, some Properties/Types subroutes)
sdk.not_found                    # 404 — SDK reports the target is gone (deleted object, unknown property, etc.)

server.unavailable               # 503 — request cancelled / server shutting down
internal                         # catch-all for 500s
```

Rule: **never leak SDK internal types or file paths in `message` or
`details`.** The code says enough; `details` carries only the
caller-provided identifiers it asked about.

## Panic handler

`middleware.Recover` converts panics to `500 internal` with a generic
message. Stack traces go to the server log, not the response body.

## Exit codes (CLI)

- `0` — success
- `1` — user error (bad args, 4xx from server)
- `2` — server error (5xx)
- `3` — transport error (can't reach server)

## Client-side (anyHelper)

`anyHelper`'s `api(method, path, body)` returns `{ ok, status, data, error,
code }`. On failure `code` is the typed envelope code
(`property.kind_mismatch`, `property.not_found`, `sdk.not_implemented`,
`space.not_found`, …), falling back to a status-derived code
(`sdk.not_implemented` for 501, `server.unavailable` for 503,
`request.not_found` for 404, `request.bad` for other 4xx, `internal` for 5xx)
when the body isn't the standard envelope. The write helpers (`createObject`,
`updateObject`, `deleteObject`, `setRecord`, `deleteRecord`) thread `code` into
their `{ ok:false, error, code }` result, so callers — and the agent, which
sees tool results verbatim — can switch on the code instead of regexing the
message. Resolver-level rejections (e.g. an unknown `"Type.prop"` key) fail
fast **client-side** with a clear message and no server round-trip (so no
`code`).
