---
title: Server configuration
description: Every any server config key with its env var, flag, default and precedence — data dir, listen address, network, index, files, push, logging.
order: 50
---
# Server configuration

The server reads one optional YAML file, then environment variables, then command-line flags — each layer overriding the last. A missing config file is not an error: every key has a default, and an unconfigured binary boots against the production network.

## Sources and precedence

1. **Config file** — `--config PATH`, else `$XDG_CONFIG_HOME/any/config.yaml` → `~/.config/any/config.yaml` → `<data-dir>/config.yaml`.
2. **Environment** — prefix `ANY_`, underscores map onto nested keys (`ANY_LISTEN_ADDR` → `listen.addr`).
3. **Flags** — override both.

## Server flags

```
--config <path>
--data-dir <path>
--mode <standalone|managed>  # who owns the server; fixed at launch
--account <accountId>        # selector when the root holds several accounts (standalone only)
--addr <host:port>           # must be a loopback address
--wallet <path>
--passkey-stdin              # read the wallet passkey from stdin (one line)
--log-level <debug|info|warn|error>
```

CLI-side flags (`--addr`, `--timeout`, `--verbose`, `--control-token` / `ANY_CONTROL_TOKEN`) are on the [CLI](cli.html) page.

## Core keys

| Key | Env | Default | Meaning |
|---|---|---|---|
| `dataDir` | `ANY_DATA_DIR` | `~/.any` | data ROOT; each account lives at `<root>/<accountId>/` (wallet.key or device.key, server.lock, server.pid, server.addr, sdk/, files/, index/); a root-level `wallet.key` is the default account with flat layout; `models/` is shared |
| `mode` | `ANY_MODE` | `standalone` | `standalone` (the user owns the server: keys on disk, account resolved from disk, logout and HTTP shutdown refused) \| `managed` (a host owns it: the account arrives over `POST /v1/auth` on every boot, logout, switch and `POST /v1/shutdown` behind the control token); managed refuses `account` and `auth.walletPath` |
| `account` | `ANY_ACCOUNT` | `""` | account to boot when the root holds several (standalone only); empty = the default account or the sole nested dir; ambiguous ⇒ the server starts unauthorized |
| `listen.addr` | `ANY_LISTEN_ADDR` | `127.0.0.1:7001` | loopback only — any other bind address is refused |
| `webUI.enabled` | — | `true` | serve the embedded `/ui` debug harness; embedded mobile hosts force it off |
| `auth.walletPath` | `ANY_WALLET_PATH` | `""` | explicit wallet file = manual mode, no per-account nesting |
| `auth.passkeyEnv` | — | `ANY_WALLET_PASSKEY` | name of the env var holding the wallet passkey |
| `network.nodeconfPath` | `ANY_NETWORK_NODECONF_PATH` | — | path to a nodeconf YAML; `network.nodeconf` takes it inline. Neither set ⇒ the embedded **production** nodeconf |
| `storage.topology` | — | `shared` | `shared` or `per-space` |
| `sync.dialTimeout` | — | `10s` | |
| `sync.changeBatchSize` | — | `100` | |
| `p2p.enabled` | — | `true` | local-network (mDNS + QUIC) discovery and sync between the account's devices |
| `p2p.port` | — | `0` | QUIC listen port; 0 = reuse the persisted port or pick an ephemeral one |
| `p2p.serviceName` | — | `""` | mDNS service type; empty = `_any._tcp` |
| `local.enabled` | `ANY_LOCAL_ENABLED` | `true` | serve the device-local store at `/v1/local`; `false` answers `409 local.disabled` and leaves existing storage collections on disk |
| `access.redeemUrl` | `ANY_ACCESS_REDEEM_URL` | `""` | base URL of the invite service `POST /v1/account/access-code` relays to; empty answers `409 access.disabled` |
| `log.defaultLevel` | `ANY_LOG_LEVEL` | `info` | |
| `log.production` | — | `false` | |
| `log.format` | — | `0` | an integer: `0` colorized \| `1` plaintext \| `2` json; a string here fails config parsing |
| `log.outputPaths` | — | `[]` | extra log files, absolute paths (`~` is not expanded), e.g. `["/var/log/any/server.log"]` |
| `log.disableStdErr` | — | `false` | stop logging to stderr (with `outputPaths` set) |
| `log.levels` | — | `[]` | per-logger overrides, `[{name, level}]`; first match wins |

