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
identity. Bypassing it would skip all of that. The write-shaped
exceptions are the `…/editor/markdown` routes, which are render/import
*transforms* over `editor_blocks`, not dataset writes. Pick by change
shape:

- **Targeted change** ("tick this box", "fix this line") →
  `PATCH …/editor/markdown` with `{edits: [{oldText, newText}]}`.
  Never do `GET → string-replace → PUT`: the PATCH matches
  server-side against the current state, so it can't clobber
  concurrent edits and a stale quote fails loudly
  (`markdown.no_match` → re-`GET` and quote the exact text).
- **Full rewrite / import** → `PUT …/editor/markdown`.
- **Tail growth** (logs, transcripts) → `POST …/editor/markdown/append`.

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
  (`property.kind_mismatch`, `property.immutable`) are not wired into
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

- **"Recently modified" lists sort on `modifiedAt`.** Every objects-collection
  row carries the derived row-root stamps `author` / `createdAt` /
  `modifiedAt` / `spaceId`; `{"sort": ["-modifiedAt"]}` is the recency
  ordering (`-createdAt` for creation order). `modifiedAt` bumps on any
  synced write to the object and converges across peers, but it's the
  author's wall clock — fine for sorting and display, never a sync fence.

- **Timestamps are instants, not numbers.** Every server-stamped time —
  the row-root stamps, chat `createdAt` / `modifiedAt`, runtime-dataset
  stamps — and every property declared with the `date` / `datetime`
  format reads back as `{"$date": "2026-08-05T17:00:00.000Z"}`. Unwrap
  the one key (`new Date(v.$date)`), and use the same shape in filter
  literals and writes: `{"modifiedAt": {"$gte": {"$date": "…"}}}`. A bare
  string or number does not error — comparisons across types go by type
  rank and instants rank above both, so `$gte` matches every row and
  `$lt` matches none. A range filter that forgets the wrapper returns a
  wrong answer, not an empty one.

- **Aggregate server-side instead of reducing client-side.** Counts per
  group, top-N rollups, tag distributions: don't page the whole dataset
  over HTTP — POST a pipeline to the sibling `…/aggregate` endpoints
  (same two scopes, snapshot-only). Put `$match` first so it runs on an
  index, and mind the deliberate MongoDB divergences (group key comes
  back as `id`; compute operators are a closed set). Date operators work
  on stored instants — `$dateTrunc` by month is a real group key. See
  `14-aggregation.md`.

## 4. Chat: newest-first reads and backward pagination

**Where `<chatObjectId>` comes from:** register the space's chat as a
bundle and use the root it returns — `POST /v1/spaces/:spaceId/bundles`
with
`{"id":"general-chat/v1","name":"General","rootTypes":["chat"],"derived":true}`.
The call is adopt-or-install, so every client lands on one object
instead of each creating its own, and `derived` makes that object's id
a function of the bundle id — computed offline, identical on every
device and member, so the chat cannot fork even when two sides install
while apart (the 1-1 case, where neither participant is the owner). The
trade is permanence: a derived root can never be deleted. Additional
purpose-specific chats get their own bundle id. Full guidance:
`16-chat.md` § Finding the chat object, `03-api.md` § Bundles.

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
object properties — pipeline in `13-index.md`, wire shape in
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
  names/descriptions), `chat`, `props` (user property values,
  default-on, FTS-only, `"<prop name>: <value>"` entry text), and
  whatever scopes property `meta` flags mint (e.g. `agent`). An
  unknown-but-valid scope returns no hits; a malformed one is `400
  search.bad_scope`. A content-only search passes `scopes` without
  `props`.
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

The reply's `spaceType` is `"any.onetoone"` — that is how you tell a
direct chat from a regular space in any list (the on-wire `type` matches,
but classify on `spaceType`). A 1-1 space carries
no chat object of its own — register one as a bundle (§ 4). It has
no owner to arbitrate, so the two clients agree out of band that the
**initiating** side ensures the bundle and the other adopts it; both
then read and write the same root.

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

## 8. Members-with-roles vs. the identities directory

Two surfaces resolve "who is this person," and they answer different
questions — don't conflate them.

- **A space's roster, with rights** → `GET /v1/spaces/:id/members`. Each
  row carries the member's `permission`
  (`owner`/`admin`/`writer`/`reader`) **and** their profile (`name` /
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

The directory carries **no rights** — it has no permission field by
design. To show "Alice is an admin of space X," read space X's members
list; iterate `IdentityInfo.spaceIds` if you need her role in each shared
space. There is no cross-space role rollup (and per the 1:1 SDK-mapping
invariant the server won't synthesize one).

**Tolerate empty names.** Profiles are encrypted and decryptable only by
contacts who received the key through a shared space's ACL or a 1-1
invite. A freshly-seen contact — or any contact on a freshly-restored
device, before background resolution completes — surfaces **id-only**
(empty `name`). Render a fallback (truncated id, generated avatar) and
let the `updated` subscribe frame fill it in. Never block UI on a
resolved name. (Do not invent a degraded "fetch the raw profile bytes"
path — there isn't one; the directory *is* the resolution surface.)

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
**synchronously inside the request** (with a reachable broker a 10 MB
attach takes roughly its object-store upload time and returns
`durable: true`); when the broker is unreachable/refusing, attach
returns fast with `durable: false` and a persistent queue retries in
the background. Either way, don't block the UI on `durable`. For a
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
`GET /v1/account`). Two kinds:

- **Built-in bundles** the server ensures at boot — today `favorites/v1`
  (guide: `25-favorites.md`). Don't install these: ensure with a
  built-in id returns `409 bundle.reserved`. Read the root id off
  `GET /v1/spaces/<techSpaceId>/bundles` and write records.
- **Your own bundles** for app-specific account data:
  `POST …/bundles` with `{"id": "<app>/v1", "derived": true,
  "datasets": [...]}` — idempotent (first call installs, later calls
  adopt), the root id identical on every device. Keep `bundle.rootId`:
  it is the `objectId` for every record call. The root is its own type,
  so additive evolution goes through `POST …/types/<rootId>/datasets`.

Dataset names are unique per space: part of the bundle's versioned
vocabulary, chosen once — `favorites/v1` owns `entries` the way it owns
its id, and a future bundle picks names that don't collide. Tree edge
cases — an entry whose folder is removed, a move that forms a cycle
across devices — are read-side product rules: compute the same view
from the same records everywhere, never repair with writes.

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
