# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Implementation slices landed:
1. **scaffolding + wallet + health** — `any init` / `any run` / `any status` /
   `any stop` / `any version` work end-to-end.
2. **SDK boot + space lifecycle** — Run opens `any-sync-sdk` (nodeconf via
   `config.LoadNodeconf`, default fallback: the embedded
   `internal/config/nodeconf-staging.yml`).
   Real routes: `GET /v1/health`, `POST /v1/shutdown`, `GET /v1/account`,
   `POST/GET/GET-:id/DELETE /v1/spaces`. Every other `/v1/spaces/**` route
   from `docs/03-api.md` is registered and returns `501 sdk.not_implemented`.
3. **`nav` virtual built-in + tree UI** — every new object is auto-stamped
   with `nav.type` / `nav.parentId` / `nav.pos` on create (`internal/nav`,
   `internal/server/handlers_objects.go::injectNavDefaults`). The web UI's
   left sidebar now renders an object tree (lazy-loaded via
   `POST /v1/spaces/:id/objects/query` filtered by `nav.parentId`); the
   space picker moved to the top of the right netlog sidebar as a
   `<select>` + popup form.
4. **Windowed query/subscribe over SSE** — reads always go through
   `POST /v1/spaces/:id/[objects/]query` (snapshot) or the matching
   `.../query/subscribe` (initial snapshot + live windowed deltas).
   Both wrap the SDK's `Query.Snapshot` / `Query.Subscribe` 1:1. Body
   shape: filter / sort / limit / offset / includeTotal /
   mailboxCapacity / driftBudgetPercent. SSE frames: `ready` →
   `snapshot{records,total}` → `changes[{versionId,added,updated,removed}]`
   → `closed{reason}` where reason is one of `server_shutdown`,
   `sdk_closed`, `overflow`, `drifted`. No `lagged` — windowed
   subs close on overflow/drift; recovery is "reconnect to get a
   fresh snapshot." `server.Run` cancels a per-process `shutdownCtx`
   and waits on `streamsWG` (10s deadline) so in-flight streams emit
   their terminal frame before the listener tears down. CLI:
   `any query-subscribe SPACE [OBJ] --dataset NAME [--filter …]
   [--sort …] [--limit N] [--total]` — one JSON line per frame on
   stdout. Wire format and contract in `docs/03-api.md` § Subscribe
   and `docs/04-events.md`. The old raw-stream endpoints
   (`/objects/:id/subscribe`, `/properties/subscribe`) and the SDK's
   `Space.Subscribe` / `Space.SubscribeProperties` are gone — the
   windowed primitive subsumes them.
5. **Members + invites + ACL over HTTP** — `Space.Members()` and
   `Space.ACL()` are now wired through. New handlers in
   `internal/server/handlers_members.go`, `handlers_invites.go`,
   `handlers_acl.go` expose:
   - `GET /v1/spaces/:id/members[/me|/requests|/subscribe|/:identity]`
   - `POST/GET/DELETE /v1/spaces/:id/invites[/:recordId]`
   - `POST /v1/spaces/join` (Service.Join — body carries the share token)
   - `POST /v1/spaces/:id/acl/{accept,decline,permissions,remove,add,
     ownership,self-remove,cancel-join,stop-sharing}`
   Permission/status enums map onto wire strings via
   `parsePermission` / `memberStatusString` /
   `spacePermissionString` (handlers_acl_common.go,
   handlers_spaces.go). Mint returns the share-friendly token
   (`space.EncodeInvite`); the joiner pastes the same string back to
   `/v1/spaces/join`. Static path segments (`/me`, `/requests`,
   `/subscribe`) are registered before the `:identity` wildcard so
   they don't get swallowed. `/subscribe` streams membership
   changes (added/changed/removed) over SSE using the SDK's
   `Members().Subscribe` callback — same pattern as sync-status.
   CLI: `any members ...` / `any invite ...` / `any join` /
   `any acl ...`. Web UI: new "Members" tab with invite-mint /
   members-list / join-requests / per-member permission picker /
   stop-sharing; sidebar gets a "join via invite" form. Body
   validation runs before resolveSpace so 400s don't pay for a space
   lookup.
6. **Chat built-in type** — `internal/chat` registers a `handler.Type`
   for per-object `chat_messages` records. Bespoke endpoints under
   `/v1/spaces/:id/objects/:objectId/chat/messages` cover writes only —
   send / edit / delete and the `…/:msgId/reactions/:emoji` toggle.
   Every write returns the shared `api.ModifyResult`
   (`{versionId, changeId, recordIds}`) — not the message body;
   `recordIds[0]` is the derived id on send. Reads go through `POST
   /v1/spaces/:id/query` with `dataset=chat_messages` (sort `_ver.id`);
   live updates through `POST /v1/spaces/:id/query/subscribe`. Reactions
   are stored emoji-first, identity at the leaf
   (`reactions.<emoji>.<accountId> = <ts>`) so the handler authorization
   is a single path-segment compare on the leaf (`op.Path[2] ==
   ctx.Change.Creator`). That storage shape is the read wire shape too —
   `/query`(`/subscribe`) return `reactions: {emoji: {accountId: ts}}`
   verbatim, no transpose. Server-stamped
   `creator` / `createdAt` / `modifiedAt` come from `sink.Derive`;
   handler rejects any client payload that tries to set them. Edit /
   delete enforce author-only via `ctx.Before.creator == ctx.Change.Creator`.
   Chronological order is the SDK's `_ver.id` creation marker (set
   once on creation, never bumped by edits — same role heart's `_o.id`
   plays). Liveness uses the per-object query/subscribe endpoint with
   `dataset=chat_messages`. CLI: `any chat send/list/edit/delete/react`.
   Optional create-only `agent` group (`{name, debugLink?, done}` —
   replaced the old `fromAgent` string) marks the message as
   agent-authored (UI hint only, not signature-verified); immutable
   post-create. `name` is the display label, `debugLink` an
   `any://<spaceId>/<debugObjId>#turn_<n>` drill-down into the run's
   debug page, `done` the liveness bool clients key a typing indicator
   on (false while the run is going; every run ends with done:true).
   Lets an agent subscribed to `chat_messages` filter to human-typed
   messages (`agent` absent) when deciding what to respond to.
   `any chat send --agent-name <name> [--agent-debug-link L]
   [--agent-done=false]`. Contract spec: task-agent-message-field.md +
   ../any-ui/docs/tasks/agent-message-field.md.
