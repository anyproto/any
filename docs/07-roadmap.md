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
8. ~~**Windows support.**~~ Resolved: the single-instance lock is an
   OS file lock on every platform (SYN-168, see Done). Nothing else is
   Unix-specific since we dropped Unix sockets.
9. ~~**External semantic-search service.**~~ Resolved: the local
   search index (`13-index.md`) provides hybrid recall; the agent's
   data is harness-owned userspace runtime datasets
   (`docs/11-agent-memory.md`), so its recall belongs to the anybao
   harness.
10. ~~**Account switching on a running server.**~~ Resolved (SYN-169):
    the engine is tearable down in place behind an engine gate, and a
    managed server's host switches with `POST /v1/auth {…, replace:
    true}` or signs out with `DELETE /v1/auth` (`02-server.md`
    § Modes). Standalone still switches by restart — by design, not
    by limitation.
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

- **Local store: sink-target validator on the public aggregate.**
  `Space.Aggregate` / `AggregateObjects` apply a blanket `ReadOnly()`,
  so a synced-source pipeline cannot `$out`/`$merge` into a local
  collection. The fence exists for a reason (`$out` could rewrite
  `<spaceId>_objects`); the replacement is a consumer-supplied target
  validator the server points at `localstore.ParseRef`.
- **Local store: cross-collection `$lookup` (any-store).** `$lookup
  from` is rejected unless it names the aggregated collection, and the
  lookup reads from the source's namespace. Resolving `from` to the
  target collection's namespace (two call sites; the read tx is
  already DB-wide) unlocks local↔synced joins — exactly what
  co-locating the local store in `sdk.db` is for.
- **`SyncStatusAPI.Peers` (or equivalent).** No production-grade
  per-space peer list on the SDK today — `/v1/spaces/:id/sync-status/peers`
  stays 501 until the SDK adds it. The diagnostic equivalent is
  surfaced via `Space.Debug().Space()` / `/v1/spaces/:id/debug`, but
  that surface is explicitly not stable.
- **`PropertiesAPI.{SetAccount, SetDevice}`.** Return errors today;
  routes are 501 until the rewrite-object (account scope) and
  device-local store (device scope) ship. `AttachType` / `DetachType`
  are live on both sides — saved views were the caller that wired the
  HTTP routes (status § 37).
- **Record-level account transport.** Dataset schema fields can
  declare `account` scope and the tech-space carrier is already keyed
  `(objectId, dataset, recordId)`, but the SDK's account mirror
  handles the objects rows only — so `POST …/modify` rejects
  `"scope":"account"` until the mirror learns dataset records. The
  local scope shipped (status § 21); account is the missing sibling
  (wanted for cross-device read state that survives device loss,
  though read-tracking proper syncs its frontier via tech-space KV
  instead).
- **`Types.Delete`.** Still not exposed over HTTP (`RemoveProperty` and
  the generic property / field PATCH shipped — status items 23 and 44).
- **`Types.Get` for non-object ids.** The SDK only returns
  `space.ErrNotFound` when the id resolves to an existing object that
  isn't tagged as a type. Ids that aren't objects at all surface as a
  wrapped store error ("tree does not exist") and currently fall
  through to `500 internal`. We could widen the 404 mapping in the
  handler if/when the SDK stabilises a sentinel for this case.
- **Scoped datasets (SYN-174).** Records that exist only for one
  account or one device — the SDK scopes FIELDS, and the private tiers
  of saved views (`24-data-views.md`) need scoped RECORDS. Shape agreed:
  scope the whole dataset (parallel `data_views_account` / `_device`),
  not a per-record flag — one dataset is one version domain, and mixing
  DAG / tech-tree / local-lexid versions in one dataset breaks
  versionId ordering and subscribe dedup. Account tier rides the
  record-level account transport above; device tier additionally needs
  the local sidecar (a record with no DAG behind it dies on
  wipe-and-rebuild).
- **Projection push-down.** The body's `projection` is honored at the
  serialisation boundary, but any-store still decodes the whole stored
  document before the server narrows it. Pushing the field set into the
  find path would cut the decode too. Measured, the decode is the
  smaller half — the wire and the fastjson round-trip are the cost — so
  this is an optimisation, not a gap. The wire contract does not change
  when it lands.
- **SDK `ProjectionOpts`.** `IncludeVariants` / `IncludeMeta` are still
  no-ops in the SDK's MVP; variant collapse and meta-stripping pending.
  **`IncludeDeleted` works** (SDK `v0.0.10` — used by the index chunkers
  to stream tombstones).

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
- **Search quality.** Highlighting (hit `data` is already a windowed
  snippet — `maxData`), per-scope weights,
  cross-space search, tunable score thresholds beyond the
  zero-similarity noise floor, query-time `VectorEf` tuning.
- **Embedding hygiene.** Re-embed on model change (currently a dim
  mismatch is a boot error suggesting removing `<data-dir>/index/`).
- **Chunk-level `require` / `exclude`.** Terms bind the hit's chunk,
  not the record (docs/13-index.md § Known limits); record-level
  semantics would need a per-record verdict over sibling chunks.
- **Object-level search grouping.** `limit` counts records
  (SYN-193); an editor page is several window records, so a long
  page can still take several slots. A per-dataset or request-level
  `groupBy: record | object` would collapse those — wrong for chat
  (a message is the result), so it needs the dataset's say.
- **Control bytes in dataset names and record ids.** The indexer now
  skips what it cannot address safely (docs/13-index.md § Store layout),
  so the id scheme is sound — but the declaration side still accepts it:
  a runtime dataset may declare a permissive `idPattern`, or a name
  carrying a control byte, and only finds out its records go unindexed.
  Rejecting at the creation API is the remaining half.
- **Local embedder follow-ups.** Multi-sequence batched decode (texts
  currently embed sequentially under one mutex); a packaged
  distribution story for the llama.cpp libs (today: `make llamacpp`
  drops them next to the binary; go:embed + extract was considered and
  deferred — pure overhead while "distribution" means `make build`).
## How to update this file

- Move items that ship to a "Done" section below (or remove them once
  they're obviously in the past).
- Add new questions as they come up during implementation.
- Keep the v1 goal list honest — if we cut something, strike it here
  so a reader knows scope moved.

## Types, parts and modules — follow-ups (shipped, see Done)

- **Namespaced chat.** Chat is shared-only in v1 (one `chat_messages`
  per object): read tracking, push topics and the unread counters are
  keyed by object, not by collection. A `<typeId>_<key>` chat instance
  needs per-collection read state and topic derivation first.
- **`data_view` as a module.** Saved views stay a registered built-in
  type owning `data_views`; the same declaration could be a `views`
  module a part declares (`{"module": "views"}`), which would let a
  type carry several view sets. Blocked on a client need.
- **Wiki folder marker.** The well-known `wiki/v1` bundle wants a
  "folder" flag next to its page type; whether that is a property, a
  `layout`, or a second type is a client decision still open.
- **Bundle-declared types beyond the root.** A bundle declares parts on
  its root only; a bundle that ships several types (a meeting type plus
  a decision type) still creates them one by one. Deterministic
  property ids and a full type declaration per bundle are the next SDK
  step.

## Runtime dataset schemas — follow-ups (SYN-147 shipped, see Done)

- **Dogfood the generic schema handler.** Collapse remaining zero-logic
  compiled-in handlers to pure declarations (`Handler: nil` +
  behavioral schema). Requires a per-dataset
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
- **SDK sentinels for dataset CRUD errors.** Key conflicts, shared
  conflicts and declaration validation surface as `fmt.Errorf` strings
  today — `any` STOPGAP-matches the messages (`datasetWriteError`).
  Wanted: exported `errors.Is`-able sentinels (decl invalid, key
  conflict, shared conflict), plus a retired-collection signal for the
  index sweep (preserve the key on the removed def tombstone).

## Property descriptors — follow-ups (SYN-211 shipped, see Done)

- **`validate` / `compute` members.** Reserved in `xFormat` and refused
  today. `validate` is one JSON-text leaf of declarative assertions
  (required / unique / range on a property) enforced at the write
  boundary only; `compute` is a read-time computed value (formula /
  rollup / lookup — a stored derived value cannot depend on an edited
  one, handlers read only their own object's immutable fields).
- **File and member relations.** A file is `any://f/<spaceId>/<fileId>`
  and a member is `any://m/<spaceId>/<identity>` — neither is an object,
  so `relation.targetTypes` cannot name them. Each needs its own slug
  plus a target member.