The passkey is the one secret the server may need at boot: it comes from the env var named by `auth.passkeyEnv`, or from stdin with `--passkey-stdin`. There is no interactive prompt.

> **Why it matters.** Nothing here points at a hosted backend. `dataDir` is the whole database — copy it and you have moved your data; the network config only names the sync nodes that relay ciphertext between your devices. See [Networks](../operations/networks.html) for staging and self-hosted nodeconfs, [Data directory](../operations/data-dir.html) for what the root holds.

## Search index (`index.*`)

| Key | Env | Default | Meaning |
|---|---|---|---|
| `index.enabled` | `ANY_INDEX_ENABLED` | `true` | `false` disables the indexer and `/search` (`409 index.disabled`) |
| `index.embedder` | `ANY_INDEX_EMBEDDER` | `auto` | `auto` (local alone without `index.openai.apiKey`; with a key, online primary + local fallback, same model, and indexed text and queries go to `index.openai.baseUrl`) \| `local` (on-device) \| `ollama` \| `openai` \| `none` (FTS-only) |
| `index.embedBatch` | `ANY_INDEX_EMBED_BATCH` | `64` | docs per embed request |
| `index.embedConcurrency` | `ANY_INDEX_EMBED_CONCURRENCY` | `0` | parallel batches; 0 = 1 for local, 4 for online |
| `index.ollama.url` | `ANY_INDEX_OLLAMA_URL` | `http://localhost:11434` | |
| `index.ollama.model` | `ANY_INDEX_OLLAMA_MODEL` | `embeddinggemma` | |
| `index.openai.baseUrl` | `ANY_INDEX_OPENAI_BASE_URL` | `https://api.deepinfra.com/v1/openai` | OpenAI-compatible `/embeddings` host |
| `index.openai.model` | `ANY_INDEX_OPENAI_MODEL` | `Qwen/Qwen3-Embedding-0.6B` | for `auto` must equal the local model |
| `index.openai.apiKey` | `ANY_INDEX_OPENAI_API_KEY` | empty | turns the `auto` primary on; sent as Bearer; never logged |
| `index.local.modelPath` | `ANY_INDEX_LOCAL_MODEL_PATH` | `""` | existing GGUF; set ⇒ no download (air-gapped) |
| `index.local.modelUrl` | `ANY_INDEX_LOCAL_MODEL_URL` | `""` | download-source override |
| `index.local.modelSha256` | `ANY_INDEX_LOCAL_MODEL_SHA256` | `""` | checksum override |
| `index.local.libDir` | `ANY_INDEX_LOCAL_LIB_DIR` | `<exe-dir>/llamacpp` | llama.cpp shared libs |
| `index.local.contextSize` | `ANY_INDEX_LOCAL_CONTEXT_SIZE` | `2048` | truncation bound in tokens |
| `index.local.queryPrefix` | `ANY_INDEX_LOCAL_QUERY_PREFIX` | `""` | empty = the Qwen retrieval instruction |
| `index.local.dim` | `ANY_INDEX_LOCAL_DIM` | `0` | Matryoshka truncation; 0 = model dim (1024) |
| `index.local.threads` | `ANY_INDEX_LOCAL_THREADS` | `0` | CPU budget of the embedder child; 0 = `NumCPU()-1` |
| `index.local.niceness` | `ANY_INDEX_LOCAL_NICENESS` | `10` | scheduling priority of the embedder child (0–19); 0 = the server's own priority |
| `index.local.requestTimeout` | `ANY_INDEX_LOCAL_REQUEST_TIMEOUT` | `3m` | bound on one frame to the embedder child; unwedges a hung GPU |
| `index.local.gpuLayers` | `ANY_INDEX_LOCAL_GPU_LAYERS` | absent | absent = offload all when a GPU backend is usable; `0` forces CPU |
| `index.local.batchDocs` | `ANY_INDEX_LOCAL_BATCH_DOCS` | `1` | docs packed per decode; N caps each text at `contextSize`/N tokens |
| `index.vector.dim` | `ANY_INDEX_VECTOR_DIM` | `0` | 0 = learned from the first embedding, then pinned |
| `index.vector.mode` | `ANY_INDEX_VECTOR_MODE` | `ivfsq` | `ivfsq` \| `btree` \| `hnsw` \| `hybrid` \| `bruteforce` |
| `index.search.stopWords` | `ANY_INDEX_SEARCH_STOP_WORDS` | `true` | strip stop words from the FTS leg |
| `index.search.ftsWeight` | `ANY_INDEX_SEARCH_FTS_WEIGHT` | `1` | RRF weight, lexical leg |
| `index.search.vectorWeight` | `ANY_INDEX_SEARCH_VECTOR_WEIGHT` | `1` | RRF weight, dense leg |
| `index.search.adaptiveWeights` | `ANY_INDEX_SEARCH_ADAPTIVE_WEIGHTS` | `false` | auto-down-weight flat FTS scores |
| `index.search.defaultOperator` | `ANY_INDEX_SEARCH_DEFAULT_OPERATOR` | `or` | `or` \| `and` for bare FTS terms |
| `index.search.minVectorSim` | `ANY_INDEX_SEARCH_MIN_VECTOR_SIM` | `0` | cosine floor; 0 = `> 0` |
| `index.search.bm25B` / `bm25K1` | `ANY_INDEX_SEARCH_BM25_B` / `_K1` | `0` | 0 = engine defaults 0.75 / 1.2 |
| `index.search.titleWeight` | `ANY_INDEX_SEARCH_TITLE_WEIGHT` | `0` | BM25F title boost; 0 = no boost and no title field in the index (0 → non-zero needs a rebuild) |
| `index.search.queryEmbedTimeout` | `ANY_INDEX_SEARCH_QUERY_EMBED_TIMEOUT` | `5s` | bound on embedding one search query; past it hybrid answers lexical-only and `mode: vector` answers `503 index.embedder_unavailable` |