7. **Atomic blocks + markdown bridge** — `internal/editor` registers
   a `handler.Type` for the `editor_blocks` dataset, one record per
   block. Per-block fields: `type` (paragraph / heading / list_item /
   …), `style` (open-ended), `text` (INLINE markdown only — no block-
   level syntax), `nav.parentId`, `nav.pos` (lexid). Bespoke endpoints
   under `/v1/spaces/:s/objects/:o/editor/blocks` cover writes only —
   create / patch / delete. PATCH takes
   `{set: {"dotted.path": value}, unset: ["dotted.path"]}` for atomic
   per-path `$set` / `$unset`. Every write returns the shared
   `api.ModifyResult` (`{versionId, changeId, recordIds}`), same as
   chat — `recordIds[0]` is the derived block id on create; the block
   body is read back via query. Block ids are auto-derived from the
   change CID (same shape chat uses). Reads go through `POST
   /v1/spaces/:id/query` with `dataset=editor_blocks` (sort
   `nav.pos`); liveness through `POST /v1/spaces/:id/query/subscribe`.
   The markdown bridge — `GET/PUT /editor/markdown` — stays as the
   one render/import transform exception (LLM tooling and Export/
   Import .md flows depend on it). GET renders blocks → markdown;
   PUT parses markdown → diffs against the current block tree →
   emits per-record create / update / delete ops, returning
   `{inserted, updated, deleted, unchanged}`. `POST
   /editor/markdown/append` is the append-only fast path: it parses
   the fragment, looks up only the tail pos (no full-doc read, no
   diff), ensures the `editor` type is attached (one object-record
   read via `editor.EnsureType` — the SDK gates `editor_blocks` writes
   on type membership, so a first write to a fresh object needs it,
   same as PUT), and creates the new blocks in one ModifyBatch —
   O(chunk), not O(doc). Purely additive; same reply shape as PUT with
   only `inserted` populated (`markdown.Append` in `internal/markdown`).
   Grow-by-append pages (e.g. the agent debug log, via
   `anyHelper.appendToObject`) use it so a run of N appends is O(N),
   not O(N²). CLI: `any editor blocks create/patch/delete`.
