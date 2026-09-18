---
title: Configuration
description: Config file, environment variables and flags — their precedence, and a reference of every key.
order: 20
---
# Configuration

Configuration comes from three layers, each overriding the last: a YAML file, `ANY_*` environment variables, then command-line flags. A missing file is not an error — every key has a default, and a bare `any run` works (with several accounts in the root and no selector it starts unauthorized and waits for `POST /v1/auth`). This page is the `any` server's; the runtime has its own [anybao.toml](../reference/anybao-toml.html).

## Local embeddings

The one setting most installs change: the default `index.embedder: auto` embeds through the online primary and falls back to the local model, `local` keeps every embedding on the device. Save this as `any-config.yml`:

```yaml
index:
  embedder: local
```

```bash
any run --config ./any-config.yml
```

The GGUF downloads on first boot if it is not in the root's model cache; full-text search works while it downloads and vector search reports `unavailable` until the model and the llama.cpp libraries are in place ([Embedders](../search/embedders.html) — model, platforms, other providers). The key governs search embeddings only: the sync network is [nodeconf](networks.html), and an agent's models are `anyrt`'s.

## Sources and precedence

1. **File** — `--config PATH`, else the first of `$XDG_CONFIG_HOME/any/config.yaml`, `~/.config/any/config.yaml`, `<data-dir>/config.yaml`.
2. **Environment** — prefix `ANY_`, underscores map to nested keys (`ANY_LISTEN_ADDR` → `listen.addr`).
3. **Flags** — override both.

```bash
ANY_LOG_LEVEL=debug any run --config ./any-config.yml --addr 127.0.0.1:7002
```

## Server flags

| Flag | Purpose |
|---|---|
| `--config <path>` | config file |
| `--data-dir <path>` | data root (default `~/.any`) |
| `--mode <standalone\|managed>` | who owns the server — fixed at launch ([Server](server.html)) |
| `--account <accountId>` | which account to boot when the root holds several (standalone only) |
| `--addr <host:port>` | listen address; must be loopback |
| `--wallet <path>` | explicit wallet file (manual wallet selection) |
| `--passkey-stdin` | read the wallet passkey from stdin (one line) |
| `--log-level <debug\|info\|warn\|error>` | log verbosity |

