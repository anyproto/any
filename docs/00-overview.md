# Overview

## Goal

A single Go binary `any` that:

1. Runs an HTTP/JSON server exposing the `any-sync-sdk` surface.
2. Ships a CLI client against that server as its default mode.

**v1 is a prototype.** It exercises the SDK through an out-of-process
façade; breaking changes to the API are expected.

## Process model

```
┌────────────────────────────────────────────────────────────────┐
│ any run  (foreground process, localhost only)                  │
│                                                                │
│  ┌────────────────────┐   ┌──────────────────────────────┐     │
│  │ HTTP/JSON server   │──▶│ any-sync-sdk                 │     │
│  │ 127.0.0.1:<port>   │   │  Space, Object, CRDT,         │     │
│  │ (plain HTTP)       │   │  types, properties, ...       │     │
│  └────────────────────┘   └──────────────────────────────┘     │
│            ▲                           │                       │
│            │                           ▼                       │
│            │                ┌──────────────────┐               │
│            │                │ any-store + any- │               │
│            │                │ sync on disk     │               │
│            │                └──────────────────┘               │
└────────────┼───────────────────────────────────────────────────┘
             │ HTTP (127.0.0.1)
┌────────────┴───────────────────────────────────────────────────┐
│ any <cmd>   (short-lived CLI, default mode)                    │
│  Builds a request, sends it, prints the response.              │
└────────────────────────────────────────────────────────────────┘
```

The CLI never opens the SDK, never touches any-store, never talks to
any-sync peers. Every caller — human, script, language binding — goes
through HTTP.

The same server also runs in-process: `internal/embedded` owns the
lifecycle for the mobile bindings (`mobile/ios`, `mobile/android`).

## Security model (v1)

**Localhost-only, no caller authentication.** The server binds a
loopback address and refuses anything else; anyone with shell access on
the machine can call it. CORS admits only a fixed allowlist of
desktop-shell webview origins (`03-api.md` § Middleware). A managed server's control
token expresses ownership, not authentication (`02-server.md` § Modes).

## What's in scope (v1)

- `any run` — foreground server, standalone or managed
  (`02-server.md`); `any init` and `POST /v1/auth` create, restore or
  select the account.
- HTTP endpoints (`03-api.md`) for: auth and account, identities,
  devices, spaces (create, join, derived, one-to-one, delete, settings),
  members / invites / ACL, objects, types / parts / properties /
  runtime datasets, bundles and the usecase catalog, the data plane
  (query, modify, upsert, aggregate), windowed query/subscribe over
  Server-Sent Events, the chat and editor modules, files, version
  history, sync status, search and backlinks over the local index, the
  event bus and processes, the local store, push notifications, debug.
- CLI commands mapping onto those endpoints (`01-cli.md`).
- YAML config, env vars and flags (`05-config.md`).

## What's out of scope (v1)

- Remote access. The listener is loopback-only.
- Install scripts and service files. You run `any run` by hand.
- Multi-account per process. One server serves one account at a time.
- GUI (beyond the `/ui` debug harness), gRPC, any transport other than
  HTTP/JSON.

## Non-goals

- Performance tuning.
- Stability guarantees for the API shape. `/v1/` is the current draft;
  breaking changes during the prototype phase are expected and are
  absorbed by bumping the version path.
- Hiding SDK concepts. Endpoints map 1:1 onto SDK methods; CLI commands
  map 1:1 onto endpoints.

## Package layout

