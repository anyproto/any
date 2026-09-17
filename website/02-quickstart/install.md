---
title: Install
description: Install the Any binary, create an experimental account, and leave the local HTTP server running for the quickstart.
order: 10
---
# Install

Install `any`, create an account, and start the local HTTP server. Use one terminal for the server and another for the client examples. Install `curl` and `jq` for the checks on this page and the next example.

This is a developer preview. Use an experimental account and data directory. APIs and data formats can change; the release license is TBD.

## From a release tarball

Download the archive for your platform from the [repository's releases](https://github.com/anyproto/any/releases). Repository and release access are required.

| Artifact | Platform |
|----------|----------|
| `any-<version>-darwin-arm64.tar.gz` | macOS Apple Silicon |
| `any-<version>-darwin-x86_64.tar.gz` | macOS Intel |
| `any-<version>-linux-x86_64.tar.gz` | Linux |
| `any-<version>-windows-x86_64.tar.gz` | Windows |
| `any-<version>-darwin-{arm64,x86_64}-sandbox.tar.gz` | macOS host apps using App Sandbox ([Builds and CI](../operations/builds-and-ci.html)) |

Each archive contains the executable, a `llamacpp/` directory of shared libraries, and `manifest.json` with the version and checksums. Keep the executable beside `llamacpp/`.

For Linux, run this in the directory containing the one archive you downloaded:

```bash
mkdir -p "$HOME/.local/opt/any" "$HOME/.local/bin"
tar -xzf any-*-linux-x86_64.tar.gz -C "$HOME/.local/opt/any"
ln -s "$HOME/.local/opt/any/any" "$HOME/.local/bin/any"
export PATH="$HOME/.local/bin:$PATH"
any version
```

If the symlink already exists, check that it points to this installation. Put the PATH line in your shell profile to use `any` from future terminals. On macOS, substitute your archive's name; on Windows, add the extracted directory to PATH.

## From source

Requires Git, Go 1.26.2 or newer, and `make`. In a directory where you keep source checkouts:

```bash
git clone https://github.com/anyproto/any
cd any
make build
export PATH="$PWD/bin:$PATH"
any version
```

`make build` writes `bin/any` with full-text and vector search enabled and tries to fetch `bin/llamacpp`. If the library download fails, `make llamacpp` retries it. A bare `go build ./cmd/any` omits the search build tags.

Run the PATH line from this checkout in each terminal, or add this checkout's absolute `bin` directory to your shell profile. On systems without `libffi.so.8` on the loader path, use the repo's dev shell for both build and run: `nix develop -c make build` and `nix develop -c any run`.

## Choose the data and network

The following shell settings select a dedicated data root and local embeddings:

```bash
export ANY_DATA_DIR="$HOME/.any-demo"
export ANY_INDEX_EMBEDDER=local
```

These examples assume no other Any configuration overrides. Environment variables override the configuration file; flags override both ([Configuration](../operations/configuration.html)).

**Sync uses the production network by default.** To use a staging or local network, follow [Networks](networks.html) before starting the server. Keep a separate data root for each network.

Local embeddings compute vectors on this device. The first run downloads the model, roughly 600 MB, into the data root's `models/` directory; you can use the object API while it downloads. Set `ANY_INDEX_EMBEDDER=none` for full-text search without an embedding model. Language-model calls made by an agent follow that agent's provider settings separately.

## Create an account

In the same terminal:

```bash
any init
```

Save the recovery phrase printed to stderr. Stdout reports an `accountId` and `created: true`. The wallet is stored at `$ANY_DATA_DIR/<accountId>/wallet.key`; without a passkey its payload is plain JSON. [Account security](../operations/security-model.html) explains the storage boundary.

Running `any init` again lists the existing accounts; `--new` adds another. To restore on another device, use the phrase so it gets a fresh device identity:

```bash
any init --mnemonic-stdin < phrase.txt
```

## Start the server

Keep the same data-root and embedder settings in this terminal:

```bash
any run
```

Wait for `LISTENING 127.0.0.1:7001`. Leave the process running. In a second terminal, check the server and account:

```bash
curl -fsS http://127.0.0.1:7001/v1/health | jq .
curl -fsS http://127.0.0.1:7001/v1/auth | jq -e '.authorized == true'
```

Health reports `status: "ok"`; the account check prints `true`. `bootstrapping` in health becomes `false` after background loading finishes. If the account check is false, verify that `init` and `run` used the same data root. A fresh root starts unauthorized until an account is created or selected ([Accounts](../auth/accounts.html)).

Stop with Ctrl-C in the server terminal. To stop it from another terminal, pass the same root: `any --data-dir "$HOME/.any-demo" stop`.

## Where things live

| Path | What |
|------|------|
| `~/.any/` | Default data root. Override with `ANY_DATA_DIR` or `--data-dir`; this guide uses `~/.any-demo/`. |
| `<data-dir>/<accountId>/` | Wallet, instance lock, databases, files, and search index. |
| `<data-dir>/models/` | Downloaded embedding models. |
| `~/.config/any/config.yaml` | Optional configuration; the data root can also contain `config.yaml`. |
| `http://127.0.0.1:7001/v1` | Default HTTP API address. Set the listener with `any run --addr HOST:PORT`; only loopback IP addresses are accepted. |
| `http://127.0.0.1:7001/ui` | Debug web UI in the standalone server, on the same listener. |

Next: **[Create your first page with curl](curl.html)**.
