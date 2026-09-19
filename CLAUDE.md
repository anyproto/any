# CLAUDE.md

Guidance for Claude Code in this repository.

## Rules for this file

This file is loaded into every session. It holds what a session needs
before touching code: rules, traps, commands and where to look. Nothing
else.

- **Current state only.** No history: no "used to", "no longer",
  "replaced", "removed", "superseded" or "since …", no dates, no "lesson
  learned" notes.
- **No plans.** Nothing about work that hasn't landed: no next steps,
  open decisions or follow-ups.
- **No changelog.** A shipped feature adds nothing here. Its contract goes
  in its `docs/NN-*.md`, and the index below already points there. No
  numbered status items, no per-feature summaries, no inventories of
  files, functions or tests (the code is the inventory).
- **No references that rot:** no Linear ids, PR numbers, branch names,
  dependency versions (`go.mod` is the only source), local paths, rollout
  notes, or "not yet" / "deferred" lists for a single feature.
- **Add a line only** for a repo-wide rule, or for a costly trap that
  isn't visible where the code lives. A trap local to one file belongs in
  a comment at that site.
- **Keep it true.** If a change makes a line here wrong, fix or delete the
  line in the same change. Stay under 300 lines.

## Repo hygiene

- **Linear ids (`SYN-123`) appear only in branch names and commit
  messages.** Never in code, comments, tests, fixtures, `docs/`,
  `website/`, this file or PR titles and bodies. Describe the behavior or
  the contract instead.
- **No local environment in anything committed:** no absolute or home
  paths, sibling-checkout paths, machine or harness names, private object
  ids, local ports or data dirs. Use repo-relative paths.
- **Do not add plans to the repo.** No plans, roadmaps, proposals, design
  deliberation, research or benchmark reports, TODO lists, task files or
  "next steps" — not as files, not in `docs/` or `website/`, not in this
  file, not in code comments. They go stale the day the work lands. Plans
  live in Linear or the PR description.
- **Read the area's `docs/NN-*.md` before writing code for it.** If a change
  makes a doc wrong, update the doc in the same change, plus the matching
  `website/` page if it covers the feature. Docs describe the current
  state only.

## What this is

`any` is one Go binary that wraps `any-sync-sdk`:

- `any run` starts a foreground HTTP/JSON server on `127.0.0.1:7001`. The
  same server runs in-process for the mobile shims (`internal/embedded`).
- Every other `any <cmd>` is a thin CLI client that calls that server over
  HTTP.

v1 is a prototype. Breaking wire changes are expected and are absorbed
by bumping the `/v1/` prefix. Start with `docs/00-overview.md` (scope,
package layout) and `docs/29-client-model.md` (the object model).

`any-sync-sdk`, `any-sync` and `any-store` are published modules. To read
the SDK at the pinned version, look in
`$(go env GOMODCACHE)/github.com/anyproto/any-sync-sdk@<version>/`. When the
SDK lacks something or has an awkward shape, change the SDK instead of
working around it here.

## Build, test, run

Run under `nix develop -c …` when the flake is available: the local
embedder's llama.cpp bindings need the system `libffi`.

```
make build              # bin/any with -tags llamacpp; regenerates swagger, fetches llama.cpp libs
make test               # go test ./..., then -tags llamacpp over the local-embedder packages
make vet
make check-deps         # untagged and mobile builds must not link libffi
make swagger            # regenerate the OpenAPI spec in internal/server/docs
make catalog-validate   # validate internal/catalog/catalog.yml
make llamacpp           # prebuilt llama.cpp libs into bin/llamacpp
make build-android      # dist/android/any.aar (needs an Android NDK)
make docs-serve         # render and serve website/
```

- **A bare `go build ./cmd/any` has no local embedder.** Only
  `-tags llamacpp` builds carry it: without the tag `index.embedder: local`
  fails boot and `auto` embeds online only. The binary is `bin/any`. A
  `./any` at the repo root is a stale leftover.
- **Server and e2e tests skip silently without `staging.yml`**, a
  gitignored nodeconf at the repo root. A green run without it proves
  little.
- **Nothing may join production from a test or a manual run.** With no
  nodeconf configured, the server joins the production network and uses
  the production push node. Tests boot against `staging.yml` or
  `config.NodeconfPlaceholder()`; a manual run sets
  `ANY_NETWORK_NODECONF_PATH`.
