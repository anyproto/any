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

invite.invalid                   # 400 — invite token malformed or unrecognized

object.not_found
object.type_required

dataset.unknown                  # no handler registered
dataset.validation               # schema or handler rejected ops

type.not_found
property.not_found
property.kind_mismatch           # write violated the immutable kind
property.immutable_field         # attempt to update type-shape field

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