8. **Per-space `spaceIndex` derived metadata** — the SDK now owns each
   space's `name` / `description` / `icon` in a derived in-space
   `spaceIndex` object (one per space, deterministic id) rather than
   in the per-account tech-space row. The write CRDT-replicates to
   every member; each peer's indexer hook mirrors the converged state
   back into its own local tech-space row, so `Service.List` rows
   update lockstep with peer renames. Wrapping surface:
   - `PATCH /v1/spaces/:spaceId` → `Space.SetMetadata` with pointer-
     to-string body `{name?, description?, iconCid?}` (absent =
     leave-as-is, empty-string = clear). Returns 204; the mirror is
     async so an immediate re-GET may briefly return stale values.
     At least one field required (`400 request.missing_field` if all
     absent).
   - `SpaceInfo.spaceIndexObjectId` — the deterministic derived id,
     populated on every single-space response and best-effort on
     `GET /v1/spaces` rows. Clients attach an SSE subscribe stream
     to that id's `objects` dataset for live updates.
   - CLI: `any space get <id>` / `any space update <id> --name=… …`
     (cobra `Changed` distinguishes "flag absent" from "flag set
     to empty"). `spaceType` is intentionally not patchable —
     pinned by the initial Create.
   - `POST /v1/spaces/:spaceId/sync` → `Space.SyncHeads`: forces an
     immediate head-sync (diff) round against responsible nodes
     instead of waiting for the ~30s periodic timer; blocks until the
     round completes, returns 204. CLI: `any space sync <id>`. Used by
     the multipeer e2e tests (`pollUntilSynced` in
     `internal/e2e/multipeer_test.go`) to collapse cross-peer
     convergence waits — sync writer then reader each poll tick.
9. **Debug surface** — the SDK's new `Space.Debug()` is wrapped at
   `GET /v1/spaces/:spaceId/debug` (per-peer headsync counters, in-
   memory) and `GET /v1/spaces/:spaceId/debug/objects/:objectId`
   (per-object tree + sync snapshot). Diagnostic only, **not stable**
   — production callers should use `/sync-status` once the SDK lands
   it. The per-object read locks the tree and walks every change, so
   it isn't a hot path. CLI: `any debug space/object`. Field-level
   docs in `internal/api/debug.go` and the SDK's
   `space/debug.go` / `internal/spaceimpl/debug.go`.
10. **Sync status + SSE state stream** — production sync state from
    the SDK's `Space.SyncStatus()` / `Service.{Status,SubscribeStatus}`.
    GETs: `/v1/spaces/:id/sync-status` (rollup),
    `/v1/spaces/:id/sync-status/objects/:objectId` (per-object;
    unknown ids return `state:"unknown"` rather than 404). SSE: two
    separate streams from the dataset-backed subscribe primitive —
    `/v1/sync-status/subscribe` (account-wide; sits outside the space
    group because `Service.SubscribeStatus` is account-scoped) and
    `/v1/spaces/:id/sync-status/objects/:objectId/subscribe`
    (per-object). Frame set: `ready` → `status` per transition →
    `lagged` on forwarder overflow → `closed` on shutdown. Reason
    strings shared with `/subscribe` so clients can switch on one
    reason set. `/sync-status/peers` stays 501 — SDK doesn't expose
    a stable peer list there yet; `/debug` is the diagnostic
    equivalent. CLI: `any sync-status space/object/subscribe`.

11. **Agent data layer (turns / chunks / memory)** — two new built-in
    types replace bobrik's markdown-transcript + runtime-typed memory
    scheme. `internal/agentlog` (type `agent_log`) puts two datasets ON
    THE CHAT OBJECT (multitype chat + agent_log, attached on first
    write): `agent_turns` — one append-only record per agent invocation
    (seq, userText, think, replies[], effects[], messageIds[],
    debugRef → agent_debug_log page, llm scalars; modify/delete
    rejected) — and `agent_chunks` — immutable summaries carrying
    EXPLICIT raw-range pointers (`fromSeq`/`toSeq` into agent_turns +
    periodStart/periodEnd unix). `internal/agentmem` (type
    `agent_memory`) puts `agent_memory_items` on a per-space brain
    object derived from the fixed seed `any/agent-brain/v1`
    (deterministic `Objects().Derive`, spaceIndex pattern): category
    (open slug set) + context required; tags/entities/keywords real
    arrays; confidence/importance/salience/accessCount numbers with
    server defaults; structured `edges` array; evolve allow-list
    (author-only, modifiedAt bumped); author-only delete. Indexes:
    turns (seq),(createdAt); chunks (seq),(periodEnd); items
    (category),(createdAt),(validFrom). Writes:
    `POST …/objects/:o/agent/turns|chunks`, `GET /agent/brain`,
    `POST/PATCH/DELETE /agent/memory[/:itemId]` (handlers_agentlog.go /
    handlers_agentmem.go); CLI `any agent …`. Reads stay on `/query` —
    no bespoke read endpoints. NO vectors stored — semantic search is
    an external service (TODO, not built; recall is non-functional
    until then; see docs/11-agent-memory.md + docs/07-roadmap.md §9).
12. **bobrik-watch** — JS-powered chat agent in `cmd/bobrik-watch/`.
    Full docs (storage shape, refresh mechanics, validation rules,
    flags, what's missing) in
    [`cmd/bobrik-watch/CLAUDE.md`](cmd/bobrik-watch/CLAUDE.md) and
    [`cmd/bobrik-watch/BOBRIK.md`](cmd/bobrik-watch/BOBRIK.md). Read
    those before changing anything under that directory.

12. **Dataset schemas + space-list query/subscribe** — built on the
    SDK's unified tech-space query (`Service.Query` /
    `SpaceIndexObjectId`) and required-schema work (`handler.Dataset.Schema`
    + `Space.Datasets` / `Service.Datasets`).
    - **Space-list query/subscribe.** `POST /v1/spaces/query` and
      `POST /v1/spaces/query/subscribe` wrap
      `Service.Query(SpaceIndexObjectId(), "spaces")` — the windowed
      snapshot + SSE primitive over the tech-space `spaces` dataset, same
      body/frames as the per-object `…/query[/subscribe]`. Records are the
      **raw** tech-index rows; `GET /v1/spaces` (`Service.List`) stays the
      mapped `SpaceInfo` convenience. `dataset` body field defaults to
      `spaces` (`profile` also available). Routes registered before the
      `:spaceId` matcher so the static `query` segment isn't swallowed.
      CLI: `any space query` / `any space subscribe`.
    - **Schemas on handlers.** Every built-in dataset handler now declares
      a `handler.Schema` (`internal/chat`, `internal/editor`): fields +
      per-field scope (chat `creator`/`createdAt`/`modifiedAt` = derived,
      rest synced; editor all synced). `Dynamic: true` keeps undeclared
      keys permitted, mirroring the `objects` dataset. Opaque content
      datasets (program/miniapp/agentdebug) stay schema-less (default
      Dynamic).
    - **Schema discovery.** `GET /v1/spaces/:id/datasets` (`Space.Datasets`)
      and `GET /v1/datasets` (`Service.Datasets`, account-scoped) return
      `[{name, schema}]` where `schema` is a JSON Schema doc with a
      per-field `x-scope` (synced/derived/local). CLI: `any datasets
      [<spaceId>]`.
    - **SDK prerequisite (`any-sync-sdk v0.0.8`).**
      The public `handler.Dataset` gained a `Schema` field + re-exported
      schema primitives (`handler.Field` / `Scope` /
      `ScopeSynced|Derived|Local` / `Leaf`); `spaceobjects.Store` honors
      it (back-compat: a zero Schema → Dynamic). The same release bumps
      `any-store/v2` to `v2.0.0-alpha.10`.
13. **Index chunkers + SDK tombstone opt-in** — the consumer-side
    search feed's contract. Full contract in
    [`docs/13-index.md`](docs/13-index.md).
    - `internal/index`: `IndexEntry` (`{Scope, ObjectId, Dataset,
      RecordId, Data, AddSeq}` — `Data == ""` ⇒ record-level removal) +
      `Chunker` interface (`Dataset()` / `TypeId()` — the `any.types`
      gate, "" = ungated / `ChunksSince(ctx, sp, objectId, since,
      yield)`, ascending by `_addSeq`) + `Registry`. Shared
      `RecordsSince` streamer chains `Projection({IncludeDeleted:true})`
      → typed `_addSeq > since` filter → `Sort _addSeq` → `Iter`.
      Scopes are an **open slug set** (`index.ValidScope`); `basic` /
      `chat` / `agent` are the vocabulary.
    - Per-handler chunkers: `editor.NewChunker()` (dataset
      `editor_blocks`, gate `editor`, scope `basic`, `Data` = block
      `text`) and `chat.NewChunker()` (dataset `chat_messages`, gate
      `chat`, scope `chat`, `Data` = message `text` only).
    - `index.NewPropChunker()` (dataset `prop` — VIRTUAL, ungated):
      indexes property VALUES from the shared `objects` collection, one
      entry per (object, indexed property), recordId = propId. Which
      props index is declared on the property definitions via
      `meta["index"] = "<scope>"` (SDK `PropertyDraft.Meta`, HTTP `meta`
      field; string/array kinds only; arrays newline-join). Built-ins
      `any.name` + `any.description` always index under `basic`
      (recordIds `name` / `description`). Per live row it emits entries
      for every catalog prop unconditionally — value text when the type
      is attached, `Data ""` otherwise (record-level eviction of
      cleared values / detached types). Catalog = per-space TTL
      snapshot (30s; `Invalidate` for tests).
    - Excluded from indexing entirely: `agent_debug_log`, `program`,
      `miniapp`, and the agent-data datasets (`agent_turns` /
      `agent_chunks` / `agent_memory_items` — dedicated gated chunker is
      a roadmap item).
    - Wiring: `server.NewIndexRegistry()` →
      `index.NewRegistry(editor.NewChunker(), chat.NewChunker(),
      index.NewPropChunker())`, stored on `deps.chunkers`.
    - **SDK prerequisites (`any-sync-sdk v0.0.10`).**
      `ProjectionOpts.IncludeDeleted` makes the find path
      (Iter/All/One/Count) surface tombstones (content wiped,
      `_deletedAt` + carried `_addSeq`) so chunkers stream deletions;
      Snapshot/Subscribe keep skipping them. Property definitions carry
      an opaque consumer `Meta map[string]string` (`PropertyDraft` /
      `PropertyDef`; not schema-bearing, so mutable once
      `UpdatePropertyMeta` lands).

14. **Search indexer (phase 2) + `/search` endpoint** — the consumer of
    the chunker feed. Full pipeline doc in
    [`docs/13-index.md`](docs/13-index.md) § Phase 2.
    - `internal/indexer`: per-space **worker pair** under one `Indexer`
      service (`New` / `Start` / `Close` / `Search`; `Sync`/`SyncSpace`
      are the synchronous test hooks). **Advance loop** (FTS path):
      `Changes().Subscribe` → cap-1 dirty chan → debounced `advance` —
      pages `ChangedSince(cursor, 256)`; per dirty object it reads the
      shared objects row once (IncludeDeleted): tombstoned ⇒ prefix
      delete `objectId:`; gated chunker whose `TypeId()` ∉ `any.types` ⇒
      prefix delete `objectId:<dataset>:` (DetachType bumps `_addSeq`,
      so detach rides the same window); else `ChunksSince`. One WriteTx
      per page (prefix deletes → record deletes → upserts) — eviction is
      addSeq-consistent, never racing the cursor; cursor persisted per
      page. **Embed loop** (vector path, parallel): drains `pending`
      docs — batch `EmbedDocs` (64) → batch `SetVectors` (one tx) →
      `EnsureVectorIndex`; 1m ticker retries. Embedder latency never
      delays the cursor or FTS searchability.
    - `Store`: one any-store DB at `<data-dir>/index/index.db`,
      collection per space; doc id **`objectId:dataset:recordId`** —
      every removal is a primary-key op (prefix ranges with bytewise
      upper bound `prefix[:len-1]+";"`); BM25 FTS on `data` + sparse
      range on `pending` ensured at open; **IVF-SQ cosine vector index
      created lazily** (`EnsureVectorIndex`) once ≥1 embedded doc
      exists — IVF trains from existing docs and cannot be created
      empty. Vector hits with similarity ≤ 0 are dropped (noise floor).
      `cursors` collection: per-space cursor + `_meta` schema-version
      (v2) & dim pin (mismatch = boot error advising
      `rm <data-dir>/index`).
    - Embedders: `indexer.Embedder` (`EmbedDocs`/`EmbedQuery`/`Dim`) —
      `ollama` (local `/api/embed`, default `embeddinggemma`, task
      prompts), `openai` (OpenAI-compatible `/embeddings`), and
      `local` (**in-process llama.cpp** via yzma purego bindings, no
      CGO; `embed_local.go`). Local defaults to Qwen3-Embedding-0.6B
      Q8_0 (1024-dim, last-pooling, L2-normalized, Qwen instruct query
      prefix), auto-downloaded sha-pinned into `<data-dir>/index/models`
      with logged progress (`embed_local_download.go`, resumable, never
      blocks boot — "still downloading" rides the outage semantics
      below); llama.cpp shared libs come from `make llamacpp`
      (`bin/llamacpp/`, pin `LLAMACPP_VERSION` in Makefile); input is
      truncated to `index.local.contextSize` tokens (default 2048, EOS
      preserved for last-pooling — explicit decision, docs/13-index.md
      § Known limits); Linux needs system libffi (NixOS: `nix develop`,
      see flake.nix). Config `index.*` (`internal/config.Index`, env
      `ANY_INDEX_*`); `embedder` defaults to `local`, `none` opts out
      (FTS-only). **An unavailable embedder never breaks the
      pipeline**: no boot probe — pending is marked whenever an
      embedder is configured, an outage freezes only the vector side
      (FTS unaffected), and recovery resumes embedding automatically;
      the dim is learned from the first successful batch (or
      `index.vector.dim`) and pinned in `_meta`. `mode=vector` during
      an outage ⇒ 503 `index.embedder_unavailable`; hybrid degrades.
    - Surface: `POST /v1/spaces/:spaceId/search` (`handlers_search.go`)
      `{query, scopes?, limit?, mode?}` → `{hits, mode, vectorStatus}`;
      modes `hybrid` (RRF k=60, default; degrades to fts without
      embedder — reply `mode` reports what ran) / `fts` / `vector` (400
      `index.no_embedder` without embedder). `vectorStatus`
      (used/unavailable/disabled/skipped) tells the consumer agent
      whether semantic recall participated and why not. 409
      `index.disabled` when `index.enabled: false`
      (`deps.indexer == nil`). CLI: `any search`. Client recipe:
      `docs/08-clients.md` § 6.
      Space discovery: `Spaces().List` + `Service.Subscribe`
      (added ⇒ spawn worker, removed/deleted ⇒ stop + `DropSpace`).
    - **any-store prerequisite (`v2.0.0-alpha.11`).** FTS
      (`$text`/BM25/`iter.Score`) + vector
      (`iter.Distance`/`VectorEf`/IVF-SQ) indexes — the `btree-fts`
      branch, tagged as `alpha.11`. The SDK pins the same version.

15. **Space `createdAt`** — the SDK (v0.0.10) stamps a derived
    `createdAt` (unix seconds, added-to-account time: create for the
    author, join for a joiner) on every new tech-space `spaces` row.
    No `any`-side code — `SpaceInfo.createdAt` (`GET /v1/spaces[/:id]`)
    and the raw space-list rows light up via passthrough; rows sort
    with `{"sort":["-createdAt"]}`. Pre-stamp rows stay zero — clients
    treat zero as unknown. Semantics + caveats in `docs/03-api.md`
    § Spaces.
16. **Aggregation pipelines (`/aggregate`)** — MongoDB-style pipelines
    over both query scopes, the aggregation siblings of `/query`:
    `POST /v1/spaces/:id/objects/aggregate` (per-space objects
    collection → `Space.AggregateObjects`) and
    `POST /v1/spaces/:id/aggregate` (per-object dataset, objectId +
    dataset in body → `Space.Aggregate`). Snapshot-only — no subscribe
    variant (any-store aggregation has no live path; re-run to
    refresh). Body: `pipeline` (required JSON array of stages:
    $match/$sort/$skip/$limit/$count/$project/$addFields/$unwind/
    $group) + optional `groupLimit`/`accumArrayLimit`/
    `memoryLimitBytes` (blocking-stage bounds, negative = unlimited)
    and `explain: true` (returns `{plan}` instead of `{records}`,
    diagnostic-only). Records are pipeline RESULT docs — group key
    comes back as `id`, never `_id`. Tombstones excluded server-side
    (SDK prepends a `_deletedAt` $match that folds into the pushdown
    prefix, so it stays index-planned). Errors:
    `400 aggregate.bad_pipeline` (parse/stage/prefix violations,
    `space.ErrBadPipeline`) and `400 aggregate.limit_exceeded`
    (`details.limit`: group/accumArray/memory, SDK limit sentinels).
    CLI: `any aggregate SPACE [OBJ] [--dataset NAME | --properties]
    --pipeline '<json>'|@FILE|-`. Client doc with examples + the
    MongoDB-divergence catalog: `docs/14-aggregation.md`.
    **SDK prerequisite (`any-sync-sdk v0.0.11`)**: the `space.Agg`
    builder (`Space.Aggregate`/`AggregateObjects`) wrapping any-store
    alpha.11's `Collection.Aggregate`.
17. **Mnemonic authorization + per-account data dirs** — the data dir
    is now a multi-account ROOT: new accounts live at
    `<root>/<accountId>/` (wallet.key, server.pid, sdk/, index/), a
    legacy root `wallet.key` is the DEFAULT account with its data flat
    at the root (no migration code), embedder models shared at
    `<root>/models/` (a model already in the legacy
    `<dir>/index/models` keeps being used). Account selection:
    `--account` / `ANY_ACCOUNT` / `account:` → root wallet → sole
    nested dir (`internal/server/identity.go::ResolveIdentity`).
    `any init [--mnemonic|--mnemonic-stdin|--index N|--new]` creates or
    RESTORES an account — same phrase ⇒ same account id, always a
    fresh device key (the supported second-device flow; wallet.key
    copies collide peerIds and break realtime sync). `any run` no
    longer auto-creates wallets: with no resolvable account the server
    starts UNAUTHORIZED — a /v1 guard middleware returns
    `401 auth.required` everywhere except health/shutdown/openapi/auth
    — and `POST /v1/auth` (`{mnemonic?|accountId?|index?}`, neither =
    generate; generated mnemonic returned once) boots the engine (pid
    lock → wallet → SDK → indexer, `internal/server/engine.go`) in
    place; `GET /v1/auth` lists local accounts. One engine per process
    lifetime; switching = restart with `--account`. CLI: `any auth
    login/status`. SDK prerequisite: `FileProviderConfig.Mnemonic/
    Index` seeding, `auth.AccountId(mnemonic, index)`, exported
    `ErrInvalidMnemonic` / `ErrMnemonicMismatch` / `ErrPasskeyRequired`
    / `ErrWrongPasskey` (any-sync-sdk `feat/auth-mnemonic`). Contract:
    docs/02-server.md § Startup + Data dir layout, docs/03-api.md
    § Auth, docs/05-config.md, docs/06-errors.md.
18. **Real space deletion** — `any-sync-sdk v0.0.12` turned
    `Service.Delete` from a local-only soft-delete into an offline-first
    real deletion: it writes the synced `remoteStatus=deleted` tombstone,
    **offloads all local state immediately** (closes watchers + Store,
    evicts the any-sync space, drops the per-space CRDT collections and
    DB file, reclaims disk even offline), and kicks a background
    reconciler that sends the signed `coordinator.SpaceDelete` (owner-only;
    a non-owned space offloads locally and the reconciler no-ops) and
    also offloads spaces the coordinator reports gone (deleted on another
    device / owner deleted a joined space). The wire surface is unchanged
    — `DELETE /v1/spaces/:spaceId` (`d.sdk.Spaces().Delete`) still returns
    204 — and no `any`-side reaction code changed: the search indexer
    already wipes a space's index on the `Spaces().Subscribe` `Removed`
    stream (`DropSpace`, status item 14), and both delete paths set
    `remoteStatus=deleted`, which the subscription classifies as
    `Removed`. The tech-space row stays in `Service.List` with
    `status:"deleted"` as a sticky tombstone, so the `GET /v1/spaces`
    active-only default filter is kept by design. This slice was a
    dependency bump (`v0.0.11` → `v0.0.12`, any-store/v2 alpha.11 →
    alpha.14) plus the `any space delete <id> --yes` CLI command and doc
    alignment (docs/01-cli.md, docs/03-api.md § Spaces). SDK contract:
    its `docs/03-space.md` § Space Lifecycle.

18. **UI command channel** — an account-wide, **in-memory** control
    channel that lets an agent drive a connected any-ui window
    (*"open this space / open this object"*), extensible to other
    UI-side ops. Deliberately NOT a dataset: a UI command is a
    transient directive to this device's window, not synced space data
    — so it goes through an in-process broadcaster, not the
    SDK/handler/CRDT machinery (same kind of consumer-side exception as
    `/search`). `POST /v1/ui/commands` (`{action, spaceId, objectId?,
    source?}`; `action` an open slug set — `open_space` / `open_object`)
    fans the command out to every connected subscriber and returns
    `{subscribers: n}` (0 = nobody listening; still 2xx,
    fire-and-forget). `GET /v1/ui/commands/subscribe` is the SSE stream:
    `ready` → `command` per publish → `closed{reason}`
    (`server_shutdown` / `overflow`). **No snapshot, at-most-once** —
    a subscriber sees only commands published after it connects, so
    there is no stale-replay on reconnect and nothing accumulates.
    Both routes are account-scoped, sitting outside the `:spaceId`
    group like `/sync-status/subscribe`, behind the `/v1` auth guard.
    `internal/server/uicmd_hub.go` (the broadcaster; non-blocking
    publish drops slow subscribers) + `handlers_ui_commands.go` +
    `internal/api/uicommand.go`. CLI: `any ui
    open-space/open-object/subscribe`. Contract: docs/15-ui-commands.md.
    Consumers: the bobrik `ui` tool (`cmd/bobrik-watch/`) and any-ui
    (`../any-ui/docs/tasks/ui-commands.md`).

19. **One-to-one (direct) spaces** — wraps the SDK's derived 1-1 space
    surface. A 1-1 is shared by exactly
    two identities, derived deterministically from both account keys
    (same id regardless of key order, immutable two-writer ACL, no
    invite/accept handshake). Because the ACL can't gate membership,
    "approve incoming" is a **local SDK state machine** surfaced as space
    statuses, not ACL ops. Endpoints (`handlers_spaces.go`): `POST
    /v1/spaces/one-to-one` (`{otherIdentity}` → `Service.OneToOne`;
    initiate/accept-by-peer/un-decline, 201, goes straight to active),
    `POST /v1/spaces/:id/one-to-one/accept` (`AcceptOneToOne`, accept a
    pending row by id), `POST /v1/spaces/:id/one-to-one/decline`
    (`DeclineOneToOne`, synced sticky), `POST
    /v1/spaces/one-to-one/register-incoming` (`{peerIdentity,
    displayHint?}` → `RegisterIncoming`, the out-of-band discovery path,
    204, idempotent). New `SpaceInfo` fields `spaceType` (=
    `anytype.onetoone`) + `author`, and status strings
    `one_to_one_pending` / `one_to_one_declined`
    (`spaceInfoToAPI`/`spaceStatusString`). **Discovery has no bespoke
    endpoint** — incoming requests are the space list filtered on `GET
    /v1/spaces?status=one_to_one_pending` (pending/declined hidden from
    the active-only default, like deleted). The coordinator **inbox
    notifier** that surfaces incoming 1-1s automatically is SDK-internal
    — auto-started in `anysyncsdk.Open` (`StartOneToOneInbox`), so `any`
    needs zero notifier wiring. Delete of a 1-1 is local-only +
    re-derivable (existing `Service.Delete` path, no `any` change). Bad
    identity / self-pairing → `400 request.invalid_field` (string-matched
    until the SDK exports sentinels, `oneToOneError`). CLI: `any
    one-to-one start/accept/decline/register/pending` (top-level group,
    aliases `1-1`/`direct`). Contract: docs/03-api.md § Spaces,
    docs/01-cli.md, docs/02-server.md § Startup, client recipe in
    docs/08-clients.md § 7, and the SDK's docs/13-one-to-one-spaces.md.

20. **Identities directory + encrypted profiles** — wraps the SDK's new
    `SDK.Identities()` (`feat/identities-directory`), the account-global,
    device-local cache of every account identity this account has
    encountered (across spaces, 1-1s, inbox invites). Account-scoped
    endpoints sitting outside the `:spaceId` group (like
    `/sync-status/subscribe`): `GET /v1/identities` (`List`), `GET
    /v1/identities/:identity` (`Get`, 404 `identity.not_found` when
    unknown), `GET /v1/identities/subscribe` (`Subscribe`, callback→SSE
    bridge — `event: identities` frames carrying `{added,updated,removed}`
    batches, same `ready`→`lagged`→`closed` envelope as the
    members/sync-status streams). `handlers_identities.go` /
    `api/identity.go` / `client/identities.go` / `cli/identities.go` (`any
    identities list/get/subscribe`, alias `contacts`); `/subscribe`
    registered before the `:identity` wildcard. `IdentityInfo`
    = `{identity, name?, description?, iconCid?, spaceIds}` — the SDK
    strips the synced `symKey` (profile-decryption secret) before it
    reaches us. **Two consequences of the same SDK change:** (a) profiles
    pushed to identityRepo are now ENCRYPTED — a contact resolves
    `name`/icon only after their key arrives via a shared-space ACL or a
    1-1 invite, so directory AND members-list profiles surface id-only
    until then (clients must tolerate empty names; cold-restored devices
    resolve in the background). (b) The `identities` dataset lives on the
    tech-space index object, so the generic `POST /v1/spaces/query[/subscribe]`
    `dataset` field is now a CLOSED allowlist `{spaces, profile}` (else
    `400 request.invalid_field`) — reaching `identities` there would leak
    the raw `symKey`; read it through `GET /v1/identities`. **Rights live
    on the members list, not here** — the directory has no permission
    field; roles (owner/admin/writer/reader) come from `GET
    /v1/spaces/:id/members`. Account profile read/write
    (`GET /v1/account`, `PUT /v1/account/metadata`) is unchanged. Contract:
    docs/03-api.md § Identities + § Account (encryption note),
    docs/01-cli.md, docs/04-events.md § Identities directory stream,
    client recipe docs/08-clients.md § 8.

21. **Files v2** — wraps the SDK files-v2 surface (`Space.Files()`:
    Attach/Open/Get/Status/SubscribeStatus/Stats/Pin/Retry/Offload/
    Delete/List/Query + SDK-level FileCacheSize/FreeUpFileCache/
    SweepFileCache). Files bind to objects; the SDK stores one
    `payloads` row per file on a derived per-object child (cleartext
    rootCid/size/networkSign/objectId + one sealed member-only blob
    with key/name/mime/inline bytes). Tiers: <4096 B inline in the
    CRDT; larger → encrypted UnixFS DAG in a local CARv2 + background
    fileV2-broker backup (offline-first persistent queue; states
    durable/inflight/limited). Wire (handlers_files.go): `POST
    /v1/spaces/:s/objects/:o/files` — attach, RAW streaming body (the
    one BodyLimit-exempt route, see routes.go Skipper; Content-Type →
    mime, `?name=&variant=&variantOf=`); `GET …/files/:fileId/content`
    — download via http.ServeContent (stored mime, Content-Disposition,
    Range/206; CORS AllowHeaders gained `Range`); Get/List/Stats/
    Status + pin/retry/offload (offload of the only copy → 409
    `file.not_durable`; content not local + not fetchable → 409
    `file.not_available` — the receiver-side retry state, signalled
    done by the row gaining `networkSign`; mapped from the SDK's
    exported sentinels `space.ErrFileNotBackedUp` /
    `ErrFileNotAvailable` / `ErrFileVariantInvalid` via errors.Is,
    pinned by TestFileErrorMapping);
    `DELETE …/files/:fileId` — synced row delete (variants cascade;
    404 `file.not_found` on unknown/already-deleted; local bytes
    reclaimed by cache GC, no network reclaim — fileprotov2 has no
    delete RPC yet);
    `GET …/files/subscribe` — FileStatus SSE (streamStatusSSE pattern,
    LOCAL transitions only); `POST …/objects/:o/files/query[/subscribe]`
    — windowed query over one object's payload rows (bridges
    `Files().Query`; 404 `file.not_found` before first attach);
    account-scoped `GET/POST /v1/files/cache[/free|/sweep]`. Config
    `files.{publicReadBaseUrl,gcInterval}` (zero interval = NO
    background cache GC). CLI: `any file
    attach/list/get/download/status/stats/subscribe/pin/retry/offload/
    query/query-subscribe/cache` + `delete --yes`. NOT wrapped (broker embedding
    surfaces): `Space.Payloads()`, `TreeHeads()`, `Track/Evict`,
    `Headless`, `Sync.TreeTypes`. Contract: docs/17-files.md (model),
    docs/03-api.md § Files, docs/04-events.md § File status stream,
    docs/08-clients.md § 9.