- Handlers carry swag annotations. CI fails when `internal/server/docs`
  doesn't match `make swagger`, and on an invalid catalog.

```
ANY_DATA_DIR=/tmp/any-smoke bin/any init
ANY_DATA_DIR=/tmp/any-smoke ANY_NETWORK_NODECONF_PATH=$PWD/staging.yml bin/any run
bin/any status                          # GET /v1/health
ANY_DATA_DIR=/tmp/any-smoke bin/any stop   # SIGTERM to the account-lock holder
```

CI and release artifacts: `docs/18-ci.md`.

## Architecture invariants

- **The CLI never opens the SDK or any-store and never talks to peers.**
  Every caller goes through HTTP. If the CLI needs something, add an
  endpoint.
- **echo v4, every route under `/v1/`**, with routes grouped per SDK
  section (`docs/02-server.md`).
- **Endpoints map 1:1 onto SDK methods, and CLI commands map 1:1 onto
  endpoints.** No convenience endpoints that aggregate SDK calls. The
  consumer-side exceptions are search and backlinks (the local index), the
  event bus and processes, the local store, the editor markdown bridge and
  catalog setup.
- **Localhost-only.** The listener refuses non-loopback addresses. There
  is no caller authentication and no rate limiting. `/v1` answers
  `401 auth.required` until an account is booted, and a managed server's
  control token expresses ownership, not identity. CORS admits only the
  fixed desktop-shell allowlist in `routes.go`.
- **Pretty-printed JSON** on the wire and on CLI stdout. No tables, no
  `--output`.
- **One error shape** for every non-2xx:
  `{"error": {"code", "message", "details?"}}`. Codes live in
  `docs/06-errors.md`. Never leak SDK types or filesystem paths in
  `message` or `details`. Map SDK errors with `errors.Is` on exported
  sentinels; a match on message text is marked `STOPGAP` until the SDK
  exports a sentinel.
- **A surface the SDK can't serve yet is still registered** and answers
  `501 sdk.not_implemented` (`notImplemented`).
- **Request and response types live only in `internal/api/`**, shared by
  the server and the CLI.
- **Logging goes through `any-sync/app/logger`.** echo is wired to it, so
  there is one log stream.
- **Reads go through query.** Object rows are read via
  `…/objects/query[/subscribe]` and dataset records via
  `…/query[/subscribe]`, with the storage collection as `dataset`. Module
  handlers (chat, editor) serve writes only, and the editor markdown
  render is the one read-side transform. Query routes are POST because
  the body carries the filter; don't turn them into GET.
- **No write sets a type or files a collection as a side effect.** A value
  or dataset write is admitted only while the object's type or one of its
  collections declares it (`400 dataset.not_declared`). Never add an
  "ensure" step to a write path.
- **Values are keyed by `propId`.** An `xKey` is a handle clients resolve
  to a `propId`; it is never a storage key.
- **POSTs are not idempotent** unless the endpoint's section in
  `docs/03-api.md` says so. There is no `Idempotency-Key`.
- **any-store filters use the typed `any-store/v2/query` package**, never
  `map[string]any` or JSON literals. Static filters are built once at
  package level. Raw shapes enter only from HTTP, through
  `query.ParseCondition`.
- **Key material never reaches a generic read.** Tech-index datasets are
  reachable only through the closed policy tables in `techspace.go` and
  `handlers_spaces_query.go`, which also strip private-key fields. Every
  per-object read (query, subscribe, aggregate, history) calls
  `identityKeysReadRefused`, and any new route that takes a dataset name
  must call it too.
- **The local store never writes an SDK collection** and never opens a
  transaction spanning an `l_*` collection and an SDK collection.
  `localstore.ParseRef` is the single place a collection name is admitted.

## Server traps

- **Engine lifecycle.** Every `/v1` request and stream runs inside the
  engine gate, and teardown holds `authMu` while it drains the gate. A
  handler or engine goroutine that takes `authMu` deadlocks teardown.
  Start engine goroutines with `engine.spawn` so teardown joins them.
- **An any-store iterator holds a read transaction.** Pull, then close.
  Never keep one open across another store call, or reader slots
  deadlock.

