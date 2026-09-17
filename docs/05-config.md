# Config

## Sources, precedence

1. Config file (YAML), loaded from `--config PATH` or the first that
   exists of `$XDG_CONFIG_HOME/any/config.yaml` →
   `~/.config/any/config.yaml` → `<data-dir>/config.yaml`.
2. Environment variables — override file values.
3. Command-line flags — override both.

A missing config file is not an error: defaults are used.

## File shape

```yaml
# Data ROOT. Each account lives in <dataDir>/<accountId>/ (wallet.key,
# server.lock, server.pid, sdk/, index/); a wallet.key directly at the root is the
# legacy flat layout and acts as the default account with its data at
# the root. config.yaml and the shared models/ cache sit at the root.
# Layout details: 02-server.md § Data dir layout.
dataDir: ~/.any

# Who owns this server (02-server.md § Modes). standalone (default):
# the user — keys in wallet.key, account resolved from disk, logout and
# HTTP shutdown refused. managed: the spawning host — the account key
# arrives over POST /v1/auth on every boot and never touches disk,
# DELETE /v1/auth / account switch / POST /v1/shutdown are allowed
# behind the control token the host holds (printed as the second stdout
# handshake line, or supplied in-process). Fixed at launch, unreachable
# over HTTP. A managed server refuses `account` and `auth.walletPath`.
mode: standalone

# Account to boot when the root holds more than one (standalone only).
# Empty = the default (root wallet.key, or the sole per-account dir);
# with several accounts and no selector the server starts unauthorized
# and waits for POST /v1/auth.
account: ""

# HTTP server listen address. Loopback IP only.
listen:
  addr: 127.0.0.1:7001

# Embedded /ui debug harness (internal/server/web.go). Default on.
# embedded.Start (the in-process host behind the mobile bindings) forces
# it off, so an in-process server is headless — /ui 404s and the
# "web ui" boot log line is silent. A subprocess host that wants the
# same writes `webUI.enabled: false` into its config.yaml.
webUI:
  enabled: true

# Auth — wallet location, passkey env var name.
auth:
  walletPath: ""                      # explicit wallet file = manual mode
                                      # (no per-account nesting; generated
                                      # when missing); default: resolved
                                      # per account
  passkeyEnv: ANY_WALLET_PASSKEY      # env var name to read the passkey from

# any-sync network. Precedence: inline nodeconf → nodeconfPath → the
# EMBEDDED default (internal/config/nodeconf-prod.yml), which is the
# PRODUCTION network — an unconfigured binary syncs against production
# from any working directory. Point at another network with
# nodeconfPath, inline nodeconf, or ANY_NETWORK_NODECONF_PATH.
# The conf is read once at startup (a change applies on restart); a conf
# that isn't YAML or names no networkId fails startup, the rest of it is
# checked when the SDK opens. GET /v1/health reports it as `networkId`.
#
# The network is a PROCESS setting: every account the server boots joins
# it. Each account dir is pinned to the network its data was written on
# (network.json, 02-server.md § Startup) and refuses to boot under
# another one — keep one data root per network.
#
# internal/config/nodeconf-placeholder.yml is a sanitized test fixture
# (Anytype stage networkId, placeholder nodes; config.NodeconfPlaceholder()):
# it boots and serves but joins no network. Nothing selects it at runtime.
#
# The embedded servers (any.aar / xcframework / embedded.Start) follow the
# same rule: an empty nodeconfYAML selects the production default;
# non-empty YAML overrides it.
#
# Files: durable file backup needs nodes typed `fileV2` in the nodeconf.
# Without them attach still works offline-first — files sit in the
# `inflight` durability state until such nodes appear
# (docs/17-files.md § Durability states).
network:
  nodeconfPath: /etc/any/nodeconf.yaml
  # OR:
  # nodeconf: |
  #   ...

# Storage topology: shared (default) | per-space.
storage:
  topology: shared

# Sync tuning (optional).
sync:
  dialTimeout: 10s                    # peer dial timeout; empty = 10s

# Local-network (p2p) discovery + sync. Devices of the same account on
# the same LAN discover each other over mDNS and sync shared spaces
# directly — including while the sync nodes are unreachable (offline
# LAN sync and account cold-restore from a nearby device). Surfaced in
# `any sync-status` (p2p / localPeers fields) and `any debug p2p`.
p2p:
  enabled: true                       # opt-out; absent/true = on. false =
                                      #   no listener, no discovery.
  port: 0                             # QUIC listen port. 0 = reuse the port
                                      #   persisted from the previous run, or
                                      #   pick an ephemeral one on first start.
  serviceName: ""                     # mDNS service type; empty = "_any._tcp".
                                      #   Override to isolate a deployment onto
                                      #   its own discovery namespace.

# Local search index (docs/13-index.md). FTS needs no external
# dependency; vector search activates when an embedder is configured.
index:
  enabled: true                       # default true; false disables the indexer + /search
  embedder: auto                      # auto (DEFAULT — online openai primary + local
                                      #   fallback, SAME model both sides) | local |
                                      # ollama | openai | none (FTS-only)
  embedBatch: 64                      # docs per embed request (0 = default 64)
  embedConcurrency: 0                 # batches embedded in parallel; 0 = 1 for local,
                                      # 4 for online openai/auto (parallel = the API win)
  ollama:
    url: http://localhost:11434       # default
    model: embeddinggemma             # default
  openai:                             # online primary for embedder: openai/auto.
                                      # The defaults carry shared DeepInfra dev
                                      # credentials (Qwen3-Embedding-0.6B) so `auto`
                                      # works with no config.
    baseUrl: https://api.deepinfra.com/v1/openai
    model: Qwen/Qwen3-Embedding-0.6B  # for auto, MUST equal the local fallback model
    apiKey: ...                       # sent as Bearer; never logged
  local:                              # llama.cpp in a child process — all fields optional;
                                      # the default embedder needs no config at all
    modelPath: ""                     # existing GGUF; set ⇒ no download (air-gapped)
    modelUrl: ""                      # download-source override for the default path
    modelSha256: ""                   # checksum override; "" with modelUrl ⇒ skip verify
    libDir: ""                        # llama.cpp shared libs; default $YZMA_LIB, else
                                      # <exe-dir>/llamacpp
    contextSize: 2048                 # truncation bound in tokens (docs/13-index.md)
    queryPrefix: ""                   # "" = Qwen retrieval instruction for the default model
    dim: 0                            # Matryoshka output truncation; 0 = model dim (1024)
    threads: 0                        # CPU budget of the embedder child process; 0 = runtime.NumCPU()-1
                                      # (leave one free). Lower it to keep background indexing off the
                                      # user's cores. Read at boot — it has no HTTP or CLI setter
                                      # (docs/13-index.md § The embedder child process)
    requestTimeout: 3m                # bound on one frame to the child (one decode group of batchDocs
                                      # texts); a hung GPU stops answering rather than failing, and this
                                      # is what unwedges it
    niceness: 10                      # scheduling priority of the embedder child (0-19, higher = more
                                      # background); 0 = leave at the server's priority. Unix nices the
                                      # child, Windows drops its priority class
    gpuLayers: ~                      # n_gpu_layers override; absent = offload all when a GPU
                                      # backend is usable (Metal/Vulkan ship in the default
                                      # bundles, CPU fallback automatic); 0 = force CPU —
                                      # weights AND compute (it also disables llama.cpp's
                                      # op_offload) (docs/13-index.md § GPU offload)
    batchDocs: 1                      # docs per llama_decode; N caps each text at contextSize/N tokens;
                                      # 1 = sequential
  vector:
    dim: 0                            # 0 = learned from the first successful embedding
    mode: ivfsq                       # ANN index: ivfsq (default — cheap ingest, churn-
                                      # friendly, ~3-4 recall@10 below exact) | btree/hnsw
                                      # (recall≈exact, costly serial ingest) | hybrid
                                      # (HNSW + RAM cache) | bruteforce/exact
  search:                             # hybrid-ranking knobs (docs/13-index.md § Search)
    stopWords: true                   # strip stop words from the FTS-leg query (default on)
    ftsWeight: 1                      # RRF weight for the lexical leg (default 1)
    vectorWeight: 1                   # RRF weight for the dense leg (default 1)
    adaptiveWeights: false            # auto-down-weight FTS per query when its scores are
                                      # flat/weak; helps paraphrastic corpora, small cost on
                                      # lexical-friendly ones — off by default
    defaultOperator: or               # how bare FTS terms combine: or (default) | and. AND
                                      # requires ALL terms — precise, but tanks recall on
                                      # natural-language queries; use only for keyword input
    queryEmbedTimeout: 5s             # bound on embedding one /search query (every embedder);
                                      # past it hybrid answers lexical-only (vectorStatus
                                      # unavailable) and mode=vector 503s — raise for a slow
                                      # remote embedder
    minVectorSim: 0                   # cosine floor for vector hits; 0 = keep hits with
                                      # similarity > 0
    bm25B: 0                          # FTS BM25 length-norm; 0 = engine default 0.75
    bm25K1: 0                         # FTS BM25 tf-saturation; 0 = engine default 1.2
    titleWeight: 0                    # BM25F boost for the title field; 0 = no boost and no
                                      # title field in the index (0 -> non-zero needs a rebuild)

# Files byte layer (files v2, docs/17-files.md). All optional — an
# absent block changes nothing.
files:
  publicReadBaseUrl: ""               # override the network-advertised public read base
                                      # ({base}/blob/{spaceId}/{rootCid}); "" = resolved
                                      # once from the network's fileV2 nodes and cached.
                                      # Set only for private deployments fronting the
                                      # object store themselves
  gcInterval: ""                      # enable the periodic file-cache safety sweep at
                                      # this cadence (e.g. "1h"). "" (default) = NO
                                      # background sweep — reclamation stays caller-driven
                                      # via /v1/files/cache/* and per-file offload

# Local store (docs/26-local-store.md): device-local, non-CRDT
# collections in the SDK's sdk.db under the "l_" tag, served at
# /v1/local. Off = every /v1/local route answers 409 local.disabled and
# nothing is created; existing local collections stay on disk.
local:
  enabled: true                       # default true

# Push-notification node (docs/20-push.md). A DIRECT out-of-band peer
# ({peerId, addrs} here, not in the nodeconf) that fans mobile push
# notifications out by topic. When the config names neither peerId nor
# addrs AND no nodeconf (the embedded production network), the
# production push node is filled in; a config that names a nodeconf
# gets a push node only by naming one. Without a peerId and addrs every
# /v1/push endpoint returns 409 push.disabled and no background push
# loops run.
push:
  enabled: null                       # tristate: null (default) = enabled iff peerId
                                      # is set; explicit false disables even with a
                                      # peer configured
  peerId: ""                          # the push node's peer id
  addrs: []                           # dial addresses, e.g. ["quic://host:port"]

# Alpha invite codes (any-invite). Base URL of the invite service;
# empty disables POST /v1/account/access-code (409 access.disabled).
access:
  redeemUrl: ""

# Logger — passthrough to any-sync/app/logger.Config.
log:
  defaultLevel: info
  production: false
  format: 0                           # 0 colorized (default) | 1 plaintext | 2 json
  outputPaths: []                     # extra outputs, e.g. ["/var/log/any/server.log"]
  disableStdErr: false
  levels: []                          # per-logger overrides: [{name: http, level: warn}];
                                      # first match wins
```

