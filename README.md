# any

All-in-one binary that wraps [`any-sync-sdk`](../any-sync-sdk) with a JSON
HTTP API plus a CLI client.

## What this is

- **One binary**: `any`.
- **Two modes**:
  - `any run` — starts an HTTP server on localhost and opens the SDK.
    Runs in the foreground; stop with Ctrl-C or `any stop`. No
    self-daemonization in v1 (future: install script / service file).
  - `any <command>` — default. CLI client; makes HTTP calls to a running
    server.
- **Thin CLI, thick server**: the server holds the SDK, any-store, and the
  any-sync connections. The CLI only builds requests and renders responses.

## Why / what's this for in v1

This is a **prototype** — its first job is to let us actually use the SDK
surface end-to-end, so we can tell which parts of the API are usable,
which are painful, and what to prioritise next. It is also the home for
the initial auth flow (wallet creation).

Narrowly in v1:
- Start the server; auto-generate a wallet on first run.
- Cover the core SDK ops over HTTP: spaces, objects, query, modify, types,
  properties.
- CLI subcommands that call those endpoints.

Deferred (see [`docs/07-roadmap.md`](docs/07-roadmap.md)):
- Subscriptions (will likely be WebSocket).
- Remote access / TCP auth.
- Install scripts, service files.
- Files, multi-account, GUI.

## Relation to other repos

```
any            (this repo)        — HTTP server + CLI
 └── imports
     any-sync-sdk                 — Go SDK (Space, Object, CRDT, ...)
      └── imports
          any-sync, any-store     — sync engine, document store
```

Read `../any-sync-sdk/docs/00-common-context.md` first for the full stack.

## Docs in this repo

- [`docs/00-overview.md`](docs/00-overview.md) — goals, scope, layout
- [`docs/01-cli.md`](docs/01-cli.md) — CLI command surface
- [`docs/02-server.md`](docs/02-server.md) — server lifecycle
- [`docs/03-api.md`](docs/03-api.md) — HTTP endpoint catalog
- [`docs/04-events.md`](docs/04-events.md) — subscriptions (deferred)
- [`docs/05-config.md`](docs/05-config.md) — config file and flags
- [`docs/06-errors.md`](docs/06-errors.md) — error response shape
- [`docs/07-roadmap.md`](docs/07-roadmap.md) — what's next, open questions

Module path: `github.com/anyproto/any`.
