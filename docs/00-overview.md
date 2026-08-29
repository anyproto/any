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

- `any run` — foreground server; takes the single-instance lock;
  graceful shutdown on SIGINT/SIGTERM or `any stop`.
- Auto-wallet-create on first run (via `auth.FileProvider`).
- HTTP endpoints for:
  - Account (own identity, metadata)
  - Spaces (create, list, get, delete, join, derive, one-to-one)
  - Objects (create, derive, delete)
  - Data plane: query, modify, delete-records
  - Types and properties (full SDK surface that ships)
  - Subscribe (per-(object, dataset) and per-space firehose) over Server-Sent Events
  - Chat (built-in type — send/list/edit/delete/react on per-object message streams)
  - Members, invites, ACL (mint/accept/decline/permissions/remove/
    ownership/self-remove/cancel-join/stop-sharing) — shipped.
  - Sync status — routes present, 501 until the SDK lands it.
- CLI subcommands for every endpoint, grouped by SDK section.
- Minimal YAML config (data dir, listen addr, network config).

## What's out of scope (v1)

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
├── anyuri/               PUBLIC: canonical any:// link grammar (19-links.md)
├── mobile/
│   ├── ios/              c-archive shim (//export + module.modulemap)
│   └── android/          gomobile bind shim (package NAME stays `mobile`)
├── internal/
│   ├── cli/              CLI subcommands, flag parsing, rendering
│   ├── server/           HTTP server, route wiring, SDK lifecycle
│   │   └── web/          embedded single-page UI served at /
│   ├── api/              request/response types shared by server and cli
│   ├── client/           HTTP client used by cli/ to call server/
│   ├── config/           config file + env var loading
│   ├── localstore/       local store naming + tag fence over the SDK's sdk.db
│   ├── markdown/         block-tree diff for the markdown round-trip
│   ├── nav/              virtual `nav` type (folder/item, parentId, pos)
│   ├── version/          binary version string
│   └── e2e/              full-stack tests (real SDK)
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

The server imports `github.com/anyproto/any-sync-sdk` and holds a
`*SDK` handle for the lifetime of the process. Each HTTP handler
translates its request body into the right SDK call and the SDK result
back into a response body. If the SDK needs a change (e.g. the
`Modify` → `VersionId` + resolved record ids gap), we raise it on the
SDK repo and block the relevant endpoint on it.
