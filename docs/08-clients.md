# Client recommendations

How a well-behaved client should call this server. These aren't new
endpoints — they're the call patterns that keep a client correct and cheap
on top of the contract in `03-api.md` (endpoints + bodies) and
`04-events.md` (SSE lifecycle). Read those for the wire shapes; read this
for *how to use them*.

## 1. Writes go through the type's handler methods

Built-in datasets are written **only** through their bespoke handler
endpoints — never through a generic write path:

- chat: `POST/PATCH/DELETE /v1/spaces/:s/objects/:o/chat/messages[/:msgId]`
  and `…/:msgId/reactions/:emoji`
- editor: `POST/PATCH/DELETE /v1/spaces/:s/objects/:o/editor/blocks[/:id]`

The handler is what stamps server-owned fields (`creator` / `createdAt` /
`modifiedAt`), enforces author-only edit/delete, and keys reactions per
identity. Bypassing it would skip all of that. The lone write-shaped
exception is `PUT /editor/markdown`, which is a render/import *transform*,
not a dataset write.

## 2. Preflight-validate writes against the bound types

An object carries an `any.types` array — the type IDs bound to it. Bind at
create time:

```
POST /v1/spaces/:spaceId/objects
{ "types": ["chat"], ... }
```

(Dedicated runtime attach/detach SDK methods are planned but not landed yet —
bind at create for now.)

- **Don't call a dataset write endpoint unless the target object has the
  matching type bound.** A write to `chat_messages` / `editor_blocks` on an
  object missing `"chat"` / `"editor"` in its `any.types` is rejected by the
  SDK handler with `dataset.validation` (400). Check the object's `any.types`
  (read its row from the per-space `objects` collection) before writing, or
  create the object with the type bound up front. Don't fire the write and
  hope.

- **Preflight-validate property values against the bound type's property
  definitions.** v1 does **not** enforce property schema server-side —
  property writes are free-form. The client is responsible for keeping values
  aligned with the type contract. Fetch the type's properties:

  ```
  GET /v1/spaces/:spaceId/types/:typeId/properties
  ```

  Each entry is a `PropertyDef` (`kind`, `required`, nested `items` /
  `properties`). Validate kind and required-ness before writing. Don't rely
  on the server to reject a mismatch today — the SDK-level guards
  (`property.kind_mismatch`, `property.immutable_field`) are not wired into
  the v1 write path, so a malformed write succeeds now and bites later.

## 3. Reads go through query / subscribe

One read path per dataset: a snapshot via `POST /v1/spaces/:id/query`, or a
live stream via `POST /v1/spaces/:id/query/subscribe`. For per-object
built-ins pass `objectId` + `dataset` (`chat_messages`, `editor_blocks`, …);
the cross-object firehose is `POST /v1/spaces/:id/objects/query[/subscribe]`.
Body shape (filter / sort / limit / offset / includeTotal / mailboxCapacity /
driftBudgetPercent) is in `03-api.md`; SSE frame lifecycle is in
`04-events.md`.

- **Prefer `query` over `subscribe`.** Use the one-shot snapshot whenever you
  don't need live updates. It's cheaper, has no mailbox/drift lifecycle to
  manage, and can't close on overflow. Only open a `subscribe` stream when
  the client actually renders changes in realtime.

- **Always set `limit`.** Every read should carry a bounded `limit`, and page
  the rest with `offset` or a cursor filter. An unbounded read can produce a
  huge snapshot response or overflow a subscribe mailbox. Treat an unbounded
  read as a bug.

- `sort` is an array of field-path strings; a `-` prefix means descending.
  `filter` is mongo-style — operators include `$lt` / `$gt`. On `subscribe`,
  `sort` is required when `limit > 0`.

## 4. Chat: newest-first reads and backward pagination

Chat uses `-_ver.id` (descending) **uniformly** — initial view, live tail,
and history paging all sort the same way. `_ver.id` is the record's
`VersionId` at creation — its position in the any-sync DAG (the SDK's
lex-monotonic, per-change DAG order). Sorting by it orders messages by
**logical DAG order**, not wall-clock time. It's stamped once at creation
and never bumped by edits, so that order is stable across message edits.
Once you set a `limit` (rule above), descending is the *only* correct
direction — see the live-tail reasoning below.

**Open a chat view** — just subscribe. Don't `query` first and then
subscribe: the subscribe stream's `snapshot` frame *is* your initial
newest-N load (the windowed engine emits it atomically with registration —
see `04-events.md` § "snapshot arrives once"). A separate up-front `query`
refetches the same rows and opens a gap/dup race against the first
`changes` frame. Use a one-shot `query` (below) only when you *don't* want
live updates.

```
POST /v1/spaces/:spaceId/query/subscribe
{ "objectId": "<chatObjectId>",
  "dataset":  "chat_messages",
  "sort":     ["-_ver.id"],
  "limit":    50 }
```

The `snapshot` frame carries the newest 50; apply it, then apply each
`changes` frame (new messages in `added`, edits in `updated`, deletes /
reaction-offs in `removed`).

**Read without subscribing** — when you only need a point-in-time render
(no live updates), the one-shot query is the same window:

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<chatObjectId>",
  "dataset":  "chat_messages",
  "sort":     ["-_ver.id"],
  "limit":    50 }
```

**Page into history** — scrolling up past the window. Take the oldest
`_ver.id` you currently hold and `query` older messages (the subscribe
window only tracks the newest `limit`; older history lives behind a paged
query, not the stream):

```
{ "objectId": "<chatObjectId>",
  "dataset":  "chat_messages",
  "sort":     ["-_ver.id"],
  "filter":   { "_ver.id": { "$lt": "<oldestMessageVersion>" } },
  "limit":    50 }
```

Repeat until a short or empty page. Reverse each page client-side if you
render oldest-at-top.

**Why descending, not ascending:** the `limit`-sized window holds the top of
the sort. With `-_ver.id` that's the newest messages, so new arrivals enter
the window (and the oldest drops out as `removed`). Ascending would pin the
*oldest* `limit` and new messages would never appear.

## See also

- `03-api.md` — endpoint catalog and request/response bodies.
- `04-events.md` — SSE frame lifecycle, `closed` reasons, capacity tuning.
- `06-errors.md` — error envelope and code namespace
  (`dataset.validation`, `property.kind_mismatch`, …).
