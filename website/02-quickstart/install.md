---
title: Install
description: Get the any binary from a release tarball or build it from source, create your first account, and start the server.
order: 10
---
# Install

any is a single binary. It runs the server (`any run`) and is the CLI client for everything else.

## From a release tarball

Releases ship one tarball per platform:

| Artifact | Platform |
|----------|----------|
| `any-<version>-darwin-arm64.tar.gz` | macOS Apple Silicon |
| `any-<version>-darwin-x64.tar.gz` | macOS Intel |
| `any-<version>-linux-x86_64.tar.gz` | Linux |
| `any-<version>-windows-x86_64.tar.gz` | Windows |
| `any-<version>-darwin-{arm64,x64}-sandbox.tar.gz` | macOS, for App-Sandboxed host apps ([Builds and CI](../operations/builds-and-ci.html)) |

Each tarball contains:

```
any[.exe]        # the server + CLI (built with the full-text and vector search legs)
llamacpp/        # prebuilt llama.cpp shared libs for the in-process embedder
manifest.json    # { version, os, arch, llamacpp_version, sha256: {path: hash} }
```

Extract it somewhere on your `PATH`, keeping `llamacpp/` next to the binary — the local embedder looks for it at `<dir-of-any>/llamacpp` by default. The embedding model itself (~600 MB) is not bundled; it downloads on first boot into `<data-dir>/models/` without blocking the server.

```bash
tar -xzf any-*-linux-x86_64.tar.gz -C ~/.local/opt/any
ln -s ~/.local/opt/any/any ~/.local/bin/any
any version
```

## From source

Requires Go 1.26 and `make`.

```bash
git clone https://github.com/anyproto/any
cd any
make build              # → ./any  (also fetches bin/llamacpp)
./any version
```

Use `make build`, not `go build ./cmd/any`: the search index is behind build tags that the Makefile passes. A tag-less binary serves `/search` with zero hits and warns only once at boot. On NixOS (or any system without `libffi.so.8` on the loader path) run both the build and the binary through the repo's dev shell: `nix develop -c make build`, `nix develop -c ./any run`.

## Create an account

```bash
any init
```

```
account A8tR…  created at ~/.any/A8tR…/wallet.key
mnemonic (write it down, it is shown once):
  word1 word2 … word12
```

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

Leave it in the foreground; it stops on Ctrl-C or `any stop`. In a second terminal:

```bash
any status
```

```json
{ "status": "ok", "version": "any v0.3.0 (sdk v0.2.4)", "account": "A8tR…", "bootstrapping": false }
```

The server serves as soon as it prints `LISTENING`; `bootstrapping` flips to `false` once the background space-loading pass finishes.

## Where things live

| Path | What |
|------|------|
| `~/.any/` | Data root (`--data-dir` / `ANY_DATA_DIR`). |
| `~/.any/<accountId>/` | This account's wallet, instance lock, databases, files, search index. |
| `~/.config/any/config.yaml` | Optional config; also `<data-dir>/config.yaml` ([Configuration](../operations/configuration.html)). |
| `http://127.0.0.1:7001` | The API (`--addr` / `ANY_LISTEN_ADDR`; loopback only). |
| `http://127.0.0.1:7001/ui` | Built-in debug web UI. |

> **Note.** With nothing configured the server syncs against the production any-sync network. For a throwaway environment set `ANY_NETWORK_NODECONF_PATH` before `any run` — see [Networks](networks.html).

Next: [curl](curl.html) or [CLI](cli.html).
