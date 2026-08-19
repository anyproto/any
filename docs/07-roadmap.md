# Roadmap & open questions

## v1 — prototype (this spec)

Goals:
- Start the server (`any run`); auto-wallet on first run.
- Wrap the SDK over HTTP/JSON with the `/v1/` prefix.
- CLI subcommands for every endpoint.
- Make it easy to exercise the SDK from scripts and terminals so we
  can see where the API is sharp / blunt.

Explicit non-goals for v1:
- Subscriptions (see `04-events.md`).
- Remote access / TCP auth.
- Install script / service files.
- Multi-account.
- ~~File upload/download.~~ Shipped — files v2 (see Done +
  `17-files.md`).

## v1.x — what we learn

Whatever the prototype teaches us gets prioritised from here. Likely
candidates based on what's already visible:

- **CLI ergonomics.** Flag shapes, table vs JSON output, patch syntax
  (`--set key=val` vs `--patch FILE`), error messages.
- **Endpoint shape fixes.** Anything that's awkward to call from a
  shell or a binding.
- **Install one-liner.** Ollama-style `curl ... | sh` that drops a
  binary and wires `systemd --user` / `launchctl` to start `any run`
  on login.

## v1.x — WebSocket multiplex (maybe)

Subscriptions shipped in v1 over SSE (see `04-events.md`). If a
consumer needs many subscriptions per connection or client-originated
control frames, `/v2/subscribe` over WebSocket becomes the right
addition. Additive — SSE endpoints stay.

## v2 — remote access

Now that subscriptions have shipped, remote access (LAN or internet)
becomes useful. Needs:

- TCP auth — token stored at `~/.any/token`, `Authorization: Bearer`.
- TLS story — bring-your-own-cert for now, reverse proxy in front.
- Multi-tenant considerations — still single-account per server, but
  clients from different machines.

## Open questions (still unresolved)

1. **Port default.** Picked 7001 arbitrarily. If it collides with
   anything real, change before first ship.
2. ~~**`any init` vs first-`any run` auto-create.**~~ Resolved: `run`
   no longer auto-creates. Accounts come from `any init` (CLI) or
   `POST /v1/auth` (HTTP onboarding); an account-less `run` starts
   unauthorized and waits.
3. **Config file location precedence.** Documented in `05-config.md`.
   Verify `$XDG_CONFIG_HOME/any/config.yaml` is what Linux users
   expect; macOS users might prefer `~/Library/Application Support/any/`.
   Good enough for v1; revisit during packaging.
4. **Passkey UX.** Env-var-only in v1. When we ship the install
   script, we'll need to decide whether the installer pipes the
   passkey to the server start command or integrates with OS
   keychains. Out of v1 scope; flagged now so we don't paint into a
   corner.
5. **Nodeconf path vs inline.** Both supported in the config file.
   Which do we document as the recommended path in README? Probably
   inline for the prototype (self-contained), path for real deploys.
6. **Query response shape.** Baseline `{ "records": [...] }` where
   each record is the `*anyenc.Value` rendered as JSON. Confirm
   anyenc's JSON is a stable wire format — our assumption is yes
   (any-store already treats it that way).
7. **CLI binary vs plugin architecture.** Current plan: monolithic
   `any` binary with subcommands. Plugins are not on the roadmap;
   flag if anyone wants them.
8. **Windows support.** Server + CLI both work in principle (nothing
   Unix-specific since we dropped Unix sockets). Verify during first
   implementation; single-instance lock needs a Windows-friendly
   replacement for the PID-based check.
9. **External semantic-search service (TODO — agent memory recall is
   non-functional until this exists).** The agent data layer
   (`docs/11-agent-memory.md`) deliberately stores no vectors; a
   separate service is planned that tails `/query/subscribe` on
   `agent_memory_items` / `agent_chunks` / `agent_turns`, embeds
   content, keys an ANN index by record id, and answers hybrid
   (vector + keyword + metadata) recall with ids the caller hydrates
   via `/query`. Until it ships: `memory.search` falls back to
   indexed recency/category/period queries; similarity dedup,
   link-gen, evolution/reflection/decay passes are dormant (the
   schema keeps their fields — edges, salience, accessCount — so they
   resume without data migration). `embeddingRef` is reserved on the
   schema as the future external-index backref.
