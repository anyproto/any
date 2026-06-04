# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Implementation slices landed:
1. **scaffolding + wallet + health** — `any init` / `any run` / `any status` /
   `any stop` / `any version` work end-to-end.
2. **SDK boot + space lifecycle** — Run opens `any-sync-sdk` (nodeconf via
   `config.LoadNodeconf`, default fallback `../test-etc/staging.yml`).
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
   Optional opaque `fromAgent` tag on create marks the message as
   agent-authored (UI hint only, not signature-verified); immutable
   post-create. Lets an agent subscribed to `chat_messages` filter to
   human-typed messages (fromAgent empty) when deciding what to
   respond to. `any chat send --from-agent <id>`.
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
   diff), and creates the new blocks in one ModifyBatch — O(chunk),
   not O(doc). Purely additive; same reply shape as PUT with only
   `inserted` populated (`markdown.Append` in `internal/markdown`).
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

11. **bobrik-watch** — JS-powered chat agent in `cmd/bobrik-watch/`.
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
    - **SDK prerequisite (branch `feat/techspace-store-query-schemas`,
      commit `74446da`).** The public `handler.Dataset` gained a `Schema`
      field + re-exported schema primitives (`handler.Field` / `Scope` /
      `ScopeSynced|Derived|Local` / `Leaf`); `spaceobjects.Store` honors
      it (back-compat: a zero Schema → Dynamic). `any` pins the
      pre-release pseudo-version off that branch; bump to the tagged
      release once the SDK cuts one.

**Always read the relevant `docs/NN-*.md` before writing code for an area**, and if
implementation diverges from a doc, update the doc in the same change.

### Build / test / run

```
go build ./cmd/any                                # binary at ./any
make build                                        # builds both any and bobrik-watch
go test ./...                                     # unit tests (config + server)
go vet ./...

# End-to-end
ANY_DATA_DIR=/tmp/any-e2e ./any init              # first-run wallet + mnemonic
ANY_DATA_DIR=/tmp/any-e2e ./any run               # foreground server
./any status                                      # GET /v1/health
./any stop                                        # POST /v1/shutdown
```

For bobrik-watch commands, see [`cmd/bobrik-watch/CLAUDE.md`](cmd/bobrik-watch/CLAUDE.md).

Module path: `github.com/anyproto/any`. Go 1.26.2. Dependencies
(`any-sync-sdk`, `any-sync`, `any-store`, `anytype-agent-runtime`) are
**published modules**, not sibling checkouts — `go.mod` pins versions
with no `replace`. The SDK is currently pinned at a pre-release
pseudo-version off branch `feat/techspace-store-query-schemas`
(`v0.0.8-0.2026…-74446dacdb2e`, the dataset-schema + unified-query work —
see status item 12); bump it to the tagged release once the SDK tags one.
`any-sync-sdk` is a private module — `GOPRIVATE=github.com/anyproto/any-sync-sdk`
(+ git SSH `insteadOf`) is needed to fetch it directly. To inspect SDK
behavior, read the module cache
(`$(go env GOMODCACHE)/github.com/anyproto/any-sync-sdk@<version>/`) or
the source checkout `../any-sync-sdk2/`.

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
module cache, or the `../any-sync-sdk2` source checkout if you have it). When an SDK
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
- **Localhost-only.** The server refuses to bind anything other than a loopback
  address and must fail clearly if `--addr 0.0.0.0:...` is passed. No auth middleware,
  no CORS, no rate limiting in v1 — those come with the remote-access story (v2).
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
| `docs/SDK-DRIFT.md` | where the SDK's own docs/comments disagree with observed v0.0.4 behavior |

Keep `docs/07-roadmap.md` honest — move shipped items to its "Done" section or
strike cut scope; add new open questions as they surface during implementation.
