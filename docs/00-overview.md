# Overview

## Goal

Ship a single Go binary `any` that:

1. Runs an HTTP/JSON server exposing the `any-sync-sdk` surface.
2. Ships a CLI client against that server as its default mode.

**v1 is a prototype.** Its purpose is to let us exercise the SDK through
an out-of-process façade so we can tell which parts of the API are
useful, which are painful, and what to prioritise next. It's also the
home for the initial auth flow (creating and inspecting the wallet).

We explicitly expect to change things based on what v1 teaches us.

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

## Security model (v1)

**Localhost-only, no auth.** The server binds 127.0.0.1. Anyone with
shell access on the machine can call it. This is fine for the v1
prototype role; a real auth story comes with the remote-access story,
and both are deferred.

## What's in scope (v1)

- `any run` — foreground server; writes PID lock; graceful shutdown on
  SIGINT/SIGTERM or `any stop`.
- Auto-wallet-create on first run (via `auth.FileProvider`).
- HTTP endpoints for:
  - Account (own identity, metadata)
  - Spaces (create, list, get, delete, join, derive, one-to-one)
  - Objects (create, derive, delete)
  - Data plane: query, modify, delete-records
  - Types and properties (full SDK surface that ships)
  - ACL, members — placeholders today on the SDK; routes mirror the
    eventual SDK shape, return 501 until the SDK lands them.
  - Sync status — same: routes present, 501 until the SDK lands.
- CLI subcommands for every endpoint, grouped by SDK section.
- Minimal YAML config (data dir, listen addr, network config).

## What's out of scope (v1)

- **Subscriptions.** No `/subscribe` endpoint, no event stream. When we
  add it we will likely use WebSocket, but the shape depends on what
  v1 teaches us about client usage patterns. See `04-events.md`.
- Remote access. TCP is localhost-only; no auth tokens.
- Install / service files. You run `any run` by hand. A future
  installer (ollama-style one-liner) comes after v1 stabilises.
- File upload / download (SDK defers files to v1.1).
- Multi-account per server. One server = one account.
- GUI, gRPC, any transport other than HTTP/JSON.

## Non-goals

- Performance tuning.
- Stability guarantees for the API shape. `/v1/` is the current draft;
  breaking changes during the prototype phase are expected and are
  absorbed by bumping the version path.
- Hiding SDK concepts. Endpoints map 1:1 onto SDK methods; CLI commands
  map 1:1 onto endpoints.

## Package layout (proposed)

```
any/
├── cmd/any/              main() — dispatches to cli or server subcommand
├── internal/
│   ├── cli/              CLI subcommands, flag parsing, rendering
│   ├── server/           HTTP server, route wiring, SDK lifecycle
│   ├── api/              request/response types shared by server and cli
│   ├── client/           HTTP client used by cli/ to call server/
│   └── config/           config file + env var loading
└── docs/                 this directory
```

Nothing is published as an external Go package.

## Relationship to the SDK

The server imports `github.com/anyproto/any-sync-sdk` and holds a
`*SDK` handle for the lifetime of the process. Each HTTP handler
translates its request body into the right SDK call and the SDK result
back into a response body. If the SDK needs a change (e.g. the
`Modify` → `VersionId` + resolved record ids gap), we raise it on the
SDK repo and block the relevant endpoint on it.
