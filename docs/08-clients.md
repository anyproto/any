# Client recommendations

The call patterns that keep a client correct and cheap on top of the
contract in `03-api.md` (endpoints + bodies) and `04-events.md` (SSE
lifecycle). Read those for the wire shapes; read this for *how to use
them*.

## 1. Writes go through the module's handler methods

Module collections are written **only** through their bespoke handler
endpoints — never through a generic write path:

- chat: `POST/PATCH/DELETE /v1/spaces/:s/objects/:o/chat/messages[/:msgId]`
  and `…/:msgId/reactions/:emoji`
- editor: `POST/PATCH/DELETE /v1/spaces/:s/objects/:o/editor/:collection/blocks[/:id]`
  — `:collection` is `editor_blocks` for the shared body, or the
  namespaced `<typeId>_<key>` of a part with its own editor

The endpoints build the ops the module's handler accepts — it stamps
server-owned fields (`creator` / `createdAt` / `modifiedAt`), enforces
author-only edit/delete and keys reactions per identity — and run the
write's side effects (a chat send notifies push). The write-shaped
exceptions are the `…/editor/:collection/markdown` routes, which are
render/import *transforms* over the editor collection, not dataset
writes. Pick by change shape:

- **Targeted change** ("tick this box", "fix this line") →
  `PATCH …/editor/:collection/markdown` with `{edits: [{oldText, newText}]}`.
  Never do `GET → string-replace → PUT`: the PATCH matches
  server-side against the current state, so it can't clobber
  concurrent edits and a stale quote fails loudly
  (`markdown.no_match` → re-`GET` and quote the exact text).
- **Full rewrite / import** → `PUT …/editor/:collection/markdown`.
- **Tail growth** (logs, transcripts) → `POST …/editor/:collection/markdown/append`.

An editor that renders empty paragraphs must emit and parse blank
runs the way the markdown routes encode them — one blank line
separates two blocks, each further blank line is an empty paragraph,
and an edge run has no separator to build on. Serialize and parse
have to be exact inverses, or every load reshapes the document and
saves the difference back. Full rule in `03-api.md` § Empty
paragraphs.

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
create time, or later through `POST …/properties/:objectId/attach/:typeId`:

```
POST /v1/spaces/:spaceId/objects
{ "types": ["page"], "initialProperties": { "any": { "name": "Notes" } } }
```

