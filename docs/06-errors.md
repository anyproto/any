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

No 401/403 in v1 — there is no auth layer.

5xx responses log at `error` level on the server with the full stack.
Clients receive the sanitized body only.

## Error code namespace

Dotted identifiers grouped by SDK section. Concrete codes are added as
handlers are implemented; examples:

```
request.bad_json                 # request body is not valid JSON
request.schema                   # JSON shape doesn't match endpoint schema
request.missing_field            # required field absent

auth.passkey_required            # wallet encrypted, no passkey provided
auth.passkey_wrong

space.not_found
space.exists                     # create conflict
space.not_joined                 # operation requires membership

invite.invalid                   # invite token malformed or unrecognized

object.not_found
object.type_required

dataset.unknown                  # no handler registered
dataset.validation               # schema or handler rejected ops

type.not_found
property.not_found
property.kind_mismatch           # write violated the immutable kind
property.immutable_field         # attempt to update type-shape field

index.disabled                   # 409 — search index turned off (index.enabled: false)
index.no_embedder                # 400 — mode=vector without an embedder configured
index.embedder_unavailable       # 503 — mode=vector while the embedder is unreachable (retryable)
search.bad_mode                  # 400 — mode not hybrid | fts | vector
search.bad_scope                 # 400 — scope not basic | chat | agent

sdk.not_implemented              # 501 — SDK placeholder (sync-status, some Properties/Types subroutes)
sdk.not_found                    # 404 — SDK reports the target is gone (deleted, never existed as a type, etc.)

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