- **Built-in field descriptors.** `handler.Field` carries
  `Description` / `XFormat` and discovery renders them, but
  `chat_messages`, `editor_blocks`, `data_views` and the `any.*` row
  fields declare none — clients still hardcode that `any.icon` is an
  icon and `chat_messages.text` is markdown.
- **Nested descriptors.** `items` / `properties` are not settable over
  HTTP and a `relation` slug nested in a composite is invisible to
  backlinks. Composites are validated by the vocabulary's fixed shapes
  (`period` / `money` / `geo`) instead.
- **Paired / inverse relations, localisation of labels, autonumber,
  unit properties** — no contract yet; see docs/27-descriptors.md
  § Not covered yet.

## Done

- **Types, parts and modules** — a type is properties plus parts, each
  part owning datasets a module serves: `records` (the runtime schema
  handler, now always namespaced to `<typeId>_<key>`), `editor` and
  `chat` (compiled-in modules with a canonical shared collection). The
  built-in `page` / `editor` / `chat` types are gone — an object holds
  a collection while it carries a declaring type (`400
  dataset.not_declared` otherwise; no write attaches a type), documents
  and chats are user types registered as bundles, and bundles declare
  `parts` instead of `datasets`. Editor routes gained `:collection`;
  types gained `weight` / `layout` and `PATCH …/types/:typeId`; search
  gained per-module chunkers over every collection a module serves;
  push resolves chats through the chat collection's owners. Contract:
  docs/03-api.md § Parts and modules, the SDK's docs/17-user-datasets.md.