## Env var overrides

Only the variables below are read:

```
ANY_DATA_DIR=/var/lib/any
ANY_MODE=managed                      # ownership mode (config: mode)
ANY_ACCOUNT=A8tR...                   # account selector (config: account; standalone only)
ANY_CONTROL_TOKEN=...                 # CLI only — the managed server's control
                                      # token, sent as X-Any-Control-Token on every
                                      # request (same as --control-token)
ANY_LISTEN_ADDR=127.0.0.1:7002
ANY_WALLET_PATH=/var/lib/any/wallet.key  # overrides auth.walletPath
ANY_WALLET_PASSKEY=...                # the passkey (default name of auth.passkeyEnv)
ANY_LOG_LEVEL=debug                   # log.defaultLevel
ANY_NETWORK_NODECONF_PATH=/etc/any/nodeconf.yaml  # network.nodeconfPath —
                                      # the override for joining a network
                                      # other than the embedded production
                                      # default (staging, local infra)

ANY_FILES_PUBLIC_READ_BASE_URL=https://files.example.com  # files.publicReadBaseUrl
ANY_FILES_GC_INTERVAL=1h              # files.gcInterval ("" = no background sweep)

ANY_PUSH_ENABLED=false                # push.enabled (tristate; unset = iff peerId).
                                      # false opts out of the production default
                                      # (docs/20-push.md § Config).
ANY_PUSH_PEER_ID=12D3Koo...           # push.peerId (the push node)
ANY_PUSH_ADDRS=quic://push:1234       # push.addrs (comma-separated)

ANY_LOCAL_ENABLED=false               # local.enabled

ANY_ACCESS_REDEEM_URL=https://invite.example.org  # access.redeemUrl

ANY_INDEX_ENABLED=false               # index.enabled
ANY_INDEX_EMBEDDER=ollama             # index.embedder (auto|local|ollama|openai|none)
ANY_INDEX_EMBED_BATCH=64              # index.embedBatch
ANY_INDEX_EMBED_CONCURRENCY=8         # index.embedConcurrency (parallel batches; online)
ANY_INDEX_OLLAMA_URL=http://localhost:11434
ANY_INDEX_OLLAMA_MODEL=embeddinggemma
ANY_INDEX_OPENAI_BASE_URL=https://api.openai.com/v1
ANY_INDEX_OPENAI_MODEL=text-embedding-3-small
ANY_INDEX_OPENAI_API_KEY=sk-...
ANY_INDEX_VECTOR_DIM=768              # index.vector.dim (0 = learned)
ANY_INDEX_VECTOR_MODE=btree           # index.vector.mode (ivfsq|btree|hnsw|hybrid|bruteforce|exact)
ANY_INDEX_SEARCH_STOP_WORDS=true      # index.search.stopWords
ANY_INDEX_SEARCH_FTS_WEIGHT=1.0       # index.search.ftsWeight
ANY_INDEX_SEARCH_VECTOR_WEIGHT=0.5    # index.search.vectorWeight
ANY_INDEX_SEARCH_ADAPTIVE_WEIGHTS=true # index.search.adaptiveWeights (auto-down-weight weak FTS)
ANY_INDEX_SEARCH_DEFAULT_OPERATOR=and # index.search.defaultOperator (or|and; AND = all terms)
ANY_INDEX_SEARCH_MIN_VECTOR_SIM=0     # index.search.minVectorSim (keep 0 for the local model)
ANY_INDEX_SEARCH_BM25_B=0.4           # index.search.bm25B (FTS length-norm)
ANY_INDEX_SEARCH_BM25_K1=1.2          # index.search.bm25K1 (FTS tf-saturation)
ANY_INDEX_SEARCH_TITLE_WEIGHT=2       # index.search.titleWeight (BM25F title boost)
ANY_INDEX_SEARCH_QUERY_EMBED_TIMEOUT=5s # index.search.queryEmbedTimeout (query embedding budget)
ANY_INDEX_LOCAL_MODEL_PATH=/models/q.gguf
ANY_INDEX_LOCAL_MODEL_URL=https://...
ANY_INDEX_LOCAL_MODEL_SHA256=06507c...
ANY_INDEX_LOCAL_LIB_DIR=/opt/llamacpp
ANY_INDEX_LOCAL_CONTEXT_SIZE=2048
ANY_INDEX_LOCAL_QUERY_PREFIX="Instruct: ...\nQuery:"
ANY_INDEX_LOCAL_DIM=512
ANY_INDEX_LOCAL_THREADS=8             # 0/unset = runtime.NumCPU()-1
ANY_INDEX_LOCAL_GPU_LAYERS=0          # 0 = force CPU-only decoding
ANY_INDEX_LOCAL_REQUEST_TIMEOUT=3m    # bound on one frame to the child (one decode group of batchDocs texts)
ANY_INDEX_LOCAL_NICENESS=10           # child scheduling priority; 0 = same as the server
ANY_INDEX_LOCAL_BATCH_DOCS=1          # index.local.batchDocs
```

