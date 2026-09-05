# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Implementation slices landed:
1. **scaffolding + wallet + health** — `any init` / `any run` / `any status` /
   `any stop` / `any version` work end-to-end.
2. **SDK boot + space lifecycle** — Run opens `any-sync-sdk` (nodeconf via
   `config.LoadNodeconf`; with nothing configured it joins the PRODUCTION
   network from the embedded `internal/config/nodeconf-prod.yml`. Tests
   pass `config.NodeconfPlaceholder()` — the sanitized
   `nodeconf-placeholder.yml` — or point `ANY_NETWORK_NODECONF_PATH` at a
   staging/local conf; they must never join production).
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
6. **Chat module** (registered as a built-in type until item 43 turned
   it into a `handler.Module`) — `internal/chat` serves the shared
   `chat_messages` collection on objects whose type declares the module. Bespoke endpoints under
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
   No version history: `chat_messages` sets `SkipHistory` (SYN-86) —
   clients render live records only; `/history` never lists chat.
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
   [--agent-done=false]`. Contract: docs/03-api.md § Chat.
   **Mentions (SYN-72)**: records carry a server-DERIVED `mentions`
   identity array (ScopeDerived, client writes rejected) — parsed from
   `any://m/…` links in text via `anyuri.ExtractMentions` plus the
   replied-to message's creator folded in at materialization
   (`deriveMentions`, reads the target via the SDK's `ChangeCtx.Get`);
   edits re-derive ($unset when empty); sparse multikey `idx_mentions`
   backs `{"mentions": id}` filters. `classifyRead` tags mentions of
   self (post-apply `ctx.Get`/`ctx.RecordId` read of the derived
   array), so `unreadMention` + `chat.unreadMentions` are live — a
   mention-adding edit badges without re-flagging `unread`. The
   canonical `any://` grammar lives in the public `anyuri/` package
   (moved from the SDK, SYN-75 — see docs/19-links.md). NOTE: chat /
   editor now actually wire `Dataset.Indexes`
   (the per-handler `Indexes()` methods used to be dead code — no
   built-in index was ensured before this).
7. **Atomic blocks + markdown bridge** — `internal/editor` serves the
   editor collections (a `handler.Module` since item 43; routes carry
   `:collection`), one record per block. Per-block fields: `type` (paragraph / heading / list_item /
   …), `style` (open-ended), `text` (INLINE markdown only — no block-
   level syntax), `nav.parentId`, `nav.pos` (lexid). An empty
   paragraph is a `paragraph` record with `text: ""`; the markdown
   bridge carries it as a blank line beyond the one separating two
   blocks (edge runs have no separator to spend), so Split/Join stay
   exact inverses and re-PUTting a GET writes nothing — the encoding
   clients need to preserve vertical spacing (docs/03-api.md § Empty
   paragraphs). Bespoke endpoints
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
   The markdown bridge — `GET/PUT/PATCH /editor/markdown` — stays as
   the one render/import transform exception (LLM tooling and Export/
   Import .md flows depend on it). GET renders blocks → markdown;
   PUT parses markdown → diffs against the current block tree →
   emits per-record create / update / delete ops, returning
   `{inserted, updated, deleted, unchanged}`. PATCH is the surgical
   variant: `{edits: [{oldText, newText, replaceAll?}]}` exact-match
   replacements resolved server-side against the current canonical
   rendering (whole-line fuzzy fallback for unicode punctuation /
   trailing whitespace), then fed through PUT's diff — so a checkbox
   tick lands as one `$set style.checked`, ids stay stable, stale
   quotes 400 (`markdown.no_match` / `ambiguous_match` /
   `overlapping_edits`) instead of clobbering concurrent edits
   (`markdown.EditContent` in `internal/markdown`; CLI `any editor
   edit`). `POST
   /editor/markdown/append` is the append-only fast path: it parses
   the fragment, looks up only the tail pos (no full-doc read, no
   diff), ensures the `editor` type is attached (one object-record
   read via `editor.EnsureType` — the SDK gates `editor_blocks` writes
   on type membership, so a first write to a fresh object needs it,
   same as PUT), and creates the new blocks in one ModifyBatch —
   O(chunk), not O(doc). Purely additive; same reply shape as PUT with
   only `inserted` populated (`markdown.Append` in `internal/markdown`).
   Grow-by-append pages (e.g. an agent's debug log) use it so a run of
   N appends is O(N), not O(N²). CLI: `any editor blocks create/patch/delete`.
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

11. **Agent data (userspace)** — the agent's data (turns, memory,
    config, secrets, triggers) is harness-owned: the anybao harness
    declares it as runtime datasets on objects it derives itself and
    owns the record shapes, validation, and search mappings; nothing
    agent-specific is compiled into this server (docs/11-agent-memory.md).
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
      keys permitted, mirroring the `objects` dataset.
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
      entry per (object, indexed property), recordId = propId. User
      props index BY DEFAULT under the dedicated FTS-only scope `props`
      (never marked pending/embedded), entry text self-describing
      `"<prop name>: <value>"`; `meta["index"]` (SDK
      `PropertyDraft.Meta`, HTTP `meta` field) is a 3-state override —
      absent → `props`, `"<scope>"` → that scope, `"none"` → excluded.
      Kinds: string, array (newline-join of string+number elements),
      number (canonical JSON). Built-ins `any.name` +
      `any.description` always index under `basic` (recordIds `name` /
      `description`, raw — no name prefix; prop-dataset docs < 64 B
      also skip embedding). Objects carrying an excluded type are
      skipped wholesale — always `__type__` (type-definition rows)
      plus the wired-in `enrich_proposal`. Per live row it emits
      entries for every catalog prop unconditionally — value text when
      the type is attached, `Data ""` otherwise (record-level eviction
      of cleared values / detached types). Catalog = per-space TTL
      snapshot (30s; `Invalidate` for tests).
    - Excluded from indexing entirely: any runtime dataset declared
      without `search` (anybao's `program_source`, `mini_app`).
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
      collection per space; doc id **`objectId:dataset:recordId`** for a
      record's first chunk, `+ U+001F + n` for chunk n — the worker
      splits any chunker entry over `Options.ChunkRunes` (2000 runes)
      into chunk docs (`chunk.go`; a hit is a record shown through its
      best chunk, `limit` counts records — SYN-193) and `planDocs`
      hash-diffs a re-streamed record's chunk
      set. Every removal is a primary-key range op (structural prefixes
      with bytewise upper bound `prefix[:len-1]+";"`; a record's docs
      are `[base, base+" ")`); BM25 FTS on `data` + sparse
      range on `pending` ensured at open; **IVF-SQ cosine vector index
      created lazily** (`EnsureVectorIndex`) once ≥1 embedded doc
      exists — IVF trains from existing docs and cannot be created
      empty. Vector hits with similarity ≤ 0 are dropped (noise floor).
      `cursors` collection: per-space cursor + `_meta` pinning the
      schema version, the vector dim and the chunk target (mismatch =
      boot error advising `rm <data-dir>/index`).
    - Embedders: `indexer.Embedder` (`EmbedDocs`/`EmbedQuery`/`Dim`) —
      `ollama` (local `/api/embed`, default `embeddinggemma`, task
      prompts), `openai` (OpenAI-compatible `/embeddings`), and
      `local` (**llama.cpp in a child process** via yzma purego
      bindings, no CGO; `embed_worker.go` supervises, `embed_local.go`
      decodes inside the child). The child is this same binary re-exec'd
      as the hidden `any run embedder` and speaks framed JSON+float32
      over stdin/stdout; it only embeds — no store, no data dir. The
      server sends a batch one decode group per frame (`batchDocs`
      texts) through a two-class one-slot semaphore, and a search query
      takes the slot ahead of waiting doc frames, so `/search` waits for
      at most the decode in flight, never a 64-doc batch; acquisition is
      ctx-aware, and a caller that gives up (queued or mid-frame)
      leaves at once while the frame completes detached and the child is
      kept — no respawn (SYN-200). A
      llama.cpp abort (Vulkan device-lost throwing through the FFI
      frame, GGML_ASSERT) kills the child, not the server: the round
      fails, docs stay `pending`, and a crash with GPU
      offload active demotes the process to `--gpu-layers 0` for the
      rest of the run (in-memory — every start tries the GPU again).
      `index.local.requestTimeout` (3m, per frame) unwedges a hung GPU,
      which stops answering rather than failing; `index.local.threads` is the
      child's CPU budget and is runtime-settable via
      `Indexer.SetEmbedThreads`; `index.local.niceness` (10) runs the
      child below the server. The `ready` frame carries a `Hardware`
      report (backends + the .so each came from, GPU/driver names,
      llama.cpp release stamp, CPU features) — logged per spawn, kept
      behind `Indexer.EmbedHardware()` for later statistics. Local defaults to Qwen3-Embedding-0.6B
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
      The query embedding in `Indexer.Search` is bounded
      (`index.search.queryEmbedTimeout`, default 5s, every embedder;
      `auto` gives the online primary half of it so the local fallback
      still decodes): past it hybrid answers fts/`unavailable` and
      vector 503s, so a cold load or a wedged child degrades a search
      instead of holding it. A frame that fails mid-batch returns the
      vectors embedded so far and the embed loop lands them.
    - Surface: `POST /v1/spaces/:spaceId/search` (`handlers_search.go`)
      `{query, scopes?, limit?, mode?, require?, exclude?, maxData?,
      passages?}` → `{hits, mode, vectorStatus}` — `limit` counts
      RECORDS: each leg reads until its window covers enough distinct
      records (lexical: one lazy `Store.openFTS` cursor pulled past the
      3×limit over-fetch, ≤1000 chunks; vector: K widened ×4 until the
      IVF pool is exhausted), fusion stays per chunk, `groupHits`
      collapses chunks into records scored by their best chunk, and
      `passages: N` (≤10) returns a record's next best chunks on the
      hit. Query embedding runs BEFORE any store read and the FTS
      cursor is closed inside the leg — never hold an any-store
      iterator across another store call (reader slots deadlock).
      `BenchmarkCutoff` / `TestIteratorEarlyCloseNoLeak` pin the
      any-store facts (a `$text` Limit changes nothing, early Close is
      free and leak-free). `require`/`exclude` bind every
      hit in every mode (vector leg post-filtered via
      `Store.FilterTerms`), hit `data` is a `maxData`-rune window
      (default 512) with `dataOffset`/`dataTotal`;
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
    `createdAt` (added-to-account time: create for the
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
    `<root>/<accountId>/` (wallet.key, server.lock, server.pid, sdk/,
    index/), a
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
    **offloads all local state** (closes watchers + Store, evicts the
    any-sync space, drops the per-space CRDT collections and DB file —
    immediate in the normal case, reclaiming disk even offline; on a
    partial sweep failure the storage file is kept so the next boot
    retries), and kicks a background
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

18. **Event bus (SYN-151)** — the account-wide **ephemeral** event bus
    that generalized (and replaced) the UI command channel:
    `POST /v1/events` publish + `GET /v1/events/subscribe` filtered
    SSE. Envelope `{type, scope, spaceId?, target?, data?, sender}` —
    `type` an open dotted slug set (`ui.open_space` / `ui.open_object`
    are the migrated UI vocabulary, target space/object in `data`);
    `scope` ∈ device/account/space (`spaceId` iff space); `sender`
    server-stamped, rejected in the body; `data` ≤ 64 KiB
    (`events.payload_too_large`); `sessionId` reserved. Deliberately
    NOT a dataset — transient signals go through an in-process
    filtered broadcaster, not the SDK/handler/CRDT machinery (same
    consumer-side exception as `/search`). **At-most-once, no
    snapshot/replay**; publish returns `{subscribers: n}` (local
    matches; 0 = nobody listening, still 2xx); slow subscribers are
    dropped with `closed{reason:overflow}`. Subscribe filters:
    repeatable `scope`/`spaceId`/`type` (exact or `x.*` prefix)/
    `target` params — AND across dimensions, OR within one. Frames
    `ready` → `event` → `closed{reason}` on the shared reason set.
    **Account/space scopes ride the SDK pub/sub (SYN-152, SDK's
    SYN-150 `Space.PubSub()`/`SDK.PubSub()`)**: dotted type → slash
    topic under `ev/` with the target always one segment (`-` when
    absent); self-owned types (registry in events_topics.go, v1:
    `editor.cursor`) go into the spoof-proof `acc/…/<accountId>`
    namespace; SSE filters become NATS-style interests, refcounted
    per (scope, pattern) with only the maximal pairwise-disjoint
    cover subscribed so overlapping patterns never double-deliver
    (events_bridge.go); local delivery of a network publish is the
    SDK's synchronous Self loopback through the bridge (no second
    fan-out; `subscribers` = hub match count, approximate);
    `sender.identity` comes from the message signature, payload
    sender claims discarded. Explicit `scope=space` subscribe
    requires a `spaceId` filter (interest is per-space). Pub/sub
    sentinels map to `events.no_read_key` / `events.too_many_patterns`
    / `events.topic_not_owned`. Constraints: 64 KiB payload, ~30
    msg/s per-peer, 100 patterns/space. Account-scoped routes
    outside the `:spaceId` group, behind the `/v1` auth guard.
    `internal/server/events_hub.go` (filtered broadcaster; internal
    producers publish through `deps.eventsHub()` — none wired yet) +
    `events_topics.go` + `events_bridge.go` + `handlers_events.go` +
    `internal/api/event.go`. CLI: `any events publish/subscribe`
    (replaced `any ui`; `/v1/ui/commands[/subscribe]` and
    docs/15-ui-commands.md are gone). Contract: docs/21-events.md.
    Consumers migrate per `../any-ui/docs/tasks/events-migration.md`.
    e2e: internal/e2e/multipeer_events_test.go. **SDK prerequisite
    (branch syn-150, pseudo-versioned)**: `Space.PubSub()` +
    `SDK.PubSub()`; the same bump renames the spaceType vocabulary
    (`anytype.space`/`anytype.onetoone` → `any.space`/`any.onetoone`,
    passthrough wire change) and drags any-sync v0.13.1 +
    any-store/v2 beta.5.

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
    `any.onetoone`) + `author`, and status strings
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
    one BodyLimit-exempt route, see routes.go Skipper; mime resolved
    header → content → name's extension for text, `files_mime.go`,
    03-api.md § Files; `?name=&variant=&variantOf=`, a binary
    upload's extension-less name gains one); `GET …/files/:fileId/content`
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
24. **Per-space chats are client-registered** — a space's "general"
    chat is no longer a server concept. Clients register it as a bundle
    (item 35) and use the returned root; `SpaceInfo.generalChatObjectId`
    and the derived `any/general-chat/v1` object are gone with no
    back-compat. Motivation is unchanged (clients that each
    `Objects().Create` a chat leave a space with two or three parallel
    ones, most visibly in a 1-1) — the convergence point moved from a
    hardcoded derive to the registry, so different clients can register
    different things. Convention: bundle id `general-chat/v1`,
    `rootTypes: ["chat"]`, `derived: true` (item 35 — the chat root's
    id is computed from the bundle id, so it can never fork; chat
    content cannot be merged across objects, so a fork has to be
    impossible rather than resolvable). Contract: docs/03-api.md § Chat (Finding the
    chat object) + § Bundles, docs/16-chat.md, docs/08-clients.md § 4.
25. **Version history** — read-only HTTP surface over the SDK's
    `Space.History()` (`internal/server/handlers_history.go`,
    `internal/api/history.go`; routes wired in `handlers_spaces.go`).
    Four GETs under `/v1/spaces/:s/objects/:o/history`: bare (`ListChanges`
    — filters dataset / recordId (requires dataset) / traceId / author,
    `limit` default 50 capped 200, opaque `cursor`, `coalesce` +
    `coalesceWindow` grouping consecutive same-author changes into one
    entry keyed by the group's newest ChangeId), `/diff` (`Diff` — no
    `base` = per-change effect diff against the version's DAG parents;
    with `base` = cumulative `base..version`), `/:version` (`ViewAt` —
    live records at that cut grouped by dataset, raw `/query` row shape)
    and `/:version/datasets/:d/records/:r` (`RecordAt` — the
    record-scope fast path, no full-view materialization). Snapshot-only: **no
    subscribe variant**. A **version is a ChangeId** — the CID every
    write already returns as `changeId` — so it resolves on any peer;
    "state at version X" is X's causal past, not a wall-clock cut, and
    concurrent branches mean there's no total order (hence DAG order +
    cursor, not a timestamp range). `timestamp` is the author's clock,
    display-only — never sort or fence on it. Synced scope only: local /
    account values never entered the DAG and are excluded from views.
    Views are request-scoped (open → serialize → `Close()` inside the
    handler; no long-lived view handles over HTTP in v1). Per the
    layering rule the SDK owns structure and the server owns semantics:
    param coupling, limit caps and error mapping live in `historyError`
    — `ErrVersionNotFound` → `404 history.version_not_found`,
    `ErrViewTooLarge` → `413 history.view_too_large` ("narrow the
    scope"), `ErrHistoryTruncated` → `404 history.truncated`. The
    `HistoryChange.Truncated` field is RESERVED (always false — the SDK
    keeps full local history; it activates with the future
    snapshot-horizon contract). Static `diff` registered before the
    `:version` wildcard. No CLI surface yet. Contract: docs/03-api.md
    § Version history, docs/06-errors.md, and the SDK's
    `docs/version-history-proposal.md`.
26. **Push notifications (SYN-47)** — heart-interoperable mobile chat
    push (same `anytype-push-server` deployment; topics, payload JSON,
    crypto byte-compatible — golden tests pin the wire shapes).
    Sender-pushes, E2E-encrypted: keys derived from ACL state inside
    the SDK, never stored; the push node is a DIRECT out-of-band peer
    from config, not nodeconf. `internal/push.Service` (indexer twin,
    built only when `config.Push.Active()`): device-token persistence
    (`push-token.json`, background re-register), hash-gated
    subscription sync loop (space-list events + 5m tick →
    RegisterSpace owned/1-1 + SubscribeAll FULL REPLACE), buffered
    notify queue (6×10s, break on ErrNoValidTopics). Sender-scoped
    chat handler hooks (the `/search`-category consumer-side
    exception — a Changes() feed would double-push remote messages):
    send → superset topics (`chats`, `chats/<sha256hex(chatId)>`, per
    mention `chats/<sha>/<id>` + bare `<id>`) with heart's chatpush
    payload, groupId = sha256hex(chatId); edit → NEWLY-ADDED mentions
    only; read/read-all → silent own-identity wakeup. Settings:
    effective mode = `chat.notifyMode` (account-scoped prop on the
    chat object) ?? `settings.notifyMode` (tech-space row, new
    guarded `settings` subtree via `PATCH /v1/spaces/:id/settings` →
    `Spaces().SetSettings`; works with push disabled, tombstoned/
    pending rows writable, `SpaceInfo.settings` passthrough) ?? all;
    no valid chat override ⇒ bulk topics, any override ⇒ per-chat
    topics for every chat. Wire: POST/GET/DELETE `/v1/push/token`,
    GET `/v1/push/subscriptions` (account-scoped, outside `:spaceId`;
    409 `push.disabled` when `deps.push == nil`). Config
    `push.{enabled,peerId,addrs}` / `ANY_PUSH_*` (addrs
    comma-separated), threaded into the SDK at OpenSDK. The PRODUCTION
    push node is the packaged default, but only when the network is too
    — `config.ApplyPushDefaults` fills `{ProdPushPeerId, ProdPushAddr}`
    iff the config names no push peer AND no nodeconf, so a staging /
    local server (and every test) never pushes through production. CLI: `any
    push token set/revoke/status`, `any push subscriptions`, `any
    space settings <id> --set/--set-bool/--set-num/--unset`. e2e:
    `internal/e2e/push_test.go`, gated on `ANY_PUSH_E2E_PEER_ID` /
    `ANY_PUSH_E2E_ADDRS` (never stands up the push server's
    Redis/Mongo). **SDK prerequisite (shipped in v0.1.9):** `pushclient`
    component + tech-space `settings` subtree (`SDK.Push()` /
    `space.PushAPI`, `Spaces().SetSettings`, `ErrPushNotConfigured`,
    `sdkconfig.Push`).
    **Receiver-side keys**: `SpaceInfo.push` = `{spaceKey, encKey,
    encKeyId}` — the SDK mirrors the derived push key material onto
    each tech-space `spaces` row (device-local `push` field: per-space
    push-key watcher + `aclKickMux` fan-out over syncacl's single
    AclUpdater slot), so mobile clients cache `{encKeyId → encKey}`
    natively (append-only — old keys still decrypt late payloads) and
    decrypt pushes while `any` is down. Plain row field ⇒ present on
    list rows AND streamed by `/v1/spaces/query/subscribe` (rotation =
    row update). Encodings heart-compatible
    (`spacePushNotificationKey`/`...EncryptionKey`). Contract:
    docs/20-push.md § Receiver-side keys, docs/08-clients.md § 11.
    **Deferred:** reactions push, ACL/invite push, desktop receive
    (platform enum is ios/android — desktop is send-only),
    `RemoveSpace` cleanup. Contract: docs/20-push.md, docs/03-api.md
    § Push notifications + § Per-space settings, docs/16-chat.md
    (`chat.notifyMode`), docs/01-cli.md, docs/05-config.md.

27. **`SpaceInfo.ownRole` is live (SYN-62)** — the SDK (v0.1.11)
    mirrors the caller's own ACL permission onto each tech-space row
    (ACL mirror watcher — the generalized push-key watcher: one pass at
    space load + one per applied ACL record), so `ownRole` on
    `GET /v1/spaces[/:id]` now reports
    `owner/admin/writer/reader/guest/none` instead of always `"none"`,
    and role changes stream as row updates on
    `POST /v1/spaces/query/subscribe`. Pure passthrough — the bump plus
    docs (03-api.md § Spaces, OpenAPI enum tag) and a FullFlow e2e
    subtest; no handler change. `"none"` doubles as "not mirrored yet"
    (space never loaded on this device, pending join, tombstone) —
    `members/me` stays the authoritative read; 1-1 participants report
    `writer`, never `owner`. Side effect: un-breaks the push
    subscription sync's owned-space `RegisterSpace` gate
    (internal/push/topics.go reads `OwnRole == PermissionOwner`, which
    never fired before).

28. **Objects `modifiedAt`** — every per-space `objects` row now
    carries a derived row-root `modifiedAt`, stamped by
    the SDK's `SystemPropertiesHandler` from the change envelope:
    seeded at create (= the creating change's time), bumped by every
    valid synced write, LWW-convergent on the change's DAG order.
    Author's clock — sort/display quality only. Both stamps are
    `{"$date": …}` instants since item 37 (unix seconds when written). Local/account-scope
    writes don't bump it. Pure
    passthrough — an SDK bump plus docs (03-api.md § Data plane,
    08-clients.md § 3, 09-query.md § Paths) and
    `TestE2E_ObjectsModifiedAt`; no `any` handler change. Client
    recency ordering: `{"sort": ["-modifiedAt"]}`. SDK prerequisite:
    anyproto/any-sync-sdk#79 (`modifiedAt` in `anytype.Properties` +
    `Sink.DeriveOnce`).

29. **SDK boot rework adoption (SYN-99)** — the SDK's `Open` now
    returns after local wiring; eager space loading + offline catch-up
    run on one SDK-owned serial background pass, completion exposed as
    `SDK.BootstrapDone() <-chan struct{}` (pre-closed for headless;
    also closes when Close cancels the pass). `any` adapts:
    `GET /v1/health` gains `bootstrapping` (true while an authorized
    engine's pass runs — serving, catch-up in background; per-space
    convergence stays on `/sync-status`); the indexer's `spawnWorker`
    retries a failed `Spaces().Get` with backoff (2s doubling, 1m cap
    — the failure is the first materialization racing the boot pass,
    and a quiescent space emits no further list events; cancelled on
    drop/close, `internal/indexer` spawn_retry_test.go); push gets a
    `Kick()` on `BootstrapDone` (engine.go) so the first subscription
    sync doesn't miss offline-created chats until the 5m tick; the
    SDK's new `space.ErrSpaceNotTracked` (read-state marks racing a
    space delete/remove) maps to `404 space.not_found` instead of 500
    (sdkOpError). Docs: 02-server.md § Startup + § Health, 03-api.md
    § Meta.

