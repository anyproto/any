---
title: Data types
description: Property kinds, value formats, the datetime instant on the wire, and the synced / local / account / derived scopes every field belongs to.
order: 40
---
# Data types

Every value in any has a structural **kind**, may carry a **format** that narrows the convention, and belongs to a **scope** that decides who it syncs to. This page is the vocabulary.

## Kinds

| Kind | JSON on the wire | Notes |
|------|------------------|-------|
| `string` | `"Dune"` | |
| `number` | `1965` | |
| `boolean` | `true` | Never indexed for search. |
| `array` | `["sci-fi", "classic"]` | Filters compare against elements — see [Reading data](reading-data.html). |
| `object` | `{"…": "…"}` | Nested paths are addressed dotted: `<typeId>.<propId>.sub`. |
| `datetime` | `{"$date": "2026-08-05T17:00:00.000Z"}` | An instant: unix milliseconds, orderable, index-keyable, computable. |
| `null` | `null` | |

Kind is pinned at the property's first write. Changing it means defining a new property.

## Datetime instants

Every timestamp the server stores — the derived `createdAt` / `modifiedAt` on object rows, chat `createdAt` / `modifiedAt` and reaction stamps, runtime-dataset `createTime` / `modifyTime`, space rows' `createdAt`, and any property declared with a `date` or `datetime` format — is an instant. It reads back as `{"$date": "<RFC 3339>"}`, and writes accept either `{"$date": "<RFC 3339>"}` or `{"$date": <unix millis>}`.

```json
{ "<typeId>": { "<propId>": { "$date": "2026-08-05T17:00:00.000Z" } } }
```

Filter literals take the same shape:

```json
{ "modifiedAt": { "$gte": { "$date": "2026-01-01T00:00:00Z" } } }
```

> **Note.** A bare number or string in a date comparison does not error — it answers wrong. Comparisons across types are decided by type rank, and instants rank above numbers and strings, so `{"$gte": 1700000000}` matches every row, `$lt` matches none and `$eq` never matches. Always wrap the literal.

Instants are what the aggregation date operators (`$year`, `$dateTrunc`, `$dateDiff`) compute on — see [Aggregation](aggregation.html). A property declared `kind: "string"` alongside a `date` / `datetime` format keeps the ISO-8601 string convention instead (`2006-01-02` for `date`, RFC 3339 for `datetime`): it sorts lexicographically, which is chronological for RFC 3339, but every date operator returns `null` for it.

## Formats

A format narrows a kind to a convention the server validates on write (`400 property.format_violation` with `details: {propId, format, reason}`; `400 property.format_invalid` for a bad definition). `format.type` implies the kind, so `kind` may be omitted.

| `format.type` | Implied kind | Value | Extras |
|---------------|--------------|-------|--------|
| `links` | `array` | Plain `any://<objectId>` URIs — no space segment, no fragment. | `format.ui` (`link` / `links` / `select` / `multiselect`), `format.filter` — a mongo-style condition over candidate objects. |
| `date` | `datetime` | An instant that lands on midnight UTC. | No `ui`. |
| `datetime` | `datetime` | Any instant. | No `ui`. |
| `select` | `string` | One option key. | `format.options.<key>` = `{name, color, pos, meta?}`. |
| `multiselect` | `array` | An array of option keys. | Same options map. |

`format.meta` is an opaque string map for format-level config. No object-existence or object-type checks run on `links` values, and option membership is not enforced on `select` values — both are dangling-tolerant by design. `tags` is reserved.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Related", "xKey": "related",
       "format": {"type": "links", "ui": "multiselect",
                  "filter": {"any.types": "page"}}}'
```

## Scopes

Every field — a property definition or a dataset record field — has a scope that decides where a write goes and who sees it.

| Scope | Who sees it | Written how |
|-------|-------------|-------------|
| `synced` | Every member of the space (default). | A change in the object's DAG. Bumps `modifiedAt`. |
| `account` | This account's devices only, invisible to other members. | Through the account's private tech space. Declarable on property definitions; dataset record fields await the SDK's record-level transport. |
| `local` | This device only, never synced. | A local materialisation — no DAG change, empty `changeId`, still visible to query/subscribe. |
| `derived` | Everyone; computed by a handler. | Read-only — client writes are rejected. Reserved for built-ins (`creator`, `createdAt`, `mentions`, row-root stamps). |

Declare a property's scope at create (`"scope": "local"`); it is pinned like the kind. Value writes need no scope parameter: `POST …/properties/:objectId/set/:typeId` routes by the declared scope, and every `propId` in one patch must resolve to the same scope. Dataset records choose the route with `"scope": "synced" | "local"` on `/modify` — see [Writing data](writing-data.html).

Chat's read-tracking flags (`unread`, `unreadMention`, `unreadReactions`) are the canonical `local` fields: each device keeps its own, nothing leaves the machine.

> **Why it matters.** Scopes let one record hold shared content and private state side by side — a message everyone sees, a read flag only you see — without a second store or a server-side user table.

## Dataset schemas and `x-scope`

The scope of every dataset field is discoverable. `GET /v1/spaces/:spaceId/datasets` returns one JSON Schema document per dataset the space hosts; `GET /v1/datasets` covers the account-level `spaces` and `profile` datasets.

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/datasets
any datasets $SPACE
```

```json
{ "datasets": [ { "name": "chat_messages", "typeId": "chat",
  "schema": { "type": "object", "additionalProperties": true,
    "properties": {
      "text":      { "type": "string",  "x-scope": "synced" },
      "creator":   { "type": "string",  "x-scope": "derived" },
      "unread":    { "type": "boolean", "x-scope": "local" } } } } ] }
```

- `x-scope` — `synced` / `derived` / `local` / `account`, per field.
- `additionalProperties: true` — a dynamic dataset: undeclared keys are permitted and default to synced (the `objects` collection, chat and editor).
- `typeId` — the owning type for type-bound datasets; records exist only on objects carrying it.
- Runtime datasets add `required`, `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id` and `x-search` — see [Runtime datasets](runtime-datasets.html).

## Related

- [Types and properties](types-and-properties.html) — declaring kinds, formats and scopes.
- [System fields](system-fields.html) — the derived stamps every row carries.