## Packages

Layout: `docs/00-overview.md` § Package layout. What it doesn't say:

- `anyuri/` is the only public package: the `any://` grammar that clients
  import. Add no other public package without an equally explicit
  contract.
- `mobile/android` declares `package mobile` because gomobile names the
  AAR's Java class after the package. `scripts/build-xcframework.sh` builds
  `-o anylib.a` because cgo names the header after `-o`, and the modulemap
  and Swift's `import AnyLib` expect `anylib.h`.
- `internal/catalog/catalog.yml` holds the well-known bundles
  (`docs/28-well-known-bundles.md`). The server refuses to boot on an
  invalid catalog.
- `internal/server/web/` is the `/ui` debug harness.
  `internal/server/docs/` is generated.

## Config and lifecycle

Precedence: config file → `ANY_*` env vars → flags (`docs/05-config.md`).
Defaults: data dir `~/.any/`, listen address `127.0.0.1:7001`.

The data dir is a root. Each account lives in `<root>/<accountId>/`
(`wallet.key` or `device.key`, `network.json`, `server.lock` / `.pid` /
`.addr`, `sdk/`, `files/`, `index/`). A flat root is the default account
(`docs/02-server.md` § Data dir layout).

Modes (`docs/02-server.md` § Modes):

- `standalone` (default): the key is on disk; logout and HTTP shutdown
  are refused.
- `managed`: the host supplies the key on every boot. `DELETE /v1/auth`,
  account switch and `POST /v1/shutdown` require the
  `X-Any-Control-Token` header.

Shutdown: every server stops on `SIGINT` / `SIGTERM`. `any stop` finds the
holder of the account lock and signals it, without HTTP. Every shutdown
drains the engine (10s) and exits 0.

CLI exit codes: `0` success, `1` user error or 4xx, `2` server 5xx, `3`
server unreachable. When the server is down, print "start it with
`any run`"; never auto-start it.

## Out of scope (v1)

- Remote access, TCP auth, TLS.
- Install scripts and service files.
- More than one account per process.
- GUI beyond the `/ui` debug harness; gRPC or any transport other than
  HTTP/JSON.

## Docs index

| File | Governs |
|------|---------|
| `docs/00-overview.md` | goals, scope, process model, package layout |
| `docs/01-cli.md` | CLI commands, flags, input formats, exit codes |
| `docs/02-server.md` | modes, startup, shutdown, instance lock, data dir, health |
| `docs/03-api.md` | HTTP endpoint catalog, body shapes, middleware, idempotency |
| `docs/04-events.md` | subscriptions (SSE): contract, frames, lifecycle |
| `docs/05-config.md` | config file, env vars, flags, first run |
| `docs/06-errors.md` | error shape, HTTP statuses, error codes |
| `docs/08-clients.md` | client call patterns and recipes |
| `docs/09-query.md` | query guide: filters, sort, paging, projection, paths, dates |
| `docs/11-agent-memory.md` | agent data as harness-owned runtime datasets |
| `docs/13-index.md` | search and link index: chunkers, indexer, embedders, `/search`, the local embedder build |
| `docs/14-aggregation.md` | aggregation pipelines: stages, pushdown, limits |
| `docs/16-chat.md` | chat client guide: rendering, liveness, read tracking |
| `docs/17-files.md` | files: storage tiers, durability, cache, variants |
| `docs/18-ci.md` | release artifacts and CI |
| `docs/19-links.md` | canonical `any://` link format |
| `docs/20-push.md` | push notifications |
| `docs/21-events.md` | event bus: publish, filtered subscribe |
| `docs/22-processes.md` | process progress and cancel over the bus |
| `docs/23-devices.md` | devices registry and active-app election |
| `docs/24-data-views.md` | saved views: the `dataview` type |
| `docs/25-favorites.md` | favourites bundle client contract |
| `docs/26-local-store.md` | local store: device-local collections in `sdk.db` |
| `docs/27-descriptors.md` | property and field descriptors (`xFormat`) |
| `docs/28-well-known-bundles.md` | the usecase catalog |
| `docs/29-client-model.md` | the object model: types, collections, properties |
| `docs/search/` | search evaluation and tuning decisions |
| `website/` | public docs site (`website/README.md` has its rules) |