22. **Scoped fields for records** — the local write route is now
    public end-to-end. SDK (`ModifyBatch.Scope`): `POST
    /v1/spaces/:id/modify` takes `"scope": "synced"|"local"`; local
    routes through `Object.LocalSet` (no DAG change, never syncs,
    flows through query/subscribe, empty `changeId`), constrained to
    fields the dataset schema declares `ScopeLocal` + explicit record
    ids, no upsert, no traceIds (`400 request.schema`; wrong-scope ops
    → `rejections`, synced-write-to-local-field → `400
    dataset.validation`). Chat declares the read-tracking flag fields
    `unread` / `unreadMention` / `unreadReactions` as ScopeLocal
    booleans (now owned by the SDK read-tracking service — see
    internal/chat/reading.go and docs/16-chat.md). Property
    definitions:
    `POST …/types/:id/properties` accepts `scope`
    (synced/account/local, pinned first-write like kind; derived
    rejected) and `GET …/properties` returns it — value writes were
    already scope-routed via `PropertiesAPI.Set`. Account scope for
    RECORD fields is declared-but-not-writable (SDK mirror covers
    objects rows only). SDK prerequisite: `ModifyBatch.Scope` +
    exported `space.ParseScope` (branch cheggaaa/scoped-record-modify;
    SDK contract test e2e/local_scope_records_test.go). `any` tests:
    handlers_modify_scope_test.go. Docs: 03-api.md § Modify records /
    § Datasets / § Types & properties / § Chat.
