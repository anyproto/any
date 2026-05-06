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
| GET    | `/v1/spaces/:spaceId/objects/:objectId/markdown`          | `markdown.Get`           |
| PUT    | `/v1/spaces/:spaceId/objects/:objectId/markdown`          | `markdown.Set`           |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/subscribe`         | `Space.Subscribe` (SSE)  |

The two `markdown` routes are aggregating endpoints (each one bundles
several SDK calls) and are a deliberate exception to the "endpoints
map 1:1 onto SDK methods" rule. They exist because the diff between
the supplied content and the stored blocks runs server-side; pushing
that round-trip to the client would mean exposing the splitter / diff
machinery over the wire. `GET` returns `{"content": "..."}`; `PUT`
takes `{"content": "..."}` and replies with `{"inserted":[…lexids…],
"updated":[…], "deleted":[…], "unchanged": N}`.

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

Tree moves use the existing `setBase` endpoint — no dedicated move
route. To relocate object `oid` under `newParent` at lexid pos `p`:

```
POST /v1/spaces/:spaceId/properties/:oid/base/nav
{ "patch": { "parentId": "<newParent>", "pos": "<p>" } }
```

Both fields land in one DAG change. The web UI ports the lexid
allocator to JavaScript (alphabet `CharsAllNoEscape`, blockSize=4,
stepSize=100 — match the Go side byte-for-byte) so the client can
compute drop-target positions without a server round-trip.

#### Object deletion

`Objects.Delete` only deletes the any-sync tree; the projection row
in the per-space `objects` collection survives the call (the any-store
collection isn't tied to the tree's lifecycle). The SDK's query
iterator filters rows that carry `_deletedAt`, so the server's
`DELETE /v1/spaces/:spaceId/objects/:objectId` first writes a
record-level tombstone via `space.Delete(ObjectId, Dataset:"objects",
RecordIds:[oid])` and **then** runs `Objects.Delete`. Order matters —
once the tree is gone the per-object Modify path can no longer write
the tombstone and the row would linger in queries indefinitely.

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
cleanly in a query string. For live updates use the SSE
subscribe endpoints — see `04-events.md`.

#### Subscribe (Server-Sent Events)

Two endpoints, mirroring the SDK's `Space.Subscribe(objectId, dataset)`
and `Space.SubscribeProperties()` 1:1:

```
GET /v1/spaces/:spaceId/objects/:objectId/subscribe?dataset=<name>
GET /v1/spaces/:spaceId/properties/subscribe
```

Response is `Content-Type: text/event-stream`. Errors before the
stream opens use the canonical JSON envelope (`request.missing_field`,
`space.not_found`, ...). Once the response status is 200, problems
become SSE `event: closed` frames.

Wire format:

```
event: ready
data: {}

event: changes
id: 47
data: [{"spaceId":"...","objectId":"...","dataset":"objects","addSeq":42},
       {"spaceId":"...","objectId":"...","dataset":"objects","addSeq":47}]

event: lagged
data: {"total": 3}

: keepalive