- **Don't call a dataset write endpoint unless the target object carries
  a type that declares the collection.** A collection — `chat_messages`,
  `editor_blocks`, a namespaced `<typeId>_<key>` — lives on an object
  only while one of its `any.types` has a part declaring it
  (`03-api.md` § Parts and modules); a write without one is `400
  dataset.not_declared`, and no write attaches a type for you. Resolve
  the declaring types once per space from `GET /v1/spaces/:id/datasets`
  (the collection's `owners`), check the object's `any.types` (read its
  row from the per-space `objects` collection) before writing, or create
  the object with the type bound up front. Don't fire the write and hope.
  A document type is the built-in `page` (plain body, no properties)
  or any type with an editor part — for a type an app ships, the
  catalog's (`28-well-known-bundles.md`), so every client and device
  converges on one.

- **Preflight-validate property values against the bound type's property
  definitions.** Fetch them once per type:

  ```
  GET /v1/spaces/:spaceId/types/:typeId/properties
  ```

  Each entry is a `PropertyDef` (`kind`, `xFormat`, and for object
  kinds nested `items` / `properties` / `required`). The server refuses
  a bad write whole: a value of the wrong `kind` is `400
  property.kind_mismatch`, an undeclared property id `400
  property.not_found`, a type the object does not carry `400
  dataset.validation`, and a value that does not fit the descriptor's
  current slug `400 property.format_violation` (`27-descriptors.md`).
  Check the same rules client-side so a form reports the problem
  before the round trip.

## 3. Reads go through query / subscribe

One read path per dataset: a snapshot via `POST /v1/spaces/:id/query`, or a
live stream via `POST /v1/spaces/:id/query/subscribe`. For per-object
collections pass `objectId` + `dataset` — the collection name
(`chat_messages`, `editor_blocks`, a namespaced `<typeId>_<key>`; read
them off `GET /v1/spaces/:id/datasets` or the type's `…/parts`); the
cross-object firehose is `POST /v1/spaces/:id/objects/query[/subscribe]`.
Body shape (filter / sort / limit / offset / includeTotal / projection /
mailboxCapacity / driftBudgetPercent) is in `03-api.md`; SSE frame
lifecycle is in `04-events.md`.

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

- **"Recently modified" lists sort on `modifiedAt`.** Every objects-collection
  row carries the derived row-root stamps `author` / `createdAt` /
  `modifiedAt` / `modifiedBy` / `spaceId`; `{"sort": ["-modifiedAt"]}` is the
  recency ordering (`-createdAt` for creation order). `modifiedAt` bumps on any
  synced write to the object and converges across peers, but it's the
  author's wall clock — fine for sorting and display, never a sync fence.

- **"Modified by X" comes from the same change as `modifiedAt`.** Render the
  two together — the pair always names one change, never one change's time
  beside another's signer (`03-api.md` § Data plane for the convergence
  rules). `modifiedBy` is an account identity in the encoding used by
  `author`, chat `creator`, `identity` in `GET /v1/spaces/:spaceId/members`
  and `id` from `GET /v1/account`: resolve name and icon through the members
  list, and through `GET /v1/identities/:identity` for a past writer who has
  since left the space. Sort and page on `modifiedAt` (indexed); filtering on
  `modifiedBy` scans. A row without it is one not yet rebuilt, or a change
  whose signer is unknown — not "nobody modified it".

- **Timestamps are instants, not numbers.** Every server-stamped time —
  the row-root stamps, chat `createdAt` / `modifiedAt`, runtime-dataset
  stamps — and every `datetime`-kind property (the `date` / `datetime`
  slugs) reads back as `{"$date": "2026-08-05T17:00:00.000Z"}`. Unwrap
  the one key (`new Date(v.$date)`), and use the same shape in filter
  literals and writes: `{"modifiedAt": {"$gte": {"$date": "…"}}}`. A bare
  string or number does not error — ordering comparisons are bracketed
  by type, so a bare literal never compares against an instant and a
  range filter that forgets the wrapper comes back empty.

- **Ordinary lists exclude the bin.** An object moved to the bin carries
  the built-in `bin` type (`03-api.md` § Types → Built-in hidden types);
  every list, tree and picker adds `{"any.types": {"$nin": ["bin"]}}` to
  its filter, and the bin view is `{"any.types": "bin"}` sorted
  `-bin.movedAt`, rendering `bin.movedBy` through the members list like
  `modifiedBy`. Move and restore are the plain
  `…/properties/:objectId/attach/bin` / `detach/bin` calls — the server
  stamps and clears the two properties — and permanent deletion stays
  `DELETE …/objects/:id`. `/search` does not know about the bin: a
  binned object's text still surfaces as a hit, and a hit carries no
  types, so drop binned hits by reading the hit's object row (or its
  `any.types` from a cached list) before rendering.

- **Aggregate server-side instead of reducing client-side.** Counts per
  group, top-N rollups, tag distributions: don't page the whole dataset
  over HTTP — POST a pipeline to the sibling `…/aggregate` endpoints
  (same two scopes, snapshot-only). Put `$match` first so it runs on an
  index, and mind the deliberate MongoDB divergences (group key comes
  back as `id`; compute operators are a closed set). Date operators work
  on stored instants — `$dateTrunc` by month is a real group key. See
  `14-aggregation.md`.

## 4. Chat: newest-first reads and backward pagination