23. **Property PATCH + select options** — closes any-ui #252
    (#1 rename / #2 delete / #3+#5 select-option CRUD + colors + order).
    Two live endpoints replace the old `501` stubs:
    - `PATCH /v1/spaces/:spaceId/types/:typeId/properties/:propId` →
      `TypesAPI.PatchProperty`, a generic `{set, unset}` per-path patch
      (`api.PropertyPatchRequest`). One endpoint covers rename +
      option create/rename/recolor/reorder/delete — **zero
      option-specific methods**, because options are just string leaves
      under `format.options.<key>.{name,color,pos,meta.<k>}` and the CRDT
      already merges per-path `$set`/`$unset`. `any` translates wire
      paths → storage (`xKey`→`x-key`), validates leaf semantics
      (`format.ui` vocab, `format.filter` parses, strings elsewhere) and
      rejects pinned paths (`kind`/`scope`/`items`/`properties`/`format`
      whole/`format.type`) → `400 property.immutable`
      (`patchPathToStorage`/`patchSetValue` in `propformat.go`). Options
      are **dangling-tolerant**: delete is a hard `$unset`, values keep
      an orphan key, membership is not validated (no archive flag).
    - `DELETE …/properties/:propId` → `TypesAPI.RemoveProperty`
      (tombstone; values not cleaned up; `404` on unknown).
    - New `select` (kind=string) / `multiselect` (kind=array)
      `FormatType`s give options a home; `PropertyFormat.Options`
      (`map[string]{Name,Color,Pos,Meta}`) + `Meta`. Read-back rides
      `formatToAPI`. First `any type` CLI surface (`internal/cli/types.go`:
      `type create/list`, `type property list/add/patch/remove`,
      `type property option set/delete`) + `internal/client/types.go`.
    - **#4 (atomic rename/delete a value across N objects) is won't-fix**
      — impossible in a per-object CRDT, and mooted: values store the
      immutable option key, so rename is one write / zero object writes;
      delete is dangling-tolerant.
    - **SDK prerequisite (shipped in v0.1.5):** `space.PatchProperty` +
      `PropertyPatch` replace `UpdatePropertyMeta`/`PropertyMetaUpdate`;
      `RemoveProperty` implemented (was a stub in v0.1.4); `FormatSelect`/
      `FormatMultiselect`; `PropertyFormat/Draft.Options+Meta`;
      `PropertyOption`; exported `space.ErrPinnedField` +
      `typetype.IsPinnedPath`. Docs: 03-api.md § Types, 01-cli.md § Types.
24. **Per-space general chat** — every space now has one deterministic
    "general" chat object, derived from a fixed seed
    (`chat.GeneralChatSeed` = `any/general-chat/v1`, `internal/chat/general.go`)
    via `Objects().Derive` — the same idempotent primitive
    `agentmem.DeriveBrainObjectId` uses. Motivation: clients that want
    "the chat for this space" (the only case for a 1-1) otherwise each
    `Objects().Create` a fresh chat, so a space ends up with two or three
    parallel chats. Surface: NO bespoke endpoint — the id is delivered
    through the existing common per-space metadata point:
    `SpaceInfo.generalChatObjectId`, populated on every single-space
    response by `spaceToAPI` (takes a ctx, derives best-effort —
    materializing the object on first sight, chat type attached, so the
    id accepts `chat/messages` writes immediately) — create / get /
    one-to-one / join. Omitted on `GET /v1/spaces` list rows (kept a
    cheap read that never materializes chats), same policy as
    `spaceIndexObjectId`. Deterministic ⇒ a joiner derives the same id
    the creator did, so local + CRDT-replicated converge. CLI: read it
    off `any space get <spaceId>`. Contract: docs/03-api.md § Chat
    (General chat) + § Spaces, docs/01-cli.md § Chat, docs/16-chat.md
    § Finding the chat object. Because the tree is materialized locally
    on every peer (derive → PutTree, never a remote fetch), the general
    chat cannot hit the joined-space "BuildTree: tree does not exist"
    mode (fixed separately by the SDK v0.1.6 bump).

**Always read the relevant `docs/NN-*.md` before writing code for an area**, and if
implementation diverges from a doc, update the doc in the same change.

### Build / test / run

```
go build ./cmd/any                                # binary at ./any
make build                                        # builds any, bobrik-watch, any-agent-runtime
make llamacpp                                     # prebuilt llama.cpp libs into bin/llamacpp
                                                  # (index.embedder: local) — also runs as
                                                  # part of `make build`; fetch failure there
                                                  # warns instead of failing the build
make build-android                                # dist/android/any.aar — arm64-v8a gomobile bind
                                                  # (single ABI), version-
                                                  # stamped via `-ldflags '$(LDFLAGS)'`; CI passes
                                                  # the resolved version as make COMMAND-LINE vars
                                                  # (VERSION=… COMMIT=… DATE=…) — env can't beat the
                                                  # Makefile's `:=`. Needs an Android NDK (CI-only).
go test ./...                                     # unit tests (config + server)
go vet ./...

# End-to-end
ANY_DATA_DIR=/tmp/any-e2e ./any init              # first-run wallet + mnemonic
ANY_DATA_DIR=/tmp/any-e2e ./any run               # foreground server
./any status                                      # GET /v1/health
./any stop                                        # POST /v1/shutdown
```

CI: one reusable workflow (`.github/workflows/_build-any.yml`, called by
`release-any.yml` on `v*` tags + `nightly-any.yml` on cron) fans out per-platform
build jobs (desktop x4 tarballs, Android `any.aar`, iOS `any.xcframework.zip`) and
fans in to a single `publish` job that ships them all in ONE GitHub Release (both
mobile assets sha256-pinned in the notes) and dispatches the 3 client repos.

For bobrik-watch commands, see [`cmd/bobrik-watch/CLAUDE.md`](cmd/bobrik-watch/CLAUDE.md).

### Running bobrik — the canonical sequence

After ANY change to Go code, `anyHelper.js`, programs, skills, or
tool-descriptions, run these three steps in order:

```
# 1. Always rebuild first — never skip this.
make build                                        # builds any, bobrik-watch, any-agent-runtime

# 2. (Re)start any and bobrik-watch (restart both so the new binaries take over).
#    e.g. stop the running instances, then:
./any run                                         # foreground server (or your start skill)
./bin/bobrik-watch                                # default: space=bao, watches chat "general"

# 3. Refresh the JS of bobrik/bao (reloads anyHelper.js, programs, skills,
#    tool-descriptions from disk into the bao space).
./bin/bobrik-watch --bootstrap                    # POST /bootstrap to the running instance
```

Step 1 is mandatory every time — `make build` always. Steps 2 and 3 are
how new JS reaches a live agent: a binary restart alone does NOT re-sync
the in-space programs/skills of an already-running watcher; `--bootstrap`
POSTs `/bootstrap` to the running watcher's control API (`--control-addr`,
default `127.0.0.1:7010`), which re-runs the bootstrap sync against disk.
That sync is **incremental/hash-gated** — unchanged programs/skills are
skipped, deleted ones swept — so it's cheap to run often.
(`--bootstrap-clean` POSTs `/bootstrap-clean`, the wipe-and-rebuild
recovery path.) See
[`cmd/bobrik-watch/CLAUDE.md`](cmd/bobrik-watch/CLAUDE.md) § Startup sync
for the mechanics.

