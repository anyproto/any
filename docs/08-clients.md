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

Every write returns the shared `api.ModifyResult`
(`{versionId, changeId, recordIds}`), never the record body —
`recordIds[0]` is the server-derived id on a create, and `versionId` is
the change's version. Use `versionId` to keep your own writes consistent
with the live stream: stamp `_ver.<op.path> = versionId` on the records
you just wrote so that when the matching `/query/subscribe` event arrives
you recognise it as your own and don't double-apply (and so you can order
it against remote changes). Read the resulting record back through
`/query` — the write never returns it.

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
  the rest. An unbounded read can produce a huge snapshot response or overflow
  a subscribe mailbox. Treat an unbounded read as a bug.

- **Page on an absolute cursor, not `offset`, for anything that mutates under
  you.** `offset` floats — row 50 becomes row 51 the moment a record lands
  ahead of it, so paging a live collection by offset silently skips and
  repeats rows. A `_ver.id` (or other indexed-field) cursor filter is
  absolute: the next page is `{ "<field>": { "$lt": <lastSeen> } }` with the
  same `sort`, and because the collection is indexed on it the page returns
  from disk directly. `offset` is fine only for a frozen, point-in-time
  snapshot you won't page across writes.

- `sort` is an array of field-path strings; a `-` prefix means descending.
  `filter` is mongo-style — operators include `$lt` / `$gt`. On `subscribe`,
  `sort` is required when `limit > 0`.

- **Aggregate server-side instead of reducing client-side.** Counts per
  group, top-N rollups, tag distributions: don't page the whole dataset
  over HTTP — POST a pipeline to the sibling `…/aggregate` endpoints
  (same two scopes, snapshot-only). Put `$match` first so it runs on an
  index, and mind the deliberate MongoDB divergences (group key comes
  back as `id`, no compute operators). See `14-aggregation.md`.

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

## 5. Live subscriptions: hold a window, recover by resubscribing

A `subscribe` stream is a moving window over the collection, not a feed you
accumulate. Treat it as one and the lifecycle stays simple.

- **Hold a window, not a database.** The `any-store` instance inside the
  server *is* the store. Keep the current window in memory and apply the
  stream's `added` / `updated` / `removed` deltas to it — no client-side DB,
  no mirror, no second copy to pour rows into and reconcile. A parallel store
  is overhead that re-implements what the subscription already gives you, and
  it's the thing that drifts out of sync with the wire.

- **Recover from a `closed` stream by resubscribing, not reconciling.** Every
  `closed` reason is terminal and means "open a fresh POST" (see `04-events.md`
  § "`closed` is terminal"). For the two load-shedding reasons, the reopened
  snapshot *already* reflects current state — rebuilding it from a giant delta
  is the expensive path the engine is deliberately refusing:
  - `drifted` — more than `driftBudgetPercent` of the window left *without
    replacements* (default 30 — ~15 rows of a 50-row window). Net departures
    count; churn that new arrivals backfill does not.
  - `overflow` — events arrived faster than the client drained the SSE mailbox
    (`mailboxCapacity`, default 256, min 16) — e.g. a cold reconnect against a
    busy collection.

  Both thresholds are request-tunable when a workload needs more headroom.
  Note the corollary to "always set a `limit`" (§3): drift detection is
  **disabled when `limit == 0`**, so an unbounded subscribe loses both the
  window auto-shift *and* the drift safety net — one more reason never to
  subscribe without a limit.

- **Window size is free on the server; the cost is the client's.** The server
  streams the window straight from the indexed DB and is indifferent to
  whether it holds 50 rows or 50,000 — size the window for the UI, not the
  server. The real cost of a large window is client memory. Optimise for
  correctness and stability first; a windowed read slower than ~100ms is
  by-design wrong and worth a bug report.

- **Cross-check client state against the DB when debugging.** `anystore-cli`
  reads the same local DB that backs `any-store`. Sort a collection by
  `-_ver.id`, mutate a record, re-query, and watch the new value land with its
  own version — the same CRDT-with-versions shape the change arrives in over
  the wire. Client in-memory state should layer versions the way the DB does,
  so the DB is the reference when reconciling a divergence.

## 6. Search: hybrid by default, read `vectorStatus` before trusting recall

`POST /v1/spaces/:spaceId/search` searches the server's local index
(BM25 full-text + semantic vectors over chats, editor blocks, and
agent-memory objects — pipeline in `13-index.md`, wire shape in
`03-api.md` § search).

```json
POST /v1/spaces/:spaceId/search
{ "query": "what did we decide about the reranker?",
  "scopes": ["chat", "agent"],     // optional scope slugs (open set)
  "limit": 10,                     // default 10, max 100
  "mode": "hybrid" }               // default; or "fts" / "vector"
```

Call patterns:

- **Default to `hybrid`.** It fuses lexical and semantic ranking
  (reciprocal rank) and degrades to FTS by itself when the embedder
  can't help. Only pin `mode: "fts"` for exact-term lookups (ids,
  names, error strings) or `mode: "vector"` when paraphrase recall
  matters more than precision.
