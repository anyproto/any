# HTTP API

## Conventions

- **Framework**: `github.com/labstack/echo` (v4).
- **Base path**: `/v1/`. All endpoints are versioned from day one.
- **Media type**: `application/json; charset=utf-8` — requests with a body
  and every response. No other content types in v1.
- **IDs in the path**: `{spaceId}`, `{objectId}`, `{typeId}`, `{propId}`
  are URL-safe strings (base58). Path segments are URL-encoded.
- **Success**: `200 OK` for reads, `201 Created` for creates,
  `204 No Content` for side-effect-only endpoints (AttachType,
  DetachType, SetDevice).
- **Errors**: see `06-errors.md`. Always JSON, always the same shape.
- **Binding**: use `echo.Context.Bind` for request bodies. Share the
  request/response types between server and CLI via `internal/api/`.

## Endpoint catalog

### Meta

| Method | Path            | Purpose                                |
|--------|-----------------|----------------------------------------|
| GET    | `/v1/health`    | server health, version, account id     |
| POST   | `/v1/shutdown`  | graceful shutdown                      |

### Account

| Method | Path                         | Purpose                                |
|--------|------------------------------|----------------------------------------|
| GET    | `/v1/account`                | own id + metadata                      |
| PUT    | `/v1/account/metadata`       | update own AccountMetadata             |

### Spaces

| Method | Path                            | Purpose                             |
|--------|---------------------------------|-------------------------------------|
| POST   | `/v1/spaces`                    | `Service.Create`                    |
| GET    | `/v1/spaces`                    | `Service.List` → `[]SpaceInfo`      |
| GET    | `/v1/spaces/:spaceId`           | `Space.Info`                        |
| DELETE | `/v1/spaces/:spaceId`           | `Service.Delete`                    |
| POST   | `/v1/spaces/join`               | `Service.Join`                      |
| POST   | `/v1/spaces/derive`             | `Service.Derive`                    |
| POST   | `/v1/spaces/one-to-one`         | `Service.OneToOne`                  |

### Objects

| Method | Path                                                      | Purpose                  |
|--------|-----------------------------------------------------------|--------------------------|
| POST   | `/v1/spaces/:spaceId/objects`                             | `Objects.Create`         |
| POST   | `/v1/spaces/:spaceId/objects/derive`                      | `Objects.Derive`         |
| POST   | `/v1/spaces/:spaceId/objects/query`                       | `Space.QueryObjects.All` |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId`                   | `Objects.Delete`         |

### Data plane

| Method | Path                                                      | Purpose             |
|--------|-----------------------------------------------------------|---------------------|
| POST   | `/v1/spaces/:spaceId/query`                               | `Space.Query.All`   |
| POST   | `/v1/spaces/:spaceId/modify`                              | `Space.Modify`      |
| POST   | `/v1/spaces/:spaceId/delete-records`                      | `Space.Delete`      |

Two query endpoints, scoped differently:

- `POST /v1/spaces/:spaceId/objects/query` — **cross-object**. Reads
  the per-space `objects` collection (one row per object's computed
  property values). Use this to find objects by property
  (e.g. `{"filter":{"<typeId>.<propId>":"Casablanca"}}`).
- `POST /v1/spaces/:spaceId/query` — **per-object**. Reads one of an
  object's own datasets (`objectId` and `dataset` required). Used
  primarily for a type object's `properties` definitions dataset; the
  per-object property *values* dataset went away when storage moved
  to the shared per-space collection.

Both use POST (not GET) because the filter/sort body doesn't fit
cleanly in a query string. **No `/subscribe` in v1** — see `04-events.md`.

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
| PATCH  | `/v1/spaces/:spaceId/types/:typeId/properties/:propId`        | `TypesAPI.UpdatePropertyMeta` |

### Properties (values on objects)

| Method | Path                                                          | Purpose                       |
|--------|---------------------------------------------------------------|-------------------------------|
| GET    | `/v1/spaces/:spaceId/properties/:objectId`                    | `PropertiesAPI.Get`           |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/base/:typeId`       | `PropertiesAPI.SetBase`       |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/account/:typeId`    | `PropertiesAPI.SetAccount`    |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/device/:typeId`     | `PropertiesAPI.SetDevice`     |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/attach/:typeId`     | `PropertiesAPI.AttachType`    |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/detach/:typeId`     | `PropertiesAPI.DetachType`    |

### Members & ACL (routes present, handlers return 501 until SDK lands them)

| Method | Path                                                 | Purpose                            |
|--------|------------------------------------------------------|------------------------------------|
| GET    | `/v1/spaces/:spaceId/members`                        | members collection                 |
| GET    | `/v1/spaces/:spaceId/members/:identity`              | one member                         |
| POST   | `/v1/spaces/:spaceId/acl/invite`                     | create / replace invite            |
| POST   | `/v1/spaces/:spaceId/acl/accept`                     | accept join request                |
| POST   | `/v1/spaces/:spaceId/acl/decline`                    | decline join request               |
| POST   | `/v1/spaces/:spaceId/acl/remove`                     | remove accounts                    |
| POST   | `/v1/spaces/:spaceId/acl/permissions`                | change permissions                 |
| POST   | `/v1/spaces/:spaceId/acl/ownership`                  | ownership transfer                 |
| POST   | `/v1/spaces/:spaceId/acl/self-remove`                | self-remove                        |

Registering these now keeps the CLI buildable and discoverable; the
handlers short-circuit with `501 Not Implemented` + error code
`sdk.not_implemented` while the SDK placeholders are empty.

### Sync status (routes present, handlers return 501 until SDK lands it)

| Method | Path                                                 | Purpose                            |
|--------|------------------------------------------------------|------------------------------------|
| GET    | `/v1/spaces/:spaceId/sync-status`                    | space-level aggregate              |
| GET    | `/v1/spaces/:spaceId/sync-status/objects/:objectId`  | per-object                         |
| GET    | `/v1/spaces/:spaceId/sync-status/peers`              | peers                              |

## Body shapes (examples)

Exact JSON tags live in `internal/api/` when implementation starts.
These are the target shapes — they mirror the SDK 1:1.

**POST /v1/spaces/:spaceId/query**

```json
{
  "objectId":   "obj_abc",
  "dataset":    "notes",
  "filter":     { "tags": "idea" },
  "sort":       ["-_ver.id"],
  "limit":      100,
  "offset":     0,
  "projection": { "includeVariants": false, "includeMeta": false }
}
```

Response: `{ "records": [ /* *anyenc.Value rendered as JSON */ ] }`.

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

Response: `{ "versionId": "..." }` (to become `{versionId, recordIds:[...]}`
once the SDK grows `ModifyResult` — see `07-roadmap.md`).

## Middleware

Minimal in v1:

- `middleware.Recover` — catch panics, return 500.
- `middleware.RequestID` — generate an id per request for the logs.
- **Logger middleware** wired to `any-sync/app/logger` — one line per
  request at info level (path, status, duration).
- `middleware.BodyLimit("1M")` — reject anything larger; prevents
  accidental uploads before the file API lands.

No CORS, no rate limiting, no auth middleware in v1.

## Pagination

Offset-based, mirroring the SDK. Cursor pagination is a future add.

## Idempotency

POST endpoints are **not** idempotent in v1 — each POST produces a new
DAG change. An `Idempotency-Key` header is a future add.
