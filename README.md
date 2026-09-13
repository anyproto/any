# any

> [!WARNING]
> **Alpha software. Do not use it in production or with an account holding
> anything you cannot afford to lose — use a dedicated mnemonic for experiments.**
>
> - **No local auth.** The HTTP server on `127.0.0.1` trusts every local
>   process: anything running as your user can call the API as you, and can
>   read the seed phrase from `wallet.key` in the data dir unless the wallet is
>   encrypted with a passkey (`ANY_WALLET_PASSKEY`).
> - **Search embeds online by default.** The default `index.embedder: auto`
>   sends indexed text and search queries to an online embedding API; set
>   `index.embedder: local` (or `none`) to keep them on the device.
> - **Under active development.** Data formats, bundle ids and APIs change
>   without migration; data written by an older build can become unreadable
>   after an upgrade.
> - **Never copy `wallet.key` between machines.** It carries the device key, so
>   a copy clones the peer id and breaks realtime sync. Restore a second device
>   from the mnemonic (`any init --mnemonic-stdin`).

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

## Install

`any-sync-sdk` is a private repo today, so `go install` needs
`GOPRIVATE` and an SSH-rewrite for `github.com/anyproto/*`:

```sh
git config --global url."git@github.com:".insteadOf "https://github.com/"
GOPRIVATE=github.com/anyproto go install github.com/anyproto/any/cmd/any@latest
```

This drops a `any` binary into `$(go env GOBIN)` (or `$GOPATH/bin`).

## Quick start

```sh
any init      # creates ~/.any/, writes wallet.key, prints mnemonic to stderr
any run       # foreground HTTP server on 127.0.0.1:7001
any status    # in another shell — GET /v1/health
any stop      # signal the server holding the account lock
```

Without an account, `any run` starts unauthorized and every data route
answers `401 auth.required` until `any auth login` (or `POST /v1/auth`)
creates or restores one.

## Why / what's this for in v1

This is a **prototype** — its first job is to let us actually use the SDK
surface end-to-end, so we can tell which parts of the API are usable,
which are painful, and what to prioritise next. It is also the home for
the initial auth flow (wallet creation).

Narrowly in v1:
- Start the server; create or restore an account over `any init` or `POST /v1/auth`.
- Cover the core SDK ops over HTTP: spaces, objects, query, modify, types,
  properties.
- CLI subcommands that call those endpoints.

Deferred (see [`docs/07-roadmap.md`](docs/07-roadmap.md)):
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
- [`docs/04-events.md`](docs/04-events.md) — subscriptions (SSE)
- [`docs/05-config.md`](docs/05-config.md) — config file and flags
- [`docs/06-errors.md`](docs/06-errors.md) — error response shape
- [`docs/07-roadmap.md`](docs/07-roadmap.md) — what's next, open questions

Module path: `github.com/anyproto/any`.
