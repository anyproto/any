# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Two implementation slices landed:
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

**Always read the relevant `docs/NN-*.md` before writing code for an area**, and if
implementation diverges from a doc, update the doc in the same change.

### Build / test / run

```
go build ./cmd/any                                # binary at ./any
go test ./...                                     # unit tests (config + server)
go vet ./...

# End-to-end
ANY_DATA_DIR=/tmp/any-e2e ./any init              # first-run wallet + mnemonic
ANY_DATA_DIR=/tmp/any-e2e ./any run               # foreground server
./any status                                      # GET /v1/health
./any stop                                        # POST /v1/shutdown
```

Module path: `github.com/anyproto/any`. Go 1.26.2. Sibling repos wired via `replace`:
`any-sync-sdk` → `../any-sync-sdk2`, `any-sync` → `../any-sync`.

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

This repo imports `any-sync-sdk` from a sibling checkout:

```
any            (this repo)  — HTTP server + CLI
 └── any-sync-sdk           — Go SDK (Space, Object, CRDT, types, properties)
      └── any-sync, any-store
```

`../any-sync-sdk/docs/00-common-context.md` has the full stack context. When an SDK
method is missing or awkward, raise it on the SDK repo rather than working around
it here — several v1 endpoints are explicitly blocked on SDK work (see
`docs/07-roadmap.md` § SDK-side prerequisites).

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

- **Subscriptions** — no `/subscribe` endpoint. Likely WebSocket when it lands
  (`docs/04-events.md`). Callers poll via query in the meantime.
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
| `docs/04-events.md` | subscriptions (deferred) — tradeoffs recorded |
| `docs/05-config.md` | config file schema, env vars, flags, first-run flow |
| `docs/06-errors.md` | error response shape, HTTP codes, code namespace |
| `docs/07-roadmap.md` | v1.x / v2 plans, open questions, SDK prerequisites |

Keep `docs/07-roadmap.md` honest — move shipped items to its "Done" section or
strike cut scope; add new open questions as they surface during implementation.