event: closed
data: {"reason": "server_shutdown"}
```

- `ready` is sent once after the SDK Subscribe call returns. Wait for
  it before treating the stream as live.
- `changes` carries a JSON array of zero-or-more events. Wait
  coalesces every event accumulated during the previous write into a
  single frame, so a slow client / network produces fewer, larger
  frames rather than head-of-line stalls. `id:` is the max `addSeq`
  in the batch (preserves Last-Event-ID semantics for future resume).
- `lagged` is emitted before a `changes` frame whenever the SDK has
  dropped events for this subscriber (slow consumer hit the per-
  subscriber mailbox cap). `total` is the cumulative drop count.
  Treat any `lagged` as "the in-stream events are no longer a full
  picture — re-Query for current state".
- `: keepalive` comments arrive every 25s during silence to defeat
  idle middlebox timeouts.
- `closed` is the terminal frame. Reasons: `server_shutdown` (signal
  or `POST /v1/shutdown`), `sdk_closed` (space or SDK released the
  subscription). Reconnect after either.

Subscriptions deliver events from registration onward only — there is
no replay. Cold-state callers must `Query` separately. POST to
`/spaces/:spaceId/shutdown` doesn't exist; the per-subscription close
is just disconnecting (or the server `closed` frame).

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

| Method | Path                                                          | Purpose                          |
|--------|---------------------------------------------------------------|----------------------------------|
| GET    | `/v1/spaces/:spaceId/properties/subscribe`                    | `Space.SubscribeProperties` (SSE) |
| GET    | `/v1/spaces/:spaceId/properties/:objectId`                    | `PropertiesAPI.Get`              |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/base/:typeId`       | `PropertiesAPI.SetBase`          |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/account/:typeId`    | `PropertiesAPI.SetAccount`       |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/device/:typeId`     | `PropertiesAPI.SetDevice`        |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/attach/:typeId`     | `PropertiesAPI.AttachType`       |
| POST   | `/v1/spaces/:spaceId/properties/:objectId/detach/:typeId`     | `PropertiesAPI.DetachType`       |

### Chat (built-in `chat` type)

| Method | Path                                                                     | Purpose                  |
|--------|--------------------------------------------------------------------------|--------------------------|
| POST   | `/v1/spaces/:spaceId/objects/:objectId/messages`                         | send a message           |
| GET    | `/v1/spaces/:spaceId/objects/:objectId/messages`                         | list messages            |
| PATCH  | `/v1/spaces/:spaceId/objects/:objectId/messages/:msgId`                  | edit own message text    |
| DELETE | `/v1/spaces/:spaceId/objects/:objectId/messages/:msgId`                  | delete own message       |
| POST   | `/v1/spaces/:spaceId/objects/:objectId/messages/:msgId/reactions/:emoji` | toggle own reaction      |

Liveness is the existing subscribe endpoint with `dataset=chat_messages`:

```
GET /v1/spaces/:spaceId/objects/:objectId/subscribe?dataset=chat_messages
```

Clients re-query for the new message body when a `changes` frame
arrives (the SSE event is just the routing tuple — see `04-events.md`).

#### Message wire shape

```json
{
  "id":               "<change-derived id>",
  "creator":          "<accountId>",
  "createdAt":        1714597200,
  "modifiedAt":       1714597200,
  "replyToMessageId": "<msgId>",
  "text":             "**hi** _there_",
  "reactions":        { "👍": ["<id1>", "<id2>"] }
}
```

`createdAt` and `modifiedAt` are unix-seconds, server-stamped. They
are equal on a never-edited message — clients detect edits by
comparing them. `text` is markdown; rendering is the client's
problem (`internal/markdown` exists if anyone wants to round-trip).

`reactions` are emoji-keyed on the wire but stored identity-keyed —
the API server transposes on read. Authorization on writes is a
single path-segment compare against `ctx.Change.Creator` in the
handler: only the change's signer can write into
`reactions.<that-identity>`. See `internal/chat/handler.go`.

#### Send

`POST /v1/spaces/:spaceId/objects/:objectId/messages`

```json
{ "text": "hello", "replyToMessageId": "abc" }
```

`text` is required, ≤ 32 KiB. `replyToMessageId` is optional, ≤ 256
bytes, and a soft reference — the server doesn't validate that the
target exists. Returns 201 with the full message record (server-
stamped fields included).

#### List

`GET /v1/spaces/:spaceId/objects/:objectId/messages?before=&after=&limit=`

Returns messages in ascending creation order (oldest first). Cursors
`before` / `after` are message ids; the server resolves them to the
underlying `_ver.id` boundary (the SDK's stable creation-version
marker — set once on creation, never updated by edits, so reordering
on edit is impossible). `limit` defaults to 50, max 200.

```json
{ "messages": [ /* ChatMessage, ... */ ] }
```

#### Edit / delete (own only)

`PATCH .../messages/:msgId` body `{ "text": "..." }` replaces the
text and bumps `modifiedAt`. `DELETE .../messages/:msgId` tombstones
the record. Both return `403 chat.not_author` for non-authors and
`404 chat.not_found` for unknown ids. The handler enforces the same
rules for peer-originated changes.

#### React (toggle)

`POST .../messages/:msgId/reactions/:emoji` (no body) toggles the
caller's reaction: adds the emoji to `reactions.<callerId>` if
absent, removes it if present. The CRDT op is `$addToSet` /
`$pull` against the caller's identity-keyed slot, so two clients
toggling at the same time can't corrupt each other. Response:

```json
{ "reactions": { "👍": ["<id>"], "🎉": ["<id>"] } }
```

### Members

| Method | Path                                                 | Purpose                            |
|--------|------------------------------------------------------|------------------------------------|
| GET    | `/v1/spaces/:spaceId/members`                        | `MembersAPI.List`                  |
| GET    | `/v1/spaces/:spaceId/members/me`                     | `MembersAPI.Me`                    |
| GET    | `/v1/spaces/:spaceId/members/requests`               | `MembersAPI.JoinRequests`          |
| GET    | `/v1/spaces/:spaceId/members/:identity`              | `MembersAPI.Get`                   |

Static path segments (`/me`, `/requests`) are registered before the
`:identity` wildcard so they don't get swallowed. The `Member` wire
shape mirrors `space.Member` 1:1; both `permission` and `status` are
strings (see "Permission / status strings" below). `requestRecordId`
is non-empty only on a pending-request entry — pass it to
`POST /v1/spaces/:id/acl/accept`.

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
| DELETE | `/v1/spaces/:spaceId/invites`                        | `ACL.RevokeAllInvites`             |
| DELETE | `/v1/spaces/:spaceId/invites/:recordId`              | `ACL.RevokeInvite`                 |
| POST   | `/v1/spaces/join`                                    | `Service.Join` — body carries the share token |

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

In the v1 RequestToJoin flow `Service.Join` returns 202: the SDK has
posted the join request, written a `joining` index entry, and the
joiner now polls `GET /v1/spaces/:id/members/me` for the status flip
to `active` after the owner accepts.

Listing returns one entry per active invite record — pass `recordId`
to the DELETE path to revoke a single invite, or DELETE the parent
collection to revoke all in one batch.

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
