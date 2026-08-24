---
title: Developer workflow
description: The day-to-day loop — init an account, run the server, drive it from the CLI or curl, watch it in the web UI, and know what lives in the data dir.
order: 50
---
# Developer workflow

Development against any is one long-running server process on your machine and whatever clients you point at it. This page is the loop you will repeat: init, run, call, inspect.

## 1. Init an account

```bash
any init
```

Creates `~/.any/` (mode 0700), generates an account, writes `~/.any/<accountId>/wallet.key`, and prints the BIP-39 mnemonic to stderr **once**. Back it up. Re-running `any init` when an account exists is a no-op that lists them.

To attach a second machine to the same account, restore from the phrase — the account id is the same, the device key is fresh:

```bash
any init --mnemonic-stdin < phrase.txt      # never copy wallet.key between machines
```

## 2. Run the server

```bash
any run                                     # foreground, 127.0.0.1:7001
any run --addr 127.0.0.1:0                  # ephemeral port; prints "LISTENING <addr>" on stdout
any run --account <id>                      # pick one when the data dir holds several
```

`any run` is foreground-only: run it in a terminal, tmux, or a user service. It never creates a wallet — on a fresh data dir it starts *unauthorized* and every route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json`, `/v1/auth` answers `401 auth.required` until `POST /v1/auth` (or `any auth login`) boots an account in place ([Accounts](../auth/accounts.html)).

Stop it with Ctrl-C or:

```bash
any stop                                    # POST /v1/shutdown — drains in-flight, 10 s deadline
```

## 3. Check health

```bash
any status                                  # GET /v1/health
```

```json
{ "status": "ok", "version": "any v0.3.0 (sdk v0.2.4)",
  "startedAt": "2026-08-24T09:00:00Z",
  "account": "A8tR…", "bootstrapping": false }
```

`bootstrapping: true` means the background space-loading pass is still running; the server is serving already.

## 4. Call it

Everything is `http://127.0.0.1:7001/v1/…` with JSON bodies. Pick whichever client fits:

| Client | When |
|--------|------|
| `curl` | Exploring; the [curl quickstart](../quickstart/curl.html) creates a space, an object, and a subscription. |
| `any …` CLI | Scripts and shells — one subcommand per endpoint, pretty JSON on stdout, `jq`-friendly ([CLI quickstart](../quickstart/cli.html)). |
| Web UI | `http://127.0.0.1:7001/ui` — a debug harness with a space picker, object tree, chat, members, and a network log. Disable with `webUI.enabled: false`. |
| Your app | Any HTTP client; live updates are SSE over a streaming POST ([JavaScript](../quickstart/javascript.html), [Python](../quickstart/python.html)). |

```bash
any --verbose space get $SPACE              # log the HTTP exchange to stderr
any --addr 127.0.0.1:7002 status            # a second server on another port
```

When the server is down the CLI exits 3 with `start it with any run in another terminal`; it never auto-starts one.

## 5. Inspect

- **Sync** — `any sync-status space $SPACE`, `any sync-status subscribe`.
- **Search index** — `any search $SPACE "reranker"`; check `vectorStatus` in the reply.
- **Raw datasets** — `any query-subscribe $SPACE $OBJ --dataset chat_messages --limit 20 --sort='-_ver.id'` prints one JSON line per SSE frame.
- **Diagnostics** — `any debug space $SPACE` / `any debug object $SPACE $OBJ` dump head-sync counters and per-object tree state (unstable output, diagnostic only).
- **Logs** — one stream on stderr through the SDK logger; `--log-level debug` or `ANY_LOG_LEVEL=debug`. Add `log.addOutputPaths: ["~/.any/server.log"]` to tee to a file ([Debugging](../operations/debugging.html)).

## The data dir

```
~/.any/                         # dataDir — a ROOT that can hold several accounts
├── config.yaml                 # optional
├── models/                     # shared embedder model cache (~600 MB, downloaded once)
└── <accountId>/
    ├── wallet.key              # 0600
    ├── server.pid              # single-instance lock (stale PIDs reclaimed)
    ├── sdk/                    # any-store databases — owned by the SDK
    ├── files/                  # file bytes, one CARv2 per root CID
    └── index/                  # local search index — derived, safe to delete
```

Two servers may share a root as long as they serve different accounts on different ports. The `index/` directory is derived state: removing it is safe, but re-indexing covers content changed *after* the removal. Never delete `files/` by hand — a file that has not been backed up yet has its only copy there ([Data dir](../operations/data-dir.html)).

Config precedence is file → `ANY_*` env → flags; the file is looked up at `--config`, `$XDG_CONFIG_HOME/any/config.yaml`, `~/.config/any/config.yaml`, then `<data-dir>/config.yaml` ([Configuration](../operations/configuration.html)).

## Building from source

```bash
make build                                  # → ./any (+ bin/llamacpp for the local embedder)
go test ./...
```

Use `make build`, not a bare `go build`: the full-text and vector search legs are behind build tags that the Makefile passes, and a tag-less binary serves `/search` with zero hits and a single boot-time warning. On NixOS run builds and the binary through `nix develop -c …` so the local embedder finds `libffi` ([Builds and CI](../operations/builds-and-ci.html)).

> **Note.** An unconfigured binary syncs against the **production** network. For experiments use a staging or local nodeconf via `ANY_NETWORK_NODECONF_PATH`, or the embedded placeholder that joins no network at all ([Networks](../quickstart/networks.html)).