An unavailable embedder never breaks boot or FTS — the vector side reports `unavailable` until it recovers. The local model (639 MB) downloads on first boot into `<root>/models/`, resumable, without blocking. Details: [Embedders](../search/embedders.html).

## Files and push

| Key | Env | Default | Meaning |
|---|---|---|---|
| `files.publicReadBaseUrl` | `ANY_FILES_PUBLIC_READ_BASE_URL` | `""` | override the network-advertised public read base; empty = resolved from the network's fileV2 nodes |
| `files.gcInterval` | `ANY_FILES_GC_INTERVAL` | `""` | cadence of the file-cache safety sweep (e.g. `1h`); empty = no background sweep |
| `push.enabled` | `ANY_PUSH_ENABLED` | `null` | tristate: unset = enabled iff a peer is configured; `false` disables |
| `push.peerId` | `ANY_PUSH_PEER_ID` | `""` | the push node's peer id |
| `push.addrs` | `ANY_PUSH_ADDRS` | `[]` | dial addresses (env: comma-separated) |

The production push node is the packaged default only when the network is too — a server pointed at another nodeconf never pushes through production unless told to.

## Example file

```yaml
dataDir: ~/.any
listen:
  addr: 127.0.0.1:7001
network:
  nodeconfPath: /etc/any/nodeconf.yaml
index:
  embedder: local
  search:
    titleWeight: 2
files:
  gcInterval: 1h
log:
  defaultLevel: info
  format: 2                      # json
  outputPaths: ["/var/log/any/server.log"]
```

```bash
ANY_DATA_DIR=/var/lib/any ANY_LISTEN_ADDR=127.0.0.1:7002 any run
```

## First run

With no config and no data dir, `any init` creates `~/.any/` (mode 0700), generates an account at `~/.any/<accountId>/wallet.key` (plain unless `ANY_WALLET_PASSKEY` is set) and prints the mnemonic to stderr once. `any run` never creates wallets: on a fresh root it starts unauthorized and waits for `POST /v1/auth`. A managed server always starts that way — its host supplies the account on every boot. The full layout is on [Data dir](../operations/data-dir.html).