A numeric or boolean value that does not parse is ignored (the file or
default value stays).

### `index.embedder: local` prerequisites

The local embedder is the **fallback** under the default `auto` (and used
directly with `index.embedder: local`; set `none` for FTS-only). It runs
llama.cpp in a child process (`any run embedder`, this same binary; no
CGO — yzma dlopens the shared libs at runtime), so a llama.cpp abort
costs a round of embedding instead of the server
(docs/13-index.md § The embedder child process). Prebuilt libs ship for
macOS arm64 (Metal), macOS x64, Linux x86_64 and Windows x86_64 (Vulkan,
with automatic CPU fallback). Only `-tags llamacpp` builds carry it
(`make build` and the release tarballs; never mobile): elsewhere `auto`
runs the online primary alone and `local` fails boot
(docs/13-index.md § Builds and the local embedder). Missing prerequisites
never break boot or FTS — the vector side reports `unavailable` until
they're met.

- **llama.cpp libs**: `make llamacpp` (also run as part of
  `make build`; a fetch failure there only warns) downloads the pinned
  prebuilt release into `bin/llamacpp/` next to the binary (override
  with `index.local.libDir` or `$YZMA_LIB`). Release tarballs carry them
  (`docs/18-ci.md`).
- **Model**: downloaded automatically into `<data-dir>/models/` on
  first boot (639 MB, progress in the server log; resumable, never
  blocks boot — vector search reports `unavailable` until it lands).