10. **Account switching on a running server.** `POST /v1/auth` boots
    exactly one engine per process lifetime; switching accounts means
    restarting with `--account <id>`. A logout/switch endpoint (tear
    the engine down, return to the unauthorized state) is plausible
    but needs every handler and SSE stream to tolerate the SDK going
    away mid-flight — not worth it until a real client asks.
11. **Strict-bind gaps (deliberate, revisit with the v2 boundary).**
    Three known soft edges in the request-boundary contract:
    - `/modify` and `/aggregate` bodies drop unknown top-level keys
      silently — their fastjson paths hand the parsed body straight to
      the SDK, and the op/stage vocabulary is SDK-owned, so a strict
      gate here would have to chase the SDK's grammar. Deferred until
      the SDK exports that vocabulary.
    - `bindBodyStrict` rejects unknown keys at any depth, but the 400
      message and `details.accepted` enumerate top-level fields only —
      misleading when the offender is nested (e.g. inside chat's
      `agent` group).
    - A non-object body maps to `request.schema` on fastjson paths but
      `request.bad_json` on encoding/json paths — two codes for one
      fault class; unifying means touching both bind layers.
12. **Collapsing the darwin `-sandbox` fork.** The sandbox flags
    (system libffi, `docs/18-ci.md` § The darwin `-sandbox` variants)
    are the strictly more conservative macOS behavior, so the end
    state is ONE darwin build using them — which would also let
    any-ui drop `disable-library-validation`. Gates: any-swift's
    in-sandbox verification passing, plus a `/usr/lib/libffi.dylib`
    probe on the **oldest supported macOS** (CI's `desktop-smoke`
    covers current macOS only — arm64 natively, x86_64 via Rosetta;
    native x86_64 also stays unprobed). Related: an upstream
    `jupiterrider/ffi` PR making the `-X filename` override a
    documented contract (an exported `Filename`), which would retire
    build-any.sh's string guards; and promoting `ffi` to a direct
    `require` so the version the build script reaches into is visible
    where versions get reviewed.

## SDK-side prerequisites

Not this repo's work; gate on the SDK:

- **`SyncStatusAPI.Peers` (or equivalent).** No production-grade
  per-space peer list on the SDK today — `/v1/spaces/:id/sync-status/peers`
  stays 501 until the SDK adds it. The diagnostic equivalent is
  surfaced via `Space.Debug().Space()` / `/v1/spaces/:id/debug`, but
  that surface is explicitly not stable.
- **`PropertiesAPI.{SetAccount, SetDevice, DetachType}`.** Return
  errors today; routes are 501 until the rewrite-object (account scope)
  and device-local store (device scope) ship. `AttachType` now works on
  the SDK — `editor.EnsureType` uses it to attach the `editor` type
  before membership-gated `editor_blocks` writes — but the HTTP route
  `/v1/spaces/:id/properties/:objectId/attach/:typeId` stays 501 (no
  agent-facing caller yet; wire it when one appears).
- **Record-level account transport.** Dataset schema fields can
  declare `account` scope and the tech-space carrier is already keyed
  `(objectId, dataset, recordId)`, but the SDK's account mirror
  handles the objects rows only — so `POST …/modify` rejects
  `"scope":"account"` until the mirror learns dataset records. The
  local scope shipped (status § 21); account is the missing sibling
  (wanted for cross-device read state that survives device loss,
  though read-tracking proper syncs its frontier via tech-space KV
  instead).
- **`Types.Delete` / `Types.RemoveProperty` / `Types.UpdatePropertyMeta`.**
  Still "not implemented" on the SDK side; routes 501.
- **`Types.Get` for non-object ids.** The SDK only returns
  `space.ErrNotFound` when the id resolves to an existing object that
  isn't tagged as a type. Ids that aren't objects at all surface as a
  wrapped store error ("tree does not exist") and currently fall
  through to `500 internal`. We could widen the 404 mapping in the
  handler if/when the SDK stabilises a sentinel for this case.
