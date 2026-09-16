---
title: Data types
description: Property kinds, the xFormat descriptor and its slug vocabulary, the datetime instant on the wire, and the synced / local / account / derived scopes every field belongs to.
order: 40
---
# Data types

Every value in any has a structural **kind**, may carry a **descriptor** (`xFormat`) that says what the value means and how it renders, and belongs to a **scope** that decides who it syncs to. This page is the vocabulary.

## Kinds

| Kind | JSON on the wire | Notes |
|------|------------------|-------|
| `string` | `"Dune"` | |
| `number` | `1965` | |
| `boolean` | `true` | Never indexed for search. |
| `array` | `["sci-fi", "classic"]` | Filters compare against elements — see [Reading data](reading-data.html). |
| `object` | `{"…": "…"}` | Nested paths are addressed dotted: `<ownerId>.<propId>.sub`. |
| `datetime` | `{"$date": "2026-08-05T17:00:00.000Z"}` | An instant: unix milliseconds, orderable, index-keyable, computable. |
| `null` | `null` | |

Kind is pinned at the property's first write. Changing it means defining a new property.

## Datetime instants

Every timestamp the server stores — the derived `createdAt` / `modifiedAt` on object rows, chat `createdAt` / `modifiedAt` and reaction stamps, runtime-dataset `createTime` / `modifyTime`, space rows' `createdAt`, and any `datetime`-kind property (the `date` / `datetime` slugs) — is an instant. It reads back as `{"$date": "<RFC 3339>"}`, and writes accept either `{"$date": "<RFC 3339>"}` or `{"$date": <unix millis>}`.

```json
{ "<typeId>": { "<propId>": { "$date": "2026-08-05T17:00:00.000Z" } } }
```

Filter literals take the same shape:

```json
{ "modifiedAt": { "$gte": { "$date": "2026-01-01T00:00:00Z" } } }
```

> **Note.** A bare number or string in a date comparison does not error — it answers empty. Ordering comparisons are bracketed by type: a number literal only ever compares against numbers, so `{"$gte": 1700000000}` matches no instant, and neither does `$lt` or a bare ISO string. Always wrap the literal.

Instants are what the aggregation date operators (`$year`, `$dateTrunc`, `$dateDiff`) compute on — see [Aggregation](aggregation.html). A `date` is a calendar day encoded as midnight UTC — format it in UTC, never in local time.

## The descriptor

`kind` is the guarantee: it is pinned and every peer validates values against it. `xFormat` is a hint: one object holding everything descriptive — the semantic slug, icon, display order, option set, relation targets, per-format config — stored opaquely by the SDK and validated only by the server at its write boundary. Every path under it is mutable.

```json
"xFormat": {
  "type":     "choice",
  "icon":     "tag",
  "pos":      "a6",
  "options":  { "<key>": { "name": "…", "color": "…", "pos": "…" } },
  "relation": { "targetTypes": ["…"], "filter": "<json text>" },
  "config":   { "multiple": true }
}
```

Those six keys and `links` are the ones the server interprets; any other top-level key is a vendor namespace stored verbatim. `links` marks a value the [link index](../types/links.html) scans for `any://` references — `link` (one string), `links` (an array), `markdown` (text) or `none` (never scanned); the `relation` and `markdown` slugs imply it. `validate` and `compute` are reserved. The slug set is open — an unknown `type` renders structurally from `kind` and gets no value checks — and this is the documented vocabulary:

| `type` | `kind` | Value the server accepts | Extras |
|--------|--------|--------------------------|--------|
| `text`, `longtext`, `phone` | `string` | Any string. | |
| `markdown` | `string` | Any string — inline markdown. | Scanned for `any://` references by the link index. |
| `url` | `string` | An absolute URL with a scheme. | |
| `email` | `string` | One `local@domain`. | |
| `choice` | `array` | Option keys — one unless `config.multiple`. | `options.<key>` = `{name, color, pos, meta?}`; the key is the stored value. |
| `relation` | `array` | Plain `any://<objectId>` URIs — no space segment, no fragment; one unless `config.multiple`. | `relation.targetTypes` (type xKeys), `relation.filter` (a query condition as JSON text). |
| `number`, `currency`, `percent`, `duration` | `number` | A number. | `config` display settings (`decimals`, `currency`, `unit`, …). |
| `rating` | `number` | A number; within `0..config.max` when `max` is set. | |
| `checkbox` | `boolean` | A boolean. | |
| `date` | `datetime` | An instant at midnight UTC. | |
| `datetime` | `datetime` | Any instant. | |
| `period` | `object` | `{from?, to?}` instants, at least one, end on or after start. | Written whole — one value. |
| `money` | `object` | `{amount, currency}` exactly. | Written whole. |
| `geo` | `object` | `{lat, lng}` in range exactly. | Written whole. |

A value that does not fit the property's current slug is `400 property.format_violation` (`details: {propId, format, reason}`); a descriptor that does not fit the kind, a reserved key or an unparseable filter is `400 property.format_invalid`. Option membership is not enforced on `choice` values and no object-existence or object-type check runs on `relation` values — both are dangling-tolerant by design. `tags` is reserved. The full contract, including the client-side rendering and tolerance rules, is the server's `docs/27-descriptors.md`.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/types/$TYPE/properties \
  -H 'Content-Type: application/json' \
  -d '{"name": "Related", "xKey": "related", "kind": "array",
       "xFormat": {"type": "relation", "config": {"multiple": true},
                   "relation": {"targetTypes": ["book"]}}}'
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

The scope of every dataset field is discoverable. `GET /v1/spaces/:spaceId/datasets` returns one JSON Schema document per dataset the space hosts; `GET /v1/datasets` covers the account-level system datasets (`spaces`, `profile`, `devices`, …).

```bash
curl http://127.0.0.1:7001/v1/spaces/$SPACE/datasets
any datasets $SPACE
```

```json
{ "datasets": [ { "name": "chat_messages", "module": "chat", "shared": true, "owners": ["<typeId>"],
  "schema": { "type": "object", "additionalProperties": true,
    "properties": {
      "text":      { "type": "string",  "x-scope": "synced" },
      "creator":   { "type": "string",  "x-scope": "derived" },
      "unread":    { "type": "boolean", "x-scope": "local" } } } } ] }
```

- `x-scope` — `synced` / `derived` / `local` / `account`, per field.
- `additionalProperties: true` — a dynamic dataset: undeclared keys are permitted and default to synced (the `objects` collection, chat and editor).
- `owners` — the types whose parts declare the collection; records exist only on objects carrying one of them. `module` names the serving module (`records`, `editor`, `chat`) and `shared` marks a module's canonical collection.
- `description` and `x-format` — the descriptive slice of each field, in the same vocabulary as a property's `xFormat`.
- Runtime datasets add `required`, `x-mutable-by`, `x-stamp`, `x-delete-by`, `x-id` and `x-search` — see [Runtime datasets](runtime-datasets.html).

## Related

- [Types and properties](types-and-properties.html) — declaring kinds, formats and scopes.
- [System fields](system-fields.html) — the derived stamps every row carries.