- **Linux**: a system `libffi.so.8` must be loadable (preinstalled on
  mainstream distros; on NixOS use `nix develop` — the repo flake's
  dev shell puts libffi and libstdc++/libgomp on `LD_LIBRARY_PATH`).
- **macOS**: libffi rides along in the binary (extracted to the user
  Caches dir on first run). The `-sandbox` release tarballs instead
  load the system `/usr/lib/libffi.dylib`, so they work inside an
  App-Sandboxed / hardened-runtime host — see `docs/18-ci.md`
  § The darwin `-sandbox` variants.

### `index.embedder: auto` (online primary + local fallback)

Prefers an online OpenAI-compatible API for speed and falls back to the
local model during an outage, so vector search stays fresh
instead of pausing. The online primary is configured by the `openai`
block (`baseUrl` / `model` / `apiKey`, `model` required); the fallback
by the `local` block (auto-downloaded at boot regardless, so it's
ready). A circuit breaker skips the primary for a cooldown after
repeated failures, then re-probes.

**Both sides must be the SAME embedding model** — the index stores one
vector space and one dimension; mixing models yields incoherent
similarity. The supported pairing is one model served two ways, e.g.
`index.openai.model: Qwen/Qwen3-Embedding-0.6B` (a host that serves it,
e.g. DeepInfra) with the default local Qwen3-Embedding-0.6B. fp16-vs-Q8
drift is negligible.