- **Query `Projection`.** Accepted in the request body but mostly
  ignored — the SDK's `Projection(opts)` no-ops `IncludeVariants` /
  `IncludeMeta` in MVP. **`IncludeDeleted` now works** (SDK `v0.0.10` —
  used by the index chunkers to stream tombstones); variant collapse
  and meta-stripping still pending.

## Index / search (phase 3+)

Phases 1–2 shipped (see Done + `docs/13-index.md`): chunker contract +
three chunkers, the indexer (per-space BM25 FTS + IVF-SQ vector index,
pluggable embedders, parallel batched pipelines),
`POST /v1/spaces/:id/search` + `any search`. Still open:

- ~~**Re-pin both deps to tagged releases.**~~ Done: `any-sync-sdk
  v0.0.10` (`feat/addseq-change-index` merged) and `any-store/v2
  v2.0.0-alpha.11` (the `btree-fts` branch tagged).
- **Backfill / re-index.** "Index from the next change" means
  pre-existing content stays unsearchable until rewritten. A deliberate
  full re-index (walk all objects, not just `_addSeq > cursor`) is an
  open design.
- **Search quality.** Snippets/highlighting, per-scope weights,
  cross-space search, tunable score thresholds beyond the
  zero-similarity noise floor, query-time `VectorEf` tuning.
- **Embedding hygiene.** Re-embed on model change (currently a dim
  mismatch is a boot error suggesting removing `<data-dir>/index/`).
- **Long-record chunk splitting.** The local embedder truncates input
  to `index.local.contextSize` tokens (head-only vector recall, FTS
  unaffected — docs/13-index.md § Known limits). Splitting one record
  into N sub-chunks is a chunker-contract change (doc-id scheme,
  tombstones for shrinking records).
- **Local embedder follow-ups.** Multi-sequence batched decode (texts
  currently embed sequentially under one mutex); a packaged
  distribution story for the llama.cpp libs (today: `make llamacpp`
  drops them next to the binary; go:embed + extract was considered and
  deferred — pure overhead while "distribution" means `make build`).
- **`UpdatePropertyMeta` (SDK).** Property `meta` flags (e.g.
  `index: "<scope>"`) are create-time-only until the SDK implements
  property-meta updates — existing properties can't be re-flagged.
- **`agent_memory_items` chunker.** Agent memory now lives in the
  built-in `agent_memory` type's dataset (docs/11-agent-memory.md); a
  dedicated gated chunker (`TypeId() == "agent_memory"`, dataset
  `agent_memory_items`) is the real path to agent-scope recall — the
  prop chunker only covers property values on objects.

## How to update this file