Module path: `github.com/anyproto/any`. Go 1.26.2. Dependencies
(`any-sync-sdk`, `any-sync`, `any-store`, `anytype-agent-runtime`) are
**published modules**, not sibling checkouts — `go.mod` is the single
source of truth for the exact versions. Don't restate version numbers
here: they drift on every bump and go stale silently. Which SDK feature
a given slice needed is captured per-item in the Status section above.
`any-sync-sdk` is a private module — `GOPRIVATE=github.com/anyproto/any-sync-sdk`
(+ git SSH `insteadOf`) is needed to fetch it directly. To inspect SDK
behavior, read the module cache
(`$(go env GOMODCACHE)/github.com/anyproto/any-sync-sdk@<version>/`).

## What this project is

`any` is a single Go binary that wraps `any-sync-sdk` with:
- an HTTP/JSON server on `127.0.0.1:7001` (started by `any run`, foreground only)
- a thin CLI client (default mode — every other `any <cmd>` is an HTTP call to the server)

The CLI **never** opens the SDK, touches any-store, or talks to any-sync peers.
Every caller — human, script, language binding — goes through HTTP. Keep this
invariant: if you're tempted to have the CLI reach into SDK or storage directly,
stop and add a server endpoint instead.

v1 is explicitly a **prototype** to exercise the SDK surface end-to-end. Breaking
changes to request/response shapes are expected and are absorbed by bumping the
`/v1/` path.