- **Check `vectorStatus` in every reply** before drawing conclusions
  from an empty/weak result set: `used` means semantic recall
  participated; `unavailable` means the embedder is configured but
  down — results are lexical-only *right now*, retry may differ;
  `disabled` means this server never runs vector search — don't
  retry, adjust your query style to lexical; `skipped` is the echo of
  your own `mode: "fts"`.
- **Hits carry identity, not full records.** `{scope, objectId,
  dataset, recordId, data, score}` — `data` is the indexed text
  (per-record, short by construction). To hydrate the full record,
  query the dataset: `POST /query` with `dataset = chat_messages /
  editor_blocks` filtered by `id == recordId`. For `dataset == "prop"`
  hits (property values), `recordId` is the propId — or the reserved
  `name` / `description` for the built-ins — and the value lives on
  the object: `GET /properties/:objectId`.
- **Scopes are an open set** of slugs: `basic` (blocks, object
  names/descriptions), `chat`, and whatever scopes property `meta`
  flags mint (e.g. `agent`). An unknown-but-valid scope returns no
  hits; a malformed one is `400 search.bad_scope`.
- **Scores compare only within one response** (BM25 vs cosine vs RRF
  are different scales across modes). Rank, don't threshold.
- **Freshness model**: new writes are FTS-searchable within ~the
  debounce (250ms); the vector leg lags by one embed round. The index
  is local and "from the next change" — content that predates indexing
  on this server is not in it (use `/query` for exhaustive reads).
- Errors: `409 index.disabled` (indexer off on this server), `400
  index.no_embedder` (`mode: "vector"` on an FTS-only server), `503
  index.embedder_unavailable` (`mode: "vector"` during an embedder
  outage — retryable).

## 7. Direct (1-1) chats: derive by identity, approve incoming

A 1-1 (direct) space is shared by exactly two identities and derived from
both account keys — both peers compute the *same* spaceId, so there is no
invite token to pass. The peer's identity is the `id` from their
`GET /v1/account`, exchanged out-of-band (QR, link, a shared space's
member list). Full contract: `03-api.md` § One-to-one (direct) spaces.

**Open / reach out** — POST the peer's identity; the space is active
immediately (implicit self-approval). Idempotent — calling it again (or
after a decline) returns the same space:

```json
POST /v1/spaces/one-to-one
{ "otherIdentity": "<peer account id>" }     // → 201 SpaceInfo (status "active")
```

The reply's `spaceType` is `"anytype.onetoone"` — that is how you tell a
direct chat from a regular space in any list (the on-wire `type` matches,
but classify on `spaceType`).

**Discover incoming requests** — when someone reaches out to you, a
*pending* row appears (surfaced automatically by the server's inbox
notifier, or seeded by your app via `register-incoming` below). It is not
materialized or synced until you accept. Read pending requests off the
space list — they're hidden from the active-only default, so filter
explicitly:

```
GET /v1/spaces?status=one_to_one_pending        // snapshot
POST /v1/spaces/query/subscribe { "dataset": "spaces" }   // live (filter rows on status)
```

The pending row carries the peer's display hint (`name` / `iconCid`) for
rendering "Alice wants to chat" without syncing anything.

**Accept / decline** by the pending row's `id` (the server reads the peer
identity off the row — you don't re-derive it):

```
POST /v1/spaces/:spaceId/one-to-one/accept      // → 200 SpaceInfo (status "active"), materializes + syncs
POST /v1/spaces/:spaceId/one-to-one/decline     // → 204; synced sticky, suppressed on all your devices
```

Decline is account-wide and never auto-resurfaces; a later explicit
`POST /v1/spaces/one-to-one` with that identity un-declines and activates.

**Out-of-band discovery** — if your app learns of an incoming request
through its own channel (no coordinator inbox), seed the pending row
yourself; it's idempotent and never materializes storage:

```json
POST /v1/spaces/one-to-one/register-incoming
{ "peerIdentity": "<peer account id>",
  "displayHint": { "name": "Alice", "iconCid": "..." } }   // → 204
```

Once a 1-1 is active, everything else is identical to a regular space —
both members are writers, so create objects, send chat
(`dataset=chat_messages`), and subscribe exactly as in §1–5. Read the two
participants through the normal members collection. Deleting a 1-1 is
local-only and re-derivable: `DELETE /v1/spaces/:spaceId` offloads it, and
a later `POST /v1/spaces/one-to-one` brings it back.

## See also

- `03-api.md` — endpoint catalog and request/response bodies.
- `04-events.md` — SSE frame lifecycle, `closed` reasons, capacity tuning.
- `06-errors.md` — error envelope and code namespace
  (`dataset.validation`, `property.kind_mismatch`, …).
- `13-index.md` — the search index: chunker contract, indexer pipeline,
  `/search` semantics.
- `14-aggregation.md` — aggregation pipelines: stage set, examples,
  limits, MongoDB divergences.