- **Property & field descriptors (SYN-211)** — one opaque `xFormat`
  bag on property and dataset-field definitions; the typed `format`
  object, `xKind` and the `meta.pos` / `meta.icon` conventions removed;
  `any` validates the vocabulary, the leaf-only PATCH rule and every
  value write against the current slug; field-level PATCH. Contract:
  docs/27-descriptors.md.
- **Cross-platform single-instance lock (SYN-168)** — one
  `gofrs/flock` implementation for every platform replaces the PID
  file plus `kill(pid, 0)` liveness probe, which had no Windows
  equivalent and left `pidlock_windows.go` a fatal stub: a Windows
  server with a wallet never bound. The lock is on
  `<account-dir>/server.lock` and the kernel releases it, so stale-lock
  reclaim is gone; `server.pid` survives only to name the holder in
  `409 auth.account_in_use`. Companion: a `windows-latest` job in
  pr-checks — the first CI that executes a Windows build.
- **Local store** — device-local, non-CRDT any-store collections at
  `/v1/local` (`docs/26-local-store.md`), inside the SDK's `sdk.db`
  under the `l_` tag (`internal/localstore` is the single fence).
  Local↔local `$out`/`$merge`/`$lookup` ship; synced→local rollups and
  cross-collection joins wait on the upstream gates below. Out of
  scope for now: auto-cleanup on space delete (a space-scoped
  collection outlives its space), TTL / expiration, a collection
  registry, subscribe, FTS/vector indexes, backup/export.
- **Long-record chunking + windowed hit data (SYN-188)** — the
  indexer splits any chunker entry over `Options.ChunkRunes` (2000)
  into chunk docs (`base<U+001F>n` ids, record range `[base,
  base+" ")`, per-record hash diff in `planDocs`); hits carry `chunk`,
  `data` is a `maxData`-rune window (default 512) with `dataOffset` /
  `dataTotal`. docs/13-index.md § Chunking long records.
- **`require` / `exclude` in every mode (SYN-187)** — vector hits are
  post-filtered against the FTS index (`Store.FilterTerms`, K widened
  up to 1000 when terms thin the leg) before fusion.
- **`limit` counts records (SYN-193)** — each leg reads until its
  window covers enough distinct records (lexical: one lazy cursor
  pulled past the fixed over-fetch; vector: K widened ×4), fusion stays
  per chunk, and `groupHits` collapses chunks into records scored by
  their best chunk; `passages: N` returns a record's next best chunks.
  Measured first (`BenchmarkCutoff`, `TestIteratorEarlyCloseNoLeak`):
  an any-store `$text` query costs the same with or without `Limit`
  and an early Close is free and leak-free. docs/13-index.md § Search.
- **Derived spaces registry (SYN-164)** — well-known per-account
  spaces (`bao`) derived from a compiled-in registry
  (`internal/server/derivedspaces.go`, seed convention
  `any/space/<name>/v1`) instead of exposing raw `Service.Derive`
  over HTTP. `GET /v1/spaces/derived` resolves ids without creating
  (`DeriveId`); `POST /v1/spaces/derived/:name` materializes lazily +
  idempotently. Derived spaces are permanent: two-layer delete guard —
  registry pre-check in the server (covers unmaterialized ids) + SDK
  row-flag refusal (`space.ErrIsDerivedSpace`, synced set-once
  `derived` on the tech-space row; `SpaceInfo.derived` passthrough).
  CLI `any space derived [create <name>]`. Contract: docs/03-api.md
  § Spaces → Derived spaces.

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
  `AnyLibStart`, addrs comma-separated (SYN-83);
  the host supplies the peer alongside its nodeconf choice; with
  neither supplied, the packaged production pair applies
  (`config.ApplyPushDefaults`).
  **Remaining:** running the gated e2e against real infra.
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
    record rendered via `value.FastJson(arena).MarshalTo`.
- **Phase 3 endpoint (cross-object query + storage shift)** —
  `POST /v1/spaces/:id/objects/query` wires `Space.QueryObjects()`
  against the per-space shared collection. Same fastjson fast-path as
  Modify; same `{records:[...]}` response shape. Storage shift on the SDK side: object property values
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