- Move items that ship to a "Done" section below (or remove them once
  they're obviously in the past).
- Add new questions as they come up during implementation.
- Keep the v1 goal list honest — if we cut something, strike it here
  so a reader knows scope moved.

## Runtime dataset schemas — follow-ups (SYN-147 shipped, see Done)

- **Dogfood the generic schema handler.** Collapse the zero-logic
  compiled-in handlers (agentconfig, agentsecrets, enrichproposal;
  parts of agentmem/agentlog/enricheddata) to pure declarations
  (`Handler: nil` + behavioral schema). Requires a per-dataset
  mutability audit first: the zero-value `MutableBy` is write-once, so
  every currently-mutable field needs an explicit `MutableByAnyone` /
  `MutableByAuthor` (+ a creator stamp where author-gated). Chat keeps
  its bespoke handler — mentions derivation and reaction-leaf
  authorization are cross-field rules the vocabulary deliberately
  excludes.
- **Index sweep for removed definitions.** Definition-removal eviction
  is lazy and process-scoped (docs/13-index.md § Removal semantics);
  a boot-time per-space sweep of stored dataset segments against the
  current catalog closes both residual leaks.
- **SDK sentinels for dataset CRUD errors.** Name conflicts and
  declaration validation surface as `fmt.Errorf` strings today —
  `any` preflights names and STOPGAP-matches decl messages
  (`datasetWriteError`). Wanted: exported `errors.Is`-able sentinels
  (decl invalid, name conflict), plus a retired-name signal for the
  index sweep (preserve `name` on the removed def tombstone), and
  reserving the consumer virtual dataset names (`prop`, `schema`)
  SDK-side.

## Done

- **Runtime dataset schemas + upsert (SYN-147)** — wraps the SDK's
  user-space dataset schemas (`TypesAPI` dataset CRUD, generic
  `SchemaHandler`, `Space.Upsert`): type-scoped endpoints
  `…/types/:typeId/datasets[/:defId[/fields[/:fieldId]]]`,
  `POST /v1/spaces/:id/upsert` (the surface's first idempotent write),
  discovery `typeId` + behavioral `x-*` keywords passthrough, the
  schema-driven search chunker (`index.SchemaChunker` +
  `DynamicChunker` worker capability, scope `basic`), `any type
  dataset …` / `any upsert` CLI. Contract: docs/03-api.md § Runtime
  dataset schemas + § Upsert records, docs/13-index.md § Schema
  chunker, the SDK's docs/17-user-datasets.md. Follow-ups above.

- **Push notifications (SYN-47)** — chat push interoperating with
  heart's `anytype-push-server` deployment (topic vocabulary, payload
  JSON, and crypto byte-compatible). SDK `pushclient` component
  (`SDK.Push()` / `space.PushAPI`, branch
  `cheggaaa/syn-47-push-client`: ACL-derived keys, topic/ciphertext
  signing, DRPC to the direct out-of-band push peer) + `any`'s
  `internal/push` policy service (token persistence + re-register,
  hash-gated subscription sync loop, buffered notify queue), the
  sender-scoped chat handler hooks (send / mention-adding edit /
  silent read — the `/search`-category consumer-side exception),
  two `notifyMode` knobs (`settings.notifyMode` on the tech-space row
  via `PATCH /v1/spaces/:id/settings` + account-scoped
  `chat.notifyMode` per chat; effective = chat ?? space ?? all),
  `/v1/push/token[,/subscriptions]` endpoints, `any push …` +
  `any space settings` CLI, config `push.*` / `ANY_PUSH_*`, gated e2e
  (`internal/e2e/push_test.go`). Full contract in `docs/20-push.md`.
  Embedded servers (any.aar / xcframework) bridge the push node through
  `embedded.Options` — `mobile.StartWithPush` /
  `AnyServerStartWithPush`, addrs comma-separated (SYN-83);
  the host supplies the peer alongside its nodeconf choice, no default
  shipped.
  **Remaining:** the staging/production push-node address (infra
  hand-off, config-only) + running the gated e2e against real infra.
  **Deferred by design:** reactions push, ACL/invite push, desktop
  receive (send-only — the push server's platform enum is ios/android).
- **Files v2** — the SDK's files-v2 work (released in
  `any-sync-sdk v0.1.0-alpha.1`, on `any-sync v0.13.0-alpha.1`) made
  file payloads space
  data (payloads rows on a derived per-object child; inline tier
  < 4096 B in the CRDT, larger files encrypted → UnixFS DAG → local
  CARv2 + background fileV2-broker backup; offline-first durability
  queue; on-demand seekable downloads; per-file offload + SDK-level
  cache GC). Wrapped 1:1: attach as a raw-body POST (the one
  BodyLimit-exempt route), `/content` download with real mime /
  Content-Disposition / Range 206, Get/List/Stats/Status,
  pin/retry/offload, `/files/subscribe` status SSE, per-object
  payload-row `files/query[/subscribe]`, account-wide
  `/v1/files/cache{,/free,/sweep}`, config `files.*`, `any file …`
  CLI. Full model in `17-files.md`. **Deliberately not wrapped** (the
  broker-embedding surfaces — the filenode-v2 broker links the SDK
  directly): `Space.Payloads()`, `Space.TreeHeads()`,
  `Service.Track/Evict`, `Headless`, `Sync.TreeTypes`. Cheap 1:1 adds
  if a use case surfaces. Open SDK asks: the SYN-30 synced files view
  (space-wide live rows feed + remote status events) and file-content
  search indexing (no payloads chunker yet). The error-sentinel ask is
  done — `space.ErrFileNotAvailable` / `ErrFileNotBackedUp` /
  `ErrFileVariantInvalid` shipped and `fileError` maps them via
  errors.Is (no string matching).
- **Real space deletion + local offload (`any-sync-sdk v0.0.12`)** —
  `DELETE /v1/spaces/:id` (`Service.Delete`) replaced the old local-only
  soft-delete with an offline-first deletion: synchronous local half
  (synced `remoteStatus=deleted` tombstone + immediate offload — closes
  watchers/Store, evicts the any-sync space, drops the per-space CRDT
  collections and DB file, reclaims disk even offline) plus a deferred
  network half (a background reconciler sends the signed
  `coordinator.SpaceDelete`, owner-only, and offloads spaces the
  coordinator reports gone). No `any`-side code change was needed — the
  handler already called `Service.Delete` and the search indexer already
  wipes per-space data on the `Subscribe` `Removed` stream
  (`DropSpace`); this was a dependency bump (`v0.0.11` → `v0.0.12`,
  any-store/v2 alpha.11 → alpha.14) plus the new `any space delete
  <id> --yes` CLI and doc alignment. The tech-space row stays in
  `Service.List` with `status:"deleted"` as a sticky tombstone, so the
  `GET /v1/spaces` active-only default filter is retained by design.
- **Aggregation pipelines (`/aggregate`)** — MongoDB-style pipelines
  over both query scopes: `POST /v1/spaces/:id/objects/aggregate`
  (objects collection) and `POST /v1/spaces/:id/aggregate` (per-object
  dataset), wrapping the SDK's `Space.AggregateObjects` /
  `Space.Aggregate` (SDK prerequisite landed as `any-sync-sdk v0.0.11`:
  the `space.Agg` builder over any-store alpha.11's aggregation
  framework, tombstone-skip `$match` prepended into the pushdown
  prefix, `ErrBadPipeline` + limit sentinels). Snapshot-only — no
  subscribe variant by design (any-store aggregation has no live
  path). `explain: true` body flag wraps `Agg.Explain`. CLI
  `any aggregate`. Client doc with examples + MongoDB-divergence
  catalog in `docs/14-aggregation.md`.
- **Mnemonic authorization + per-account data dirs** — `any init
  --mnemonic[-stdin]` restores an account from its BIP-39 phrase with
  a FRESH device key (the supported second-device flow; verbatim
  `wallet.key` copies clone the device key, collide peerIds, and
  degrade realtime sync to the ~30s headsync timer — verified e2e in
  `internal/e2e/multidevice_techspace_test.go`). The data dir became a
  multi-account ROOT: new accounts at `<root>/<accountId>/`, a legacy
  root `wallet.key` stays the default account with flat data (no
  migration), embedder models shared at `<root>/models/`. `run` no
  longer auto-generates wallets — without a resolvable account the
  server starts unauthorized (`401 auth.required` guard) and
  `POST /v1/auth` generates/restores/selects + boots the engine in
  place (UI onboarding path); `GET /v1/auth` lists local accounts.
  Selector: `--account` / `ANY_ACCOUNT` / `account:`. SDK side:
  `FileProviderConfig.Mnemonic/Index` seeding + `auth.AccountId`.
- **Agent data layer (turns / chunks / memory)** — built-in
  `agent_log` (datasets `agent_turns` + `agent_chunks` on the chat
  object) and `agent_memory` (`agent_memory_items` on the seed-derived
  per-space brain object) types with validated record shapes,
  server-stamped fields, declared indexes, append-only turn/chunk
  semantics, and chunk→turns drill-down pointers (`fromSeq`/`toSeq`).
  Write endpoints under `/agent/*` + `any agent` CLI; reads via the
  query primitive. Replaces bobrik's markdown-transcript +
  runtime-typed memory scheme. See `docs/11-agent-memory.md`; semantic
  recall itself is gated on the external search service (Open
  questions #9).
- **v1 scaffolding + wallet + health slice** — `cmd/any`, `internal/{cli,server,client,config,api,version}`,
  echo v4 under `/v1`, `GET /v1/health`, `POST /v1/shutdown`, PID-lock with stale
  reclaim, loopback-only bind guard, uniform error envelope, `auth.FileProvider`
  wallet creation with first-run mnemonic print.
- **SDK boot + space lifecycle** — `server.OpenSDK` opens
  `any-sync-sdk` against the wallet provider on Run; nodeconf YAML is
  loaded via `internal/config.LoadNodeconf` (precedence: inline →
  configured path → `ANY_NETWORK_NODECONF_PATH` → embedded
  `internal/config/nodeconf-prod.yml`). Storage lives at
  `<dataDir>/sdk/`. Real handlers wired:
  - `GET /v1/account` (Id only — Metadata reserved, SDK does not expose it yet)
  - `POST /v1/spaces`, `GET /v1/spaces`, `GET /v1/spaces/:id`, `DELETE /v1/spaces/:id`
    (real offline-first deletion as of `any-sync-sdk v0.0.12` — see the
    Done entry; the row stays in List with `status:"deleted"` as a
    sticky tombstone)
  All other `/v1/spaces/**` endpoints from `docs/03-api.md` are
  registered and short-circuit with `501 sdk.not_implemented`, including
  `PUT /v1/account/metadata`, `POST /v1/spaces/{join,derive,one-to-one}`,
  the full Objects/Types/Properties/Members/ACL/Sync-status surface, and
  the data-plane (`query`/`modify`/`delete-records`).
- **Objects + types + properties + data plane** — real handlers wired
  for the SDK surface that's shipped:
  - `POST /v1/spaces/:id/objects`
  - `POST /v1/spaces/:id/modify`, `POST /v1/spaces/:id/delete-records`
    (return `{versionId, changeId, recordIds}` — the full `ModifyResult`)
  - `POST /v1/spaces/:id/types`, `POST /v1/spaces/:id/types/:typeId/properties`
    (PropertyKind allowlist enforced at the boundary: `string` /
    `number` / `boolean` / `null` / `array` / `object` — anything else
    is rejected with 400 `request.schema`)
  - `GET /v1/spaces/:id/properties/:objectId` (record rendered via
    `*anyenc.Value.FastJson(arena).MarshalTo` and wrapped in
    `{"record": ...}` as `json.RawMessage`)
  - `POST /v1/spaces/:id/properties/:objectId/set/:typeId`
  Modify / Objects.Create.InitialProperties / Properties.Set parse
  request bodies once with a pooled `*fastjson.Parser` and pass
  `*fastjson.Value` directly into the SDK — anyenc converts in one
  walk via `Arena.NewFromFastJson`, no `map[string]any` intermediate.
  Still 501: Objects.Delete, Query, Types.{List,Get,Delete,Properties,
  Remove/UpdateProperty}, Properties.{AttachType, DetachType} (the former
  per-scope SetAccount/SetDevice endpoints were removed — scoped `Set`
  subsumes them), all `/members`, `/acl/**`, `/sync-status/**`, and the
  Spaces lifecycle surface that's still SDK-blocked
  (`/spaces/{join,derive,one-to-one}`).
- **Phase 2 endpoints (Objects.Delete, Types read surface, Query)** —
  five more endpoints flipped from 501 to real:
  - `DELETE /v1/spaces/:id/objects/:objectId` — calls `Objects.Delete`,
    returns 204. Any error (including the second-call "tree does not
    exist" response from any-sync) maps to `404 sdk.not_found`.
  - `GET /v1/spaces/:id/types` — `Types.List`. Built-in `any` is always
    first with `builtIn:true`.
  - `GET /v1/spaces/:id/types/:typeId` — `Types.Get`. `space.ErrNotFound`
    maps to `404 sdk.not_found`. The literal id `"any"` returns the
    synthetic built-in.
  - `GET /v1/spaces/:id/types/:typeId/properties` — `Types.Properties`.
    Returns `{properties: [<PropertyDef>, ...]}` with kind rendered as
    a string per the wire allowlist; recursive `items`/`properties`/
    `required` emitted only when the SDK returns them populated.
  - `POST /v1/spaces/:id/query` — chains `Filter / Sort / Limit /
    Offset / All`. Body parsed once with a pooled `*fastjson.Parser`;
    `filter` is passed straight through as `*fastjson.Value`. Each
    record rendered via `value.FastJson(arena).MarshalTo`. The
    `projection` field is **parsed but ignored** — the SDK's
    `Projection(opts)` is a no-op in MVP.
- **Phase 3 endpoint (cross-object query + storage shift)** —
  `POST /v1/spaces/:id/objects/query` wires `Space.QueryObjects()`
  against the per-space shared collection. Same fastjson fast-path as
  Modify; same `{records:[...]}` response shape. `projection` parsed
  but ignored. Storage shift on the SDK side: object property values
  are now one row per object in the shared `objects` collection
  (keyed by objectId), not in a per-object `properties` dataset.
  Type objects appear in the same collection (rows whose `any.types`
  contains `__type__`); filter on that to separate types from
  instances. The existing `/v1/spaces/:id/query` keeps its body shape
  but its useful scope narrowed — it's now mainly for reading a type
  object's `properties` (definitions) dataset, since values moved.
- **Editor: atomic blocks + markdown bridge** — `internal/editor`
  registers a `handler.Type` for the `editor_blocks` dataset, one
  record per block. Endpoints under
  `/v1/spaces/:s/objects/:o/editor/blocks` cover
  list / create / patch / delete; PATCH takes
  `{set: {"dotted.path": value}, unset: ["dotted.path"]}` for atomic
  per-path `$set` / `$unset`. Per-block fields: `type`, open-ended
  `style`, INLINE-markdown `text`, `nav.parentId`, `nav.pos` (lexid).
  Block ids auto-derive from the change CID (same shape chat uses).
  List returns DFS document order. Liveness reuses the generic
  subscribe primitive with `dataset=editor_blocks`. The markdown
  surface moved into the same namespace — `GET/PUT /editor/markdown`
  — and stayed (LLM tools and import/export flows depend on it); it
  now operates over the same `editor_blocks` dataset internally — GET
  renders blocks → markdown; PUT parses markdown → diffs the block
  tree → per-record create/update/delete. Same `{inserted, updated,
  deleted, unchanged}` response shape; the ids in those slices are
  block ids now, not lexids. Old `md_blocks` dataset is gone —
  anything still pointing at it must move to `editor_blocks`. Chat
  routes moved alongside: `/v1/spaces/:id/objects/:objectId/chat/messages`
  (same wire shape, namespaced path). CLI: `any editor blocks
  list/create/patch/delete`.
- **Two-step object delete** — `DELETE /v1/spaces/:id/objects/:objectId`
  now tombstones the row in the per-space `objects` collection
  *before* tearing down the any-sync tree (`handlers_objects.go`).
  Required because once the tree is gone, the per-object Modify path
  can't write the tombstone, and queries would keep returning the
  ghost row indefinitely.
- **Chat built-in type** — `internal/chat` registers a `handler.Type`
  whose per-object `chat_messages` dataset stores one record per
  message. Aggregating endpoints under
  `/v1/spaces/:id/objects/:objectId/chat/messages` cover
  send / list / edit / delete; reactions toggle via
  `…/chat/messages/:msgId/reactions/:emoji`. Storage shape:
  `{id, creator, createdAt, modifiedAt, replyToMessageId, text, reactions}`,
  reactions stored as
  `reactions.<emoji>.<accountId> = <changeTimestamp>` — emoji first,
  identity at the leaf — so authorization on write is a single
  path-segment compare against `ctx.Change.Creator` and the leaf
  timestamp is server-derived via `sink.Derive`. The API server
  rolls up to `{emoji: [accountId, ...]}` sorted by timestamp for the
  wire. Server-stamped fields
  (`creator`, `createdAt`, `modifiedAt`) come from `sink.Derive`,
  invisible to client payloads — handler rejects creates carrying
  any field other than `text` / `replyToMessageId`. Edit / delete
  enforce author-only (`ctx.Before.creator == ctx.Change.Creator`),
  surfaced as 403 `chat.not_author`. Chronological order uses the
  SDK's existing `_ver.id` creation marker; pagination cursors are
  message ids that resolve to that boundary. Liveness reuses the
  generic subscribe primitive with `dataset=chat_messages`. Out of
  scope for v1: pinned, blocks (typed text/link/embed/quote),
  per-emoji-per-identity unread reaction tracking — v1.x followups.
  Read tracking, attachments, and mentions (derived `mentions` field
  + `unreadMention` badging, SYN-72) have since shipped.
- **Subscriptions over SSE** — `GET /v1/spaces/:id/objects/:objectId/subscribe?dataset=…`
  and `GET /v1/spaces/:id/properties/subscribe` stream CRDT apply
  events as Server-Sent Events. Wire format: `event: ready` →
  `event: changes` (JSON array, free batching via `mb.Wait`) →
  `event: lagged` (when `Subscription.Dropped` grows) →
  `event: closed{reason}` on shutdown. The handler races
  `mb.Mailbox.Wait` against a per-process `shutdownCtx`; `server.Run`
  cancels it and waits on `streamsWG` (10s deadline) so in-flight
  streams emit their terminal frame before the listener tears down.
  CLI `any subscribe …` and `internal/client.StreamSubscribe…` ship
  alongside.
- **Nav virtual built-in + tree UI** — `internal/nav` defines a
  synthetic `nav` type (`type` 1=item / 2=folder, `parentId`, `pos`
  via lexid). `POST /v1/spaces/:id/objects` auto-stamps these on every
  create (`injectNavDefaults` — caller-supplied values win, otherwise
  defaults: item, root parent, next-pos after the folder's current max).
  `GET /v1/spaces/:id/types` surfaces `nav` alongside the SDK types so
  the UI can render an editor for it. The web UI's left sidebar is now
  a lazy-loaded tree (queries `nav.parentId` per folder); the space
  picker moved to the right sidebar.
- **Dataset schemas + space-list query/subscribe + discovery** — on the
  SDK's unified tech-space query (`Service.Query` / `SpaceIndexObjectId`)
  and required-schema work (`handler.Dataset.Schema`, `Space.Datasets` /
  `Service.Datasets`). `POST /v1/spaces/query` + `/query/subscribe` wrap
  `Service.Query(SpaceIndexObjectId(), "spaces")` — windowed snapshot +
  SSE over the tech-space `spaces` dataset (raw rows; `GET /v1/spaces`
  stays the mapped `SpaceInfo` convenience). `chat` / `editor` handlers
  declare `handler.Schema` (per-field synced/derived/local).
  `GET /v1/spaces/:id/datasets` (`Space.Datasets`) and `GET /v1/datasets`
  (`Service.Datasets`) expose a JSON Schema per dataset with a per-field
  `x-scope`. CLI: `any space query` / `any space subscribe` / `any
  datasets`. Pins the SDK at the tagged `any-sync-sdk v0.0.8`
  release (also bumps `any-store/v2` to `alpha.10`).
- **Index chunkers (phase 1) + SDK tombstone opt-in** — the
  consumer-side search feed's contract and three chunkers, handlers
  only (no indexer). `internal/index` defines `IndexEntry` / `Chunker` /
  `Registry` plus the shared `RecordsSince` streamer and the
  `AgentMemoryChunker` (scope `agent`, dataset `objects`). Per-handler
  `editor.NewChunker()` (scope `basic`) and `chat.NewChunker()` (scope
  `chat`). Deletions stream as tombstone entries (`Data == ""`).
  `server.NewIndexRegistry` wires all three onto `deps.chunkers` — no
  consumer, no HTTP endpoints yet. SDK side (branch
  `feat/addseq-change-index`, tagged as `v0.0.10`):
  `ProjectionOpts.IncludeDeleted` makes the find path
  (Iter/All/One/Count) surface tombstones so chunkers can stream
  deletions. Full contract in `docs/13-index.md`.
- **Search indexer (phase 2) + search endpoint** — `internal/indexer`:
  per-space worker pair (advance loop: change feed → chunkers → FTS,
  cursor-driven, batched; embed loop: pending docs → batch embed →
  batch vector insert, fully parallel so embedder latency never delays
  FTS). One local any-store DB (`<data-dir>/index/index.db`), per-space
  collections with BM25 FTS + lazily-created IVF-SQ cosine vector
  index. Pluggable embedders: `ollama` / OpenAI-compatible (config
  `index.*`); none ⇒ FTS-only. Object deletion → purge rule; agent
  chunker amended to tombstone live non-memory rows. Surface:
  `POST /v1/spaces/:id/search` (hybrid RRF / fts / vector) + `any
  search` — the one sanctioned non-1:1 endpoint. Requires `any-store/v2`
  `v2.0.0-alpha.11` (FTS + vector; former `btree-fts` branch).