30. **Built-in `page` type — REMOVED by item 43.** It was the marker
    type for "this object is a document" (no dataset, no properties —
    the SDK freezes registered types' property definitions, so a
    built-in could never carry per-space columns), introduced because
    each client minting its own `pages` type raced into parallel
    definitions. The convergence problem is now solved by registering
    the document type as a bundle (`page/v1` by convention), which is
    a user type with properties, a weight, a layout and an editor part.
    `any.tags` (the SDK's free-form string array on `any`) stays.

31. **Devices registry + active-app election (SYN-165)** — the
    account's device list in the tech-space system dataset `devices`
    (row id = peerId, all synced: name/os/version, `apps` open-slug
    install flags, `activeClaims` per-slug `{seq, at}`), wrapping the
    SDK's typed surface (`Spaces().SetDevice/ClaimActive/DeleteDevice/
    ListDevices`, `SDK.PeerId()`, `space.ActiveDevice`). Election is
    reader-side and deterministic on writer-supplied claim data —
    highest `seq`, tie highest `at`, tie largest peerId, candidates
    only rows still carrying the slug under `apps` — NEVER on `_ver`
    (versionIds are peer-local). The SDK's `space.ActiveDevice` is the
    single implementation; `GET /v1/devices` returns it pre-resolved
    as `active: {slug: peerId}` plus `self` (this server's peerId) so
    UI and runtimes never reimplement the rule. Account-scoped routes
    (`handlers_devices.go`): GET `/v1/devices`, POST
    `/v1/devices/query[/subscribe]` (raw windowed primitive, dataset
    fixed), PUT `/v1/devices/me` (self-row `{name?, apps?}`,
    `"apps":{"slug":null}` uninstalls), POST `/v1/devices/activate`
    (`{app}`, self-heals the install flag), DELETE
    `/v1/devices/:peerId` (404 `device.not_found`; sticky tombstone —
    a pruned peerId can never re-register). Engine boot upserts the
    self row (os/version each boot; hostname name only on first
    registration — `registerDevice` in engine.go). CLI: `any devices
    list/register/activate/remove/query/subscribe`. e2e:
    `internal/e2e/multipeer_devices_test.go` (two devices, same
    mnemonic: concurrent claims converge to one winner on both
    readers; uninstall moves the role; prune). Contract:
    docs/23-devices.md (model + election + decision matrix),
    docs/03-api.md § Devices, docs/01-cli.md.

32. **Runtime dataset schemas + upsert (SYN-147)** — wraps the SDK's
    user-space dataset schemas: clients define a dataset ON A USER TYPE
    at runtime (`POST/GET/PATCH/DELETE
    /v1/spaces/:s/types/:t/datasets[/:defId[/fields[/:fieldId]]]` →
    `TypesAPI.AddDataset/Datasets/PatchDataset/RemoveDataset(Field)`),
    and the SDK's generic SchemaHandler enforces the declaration on
    every peer — required fields, write-once vs author-mutable
    (`mutableBy`), author-only delete (needs a `stamp:creator` field),
    derived creator/createTime/modifyTime stamps, id rule
    (`auto`|`user` + pattern/maxLen). Behavioral parts pin first-write;
    display leaves (`description`/`displayName`/`search.title`/
    `search.text`) patch via `{set,unset}`; evolution is additive-only
    (added fields never `required`). Data rides the existing
    modify/query surface; `POST /v1/spaces/:s/upsert` (`Space.Upsert`,
    body `{objectId, dataset, records[{id,fields}], pageSize?,
    traceIds?}`) adds idempotent batch ingest for `id:user` datasets —
    diff-by-record-id, only declared-mutable fields written, identical
    records skipped, per-record rejection codes
    (`upsert.immutable_field`/`not_author`/`record_deleted`/`rejected`)
    inside the 200 body. Discovery: `DatasetSchema.TypeId` + behavioral
    `x-*` keywords (incl. `x-search {title,text}`) flow through
    `GET /v1/spaces/:id/datasets`. Search: `index.SchemaChunker`
    (virtual name `schema`, self-gated `DynamicChunker` — the worker
    prefix-evicts per-dataset on type detach + def removal) indexes
    runtime records by the x-search mapping under scope `basic`; no
    catalog TTL (the SDK snapshot refreshes synchronously on defs
    apply); `prop`/`schema` names reserved at the creation API. Error
    mapping in `datasetWriteError` (sentinels + STOPGAP string-matched
    decl errors — SDK sentinel follow-up in docs/07-roadmap.md, along
    with the dogfood handler-collapse audit and the removed-def index
    sweep). CLI: `any type dataset …`, `any upsert`. Contract:
    docs/03-api.md § Runtime dataset schemas + § Upsert records,
    docs/13-index.md § Schema chunker, docs/06-errors.md, and the SDK's
    docs/17-user-datasets.md (vocabulary, convergence rules, storage
    model). **SDK prerequisite:** shipped in `any-sync-sdk v0.2.0`.
33. **Process helper (SYN-153)** — progress reporting + cancel over the
    event bus: `process.*` events (envelope target = process id, keyed
    `(sender.identity, id)`, descriptor folded into every frame) + an
    in-memory last-event-wins registry with staleness expiry (running
    45s / terminal 60s, heartbeat ≤15s, lazy sweep — no janitor; a
    restart forgets everything). Surface: `GET/POST /v1/processes`,
    `POST /v1/processes/:id/{progress,finish,cancel}` — finish takes
    `{status: done|failed|cancelled, error?}` (error iff failed);
    cancel resolves the composite key (`404 process.not_found` /
    `409 process.ambiguous` + `details.identities`), emits
    `process.cancel {identity}` on the process's own scope, never
    mutates state — the owner reacts and finishes. Progress fields are
    all-optional (absent = keep, `{}` = pure heartbeat); non-failed
    events clear a stored error; unvalidated remote payload fields
    are sanitized before entering the view. Registry = synchronous
    construction-time hub tap (loss-free); EVERY network-scope bus
    publish (`/v1/processes` and raw `/v1/events` alike,
    `publishNetworkEvent`) applies directly to the view (Self
    loopback needs an interest). Shared bus plumbing: `publishScoped`
    / `validateEventScope` / `validTargetToken` (handlers_events.go)
    serve both surfaces. Remote visibility: standing account-scope
    `ev/process/>` interest acquired at boot with backoff-retry
    (release on close; the bridge retries failed re-subscribes —
    `bridgeResyncRetry`); space scope only while a local subscriber
    holds an interest covering `process.*` on the space. Internal
    producers (SYN-155, all device scope,
    `indexer.Options.OnProcess` → `deps.indexerProcess`, threaded
    through `OpenIndexer`/`NewEmbedder`/`NewLocal`; fts+embed gated on
    `Options.AnnounceAfter` — announce only past 3s of elapsed work,
    so usual per-edit indexing never appears; time not queue-size
    because docs/s varies ~50× with text length): `index.embed.<spaceId>`
    (done/total docs via `Store.PendingCount`, 500ms gate tick +
    10s mid-drain heartbeat), `index.fts.<spaceId>` (gate checked at
    page boundaries, done = changes, total unknown),
    `index.model_download` (always announces at start — even offline;
    done/total bytes; retry failures are progress-with-message, not
    terminal; complete .part installs without a network round-trip —
    the 416 wedge). All three share `procReporter`
    (process_report.go): gate tick + heartbeat + joined-ticker
    terminal, so no frame trails a terminal. Registry folds counters
    on every frame (pointer decode: absent = keep, negatives
    clamped); progress POSTs fold atomically under the registry lock
    (`mergeOwn`). Worker-cancel → cancelled; generic failure messages
    — no fs paths on the wire; cancel requests ignored by all three. CLI:
    `any process list/cancel`. Files: internal/server/processes.go +
    handlers_processes.go, api/process.go; e2e
    internal/e2e/multipeer_processes_test.go. Contract:
    docs/22-processes.md.

34. **Derived spaces registry (SYN-164)** — well-known per-account
    spaces derived deterministically from the account keys + a fixed
    seed, so every client/device converges on THE space (no
    check-then-create races). Raw `Service.Derive`/`DeriveId` stay off
    the wire (a free seed would mint a permanent space and invite
    silent collisions); the vocabulary is the compiled-in registry
    `internal/server/derivedspaces.go` (seed convention
    `any/space/<name>/v1`; v1 entry: `bao`, the agent space).
    Surface: `GET /v1/spaces/derived` → `[{name, spaceId, created,
    status?}]` (ids resolved ONCE at engine boot — `deps.derived`;
    resolves, never creates; `created` = usable row exists on any
    device, tombstoned rows report created=false) and
    `POST /v1/spaces/derived/:name` → Service.Derive, lazy +
    idempotent, 201 SpaceInfo, registry DisplayName rides
    `DeriveRequest.Name` on first materialization (404
    `space.derived_unknown` off-registry, 409 `space.deleted` for a
    pre-guard-wedged row). Derived spaces are PERMANENT: DELETE
    refuses 409 `space.derived_undeletable` via a layered guard —
    server pre-check on the boot-resolved ids (covers unmaterialized
    ids; infallible map lookup) + SDK flag refusal
    (`space.ErrIsDerivedSpace`; Derive stamps a synced set-once
    `derived` bool on the tech-space row, pinned by the handler like
    `type`, healed onto pre-flag rows by re-running Derive) + the
    SDK's apply-side handler drops `remoteStatus=deleted` on flagged
    rows from any peer + its deletion reconciler exempts them (a
    coordinator NotExists for a space derived offline must not
    tombstone it). `SpaceInfo.derived` passthrough; joiners never
    carry the flag, so their removal stays allowed. Side effects of
    the same SDK bump: SDK Delete now refuses the tech-space id
    (`ErrIsTechSpace`) and row-less ids (`ErrSpaceUnknown` → 404
    instead of a silent 204). CLI: `any space derived
    [create <name>]`. e2e: internal/e2e/derived_spaces_test.go.
    Contract: docs/03-api.md § Spaces → Derived spaces,
    docs/06-errors.md. **SDK prerequisite (shipped in v0.2.1):**
    Delete guards + `ErrIsDerivedSpace` + `SpaceInfo.Derived` +
    `DeriveRequest.Name`.

35. **Bundles registry over HTTP** — what a space has installed lives
    in the SDK's per-space registry (`Space.Bundles()`, the `bundles`
    dataset on the spaceIndex object; design in the SDK's
    `docs/bundles.md`). A bundle is one NON-derived root object under a
    permanent versioned id, with setup objects derived from it
    (`ParentId`), so one converged id names the whole install. A
    derived root cannot be deleted, so two devices installing while
    apart would leave a permanent shadow install; the registry picks
    one deterministic winner (`rootId`, LWW), keeps every claim in the
    add-only `roots` set, and leaves the rest in `losers` — mergeable
    and deletable.
    - **Clients register their own.** The server keeps NO catalog and
      installs nothing: `internal/bundles` is a generic engine
      (`Install{Id,Name,RootTypes,RootProperties}`, `Resolver`
      with Ensure/Get/List/Resolve/ResolveRetry, `Child`), and
      `internal/server/handlers_bundles.go` is the wire surface.
    - Endpoints: `POST /v1/spaces/:s/bundles` (adopt-or-install →
      `{bundle, installed}`; the server creates the root with the
      requested types/properties), `GET …/bundles`,
      `GET …/bundles/:bundleId`, `POST …/bundles/:bundleId/resolve`
      (`{loserRootId}`, idempotent), `POST …/bundles/:bundleId/children`
      (`{seed, types?}` → deterministic child of the winner). **Bundle
      ids carry a slash, so path segments are percent-encoded**
      (`general-chat%2Fv1`); bodies take them verbatim. Rows are also
      readable through the generic dataset surface (that path is
      read-only — the SDK fences the dataset off modify).
    - The `id` is the whole identity — marketplace id, app slug, or a
      versioned convention like `general-chat/v1` — so there is no
      separate provenance field.
    - **Derived roots (SYN-172).** `"derived": true` installs the
      bundle on the root DERIVED from its id
      (`spaceindex.BundleRootSeed`, seed `builtin:bundleRoot:<id>`).
      A derived root change carries no identity/signature/timestamp, so
      the id is a pure function of (space, bundle id): every device
      computes it offline and no install can fork — the only workable
      shape for a 1-1, where the ACL owner is a synthetic key nobody
      holds, both participants are writers, and neither can ever take
      the owner escape below (the ticket's `409 bundle.not_ready`
      deadlock). A derived install runs the same convergence wait
      (30s connected, 3s with no peer — a head-sync round against
      nobody always answers the same) but installs when it expires
      instead of refusing; the residual risk is that a blind claim
      demotes an unseen CREATED install of the same id, irreversibly
      (docs/03-api.md states the trade).
      Costs, both permanent: no
      uninstall (any-sync's `ErrCantDeleteDerivedObject`) and no
      migration — an existing created install is ADOPTED, with
      `Bundle.Derived` reporting which it is. If a created and a
      derived root are both claimed, **the derived one wins on every
      replica** (read-side verdict over the add-only `roots` set —
      order-independent, unraceable), so the created one stays an
      ordinary resolvable loser and the undeletable root is never
      stranded as one. Children of a derived root bind BY SEED
      (`<rootId>/<seed>`), not `ParentId` — any-sync rejects a derived
      object as a parent (`objecttree.ErrDerivedParent`) — losing only
      a cascade that is moot on an undeletable root. SDK:
      `EnsureBundleRequest.{DerivedRoot,RootTypes}`, `Bundle.Derived`,
      `BundlesAPI.DerivedRootId` (pure computation, no registry read).
    - **Convergence gate on install.** Adoption is a pure read (so
      readers/guests resolve installs they cannot create; the install
      path is a write and 403s for them). Installing a CREATED root
      first runs
      `sp.WaitIndexSynced` bounded 30s — the registry rides the space's
      index tree, and ensuring against unsynced state reads "nothing
      installed" and forks a second root. A bare `SyncHeads` nil is not
      proof (any-sync swallows per-peer failures); the wait also
      demands the Synced rollup, and its local fast path keeps an
      offline owner of a seeded space instant. When the wait fails, the
      OWNER installs anyway (offline-first: only this account's own
      devices could compete, and the registry converges those), any
      other member gets `409 bundle.not_ready`.
    - **Merging is the client's job, timing is the server's.** Resolve
      deletes a losing root only after the client says it merged; the
      server refuses (`409 bundle.loser_not_ready`) unless the SDK
      reports the root `SyncStateSynced` (unknown — the post-restart
      and never-arrived state — does NOT count) and it has been
      observed as a loser for `Resolver.Grace` (5min). The clock starts
      at first OBSERVATION (`Get`/`List`/boot pass warm it), not at the
      first resolve call. Permanent verdicts come first: the winner or
      an unclaimed root is `409 bundle.not_loser`, an already-resolved
      one is 204. The SDK's `ErrLoserNotSynced` maps to the same
      retryable code. After a timing refusal the server retries in the
      background — one loop per loser however often the client polls,
      in-memory, dropped on restart. An adopted winner whose tree is
      not local is `409 bundle.not_ready` rather than an id that 404s
      on write; `/children` maps `objecttree.ErrParentNotFound` to the
      same.
    - **Input is bounded and pre-flighted**: id ≤256B, name ≤1024B,
      rootTypes ≤32, rootProperties ≤64KiB, seed ≤256B; type existence
      (`Types().Get`) and property formats (`validateFormatValues`,
      the objectCreate gate) are checked BEFORE the root is created, so
      a rejected request leaves no orphan. The create+register section
      runs on a context detached from the request (shutdown-bounded, 2m
      timeout) so a client disconnect mid-Ensure cannot orphan a root.
      Records are permanent and `roots` only grows — ids are a small
      fixed vocabulary, not a scratch namespace.
    - **Nobody arbitrates who installs** — the server used to pick a
      sole installer, and no longer does. Clients either ask for a
      derived root (the id both sides would compute anyway), agree out
      of band, or handle `losers`.
    - Boot pass (`internal/server/derivedsetup.go`): for the well-known
      derived spaces the account already has, `WaitListSynced` (90s) →
      open → `WaitIndexSynced` (90s) → List, so a client ensuring right
      after a restore meets the converged registry instead of an empty
      one. It never materializes a space, installs nothing, and deletes
      nothing — losing roots are logged, not resolved. The list wait
      falls through to the local space list; an expired index wait
      skips the entry until the next boot (a read before convergence is
      the blind read this pass prevents). A never-set-up space
      converges to an empty registry rather than stalling.
    - Tests: internal/bundles/bundles_test.go (engine logic against a
      fake space — verdict order, timing guards, idempotency, retry),
      internal/server/handlers_bundles_test.go (ensure/adopt, root
      properties, list/get, children, resolve
      verdicts, dataset read, derived install / adopt-precedence /
      children / validation), internal/e2e/multipeer_bundles_test.go
      (joiner adopts the owner's root, children converge),
      multipeer_onetoone_test.go (both sides install the derived chat
      on the FIRST attempt — no convergence polling — and their copies
      merge), derived_spaces_test.go, and the SDK's
      e2e/bundles_test.go `TestE2E_BundlesDerivedRoot`. Contract:
      docs/03-api.md § Bundles (incl. Derived roots).
36. **Type xKey lives on the meta-type (SYN-173)** — a type's
    programmatic handle moved from `any.xkey` to `type.xkey` in the
    SDK. `any` is the universal type, so declaring `xkey` there
    advertised it on every object (`GET …/types/any/properties`) and
    let any row carry one; the meta-type's namespace is fenced by the
    handler's membership check, so only rows carrying the `__type__`
    marker in `any.types` can. Marker and namespace are deliberately
    different strings — `_`-prefixed top-level fields are
    protocol-owned, so `__type__` cannot be a storage namespace.
    Wire-visible consequences, both passthrough: `GET
    /v1/spaces/:id/types` gains a third synthetic built-in row (`type`,
    the meta-type, one `xkey` property) ahead of the registered types,
    and its id/xKey are now reserved against user types by the existing
    `type.xkey_conflict` guard. `TypeInfo.xKey` is unchanged; only the
    raw row path moved. No back-compat — types created before the bump
    read back with an empty `xKey`. The web UI's object-type filter
    skips the synthetic ids. Contract: docs/03-api.md § Types, SDK
    docs/06-data-structure.md.
37. **Datetime values (SYN-136)** — every timestamp is any-store's
    native instant (`TypeDateTime`: unix millis, memcmp-orderable,
    index-keyable) instead of an ISO string or an epoch number, which is
    what the date operators (`$year`/`$dateTrunc`/`$dateDiff`) compute
    on — they returned null against everything the server stored.
    **Wire shape: `{"$date": "2026-08-05T17:00:00.000Z"}`** in both
    directions (writes also take `{"$date": <millis>}`), including
    filter literals — a bare number or string doesn't error, it answers
    empty: ordering comparisons are bracketed by type (any-store
    v2.0.2), so a number or string literal never matches an instant,
    and a wrapped `$date` filter never matches a leftover epoch
    number. SDK side (`any-sync-sdk`): new `datetime` property
    kind, implied by the `date` / `datetime` formats (`kind: "string"`
    stays accepted for the legacy ISO convention, and kind is pinned
    first-write, so existing properties never move); derived stamps
    (objects-row `createdAt`/`modifiedAt`, tech-space `spaces.createdAt`,
    runtime-dataset `createTime`/`modifyTime`) are instants. `any` side:
    `api.PropertyKindDatetime` on the types surface, `checkFormatValue`
    validates the ext-JSON instant (a `date`-format value must land on
    midnight UTC) while string-kind props keep the old checks, chat's
    `createdAt`/`modifiedAt` AND its reaction leaves
    (`reactions.<emoji>.<accountId>`) are instants, and the prop chunker
    indexes a date as its RFC 3339 text so search still matches
    "2026-08".
    **No migration** — the SDK's new version-driven re-index (SYN-178)
    rebuilds affected rows from the DAG when a handler version bumps,
    lazily per object plus a background sweep per space. Consequence for
    `any`: the search indexer now tracks the SDK's per-space
    `Generation` next to its cursor and drops + reindexes the space when
    it changes (a wiped sdk.db restarts applySeq at 0, which silently
    froze the index before). Contract: docs/03-api.md § Types + § Data
    plane + § Chat, docs/09-query.md § Dates, docs/14-aggregation.md,
    docs/08-clients.md § 3.

38. **Favourites as a client bundle + created tech roots + locked bundle
    reads** — favourites is a CLIENT-REGISTERED bundle (`favorites/v1`,
    canonical declaration in docs/25-favorites.md; no server code, no
    boot ensure, no reserved ids). The tech space accepts both bundle
    root strategies: `derived: true`, or the default CREATED root minted
    by the SDK's Ensure (self-typed, declaration-carrying) — deletable
    (`DELETE …/objects/:rootId` = uninstall, id reads uninstalled,
    reinstall mints fresh) and forking on concurrent offline installs
    (`…/bundles/:id/resolve` is routed on the tech space). Read-side
    lock: `GET …/bundles[/:id]` answers after the registry convergence
    wait and carries `synced` (true = absence definitive; get returns
    `{bundle, synced}`); ensure's convergence gate is the same wait.
    Client startup contract: locked read → adopt; ensure on first
    write; fork → merge loser entries → resolve. SDK prerequisite
    (PR #108 branch): created roots with declarations + SDK-minted
    roots (`NewRoot` optional), tech `ResolveLoser`, bundle-root-only
    `Objects().Delete`, catalog release on type-object purge.

39. **Built-in `data_view` type: saved views (SYN-175)** —
    `internal/dataview` registers `data_view`, attachable to ANY object
    including a TYPE object (that is how "views on a type" works —
    `AttachType` has no meta-type guard), owning the `data_views`
    dataset: one record per saved view
    (`name`/`icon`/`pos`/`layout`/`query`/`layoutSettings`). Built-in
    for the `page` reason — views are shared client vocabulary, and a
    client-minted user type races into parallel definitions. `query`
    and `layoutSettings` stay **opaque** (checked "is an object",
    nothing more): clients own the filter/sort/groupBy vocabulary and
    reconcile rules naming a deleted property, because validating refs
    server-side would make a deleted property a WRITE FAILURE instead
    of a rule the client marks invalid. `filter`/`sort` are the
    `/query` body shapes verbatim, so a view feeds straight into
    query/subscribe; `query.type` is `"plain"` today, the
    discriminator reserving room for an aggregation-backed view.
    **No bespoke endpoints and no CLI** — writes ride
    `POST /v1/spaces/:id/modify`, reads `…/query[/subscribe]` sorted by
    `pos` (indexed). **No bespoke handler either**: the dataset declares
    `Handler: nil` and the SDK's generic SchemaHandler enforces the
    declaration — `name`+`pos`+`layout` required (`pos` because an
    absent one sorts as `""`, ahead of every positioned view on every
    peer), everything synced `MutableByAnyone` + `DeleteByAnyone` (a
    shared view is space furniture; readers are ACL-fenced),
    `creator`/`createdAt`/`modifiedAt` stamped
    (`StampCreator`/`CreateTime`/`ModifyTime`, client writes rejected),
    `IdRule: IdUser` so the default view is a fixed id + upsert instead
    of a create-on-open race. `Dynamic: true` — schema is enforced on
    every peer at apply time, so a closed keyspace would silently drop
    a newer client's key on an older peer. **A deleted record id is
    burned permanently**: re-upserting it returns 200 with a
    `rejections` entry and creates NOTHING, so the documented ensure
    inspects `rejections` and walks a deterministic id sequence
    (`default`, `default-2`, …) — the sequence is what keeps two
    devices converging on the same replacement
    (TestServer_DataView_DeletedIdIsBurned).
    Device tier: `localSettings` is a top-level `ScopeLocal` object
    mirroring `layoutSettings` (scope is per TOP-LEVEL field, so
    `layoutSettings.widths` cannot be device-local on its own) — the
    client renders the merge, local wins. Not search-indexed (no
    chunker). Only the SHARED tier ships; account/device-private views
    need scoped DATASETS (SYN-174), and date bucketing in `groupBy`
    needs native datetime values (SYN-136) — grouping is client-driven
    via `/objects/aggregate` for distinct values + counts, then one
    plain query window per visible group. Same slice wires the two
    former 501s `POST …/properties/:objectId/{attach,detach}/:typeId`
    (`handlers_properties.go`, idempotent; attach pre-flights BOTH ids
    — `404 object.not_found` / `type.not_found` — because `any.types`
    is a synced DAG write with no validation behind it, so a typo'd
    type would replicate forever; detach checks neither, it is the
    repair path, and leaves records as read-tolerant orphans). Contract:
    docs/24-data-views.md, docs/03-api.md § Types + § Properties,
    client recipe docs/08-clients.md § 12.
40. **Local store** — device-local, non-CRDT any-store collections at
    `/v1/local` (`internal/localstore` + `handlers_local.go`,
    `api/local.go`, `client/local.go`, `cli/local.go` — `any local …`).
    They live INSIDE the SDK's `sdk.db` (handle via `SDK.Store()`,
    any-sync-sdk#111) under a name tag — `l_a_<name>` /
    `l_s_<spaceId>_<name>` — because a DB-wide read tx is what makes
    local↔synced `$lookup` and `$out`/`$merge` rollups possible
    (both gated upstream today; docs/07-roadmap.md); the SDK's orphan
    sweep classifies the `l` prefix as a fixed collection and never
    touches it. `localstore.ParseRef` is the single fence: every name
    reaching any-store — wire refs, drop, and the raw `$out`/`$merge
    into`/`$lookup from` names inside a pipeline — passes it, so the
    server can never address an SDK collection (`400
    local.bad_sink_target`). Two invariants kept structurally: `any`
    never writes an SDK collection (a direct write is reverted by
    re-index) and never opens a tx spanning a local and an SDK
    collection. Not a dataset: no type/schema/handler, no `_ver`, no
    subscribe, not search-indexed; **a space-scoped collection
    outlives its space** (no cleanup hook — `drop` is the cleanup
    path; 1-1 re-derivation inherits stale rows), so a manual wipe of
    `sdk.db` loses local data. Sink/lookup targets inside a pipeline
    must be existing local collections (never minted by a sink). Writes chunk at 256 docs per
    tx (single any-store writer shared with the CRDT apply path);
    delete-by-filter is not atomic per call. Config `local.enabled`
    (`ANY_LOCAL_ENABLED`, default true) → `409 local.disabled`.
    Contract: docs/26-local-store.md, docs/03-api.md § Local store,
    docs/06-errors.md, docs/01-cli.md, docs/05-config.md,
    docs/02-server.md § Data dir layout.
41. **Objects `modifiedBy`** — every per-space `objects` row carries a
    derived row-root `modifiedBy` next to `modifiedAt`: the account
    identity that signed the change `modifiedAt` names — the object's
    last writer, equal to `author` until someone edits it. Same
    StrKey encoding (`PubKey.Account()`) as `author`, chat `creator`,
    the `identity` of `GET …/members` and the `id` of
    `GET /v1/account` — all five cross-compare directly, no
    re-encoding. One change stamps the pair at one VersionId, so the two
    move together and a row never pairs one change's time with
    another's signer; concurrent writers resolve on DAG order, not
    clock. Absent = a row not yet rebuilt, or a latest change with no
    known signer — never "nobody". `modifiedAt` is indexed,
    `modifiedBy` is not (filtering on it scans). Pure passthrough — an
    SDK bump plus docs (03-api.md § Data plane, 08-clients.md § 3,
    09-query.md § Paths, website database/system-fields) and
    `TestE2E_ObjectsModifiedAt` / `TestE2E_MultipeerModifiedBy`; no
    `any` handler change. Consequence: the SDK's objects-handler
    LocalVersion bump re-indexes every object row from the DAG
    (lazily per object plus the background sweep, item 37). applySeq
    keeps climbing across the rebuild, so `Generation` does not rotate
    and the search index is NOT dropped — rebuilt rows resurface on
    the change feed and are re-indexed incrementally. SDK
    prerequisite: anyproto/any-sync-sdk#113.
42. **Query projection (SYN-207)** — `projection` is honored on every
    windowed query/subscribe body (`/objects/query[/subscribe]`,
    `/query[/subscribe]`, `/spaces/query[/subscribe]`, `/devices/…`,
    `/objects/:o/files/query[/subscribe]` — one shared builder, so they
    move together, plus `/v1/local/query` for API alignment: the
    protocol-field rules are inert there since a local record has no
    `_ver` and no delivery counters, and `$project` inside
    `/v1/local/aggregate` is the pipeline equivalent). Grammar is mongo's: a flat object of dotted field
    paths to `1` / `-1`, mode inferred (`{"any":1}` include,
    `{"_ver":-1}` exclude), deepest mark wins so `{"nav":1,"nav.pos":-1}`
    is a subtree minus a leaf. **Zero SDK work**: any-store has no
    find-path projection and `ProjectionOpts` is only `IncludeDeleted`,
    so this is `any`'s serialisation boundary — `internal/server/
    projection.go` (parse + shape) + `projection_shape.go`
    (`recordShaper`, which generalised the old `strip ...string`
    blocklist; the blocklist still runs LAST, so naming a withheld
    tech-space field cannot surface it).
    Three client-facing rules: `id` always ships (`{"id":-1}` is 400),
    `_ver` narrows automatically (never name a `_ver` path;
    `{"_ver":-1}` drops it), and a projected field the record lacks
    stays absent. `_`-prefixed fields sit OUTSIDE mode inference with
    their own defaults — `_ver` in (narrowed), `_traces`/`_deletedAt`
    in (whole — `_traces` is the optimistic client's echo-correlation
    map), `_addSeq`/`_applySeq` out — so `{"_ver":-1}` alone still
    means "every user field". No projection (or an empty one) ⇒
    byte-identical to before, counters included.
    **The `_ver` rule**: follow the INCLUSIONS, keeping `*` (the
    per-level default marker) at every level descended into and copying
    matched subtrees verbatim, then apply the EXCLUSIONS so a dropped
    field drops its version too. Contract: for every included path the
    narrowed map resolves to the same version as the full one (lookup
    falls back to `*` exactly where it did). Excluded paths are outside
    the contract. Subtrees are never collapsed to their max version —
    that over-reports a leaf and makes a client discard a live local
    edit. Arrays are descended ELEMENT-WISE (`{"tags.name":1}`).
    Applies to `changes` frames too (requirement 2 of the issue): docs
    and per-field ops. Only `$set`/`$unset` reach the wire (the SDK
    normalises `$inc`/`$addToSet`/`$pull` in its `internal/subscribe`
    `projectOp`), so ops are keep / narrow-the-payload / drop; the
    multi-field form (empty path, payload keys are DOTTED PATHS) is
    classified per key — which also closed a blocklist hole, since a
    top-level `Del` never matched `guestKey.x`. A $set whose narrowed
    payload holds nothing becomes a $unset, so the op and the doc in
    the same frame agree the field is gone (in the multi-field form
    those keys split into a companion $unset op — hence `op()` returns
    a SLICE); `ops` can come back empty under a projection (documented
    on api.QuerySubscribeRecord). A multi-field $unset's payload VALUES
    are placeholders the CRDT ignores, so only its KEYS may be
    classified — narrowing a placeholder drops the removal.
    Four traps worth remembering: mark parsing MUST use GetFloat64
    (fastjson's GetInt answers 0 for `1.0`, silently inverting include
    into exclude); deepest-wins has to be honoured by all THREE walkers
    (record body, op path, `_ver`) or the halves disagree —
    TestProjection_OpWalkersAgree drives the two op walkers off one
    table for exactly that reason; an array element that projects to
    nothing must stay as `{}` so length and indices survive; and the
    local store passes `freeform`, which turns the whole `_`-prefixed
    protocol namespace off (a local record has none, so `_x` there is
    the caller's own field and counts towards mode inference).
    Include mode builds the fastjson value from only the named anyenc
    subtrees, so the conversion and the marshal are O(projected) and
    allocation-free; exclude mode converts then carves (the kept keys
    are the record's own `[]byte`, and re-keying them would allocate
    per field per record). Measured (`BenchmarkShapeRecord`,
    `BenchmarkObjectsQueryProjection`): per record 1359 ns / 1023 B /
    2 allocs → 306 ns / 195 B / 0 allocs for `{"any":1,"nav":1,
    "_ver":-1}`; end-to-end through the handler 2.0× faster and 5.2×
    less wire. Exclude-only (`{"_ver":-1}`) is 1.2× — it still pays the
    full decode, which is why the store-side push-down stays in
    docs/07-roadmap.md. CLI: `--projection 'any,nav'` / `'-_ver'` on
    every windowed command. Contract: docs/09-query.md § Projection,
    docs/03-api.md, docs/04-events.md.
43. **Auth ownership model (SYN-169)** — `mode` (`--mode` / `ANY_MODE` /
    config / `embedded.Options.Mode`) declares who owns the server and
    derives key custody, account selection, logout/switch and shutdown
    rights from it: `standalone` (default — the user; `wallet.key` on
    disk, account resolved from disk, logout and HTTP shutdown refused)
    vs `managed` (the spawning host; account key supplied per boot over
    `POST /v1/auth` and never on disk, `DELETE /v1/auth` + `replace`
    switch + `POST /v1/shutdown` allowed behind the **control token**).
    Mode is fixed at launch and unreachable over HTTP; `GET /v1/auth`
    reports `mode` + `capabilities{deauthorize,switchAccount,shutdown}`
    bits (clients branch on bits, never the string) and an empty
    `accounts` list on managed. The token is minted per managed server
    and printed as the second stdout handshake line (`CONTROL_TOKEN
    <hex>` after `LISTENING`) unless the in-process host passes
    `embedded.Options.ControlToken` (required in managed); the header is
    `X-Any-Control-Token`, checked by `requireControl` (control.go),
    `403 control.forbidden`. **Managed custody**: `auth.NewMnemonicProvider`
    (SDK v0.2.8, in-memory account key) + a device key cached at
    `<root>/<accountId>/device.key` (devicekey.go, JSON envelope v1,
    0600, minted once) so every login keeps the peerId; `bootEngine`
    takes a `credential` opener (`fileCredential` / `managedCredential`,
    wallet.go + devicekey.go) and `AccountID` is widened to
    `auth.Provider`. **Engine lifecycle**: the engine (lock, SDK,
    indexer, push, local store, chunkers, its goroutines) is tearable
    down in place behind an `engineGate` (gate.go) — every `/v1`
    request and stream runs inside it; `teardownEngine` flips `ready`
    off, cancels the engine ctx (streams emit `closed{deauthorized}`;
    process exit keeps `server_shutdown`), drains the gate (10s),
    joins the engine goroutines (`registerDevice`, push kick,
    `holdProcessInterest`, `bootstrapDerivedSetups`, bundle
    `ResolveRetry` — all via `engine.spawn`), detaches the events bridge
    (`gen` counter refuses stragglers), resets the process view and
    bundle observations, closes resources, clears the deps fields.
    `deps.shutdownCtx` is the LIVE engine's ctx (nil while unauthorized);
    `streamsWG`/`cancelShutdown` are gone. **Nothing inside the gate
    may take `authMu`** (teardown holds it while draining) — exempt
    routes (health, auth, shutdown) read through short gate
    enter/leave. **`POST /v1/auth` decides on the derived account**:
    same account → `200 {alreadyAuthorized:true}` (never tears down);
    `{}` while authorized → 409 `auth.already_authorized`; different →
    403 `auth.not_managed` (standalone) / 409 `auth.account_mismatch`
    (managed, no `replace`) / `switchAccount` (managed + `replace`:
    teardown then boot under one `authMu` hold; a failed boot leaves
    the server unauthorized); refusals never echo the derived id;
    `{accountId}` is 400 on managed. **Lifecycle**: `POST /v1/shutdown`
    → 403 `shutdown.not_managed` on standalone, token-gated on managed;
    `any stop` sends no HTTP — `server.FindRunning` probes each account
    dir's `server.lock` with a non-blocking flock (held = running),
    `StopRunning` SIGTERMs the holder and waits for the lock to free;
    `server.addr` is written beside `server.pid` and the CLI's root
    `PersistentPreRun` resolves `--addr` from it (skipping run/init/
    stop). CLI: `--mode`, `--control-token`/`ANY_CONTROL_TOKEN`, `any
    auth logout`, `any auth login --replace`. Mobile shims:
    `StartWithMode` / `AnyLibStartWithMode` (existing entry points stay
    standalone). Decided: wallet-blob credential dropped (device key is
    server-cached, so it carried nothing the phrase does not); no
    migration between custodies (a managed login with a legacy
    flat-root account's phrase lands in `<root>/<id>/` fresh with a
    new peerId). Tests: gate_test, devicekey_test, running_test,
    `TestAuth_{StatusManaged,ManagedBoot,DecisionTable,SwitchInPlace,
    TeardownResetsDeps}`, e2e `TestDesktopContract_*` (managed
    handshake) + `TestE2E_ManagedLifecycle`. Contract: docs/02-server.md
    § Modes / § Startup / § Shutdown / § Data dir layout, docs/03-api.md
    § Meta + § Auth, docs/04-events.md (`deauthorized`), docs/05-config.md,
    docs/06-errors.md, docs/01-cli.md, docs/08-clients.md § 14 (the
    normative client rules). Rollout: the desktop shell must adopt
    `--mode managed` + token + keychain restore-at-launch (it stops the
    sidecar with an ungated `POST /v1/shutdown` today, which now 403s
    on a standalone server); IOS-615 is unblocked.
44. **Property & field descriptors (SYN-211)** — one opaque `x-format`
    object (wire `xFormat`) describes both type properties and runtime
    dataset fields; the SDK stores it verbatim, created whole
    and patched per path, and enforces only `kind`. Gone, no
    back-compat and no migration: the typed `format` object,
    `FormatType`, `xKind`, `format.ui`, the `meta.pos` / `meta.icon`
    conventions and kind defaulting from a format (`kind` is required
    on create; `meta` narrows to `index`). `any` is the semantics
    boundary (`internal/server/descriptor.go`): on create and PATCH the
    six interpreted keys (`type`, `icon`, `pos`, `options`, `relation`,
    `config`) are typed, the slug is checked against the pinned kind
    for the v1 vocabulary (text/longtext/url/email/phone, choice,
    relation, number/currency/percent/rating/duration, checkbox,
    date/datetime, period/money/geo; `tags` reserved), `validate` /
    `compute` are reserved, vendor keys pass verbatim; the leaf-only
    PATCH rule is structural — a `set` never carries an object, so a
    container can only be unset; every property write (`/set`,
    object-create `initialProperties`, bundle `rootProperties`) is
    validated against the CURRENT slug incl. `config.multiple` arity
    and the three compound shapes; `xKey` is unique within the type
    on add and rename (`409 property.xkey_conflict`); backlinks select
    `xFormat.type == relation`. Dataset fields round-trip
    `description` / `shape` / `xFormat` and gain
    `PATCH …/datasets/:defId/fields/:fieldId` (`TypesAPI.PatchDatasetField`;
    name, description, `xFormat.*`; the descriptive slice stays out of
    the SDK's `SchemaRev`, and discovery renders `description` /
    `x-format` per field — `handler.Field` carries them too, so
    built-ins can declare descriptors later). CLI: `any type property
    add --kind … --x-format '<json>'`, option sugar on
    `xFormat.options.*`, `any type dataset field patch`. Contract:
    docs/27-descriptors.md (client rules), docs/03-api.md § Types +
    § Runtime dataset schemas, docs/06-errors.md; SDK
    docs/06-data-structure.md § The `x-format` descriptor.

43. **Types, parts and modules** — a type is properties plus **parts**
    (display units a client renders), each part owning datasets served
    by a **module**: `records` (the runtime schema handler, item 32,
    now always namespaced to the collection `<typeId>_<key>`), `editor`
    and `chat` (`handler.Module`s in `internal/editor` / `internal/chat`
    — `NewModule()`, registered through `sdkconfig.Config.Modules` in
    `serverModules()`; the SDK registers a module's canonical
    collection statically and mints a `handler.Dataset` per namespaced
    instance). A shared dataset (`"shared": true`) is the module's
    canonical collection — `editor_blocks`, `chat_messages` — so an
    object carrying two document types has one body; `chat` is
    shared-only, `records` never shares. **The write gate is "the object
    carries a declaring type"**: the SDK checks ownership at local
    write time (`space.ErrDatasetNotDeclared` → `400
    dataset.not_declared`), inbound apply stays read-tolerant, and no
    write attaches a type (`editor.EnsureType` / `chat.ensureType` are
    gone). The built-in `page` / `editor` / `chat` types are gone with
    `internal/page` and `internal/ensure`; documents and chats are user
    types registered as bundles (`Install.Parts`; `EnsureBundleRequest.
    Parts` — bundles declare `parts`, not `datasets`). Surface:
    `GET/POST …/types/:typeId/parts`, `PATCH/DELETE …/parts/:partId`,
    `POST …/parts/:partId/datasets` (`handlers_typeparts.go`; dataset
    routes keep their paths, `AddDatasetResponse` gained `collection`);
    `PATCH …/types/:typeId` with `weight` / `layout` (meta-type
    built-ins `type.weight` / `type.layout`, `TypesAPI.Patch`; create
    takes them too); editor routes are `…/editor/:collection/{blocks,
    markdown}` (`editorCollection` resolves the segment against
    `Space.Datasets`, `404 dataset.not_found` off-catalog); chat paths
    unchanged; discovery rows carry `owners` / `module` / `shared`
    (`DatasetSchema.TypeId` removed). Errors: `dataset.not_declared`,
    `dataset.key_conflict` (replaces `name_conflict`),
    `dataset.shared_conflict`, `dataset.module_unknown`,
    `dataset.module_owned`. Search: `index.ModuleChunker` (one per
    module, resolved per space from `Space.Datasets`, entries carry the
    real collection, `DynamicChunker` eviction on owners) with
    `MultiReconciler` for the editor's per-collection window diff
    (`worker.reconcileMulti`); the schema chunker skips non-records
    collections. Push: chats are the objects carrying an owner of
    `chat_messages` (`chatOwners` / `chatOwnersFilter`), not
    `any.types: chat`; `chat.unreadCount` / `chat.notifyMode` keep
    their paths (module namespace on the objects row via
    `store.ModuleGrants`). Tests mint module types with
    `installModuleType` / `mustCreateModuleObject`
    (`internal/server/modules_test.go`, e2e twins in
    `internal/e2e/modules_test.go`). CLI: `any type part
    list/add/patch/remove`, `any type update`, `any type dataset add
    <spaceId> <typeId> <partId>`, `--collection` on every editor
    command. Contract: docs/03-api.md § Parts and modules + § Objects
    + § Chat, docs/06-errors.md, docs/13-index.md, docs/16-chat.md,
    docs/25-favorites.md; SDK docs/17-user-datasets.md. Deferred
    (docs/07-roadmap.md): namespaced chat, `data_view` as a module,
    full type declarations on bundles (properties / layout / weight
    with deterministic property ids) and the well-known `page/v1` /
    `chat/v1` / `wiki/v1` contracts.
    **CRDT version mark** (same pair): the SDK stamps
    `space.CRDTVersion` on the tech space's index object (`crdtVersion`
    system dataset, monotonic by handler rule) at Open; a higher stored
    mark refuses Open (`space.ErrCRDTVersionNewer` →
    `409 sdk.crdt_version_newer` on `POST /v1/auth` and on every synced
    write once a raise arrives at runtime — the account turns
    read-only). `GET /v1/health` carries `crdtVersion {supported,
    stored, newer}` (`deps.crdtVersion`). Bump the SDK constant when a
    release writes data the previous one cannot read; the guard covers
    releases from this one on. Contract: docs/02-server.md § Startup /
    § Health, docs/06-errors.md; SDK docs/08-versioning.md.


**Always read the relevant `docs/NN-*.md` before writing code for an area**, and if
implementation diverges from a doc, update the doc in the same change.

### Build / test / run

**Build with `make build`, not bare `go build`.** The search index is
behind build tags (`INDEX_TAGS := fts vector`, docs/13-index.md
§ build tags) and `make build` passes them; a tag-less `go build
./cmd/any` produces a server whose `/search` silently returns ZERO
hits — the only symptom is one boot-time WARN ("built without the
fts/vector tags"), everything else works, and you'll chase phantom
index bugs (2026-08-20 lesson). Run builds and the binary under
`nix develop -c …` when the flake env is available — the vector leg's
llama.cpp bindings need libffi, which the dev shell provides
(a bare tagged binary panics on `libffi.so.8` at startup).

```
make build                                        # canonical: bin/any, with
                                                  # -tags '$(INDEX_TAGS)' (fts vector)
go build ./cmd/any                                # AVOID for servers you'll query:
                                                  # no index tags -> search returns nothing
make llamacpp                                     # prebuilt llama.cpp libs into bin/llamacpp
                                                  # (index.embedder: local) — also runs as
                                                  # part of `make build`; fetch failure there
                                                  # warns instead of failing the build.
                                                  # GPU-capable bundles (Metal / Vulkan) with
                                                  # automatic CPU fallback — docs/13-index.md
                                                  # § GPU offload
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
ANY_DATA_DIR=/tmp/any-e2e ./any stop              # signal the server holding the
                                                  # account lock (no HTTP — a standalone
                                                  # server refuses POST /v1/shutdown)
```

CI: one reusable workflow (`.github/workflows/_build-any.yml`, called by
`release-any.yml` on `v*` tags + `nightly-any.yml` on cron) fans out per-platform
build jobs (desktop x6 tarballs — 4 platforms plus 2 App-Sandbox-safe darwin
`-sandbox` variants, Android `any.aar`, iOS `any.xcframework.zip`) and
fans in to a single `publish` job that ships them all in ONE GitHub Release (both
mobile assets sha256-pinned in the notes) and dispatches the 3 client repos.

Module path: `github.com/anyproto/any`. Go 1.26.2. Dependencies
(`any-sync-sdk`, `any-sync`, `any-store`) are
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
├── anyuri/               PUBLIC: canonical any:// link grammar (docs/19-links.md)
├── mobile/ios/           iOS c-archive shim (//export + module.modulemap)
├── mobile/android/       Android gomobile bind shim (package NAME stays `mobile`)
├── internal/
│   ├── cli/              CLI subcommands, flag parsing, rendering
│   ├── server/           HTTP server, route wiring, SDK lifecycle
│   ├── api/              request/response types shared by server and cli
│   ├── client/           HTTP client used by cli/ to call server/
│   ├── config/           config file + env var loading
│   └── localstore/       local store naming + tag fence over the SDK's sdk.db
└── docs/
```

Everything lives under `internal/` with ONE deliberate exception:
`anyuri/` is public (`github.com/anyproto/any/anyuri`) — any owns the
link format and clients/agents import the Build/Parse rule instead of
reimplementing it (SYN-75). Don't add further public packages without
the same kind of explicit contract.

The two `mobile/` shims are not packages anyone imports — they're
binding surfaces, and they sit outside `cmd/` because `cmd/` is Go's
convention for RUNNABLE binaries and neither a c-archive nor an AAR is
one. Both are thin adapters over `internal/embedded`, which
owns the actual lifecycle. Two things there look like mistakes and
aren't: `mobile/android` declares `package mobile` (gomobile derives the
AAR's Java class from the package NAME, so renaming it breaks Android
consumers), and `build-xcframework.sh` builds `-o anylib.a` from
`./mobile/ios` (cgo names the generated header after `-o`, and
`anylib.h` is what the modulemap and Swift's `import AnyLib`
depend on). Request/response
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
  Exceptions, all consumer-side features rather than SDK methods:
  `POST /v1/spaces/:id/search` (the search index, built on `Changes()`
  + the chunkers, `docs/13-index.md`), `/v1/events` + `/v1/processes`
  (the ephemeral bus, `docs/21-events.md`), and `/v1/local` (the local
  store over `SDK.Store()`, `docs/26-local-store.md`). The local store
  never writes an SDK collection and never opens a transaction that
  spans a local (`l_*`) and an SDK collection — `localstore.ParseRef`
  is the one place a collection name is admitted.
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
- **Dataset reads go through `/query` and `/query/subscribe`.** The compiled-in
  modules (chat, editor) keep bespoke handlers for *writes* only (POST/PATCH/
  DELETE and reactions). Reads always go through the per-object query primitive
  with the matching `dataset` value — the collection name (`chat_messages`,
  `editor_blocks`, a namespaced `<typeId>_<key>`, ...). One read path per
  dataset, one wire shape per snapshot. Sole exception: `GET
  /editor/:collection/markdown` is a render transform, not a dataset read.