## Repo relationship

This repo imports the **published** `any-sync-sdk` module:

```
any            (this repo)  — HTTP server + CLI
 └── any-sync-sdk           — Go SDK (Space, Object, CRDT, types, properties)
      └── any-sync, any-store
```

The full stack context lives in the SDK's `docs/00-common-context.md` (in the
module cache). When an SDK
method is missing or awkward, raise it on the SDK repo rather than working around
it here — several v1 endpoints are explicitly blocked on SDK work (see
`docs/07-roadmap.md` § SDK-side prerequisites).

**Property `xKey` is client-side only — the SDK never sees it.** Property
values are stored and validated at `record[typeId][propId]`; writes MUST key
by the content-addressed `propId`, not `xKey` (keying by `xKey` →
`property.not_found`). `xKey` is an optional stable label clients may attach
to map their own keys → `propId`; `GET /types/:id/properties` returns it
alongside `{id, name, kind}` so callers can resolve `xKey → propId`.

## Planned package layout

From `docs/00-overview.md`:

```
any/
├── cmd/any/              main() — dispatches to cli or server subcommand
├── internal/
│   ├── cli/              CLI subcommands, flag parsing, rendering
│   ├── server/           HTTP server, route wiring, SDK lifecycle
│   ├── api/              request/response types shared by server and cli
│   ├── client/           HTTP client used by cli/ to call server/
│   └── config/           config file + env var loading
└── docs/
```

Nothing is published externally; everything under `internal/`. Request/response
types live in `internal/api/` and are imported by both `server/` and `cli/` — do
not redefine them on one side.

## Architectural invariants

These cut across files and are easy to violate accidentally:

- **HTTP framework is `github.com/labstack/echo` v4.** Don't introduce another router
  (gin, chi, net/http by hand) without a reason. Routes group per SDK section via
  `echo.Group` — see `docs/02-server.md` for the pattern.
- **Every route lives under `/v1/` from day one.** Including health and shutdown.
  The next iteration bumps the prefix rather than breaking paths in place.
- **Endpoints map 1:1 onto SDK methods; CLI commands map 1:1 onto endpoints.** If the
  SDK has it, we expose it. If it doesn't, we don't. Don't invent convenience
  endpoints that aggregate multiple SDK calls — that's a v1.x decision.
  Sole exception: `POST /v1/spaces/:id/search` — the search index is a
  consumer-side feature built on `Changes()` + the chunkers
  (`docs/13-index.md`), not an SDK method.
- **Localhost-only.** The server refuses to bind anything other than a loopback
  address and must fail clearly if `--addr 0.0.0.0:...` is passed. No auth middleware,
  no rate limiting in v1 — that comes with the remote-access story (v2). CORS: only the fixed desktop-shell webview allowlist (any-ui PR-095; `routes.go`), which doesn't change the loopback trust model.
- **Output format is pretty-printed JSON.** Both the server wire format and the
  CLI's stdout. No table rendering, no `--output` flag in v1.
- **Error response shape is uniform.** `{"error": {"code", "message", "details?"}}`
  for every non-2xx, regardless of status. See `docs/06-errors.md` for the code
  namespace (`space.not_found`, `sdk.not_implemented`, etc.). Never leak SDK
  internal types or filesystem paths in `message`/`details`.
- **SDK placeholder endpoints return 501** with `sdk.not_implemented`: ACL, members,
  sync-status. Register the routes anyway so the CLI stays buildable and discoverable.
- **Logging goes through `any-sync/app/logger`.** Echo's logger is wired to the same
  backend — one log stream for the whole process. Don't introduce a second logger.
- **POST `/v1/spaces/:spaceId/query`** uses POST (not GET) because the filter/sort
  body doesn't fit a query string. Don't "fix" this to GET.
- **Dataset reads go through `/query` and `/query/subscribe`.** Built-in types
  (chat, editor) keep bespoke handlers for *writes* only (POST/PATCH/DELETE and
  reactions). Reads always go through the per-object query primitive with the
  matching `dataset` value (`chat_messages`, `editor_blocks`, ...). One read
  path per dataset, one wire shape per snapshot. Sole exception: `GET
  /editor/markdown` is a render transform, not a dataset read.
- **POSTs are not idempotent in v1.** Each POST produces a new DAG change. No
  `Idempotency-Key` yet.
- **any-store filters are built with the typed `any-store/v2/query` package**
  (`query.Key` / `NewComp` / `NewCompValue` / `NewInValue` / `Text` /
  `Exists` / `And` / `Or`) — never as `map[string]any` or JSON-string
  literals. Typed filters are compile-checked, skip parsing, and are
  immutable once built; **static filters (constant paths/values) are built
  once at package level and reused** — only dynamic parts are built per
  call. Client-supplied filters arriving over HTTP are the sole place raw
  shapes enter (parsed by `query.ParseCondition` at the boundary).

## Config and lifecycle

Precedence: config file → env vars (`ANY_*`) → flags. See `docs/05-config.md`.
Defaults: `~/.any/` data dir, `127.0.0.1:7001` listen.

Data dir layout:
```
<data-dir>/
├── wallet.key         # auth.FileProvider wallet (0600)
├── server.pid         # single-instance lock (stale PIDs are reclaimed)
├── config.yaml        # optional
└── storage/           # any-store — owned by SDK
```

Shutdown paths: `SIGINT`/`SIGTERM` or `POST /v1/shutdown`. Both drain in-flight with
a 10s deadline, close the SDK, exit 0.

## CLI exit codes

`0` success / `1` user or 4xx / `2` server 5xx / `3` can't reach server.
When the server isn't running, print the "start it with `any run`" message — no
auto-start.

## What's deferred (don't implement in v1)

- Remote access, TCP auth, TLS.
- Install scripts / service files.
- File upload/download (SDK defers files to v1.1).
- Multi-account per server. One server = one account; two accounts = two data
  dirs on two ports.
- GUI, gRPC, any transport other than HTTP/JSON.

## Docs index

| File | What it governs |
|------|-----------------|
| `docs/00-overview.md` | goals, scope, process model, package layout |
| `docs/01-cli.md` | full CLI command surface, flags, input formats |
| `docs/02-server.md` | server lifecycle, startup, shutdown, data dir |
| `docs/03-api.md` | HTTP endpoint catalog, body shapes, middleware |
| `docs/04-events.md` | subscriptions (SSE) — contract, lifecycle, tradeoffs |
| `docs/05-config.md` | config file schema, env vars, flags, first-run flow |
| `docs/06-errors.md` | error response shape, HTTP codes, code namespace |
| `docs/07-roadmap.md` | v1.x / v2 plans, open questions, SDK prerequisites |
| `docs/08-clients.md` | client call-pattern recommendations (writes via handlers, reads via query/subscribe, chat newest-first paging) |
| `docs/09-query.md` | any-store query guide — filter operators, array matching, sort, paging, indexes, anyHelper surface |
| `docs/10-coverage.md` | anyHelper ↔ server endpoint coverage map (what's wrapped, what's deliberately out of agent scope) |
| `docs/11-agent-memory.md` | agent data layer — turns/chunks/memory datasets, layering model, drill-down pointers |
| `docs/12-rlm-search.md` | RLM-style `search@v1` program (implemented) — recursive-LM recall without a vector index; loop mechanics, stats, guardrails |
| `docs/13-index.md` | search index — `IndexEntry`/`Chunker` contract, scopes, tombstones, addSeq; the indexer (store layout, advance/embed loops, purge rule), `/search` modes + errors |
| `docs/14-aggregation.md` | aggregation pipelines — `/aggregate` endpoints, stage set, pushdown guidance, limits, MongoDB-divergence catalog |
| `docs/15-ui-commands.md` | UI command channel — account-wide in-memory agent→any-ui control (`/v1/ui/commands[/subscribe]`), at-most-once, command shape, SSE frames |
| `docs/16-chat.md` | chat client guide — building a messenger UI on `chat_messages`: rendering, liveness, and SDK read-tracking (account-private, forward-only unread state) |
| `docs/17-files.md` | files v2 — storage tiers, durability states, cache/offload/pin, variants, read paths, what's deliberately not wrapped |
| `docs/18-ci.md` | the `any` artifact + CI — tarball layout, manifest, published platforms, the `ANY_CI_TOKEN` secret, build/publish/dispatch flow |
| `docs/19-links.md` | canonical `any://` link format — kind registry (o/m/s/p/f, reserved i), path composition rule, fragment rule, extension policy, legacy bare-form back-compat |
| `docs/search/` | search evaluation & decisions — chunking before/after, BEIR results, hybrid-knob tuning, why the defaults; complements `13-index.md` (the contract) |

Keep `docs/07-roadmap.md` honest — move shipped items to its "Done" section or
strike cut scope; add new open questions as they surface during implementation.