```
any/
├── cmd/
│   ├── any/              main() — the CLI root; `any run` starts the server
│   └── anydocs/          renders website/ into a static site (`make docs`)
├── anyuri/               PUBLIC: canonical any:// link grammar (19-links.md)
├── mobile/
│   ├── ios/              c-archive shim (//export + module.modulemap)
│   └── android/          gomobile bind shim (package NAME stays `mobile`)
├── internal/
│   ├── cli/              CLI subcommands, flag parsing, rendering
│   ├── client/           HTTP client used by cli/ to call server/
│   ├── api/              request/response types shared by server and cli
│   ├── server/           HTTP server, route wiring, engine (SDK) lifecycle
│   │   ├── docs/         generated OpenAPI spec (`make swagger`)
│   │   └── web/          embedded debug harness served at /ui
│   ├── config/           config file + env var loading, embedded nodeconf
│   ├── embedded/         in-process server lifecycle for the mobile shims
│   ├── bundles/          bundle install / adopt / resolve engine
│   ├── catalog/          usecase catalog (catalog.yml) loader + validator
│   ├── chat/             `chat` module (chat_messages)
│   ├── editor/           `editor` module (editor_blocks)
│   ├── markdown/         markdown ↔ block-tree bridge
│   ├── page/ miniapp/ bin/ dataview/   built-in hidden types
│   ├── index/            chunker contract (text + link entries)
│   ├── indexer/          search + link index, embedders
│   ├── localstore/       local store naming + tag fence over the SDK's sdk.db
│   ├── push/             push notifications (token, topic sync, notify)
│   ├── version/          binary version string
│   └── e2e/              full-stack tests (real SDK)
├── scripts/              build-any.sh, build-xcframework.sh, fetch-llamacpp.sh
├── makefiles/            android.mk
├── website/              public docs site sources
└── docs/                 this directory
```

Nothing is published as an external Go package, with one deliberate
exception: `anyuri/` (`github.com/anyproto/any/anyuri`) — the canonical
`any://` link grammar. `any` owns the format, and clients/agents import
the Build/Parse rule instead of reimplementing it (see
[19-links.md](19-links.md)).

The `mobile/` shims aren't imported either — they're binding surfaces
(one c-archive, one AAR), both thin adapters over `internal/embedded`,
which owns the lifecycle. They sit outside `cmd/` because `cmd/` is Go's
convention for runnable binaries and neither artifact is one.

## Relationship to the SDK

The server imports `github.com/anyproto/any-sync-sdk`. While an account
is booted, its engine holds the `*SDK` handle (`02-server.md`
§ Startup). Each HTTP handler translates its request body into the
SDK call and the SDK result back into a response body. When the SDK
lacks a method an endpoint needs, the change goes into the SDK, not
around it here.

## Docs index

| File | Governs |
|------|---------|
| [00-overview.md](00-overview.md) | goals, scope, process model, package layout |
| [01-cli.md](01-cli.md) | CLI commands, flags, input formats, exit codes |
| [02-server.md](02-server.md) | modes, startup, shutdown, instance lock, data dir, health |
| [03-api.md](03-api.md) | HTTP endpoint catalog, body shapes, middleware |
| [04-events.md](04-events.md) | subscriptions (SSE) — contract, frames, lifecycle |
| [05-config.md](05-config.md) | config file, env vars, flags, first run |
| [06-errors.md](06-errors.md) | error shape, HTTP statuses, error codes |
| [08-clients.md](08-clients.md) | client call patterns |
| [09-query.md](09-query.md) | any-store query guide — filters, sort, paging, projection, paths |
| [11-agent-memory.md](11-agent-memory.md) | agent data as harness-owned runtime datasets |
| [13-index.md](13-index.md) | search and link index — chunkers, indexer, embedders, `/search` |
| [14-aggregation.md](14-aggregation.md) | aggregation pipelines — stages, pushdown, limits |
| [16-chat.md](16-chat.md) | chat client guide — rendering, liveness, read tracking |
| [17-files.md](17-files.md) | files — storage tiers, durability, cache, variants |
| [18-ci.md](18-ci.md) | release artifacts and CI |
| [19-links.md](19-links.md) | canonical `any://` link format |
| [20-push.md](20-push.md) | push notifications |
| [21-events.md](21-events.md) | event bus — publish, filtered subscribe |
| [22-processes.md](22-processes.md) | process progress and cancel over the bus |
| [23-devices.md](23-devices.md) | devices registry and active-app election |
| [24-data-views.md](24-data-views.md) | saved views — the `dataview` type |
| [25-favorites.md](25-favorites.md) | favourites bundle client contract |
| [26-local-store.md](26-local-store.md) | local store — device-local collections |
| [27-descriptors.md](27-descriptors.md) | property and field descriptors (`xFormat`) |
| [28-well-known-bundles.md](28-well-known-bundles.md) | the usecase catalog |
| [29-client-model.md](29-client-model.md) | the object model for clients |
| [search/README.md](search/README.md) | search evaluation and tuning decisions |