- **No write attaches a type.** A module collection lives on an object only
  while the object carries a type whose part declares it (item 43); a write
  without one is `400 dataset.not_declared`. Never add a "ensure the type is
  attached" step to a write path — clients attach types deliberately.
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

Data dir layout (a ROOT; each account under `<root>/<accountId>/`, the legacy
flat root is the default account — docs/02-server.md § Data dir layout):
```
<root>/
├── config.yaml        # optional
├── models/            # shared embedder model cache
└── <accountId>/
    ├── wallet.key     # standalone: auth.FileProvider wallet (0600)
    ├── device.key     # managed: cached device key (0600, minted once, never portable)
    ├── server.lock    # single-instance OS file lock (kernel-released)
    ├── server.pid     # holder's pid — error messages only, never proof of life
    ├── server.addr    # holder's bound address — CLI convenience only
    ├── sdk/  files/  index/
```

Shutdown paths: `SIGINT`/`SIGTERM` (what `any stop` sends, after finding the holder
by its held lock) on every server; `POST /v1/shutdown` only on a managed server,
with the control token (a standalone server answers 403). Both tear the engine
down with a 10s drain, close the SDK, exit 0.

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
| `docs/09-query.md` | any-store query guide — filter operators, array matching, sort, paging, indexes, xKey paths |
| `docs/11-agent-memory.md` | agent data — harness-owned userspace runtime datasets; pointer to the anybao repo |
| `docs/13-index.md` | search index — `IndexEntry`/`Chunker` contract, scopes, tombstones, addSeq; the indexer (store layout, advance/embed loops, purge rule), `/search` modes + errors |
| `docs/14-aggregation.md` | aggregation pipelines — `/aggregate` endpoints, stage set, pushdown guidance, limits, MongoDB-divergence catalog |
| `docs/16-chat.md` | chat client guide — building a messenger UI on `chat_messages`: rendering, liveness, and SDK read-tracking (account-private, forward-only unread state) |
| `docs/17-files.md` | files v2 — storage tiers, durability states, cache/offload/pin, variants, read paths, what's deliberately not wrapped |
| `docs/18-ci.md` | the `any` artifact + CI — tarball layout, manifest, published platforms, the `ANY_CI_TOKEN` secret, build/publish/dispatch flow |
| `docs/19-links.md` | canonical `any://` link format — kind registry (o/m/s/p/f, reserved i), path composition rule, fragment rule, extension policy, legacy bare-form back-compat |
| `docs/20-push.md` | push notifications — sender-pushes E2E-encrypted model, heart-compatible topics + payload, notifyMode settings, `/v1/push/*` + settings PATCH, config, local e2e recipe |
| `docs/21-events.md` | event bus — `/v1/events` publish + filtered SSE subscribe, envelope/scopes/filters, at-most-once semantics, `ui.*` types (doc 15 retired into this) |
| `docs/22-processes.md` | process helper — `process.*` convention over the bus, `/v1/processes` endpoints, composite key, heartbeat/staleness, cancel flow, internal producers |
| `docs/23-devices.md` | devices registry & active-app election — tech-space `devices` dataset, `/v1/devices` surface, reader-side election rule, runtime-vs-UI decision matrix |
| `docs/24-data-views.md` | saved views — `data_view` type & `data_views` record shape, what stays opaque and why, shared/account/device tiers, the client grouping recipe |
| `docs/25-favorites.md` | favourites client contract — canonical `favorites/v1` install request, locked-read/ensure-on-first-write startup, fork merge+resolve, soft-delete, mirror recipe, tree-policy decisions |
| `docs/26-local-store.md` | local store — device-local, non-CRDT collections in `sdk.db` under the `l_` tag: why the same file, the fence, model, `/v1/local` surface, limits, what it is NOT |
| `docs/27-descriptors.md` | property & field descriptors — the `xFormat` bag: guarantee boundary (`kind` vs hint), the six interpreted keys, merge model, leaf-only PATCH rule, v1 slug vocabulary + value checks, composites, client rendering/tolerance/ordering rules, what the server enforces, not-covered list |
| `docs/search/` | search evaluation & decisions — chunking before/after, BEIR results, hybrid-knob tuning, why the defaults; complements `13-index.md` (the contract) |

Keep `docs/07-roadmap.md` honest — move shipped items to its "Done" section or
strike cut scope; add new open questions as they surface during implementation.