**Where `<chatObjectId>` comes from:** the space's one chat is the
catalog's `general-chat` usecase — `POST /v1/catalog/general-chat/setup
{"spaceId": …}` — and `<chatObjectId>` is the `rootId` of its
`system:general-chat/v1` bundle in the reply. The call is
adopt-or-install, so every client lands on one object, and the root is
derived — its id is a function of the space and the bundle id,
computed offline, identical on every device and member, so the chat
cannot fork even when two sides install while apart (the 1-1 case,
where neither participant is the owner). The `chat` module is reserved
to the server: no client declares a chat part, and the general-chat
root is the only object that may carry its type, so there is no other
chat to find. Full guidance: `16-chat.md` § Finding the chat object for
a space, `03-api.md` § Chat.

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
see `04-events.md` § Contract that clients must respect). A separate up-front `query`
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
  § Contract that clients must respect). For the two load-shedding reasons, the reopened
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
  server. The real cost of a large window is client memory.

- **Cross-check client state against the DB when debugging.** `any-store-cli`
  reads the same local DB that backs `any-store`. Sort a collection by
  `-_ver.id`, mutate a record, re-query, and watch the new value land with its
  own version — the same CRDT-with-versions shape the change arrives in over
  the wire. Client in-memory state should layer versions the way the DB does,
  so the DB is the reference when reconciling a divergence.

## 6. Search: hybrid by default, read `vectorStatus` before trusting recall

`POST /v1/spaces/:spaceId/search` searches the server's local index
(BM25 full-text + semantic vectors over chats, editor blocks, and
object properties — pipeline in `13-index.md`, wire shape in
`03-api.md` § POST /v1/spaces/:spaceId/search).

```json
POST /v1/spaces/:spaceId/search
{ "query": "what did we decide about the reranker?",
  "scopes": ["chat", "agent"],     // optional scope slugs (open set)
  "limit": 10,                     // records; default 10, max 100
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
  down, still loading its model, or slower than the server's query
  budget (`index.search.queryEmbedTimeout`, default 5 s) — results are
  lexical-only *right now*, retry may differ;
  `disabled` means this server never runs vector search — don't
  retry, adjust your query style to lexical; `skipped` is the echo of
  your own `mode: "fts"`.
- **Hits carry identity, not full records.** `{scope, objectId,
  dataset, recordId, chunk?, data, dataOffset?, dataTotal, score}` —
  `data` is a ≤ `maxData`-rune window (default 512) of the indexed
  text around the first matching term; `dataOffset` / `dataTotal`
  locate it in the chunk's full text, and `maxData: -1` asks for the
  whole chunk. Long records index as several chunks; the hit is the
  record's best-ranked one (`chunk` says which), one hit per record,
  and `limit` counts records — no client-side dedupe. Ask for
  `passages: N` (max 10) to get the record's next best matching chunks
  on `hit.passages`, same window fields. To hydrate the full record,
  query the dataset: `POST /query` with `dataset = chat_messages /
  editor_blocks` filtered by `id == recordId`. For `dataset == "prop"`
  hits (property values), `recordId` is the propId — or the reserved
  `name` / `description` for the built-ins — and the value lives on
  the object: `GET /properties/:objectId`.
- **Scopes are an open set** of slugs: `basic` (blocks, object
  names/descriptions), `chat`, `props` (user property values,
  default-on, FTS-only, `"<prop name>: <value>"` entry text), and
  whatever scopes a property's `meta.index` or a runtime dataset's
  `search.scope` names (e.g. `agent`, `meetings`). An
  unknown-but-valid scope returns no hits; a malformed one is `400
  search.bad_scope`. A content-only search passes `scopes` without
  `props`.
- **Scores compare only within one response** (BM25 vs cosine vs RRF
  are different scales across modes). Rank, don't threshold.
- **Freshness model**: new writes are FTS-searchable within ~the
  debounce (250ms); the vector leg lags by one embed round. The index
  is local: a new or rebuilt index backfills every record the server
  holds, and until the backfill finishes `/query` is the exhaustive
  read.
- Errors: `409 index.disabled` (indexer off on this server), `400
  index.no_embedder` (`mode: "vector"` on an FTS-only server), `503
  index.embedder_unavailable` (`mode: "vector"` while the embedder is
  down, loading, or over the query budget — retryable).

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

The reply's `spaceType` is `"any.onetoone"` — that is how you tell a
direct chat from a regular space in any list (the on-wire `type` matches,
but classify on `spaceType`). Its chat is the general chat, set up by
both sides (below).

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

The 1-1's chat is the same general chat as everywhere else: both
participants run `POST /v1/catalog/general-chat/setup {"spaceId": …}`
(§ 4) — the initiator right after creating the space, the acceptor
after accept — and land on the one derived root on the first attempt;
neither side is refused while the other is unsynced (`16-chat.md`
§ Finding the chat object for a space).

## 8. Members-with-roles vs. the identities directory

Two surfaces resolve "who is this person," and they answer different
questions — don't conflate them.

- **A space's roster, with rights** → `GET /v1/spaces/:id/members`. Each
  row carries the member's `permission`
  (`owner`/`admin`/`writer`/`reader`/`guest`) **and** their profile (`name` /
  `iconCid`) **and** `status` — everything a "Members" panel with avatars
  and roles needs, in one call. This is the authoritative source for
  roles. Subscribe to `…/members/subscribe` for live role/membership
  changes.

- **A display name/icon for any account id you hold** (a chat message's
  `creator`, a 1-1 peer, a mention) → `GET /v1/identities[/:identity]`,
  the account-global directory. Cache it once and resolve ids across
  every space. Subscribe to `/v1/identities/subscribe` to keep the cache
  fresh as profiles resolve.

```
GET /v1/spaces/:id/members        # roster + roles for ONE space
GET /v1/identities                # global id → profile, all spaces
GET /v1/identities/:identity      # one contact (404 if never seen)
```

The directory carries **no rights** — it has no permission field. To
show "Alice is an admin of space X," read space X's members list;
iterate `IdentityInfo.spaceIds` if you need her role in each shared
space. There is no cross-space role rollup.

**Tolerate empty names.** Profiles are encrypted and decryptable only by
contacts who received the key through a shared space's ACL or a 1-1
invite. A freshly-seen contact — or any contact on a freshly-restored
device, before background resolution completes — surfaces **id-only**
(empty `name`). Render a fallback (truncated id, generated avatar) and
let the `updated` subscribe frame fill it in. Never block UI on a
resolved name; the directory is the only resolution surface.

## 9. Files: attach fire-and-forget, download as plain HTTP

The full model is `17-files.md`; the call pattern for a client that
attaches and renders files:

**Attach and move on.** Upload with the raw-body POST and treat the
201 as done — registration is durable in the CRDT immediately:

```
POST /v1/spaces/:id/objects/:objId/files?name=photo.jpg
Content-Type: image/jpeg
<raw bytes>
→ 201 {fileId, size, inline, durable:true|false, cached:true, …}
```

Know what attach latency includes: the network backup is attempted
**synchronously inside the request** (with a reachable broker the attach
takes the upload time and returns `durable: true`); when the broker is
unreachable/refusing, attach returns fast with `durable: false` and a
persistent queue retries in the background. Either way, don't block the UI on `durable`. For a
"not backed up" badge, hold `GET …/files/stats` and refresh it on
`GET …/files/subscribe` events (state `inflight`/`limited` →
`durable`). `limited` means the network refused for quota — offer a
retry (`POST …/files/:fileId/retry`) after the user frees space.

**Receiving a file another member sent.** There is no push
notification channel to build — the file IS space data. Subscribe to
the object's payload rows
(`POST …/objects/:objId/files/query/subscribe`): the sender's file
shows up as an `added` row, and — the part that matters — the moment
it becomes fetchable shows up as an **update on the same row when
`networkSign` lands** (the broker's custody receipt is a synced
cleartext row field; usually the row arrives already signed, since the
sender's attach completes the backup synchronously). Then GET
`…/files/:fileId/content`. Downloading before that point returns
`409 file.not_available` — a retry-later state, not an error to
surface. Two things that do NOT signal remote availability: the
per-space `GET …/files/subscribe` status stream (deliberately local
transitions only) and polling in a tight loop (just wait for the row
update). Names/mime for rendering come from `GET …/files/:fileId`
(the payload rows carry only the cleartext fields).

**Render by URL, not by API call.** The content endpoint is a regular
HTTP resource with correct mime, filename, and Range support — point
media elements straight at it and let the browser stream/seek:

```html
<img   src="http://127.0.0.1:7001/v1/spaces/SP/files/F1/content">
<video src="http://127.0.0.1:7001/v1/spaces/SP/files/F2/content" controls>
```

Reference a file from a record as its `fileId` (plus whatever denorm
you want for instant rendering — size, mime, name are stable). For
thumbnails, attach the rendered image as a variant
(`?variant=thumb&variantOf=<fileId>`) and request
`…/content?variant=thumb`.

**Per-object file lists** come from `GET …/files?objectId=` (typed,
with names) or live from `POST …/objects/:objId/files/query/subscribe`
(cleartext rows — join names from a `GET /files` pass). The query path
404s (`file.not_found`) until the object's first attach — treat that
as an empty list, not an error.

**Deleting.** `DELETE …/files/:fileId` removes the file for every
member and cascades to its variants; a live files/query window sees
the row as a `removed`. It is not idempotent — an already-deleted id
404s, which a UI retry should treat as success. Local bytes are
reclaimed by cache GC, not synchronously (`17-files.md` § Delete).

## 10. Live-surface budget: subscribe to views, not data

The single most expensive thing a client can do is hold a wide live
surface. Each subscription costs the server a held window, a mailbox,
and event-build work on every write to the watched scope — and costs
the client a stream to drain and recover. History size is nearly free
(reads are indexed); *breadth of live surface* is what scales badly.
Budget it by principle:

- **State is queryable; subscriptions only tell you it changed.**
  Anything worth rendering or reacting to is materialized into rows
  and row properties (unread flags and counters are the worked
  example). Never keep a subscription open in order to *know*
  something — query for it on demand; subscribe only to *learn of
  changes* to what is currently rendered.

- **The live surface scales with what's on screen, not with how much
  data exists.** One subscription per open view: the visible list
  (one space-wide objects query), the open document or chat (one
  per-object subscription). NEVER one subscription per object of a
  collection — "subscribe to all chats/documents to watch them" is
  the canonical anti-pattern; a thousand objects must not mean a
  thousand streams. Closing a view closes its subscription.

- **Aggregates ride rows so that one list subscription covers them.**
  When you are tempted to fan out subscriptions to compute something
  across objects (unread totals, activity badges), the answer is a
  materialized property on the object row plus the list subscription
  you already hold. If the aggregate you need is not materialized,
  ask for it to be — do not fan out around the gap.

- **Durable consumers use cursors, not event streams.** Anything that
  must not miss changes (an indexer, a notifier, an exporter) persists
  its own cursor / high-water marks in its own storage, treats live
  events purely as a wake-up signal, and catches up by pulling
  since-cursor or diffing against a snapshot. Events may drop by
  design (overflow, drift, restarts); the recovery contract is always
  "re-pull / resubscribe", never "the stream is complete".

- **Recover from state, not from history.** After a disconnect, a lost
  cursor, or a generation change, rebuild from a snapshot of current
  rows — never by replaying the missed event gap. Snapshots are one
  indexed query; gap replay is unbounded.

Worked example of all five at once: desktop notifications across every
chat in every space — one objects subscription per space watching
counter properties, point queries for toast bodies, a client-local
last-notified marker as the cursor, snapshot-as-badges on boot. See
`16-chat.md` § Desktop notifications.

## 11. Mobile push: cache the space keys, decrypt without the server

A push notification arrives when `any` is probably NOT running — the
OS hands it to your notification extension (iOS NSE / Android
messaging service), and there is no server process to ask. Everything
you need to render it must already be cached natively.

Every space row carries the material as `SpaceInfo.push`
(`{spaceKey, encKey, encKeyId}`; omitted while the SDK hasn't derived
it yet — e.g. a join still pending):

```
GET /v1/spaces                        → upsert push of every row
POST /v1/spaces/query/subscribe       → rotations arrive as row updates
```

The receiver loop is three rules:

1. **Append-only key cache per space** — `{encKeyId → encKey}` in the
   OS keystore, readable from the notification process (iOS: shared
   access group, available after first unlock; Android:
   Keystore-wrapped prefs). Never delete old keys: late payloads carry
   the pre-rotation `keyId`.
2. **Refresh while alive** — upsert on every app foreground from
   `GET /v1/spaces`; hold the space-list subscribe stream while the
   app is open so a read-key rotation lands before the next push
   encrypted under it.
3. **On push**: look up the message's `keyId` → cache miss ⇒ show a
   generic "New message" and move on; hit ⇒ AES-256-GCM decrypt
   (12-byte nonce prefixed to the ciphertext, no AAD) and render from
   the payload JSON (`spaceId` / `chatId` / `senderName` / `text` are
   all in the plaintext).

Full contract — payload shape, key derivations, heart compatibility,
the security bound on a leaked `encKey` — in `20-push.md`
(§ Receiver-side keys).

## 12. Account-level data: bundles on the tech space

Anything private to the account, synced across its devices and spanning
spaces, is a bundle on the **tech space** (`techSpaceId` from
`GET /v1/account`). The startup contract:

1. **Read, don't ensure.** `GET /v1/spaces/<tech>/bundles` is LOCKED on
   registry convergence and replies with `synced`: when true, an absent
   bundle is definitively not installed; when false (cold offline
   device), treat absence as provisional and re-read after sync. Adopt
   by taking `rootId` off the row; subscribe to the raw `bundles`
   dataset for live updates.
2. **Ensure on first write.** `POST …/bundles` with `{"id": "<app>/v1",
   "hidden": true, "parts": [...]}` — a CREATED root minted by the
   server, self-typed on the tech space so the records live on it,
   hidden from pickers (nothing attaches it elsewhere), deletable
   (uninstall = `DELETE …/objects/<rootId>`). Idempotent: the first
   call installs, later calls adopt. Use a created root: the registry
   already converges installs, so `"derived": true` buys nothing but
   permanence. Derive only when a fork would be UNMERGEABLE (chat-like
   content — the server's own general chat is the canonical case), and
   accept the price: permanent, uninstallable.
3. **On a fork** (two devices installed while apart): the registry
   converges on one winner, the other lands in `losers`. Merge the
   loser's records into the winner through your own schema, then
   `POST …/bundles/:id/resolve` with the loser root id.

A bundle's records datasets are namespaced to its root: `favorites/v1`
declares an `entries` part and reads and writes the collection
`<rootId>_entries` (read the name off the parts list — guide:
`25-favorites.md`), so two bundles never collide on a key. Tree edge
cases — an entry whose folder is removed, a
move that forms a cycle across devices — are read-side product rules:
compute the same view from the same records everywhere, never repair
with writes.

## 13. Saved views: ensure the defaults, patch by path, one window per visible group

A client both *writes* saved views (`24-data-views.md`) as shared
configuration and *reads* them back on every render, so the call
patterns matter more than the record shape.

- **Bind the type once, at create where you can.** A new host object
  takes `{"types": ["dataview"]}` on `POST …/objects`; an existing one
  needs `POST …/properties/:objectId/attach/dataview`. Attach is
  idempotent, so calling it on every open is *correct but wasteful* —
  it is a DAG write. Attach when you first add a view, not when you
  open the object.

- **Ensure the default dataview and its default view, never
  create-on-open.** Two levels: a `dataviews` record (`default`,
  `{name, pos}`) and a `views` record (`default`, `{dataview:
  "default", name, layout, pos}`), each upserted under a fixed id so two
  devices opening the same object converge on one table with one view
  instead of minting two. **Then read `rejections`.** A deleted id is
  burned forever, and re-upserting it returns `200` with a rejection and
  creates nothing — a client that checks only the status code renders an
  empty view list with no error. On a rejection, fall through to the
  next id in a deterministic sequence (`default-2`, `default-3`, …);
  walking the same sequence everywhere is what keeps devices converging
  on the same replacement. View ids are one namespace per host, so a
  second dataview's views take `<dataviewId>.<key>` ids
  (`board.default`) and walk their own sequence.

- **Never offer to delete the last view — and delete a dataview's
  views yourself.** "At least one dataview with one view always exists"
  cannot be enforced server-side — the delete gate is per-record, not
  per-collection — so it is your rule; it also protects users from
  burning the well-known ids. Deleting a dataview does not cascade: its
  views stay as orphans (`{"filter": {"dataview": "<id>"}}` still finds
  them), so delete them in the same batch, or re-parent them with one
  `$set dataview`.

- **Patch by path; a root `$set` merges, it does not replace.**
  `{"type": "$set", "path": "layoutSettings.order", "value": [...]}`
  touches one path and bumps `modifiedAt`; a root `$set` of the whole
  record merges its keys and leaves `creator`, `createdAt` and your
  `localSettings` intact. Per-path writes still concurrent-merge better:
  two members retuning different parts of a view both keep their edit.

- **Autosave: debounce the synced half, and put churn in the local
  half.** Dragging a column boundary emits a write per frame if you let
  it. Column widths belong in `localSettings` (`"scope": "local"` —
  explicit record id, no upsert, no DAG change, invisible to other
  members); genuine shared intent — renames, filter changes, column
  visibility and order — goes to the synced fields, debounced. Render
  the merge of `layoutSettings` and `localSettings`, local winning per
  key.

- **One subscription per list, not one per view.** The dataview list is
  a `…/query/subscribe` window on `dataset: "dataviews"` sorted by
  `pos`; the active dataview's view list is a second window on
  `dataset: "views"` with `{"filter": {"dataview": "<id>"}}`, sorted
  by `pos` (indexed) — §10's budget applies unchanged. The *contents* of
  the active view are a third window; inactive dataviews and views cost
  nothing.

- **Save the query keyed by `propId`, and scope it by type.** `xKey`
  paths never reach the server, so a saved view keyed by xKey resolves
  for nobody; propIds also survive a property rename. And a saved filter
  must carry `{"any.types": "<typeId>"}` — `objects` holds every object
  in the space and the negation operators match field-absent rows, so an
  unscoped "status is not done" returns type definitions and bundle
  roots along with the rows you wanted.

- **Grouping is preflight-then-fan-out, one window per VISIBLE group.**
  There is no grouped query. On opening a view with a `groupBy`, run a
  preflight `/objects/aggregate` for the property's distinct values with
  counts; that answer decides whether the field is groupable at all (more
  distinct values than your column budget — a few dozen, well under
  `groupLimit` — or a `400 aggregate.limit_exceeded` means no, fall back
  to the ungrouped list without dropping the `groupBy`). Take columns
  from the property's option catalog in its `pos` order so empty options
  still get a column and columns don't reshuffle by count, then read each
  group through its own plain windowed query. Collapsed and off-screen
  groups get no window.

  `/aggregate` is snapshot-only, so nothing about grouping updates
  itself. Split it: the **column set streams** — subscribe to the
  `properties` dataset on the type object and a new or renamed option
  arrives live, no polling — while **counts and dangling keys** need the
  preflight re-run on a coalesced timer (tens of seconds) while the view
  is *visible*, plus immediately whenever the filter, the `groupBy` or
  the catalog changes. Stop the timer when the view is hidden.

  Two shapes bite here. A **multiselect** needs `$unwind` before
  `$group`, or you group by the whole array and get one column per
  distinct *combination*; counts then legitimately sum to more than the
  object count. And the **"no value" group** arrives as `id: null` for a
  single-value property but is *absent* for a multiselect (`$unwind`
  drops docs missing the field), so count it separately with
  `{"$exists": false}` — type-scoped, since that operator matches
  everything without the field.

- **Reconcile broken rules client-side.** `query` and `layoutSettings`
  are opaque to the server precisely so a rule naming a deleted property
  fails *soft*: you keep the view editable and mark the rule invalid,
  rather than the server rejecting the write. Options are
  dangling-tolerant the same way — a value can reference an option key
  that no longer exists in the catalog.

- **Timestamps are instants.** `createdAt` / `modifiedAt` read as
  `{"$date": "<RFC 3339>"}`; a numeric decode target silently yields
  zero, and a filter literal needs the same shape.

## 14. Auth: read the capability bits, hold the phrase, log in every launch

The server's ownership mode (`02-server.md` § Modes) decides what an
auth UI may offer. These rules are normative for every client:

1. **Read `GET /v1/auth` before rendering any auth affordance.** Show
   sign-out, account switching and quit only where the matching
   `capabilities` bit is true. Never infer a capability from the `mode`
   string.
2. **On a managed server the client owns the account list.** The server
   cannot enumerate keys it never stored; `accounts` is empty. Build the
   picker from your keystore.
3. **Store the recovery phrase.** It is the portable identity
   credential: it restores the account on any device and it is what
   the user backs up. Show it once at generation. It carries no device
   identity — the server caches the device key per account, so
   replaying the phrase keeps the same peerId.
4. **Delete the stored credential on sign-out**, or the account is not
   actually signed out on that device. A keystore wrapper without a
   delete is incomplete.
5. **Never write key material to a log, breadcrumb or crash report.**
6. **Treat `200 {alreadyAuthorized: true}` as success**, not an error
   path. On a **mnemonic** request it is also how you confirm a phrase
   you hold belongs to the running account, so it is safe to persist
   afterwards; an `accountId` request confirms only the id.
7. **Pass `replace: true` only on a deliberate user-initiated switch.**
   It invalidates every subscription held against the old account
   (`closed{reason: deauthorized}`).
8. **Handle `401 auth.required` at the transport layer**, not only at
   boot. A managed server can return to unauthorized mid-session; route
   back to the auth gate.
9. **Re-read the bound address after any lifecycle transition.** A
   `:0` server gets a different port on respawn.
10. **Never treat loopback as authenticated.** Any same-user process can
    reach the API; the mode gates express ownership, not
    authentication. A managed host keeps its control token to itself.
11. **A second engine for the same account on one device needs its own
    root.** Device identity is cached per `(root, account)`; sharing a
    root means sharing a datastore.

Managed hosts, per launch: spawn `run --mode managed --addr
127.0.0.1:0`, read `LISTENING` and `CONTROL_TOKEN` from stdout (or pass
`embedded.Options.Mode` + `ControlToken` in-process), `POST /v1/auth
{mnemonic}` with the token, and stop with `POST /v1/shutdown` + token.

## 15. Links panel: backlinks, forward links, refresh on the bus event

The contextual panel of an object, a block or a record is three reads
over the link index (`03-api.md` § Links and backlinks, pipeline in
`13-index.md` § Links):

```
GET /v1/spaces/:s/objects/:o/backlinks                       what links here
GET /v1/spaces/:s/objects/:o/backlinks?record=<b>&dataset=editor_blocks   what links this block
GET /v1/spaces/:s/objects/:o/links                           what this links to
```

Call patterns:

- **Render `object` and `parts` as two groups.** `object` is what
  points at the entity itself; `parts` what points at one of its
  blocks, messages or values. A block link is a block link — never
  count it as a second link to the page.
- **Show the kind.** `mention`, `link`, `card`, `embed`, `relation`;
  the set is open, so render an unknown kind as a plain reference.
- **Navigate by the source place.** `source.dataset` +
  `source.recordId` locate the block or message; `prop` + the property
  id, with `source.typeId`, locate a value. Build the deep link with
  `anyuri.BuildRecord` / the object form — never guess from the target.
- **Refresh on `links.updated`.** Subscribe to the device bus
  (`GET /v1/events/subscribe?scope=device&type=links.updated`) and
  re-read when `data.targets` names the entity's object key
  (`any://o/<sp>/<obj>`), or its own key for an identity or file —
  match on `targets`, never on the event's `spaceId`, which names the
  space the writing records live in. The
  index lags a write by the indexer's debounce, so read the panel on
  open and on the event, never assume the write is visible in the
  same tick.
- **Across spaces**, `GET /v1/backlinks?target=any://o/<sp>/<obj>`
  answers from every space this device indexes; a space absent from
  the reply has no edge to show. Names come from one
  `objects/query` with `id $in` per space.
- **Write links so the index sees them.** Object references in text
  are markdown links with `any://` destinations; a whole-line
  `[Name](any://o/…)` paragraph is a card; property references go
  into a `relation` property as bare `any://<objectId>` values — the
  only form a relation accepts, so a relation never points across
  spaces or at a block; a text link does. A runtime dataset field
  that carries references declares `xFormat.links`
  (`27-descriptors.md`), or the index never reads it; `xFormat.links:
  "none"` keeps a field out of backlinks on purpose.
- **Identity targets are per space.** `any://m/<sp>/<identity>` keys
  mentions in one space; "everywhere I am mentioned" is one
  `GET /v1/backlinks?target=` per space the client holds.
- **Above 500 backlinks the reply is a cut, not a page.** `truncated`
  says so; the order is the index's own (source object, dataset,
  record), not recency, and there is no continuation — a popular
  object shows an arbitrary 500. Narrow with `?kind=` or a part.

## See also

- `03-api.md` — endpoint catalog and request/response bodies.
- `04-events.md` — SSE frame lifecycle, `closed` reasons, capacity tuning.
- `06-errors.md` — error envelope and code namespace
  (`dataset.validation`, `property.kind_mismatch`, …).
- `13-index.md` — the search index: chunker contract, indexer pipeline,
  `/search` semantics.
- `14-aggregation.md` — aggregation pipelines: stage set, examples,
  limits, MongoDB divergences.
- `16-chat.md` — chat client guide: read tracking, the viewport rule,
  unread divider, client-side desktop notifications.
- `17-files.md` — files v2: tiers, durability states, cache/offload,
  variants.
- `20-push.md` — push notifications: sender model, payload wire shape,
  receiver-side key cache.
- `24-data-views.md` — saved views: record shape, what stays opaque,
  scope tiers, the grouping recipe in full.