CLI (client) flags: `--addr` (default: the address the running server recorded in `server.addr`, else `127.0.0.1:7001`), `--timeout` (default 30s), `--verbose` (log the HTTP exchange to stderr), `--control-token` / `ANY_CONTROL_TOKEN` (a managed server's control token).

## Core keys

| Key | Env | Default | Meaning |
|---|---|---|---|
| `dataDir` | `ANY_DATA_DIR` | `~/.any` | the multi-account root ([Data directory](data-dir.html)) |
| `mode` | `ANY_MODE` | `standalone` | `standalone` or `managed`; managed refuses `account` and `auth.walletPath` |
| `account` | `ANY_ACCOUNT` | "" | account selector (standalone only); empty = the default account |
| `listen.addr` | `ANY_LISTEN_ADDR` | `127.0.0.1:7001` | loopback only |
| `webUI.enabled` | — | true | serve the embedded debug UI at `/ui`; app-embedded boots force it off |
| `auth.walletPath` | `ANY_WALLET_PATH` | "" | explicit wallet = manual mode, no per-account nesting |
| `auth.passkeyEnv` | — | `ANY_WALLET_PASSKEY` | name of the env var holding the wallet passkey |
| `network.nodeconfPath` / `network.nodeconf` | `ANY_NETWORK_NODECONF_PATH` | embedded production conf | which any-sync network to join ([Networks](networks.html)) |
| `storage.topology` | — | `shared` | `shared` or `per-space` |
| `sync.dialTimeout` / `sync.changeBatchSize` | — | 10s / 100 | sync tuning |
| `log.defaultLevel` | `ANY_LOG_LEVEL` | `info` | also `log.production`, `log.format` (`0` colorized \| `1` plaintext \| `2` json), `log.outputPaths` (extra absolute file paths), `log.disableStdErr` |
| `local.enabled` | `ANY_LOCAL_ENABLED` | true | serve the device-local store at `/v1/local`; false answers `409 local.disabled` |
| `access.redeemUrl` | `ANY_ACCESS_REDEEM_URL` | "" | invite service behind `POST /v1/account/access-code`; "" answers `409 access.disabled` |

The passkey is the one secret the server may need at boot. It arrives from the env var named by `auth.passkeyEnv` or from `--passkey-stdin` — never an interactive prompt, so the server runs under a supervisor or a shell pipeline. Without either the wallet must be plain.

## Local network (p2p)

| Key | Default | Meaning |
|---|---|---|
| `p2p.enabled` | true | devices of the same account on one LAN discover each other over mDNS and sync directly, even with sync nodes unreachable |
| `p2p.port` | 0 | QUIC port; 0 = reuse last run's port or pick an ephemeral one |
| `p2p.serviceName` | "" | mDNS service type; "" = `_any._tcp`; override to isolate a deployment |

## Search index

| Key | Env | Default |
|---|---|---|
| `index.enabled` | `ANY_INDEX_ENABLED` | true — false disables the indexer and `/search` (409 `index.disabled`) |
| `index.embedder` | `ANY_INDEX_EMBEDDER` | `auto` (`local` \| `ollama` \| `openai` \| `none`) |
| `index.embedBatch` | `ANY_INDEX_EMBED_BATCH` | 64 |
| `index.embedConcurrency` | `ANY_INDEX_EMBED_CONCURRENCY` | 0 = 1 local, 4 online |
| `index.ollama.{url,model}` | `ANY_INDEX_OLLAMA_*` | `http://localhost:11434`, `embeddinggemma` |
| `index.openai.{baseUrl,model,apiKey}` | `ANY_INDEX_OPENAI_*` | DeepInfra URL + Qwen3-Embedding-0.6B, no key; `apiKey` turns the `auto` primary on |
| `index.local.{modelPath,modelUrl,modelSha256,libDir,contextSize,queryPrefix,dim,threads,niceness,requestTimeout,gpuLayers,batchDocs}` | `ANY_INDEX_LOCAL_*` | see [Embedders](../search/embedders.html) |
| `index.vector.dim` | `ANY_INDEX_VECTOR_DIM` | 0 = learned from the first embedding |
| `index.vector.mode` | `ANY_INDEX_VECTOR_MODE` | `ivfsq` (`btree` \| `hnsw` \| `hybrid` \| `bruteforce`) |
| `index.search.{stopWords,ftsWeight,vectorWeight,adaptiveWeights,defaultOperator,minVectorSim,bm25B,bm25K1,titleWeight,queryEmbedTimeout}` | `ANY_INDEX_SEARCH_*` | true, 1, 1, false, `or`, 0, 0, 0, 0, `5s` — see [Hybrid ranking](../search/hybrid.html) |

## Files and push

| Key | Env | Default | Meaning |
|---|---|---|---|
| `files.publicReadBaseUrl` | `ANY_FILES_PUBLIC_READ_BASE_URL` | "" | override the network-advertised public read base; "" = resolved from the network's file nodes |
| `files.gcInterval` | `ANY_FILES_GC_INTERVAL` | "" | periodic file-cache sweep cadence (e.g. `1h`); "" = no background sweep |
| `push.enabled` | `ANY_PUSH_ENABLED` | null | tristate: null = enabled iff a peer is configured; false disables |
| `push.peerId` / `push.addrs` | `ANY_PUSH_PEER_ID` / `ANY_PUSH_ADDRS` | production node when the network is production | the push node, a direct out-of-band peer ([Push](../notifications/push.html)) |

## A complete example

A staging nodeconf, local embeddings, periodic file-cache cleanup and a log file (paths are yours to replace):

```yaml
dataDir: ~/.any
listen:
  addr: 127.0.0.1:7001
network:
  nodeconfPath: /etc/any/nodeconf.yaml     # omit to join production
index:
  embedder: local
  search:
    adaptiveWeights: true
files:
  gcInterval: 1h
log:
  defaultLevel: info
  outputPaths: ["/var/log/any/server.log"]
```

> **Note.** With nothing configured the binary joins the **production** any-sync network from an embedded node configuration. Tests and staging setups must point `network.nodeconfPath` (or `ANY_NETWORK_NODECONF_PATH`) elsewhere — see [Networks](networks.html).

## First run

`any init` with no config and no data dir creates `~/.any/` (mode 0700), generates an account under `~/.any/<accountId>/wallet.key` (plain unless `ANY_WALLET_PASSKEY` is set) and prints the mnemonic to stderr once. `--mnemonic` / `--mnemonic-stdin` derive an existing account instead — same phrase, same account id, fresh device key. `any run` on a fresh root starts unauthorized and waits for `POST /v1/auth`, the HTTP flavour of init for UI onboarding; a managed server starts that way on every launch ([Accounts](../auth/accounts.html)).

The full key list with every environment variable is also in the [config reference](../reference/config.html).