## Passkey

The passkey is the one secret a standalone server may need to open a
wallet. Sources:

1. `any init --passkey-stdin` reads it from stdin (single line; not
   combinable with `--mnemonic-stdin`).
2. Otherwise the env var named by `auth.passkeyEnv` (default
   `ANY_WALLET_PASSKEY`). `any run` and `POST /v1/auth` read only this.
3. Neither set — no passkey; the wallet must be plain.

No interactive TTY prompt, so the server runs under a supervisor or a
shell pipeline.

## Server flags

`any run`:

```
--config <path>
--data-dir <path>
--mode <standalone|managed>
--account <accountId>        # selector when the root holds several (standalone only)
--addr <host:port>           # bind address; loopback IP only
--wallet <path>
--log-level <debug|info|warn|error>
```

`any init` takes the same flags except `--mode` and `--addr`, plus
`--passkey-stdin` (§ Passkey); `any stop` takes `--config`, `--data-dir`
and `--account` (`01-cli.md` § Meta).

## CLI flags

```
--addr <host:port>           # default: the address the one running server
                             # under the data dir recorded, else 127.0.0.1:7001
--control-token <hex>        # managed server's control token (or ANY_CONTROL_TOKEN)
--timeout <duration>         # request timeout, default 30s
--verbose                    # log HTTP exchange to stderr
```

## First run

With no config file and no data dir, `any init` does:

1. Create `~/.any/` (mode 0700).
2. Generate an account and write `~/.any/<accountId>/wallet.key` —
   plain unless a passkey is supplied (§ Passkey). With
   `--mnemonic`/`--mnemonic-stdin` the account is derived from the
   supplied phrase instead (restore / second device; fresh device key
   either way).
3. Print the mnemonic to stderr (generation only), with a prominent
   warning to back it up, and `{accountId, created}` on stdout.

`any run` does not create wallets (outside an explicit
`auth.walletPath`): on a fresh root it starts unauthorized and waits for
`POST /v1/auth` (which can also generate or restore the account — the
HTTP flavor of init for UI onboarding). See `02-server.md` § Startup and
`03-api.md` § Auth.
