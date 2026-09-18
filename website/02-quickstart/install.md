---
title: Install
description: Get the `any` binary from a release tarball or build it from source, pick a data root and network, create your first account, and start the server.
order: 10
---
# Install

`any` is a single binary. It runs the server (`any run`) and is the CLI client for everything else. The checks on this page and the quickstarts after it use `curl` and `jq`.

## From a release tarball

[Releases](https://github.com/anyproto/any/releases) ship one tarball per platform:

| Artifact | Platform |
|----------|----------|
| `any-<version>-darwin-arm64.tar.gz` | macOS Apple Silicon |
| `any-<version>-darwin-x86_64.tar.gz` | macOS Intel |
| `any-<version>-linux-x86_64.tar.gz` | Linux |
| `any-<version>-windows-x86_64.tar.gz` | Windows |
| `any-<version>-darwin-{arm64,x86_64}-sandbox.tar.gz` | macOS, for App-Sandboxed host apps ([Builds and CI](../operations/builds-and-ci.html)) |

Each tarball contains:

```
any[.exe]        # the server + CLI (built with the full-text and vector search legs)
llamacpp/        # prebuilt llama.cpp shared libs for the local embedder
manifest.json    # { version, os, arch, llamacpp_version, sha256: {path: hash} }
```

Extract it somewhere on your `PATH`, keeping `llamacpp/` next to the binary — the local embedder looks for it at `<dir-of-any>/llamacpp` by default. The embedding model itself (~600 MB) is not bundled; it downloads on first boot into `<data-dir>/models/` without blocking the server.

```bash
mkdir -p "$HOME/.local/opt/any" "$HOME/.local/bin"
tar -xzf any-*-linux-x86_64.tar.gz -C "$HOME/.local/opt/any"
ln -s "$HOME/.local/opt/any/any" "$HOME/.local/bin/any"
export PATH="$HOME/.local/bin:$PATH"      # put this in your shell profile too
any version
```

On macOS substitute your tarball's name; on Windows add the extracted directory to `PATH`.

## From source

Requires Git, Go 1.26.2 or newer, and `make`.

```bash
git clone https://github.com/anyproto/any
cd any
make build              # → bin/any  (also fetches bin/llamacpp)
export PATH="$PWD/bin:$PATH"
any version
```

Run the `PATH` line in every terminal you use below, or put the repo's `bin` on `PATH` in your shell profile: the `any` commands then run the binary you just built, with `bin/llamacpp` next to it where the local embedder looks. Otherwise a bare `any` fails or runs an older install. If the llama.cpp download fails, `make llamacpp` retries it.

Use `make build`, not `go build ./cmd/any`: the search index is behind build tags that the Makefile passes. A tag-less binary serves `/search` with zero hits and warns only once at boot. On NixOS (or any system without `libffi.so.8` on the loader path) run both the build and the binary through the repo's dev shell: `nix develop -c make build`, `nix develop -c any run`.

## Choose the data root and the network

Everything below runs in a dedicated data root with the local embedder, so an experiment never touches `~/.any/` and never sends text to an online embedding provider:

```bash
export ANY_DATA_DIR="$HOME/.any-demo"
export ANY_INDEX_EMBEDDER=local
```

Environment variables override the config file and flags override both ([Configuration](../operations/configuration.html)). `local` runs llama.cpp on this device; the ~600 MB model downloads into `$ANY_DATA_DIR/models/` on first boot and the object API serves while it does. `none` keeps full-text search and skips the model. An agent's LLM provider is a separate setting on the `anyrt` side ([Embedders](../search/embedders.html)).

> **Note.** With nothing configured the server syncs against the **production** any-sync network under the account you are about to create. For a throwaway environment set `ANY_NETWORK_NODECONF_PATH` before `any init`, and keep one data root per network — see [Networks](networks.html).

## Create an account

Same terminal, same exports:

```bash
any init
```

The recovery phrase prints to stderr, once, between two rules; stdout carries the result:

```json
{
  "accountId": "A8tR…",
  "created": true
}
```

The wallet lands at `$ANY_DATA_DIR/<accountId>/wallet.key` — plain JSON unless `ANY_WALLET_PASSKEY` is set ([Security model](../operations/security-model.html)). A second `any init` on the same data dir changes nothing and lists the accounts it holds (`--new` adds another).

The mnemonic is the only way to restore the account on another device; there is no server-side recovery ([Encryption](../understanding/encryption.html)). To add a second device later:

```bash
any init --mnemonic-stdin < phrase.txt    # same account id, fresh device key
```

## Start the server

```bash
any run
```

```
LISTENING 127.0.0.1:7001
```

Leave it in the foreground; it stops on Ctrl-C, or on `any stop --data-dir "$HOME/.any-demo"` from a terminal without the exports. In a second terminal:

```bash
any status
curl -fsS http://127.0.0.1:7001/v1/auth | jq -e '.authorized == true'
```

```json
{
  "status": "ok",
  "version": "any v0.1.2 (commit 1a2b3c4, built 2026-09-09T10:00:00Z)",
  "startedAt": "2026-09-10T08:00:00Z",
  "networkId": "N83gJpVd9MuNRZAuJLZ7LiMntTThhPc6DtzWWVjb1M3PouVU",
  "account": "A8tR…",
  "bootstrapping": false,
  "crdtVersion": { "supported": 2, "stored": 2, "newer": false }
}
```

The server serves as soon as it prints `LISTENING`; `bootstrapping` flips to `false` once the background space-loading pass finishes. The auth check prints `true`; `false` means `init` and `run` saw different data roots — a fresh root boots unauthorized until an account is created or selected ([Accounts](../auth/accounts.html)).

## Where things live

| Path | What |
|------|------|
| `~/.any/` | Default data root (`--data-dir` / `ANY_DATA_DIR`); this page uses `~/.any-demo/`. |
| `<data-dir>/<accountId>/` | This account's wallet, instance lock, databases, files, search index. |
| `<data-dir>/models/` | Downloaded embedding models. |
| `~/.config/any/config.yaml` | Optional config; also `<data-dir>/config.yaml` ([Configuration](../operations/configuration.html)). |
| `http://127.0.0.1:7001` | The API (`--addr` / `ANY_LISTEN_ADDR`; loopback only). |
| `http://127.0.0.1:7001/ui` | Built-in debug web UI. |

Next: [curl](curl.html) or [CLI](cli.html).
