# Config

## Sources, precedence

1. Config file (YAML), loaded from `--config PATH` or
   `$XDG_CONFIG_HOME/any/config.yaml` → `~/.config/any/config.yaml` →
   `<data-dir>/config.yaml`.
2. Environment variables — override file values.
3. Command-line flags — override both.

A missing config file is not an error: defaults are used.

## File shape

```yaml
# Data ROOT. Each account lives in <dataDir>/<accountId>/ (wallet.key,
# server.pid, sdk/, index/); a wallet.key directly at the root is the
# legacy flat layout and acts as the default account with its data at
# the root. config.yaml and the shared models/ cache sit at the root.
# Layout details: 02-server.md § Data dir layout.
dataDir: ~/.any

# Account to boot when the root holds more than one. Empty = the
# default (root wallet.key, or the sole per-account dir); with several
# accounts and no selector the server starts unauthorized and waits
# for POST /v1/auth.
account: ""

# HTTP server listen address. Loopback only in v1.
listen:
  addr: 127.0.0.1:7001

# Auth — wallet location, optional passkey env var name.
auth:
  walletPath: ""                      # explicit wallet file = manual mode
                                      # (no per-account nesting); default:
                                      # resolved per account
  passkeyEnv: ANY_WALLET_PASSKEY      # env var name to read passkey from

# any-sync network. Path to a nodeconf YAML, or inline. When neither is
# set, an EMBEDDED fallback ships inside the binary (vendored at
# internal/config/nodeconf-staging.yml) so packaged installs boot from
# any working directory. The embedded conf is the sanitized fixture
# (staging networkId, placeholder nodes) — it works locally but joins no
# network; configure a real nodeconf to sync.
network:
  nodeconfPath: /etc/any/nodeconf.yaml
  # OR:
  # nodeconf: |
  #   ...

# Storage topology. "shared" or "per-space".
storage:
  topology: shared

# Sync tuning (all optional).
sync:
  dialTimeout: 10s
  changeBatchSize: 100

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
                                      # Defaults are pre-filled with shared DeepInfra
                                      # dev creds (Qwen3-Embedding-0.6B) so `auto` works
                                      # zero-config — TEMPORARY, rotated/removed at launch.
    baseUrl: https://api.deepinfra.com/v1/openai
    model: Qwen/Qwen3-Embedding-0.6B  # for auto, MUST equal the local fallback model
    apiKey: ...                       # sent as Bearer; never logged
  local:                              # in-process llama.cpp — all fields optional;
                                      # the default embedder needs no config at all
    modelPath: ""                     # existing GGUF; set ⇒ no download (air-gapped)
    modelUrl: ""                      # download-source override for the default path
    modelSha256: ""                   # checksum override; "" with modelUrl ⇒ skip verify
    libDir: ""                        # llama.cpp shared libs; default <exe-dir>/llamacpp
    contextSize: 2048                 # truncation bound in tokens (docs/13-index.md)
    queryPrefix: ""                   # "" = Qwen retrieval instruction for the default model
    dim: 0                            # Matryoshka output truncation; 0 = model dim (1024)
    threads: 0                        # llama.cpp compute threads; 0 = runtime.NumCPU()-1 (leave one free)
  vector:
    dim: 0                            # 0 = learned from the first successful embedding
    mode: ivfsq                       # ANN index: ivfsq (default — cheap ingest, churn-
                                      # friendly, ~3-4 recall@10 below exact) | btree/hnsw
                                      # (recall≈exact, costly serial ingest) | hybrid | bruteforce
  search:                             # hybrid-ranking knobs (docs/13-index.md § Search)
    stopWords: true                   # strip stop words from the FTS-leg query (default on)
    ftsWeight: 1                      # RRF weight for the lexical leg (default 1)
    vectorWeight: 1                   # RRF weight for the dense leg (default 1)
    minVectorSim: 0                   # cosine floor for vector hits; 0 = legacy ">0"
    bm25B: 0                          # FTS BM25 length-norm; 0 = engine default 0.75
    bm25K1: 0                         # FTS BM25 tf-saturation; 0 = engine default 1.2
    titleWeight: 0                    # BM25F boost for the title field; 0 = default 1.0

# Logger — passthrough to any-sync/app/logger.Config.
log:
  defaultLevel: info
  production: false
  format: colorized                   # colorized | plaintext | json
  addOutputPaths: []                  # e.g. ["~/.any/server.log"]
```

## Env var overrides

Prefix `ANY_`, underscores map to nested fields. Examples:

```
ANY_DATA_DIR=/var/lib/any
ANY_ACCOUNT=A8tR...                   # account selector (config: account)
ANY_LISTEN_ADDR=127.0.0.1:7002
ANY_WALLET_PATH=/var/lib/any/wallet.key  # overrides auth.walletPath
ANY_WALLET_PASSKEY=...                # read directly
ANY_LOG_LEVEL=debug                   # shorthand for log.defaultLevel

ANY_INDEX_ENABLED=false               # index.enabled
ANY_INDEX_EMBEDDER=ollama             # index.embedder (local|ollama|openai|auto|none)
ANY_INDEX_EMBED_BATCH=64              # index.embedBatch
ANY_INDEX_EMBED_CONCURRENCY=8        # index.embedConcurrency (parallel batches; online)
ANY_INDEX_OLLAMA_URL=http://localhost:11434
ANY_INDEX_OLLAMA_MODEL=embeddinggemma
ANY_INDEX_OPENAI_BASE_URL=https://api.openai.com/v1
ANY_INDEX_OPENAI_MODEL=text-embedding-3-small
ANY_INDEX_OPENAI_API_KEY=sk-...
ANY_INDEX_VECTOR_DIM=768              # index.vector.dim (0 = probe)
ANY_INDEX_VECTOR_MODE=btree           # index.vector.mode (btree|hybrid|bruteforce|ivfsq)
ANY_INDEX_SEARCH_STOP_WORDS=true      # index.search.stopWords
ANY_INDEX_SEARCH_FTS_WEIGHT=1.0       # index.search.ftsWeight
ANY_INDEX_SEARCH_VECTOR_WEIGHT=0.5    # index.search.vectorWeight
ANY_INDEX_SEARCH_MIN_VECTOR_SIM=0     # index.search.minVectorSim (keep 0 for the local model)
ANY_INDEX_SEARCH_BM25_B=0.4           # index.search.bm25B (FTS length-norm)
ANY_INDEX_SEARCH_BM25_K1=1.2          # index.search.bm25K1 (FTS tf-saturation)
ANY_INDEX_SEARCH_TITLE_WEIGHT=2       # index.search.titleWeight (BM25F title boost)
ANY_INDEX_LOCAL_MODEL_PATH=/models/q.gguf
ANY_INDEX_LOCAL_MODEL_URL=https://...
ANY_INDEX_LOCAL_MODEL_SHA256=06507c...
ANY_INDEX_LOCAL_LIB_DIR=/opt/llamacpp
ANY_INDEX_LOCAL_CONTEXT_SIZE=2048
ANY_INDEX_LOCAL_QUERY_PREFIX="Instruct: ...\nQuery:"
ANY_INDEX_LOCAL_DIM=512
ANY_INDEX_LOCAL_THREADS=8            # 0/unset = runtime.NumCPU()-1
```

### `index.embedder: local` prerequisites

The local embedder is the **fallback** under the default `auto` (and used
directly with `index.embedder: local`; set `none` for FTS-only). It runs
llama.cpp in-process (no CGO — yzma dlopens the
shared libs at runtime). Supported platforms: macOS arm64 (Metal) and
Linux amd64 (CPU). Missing prerequisites never break boot or FTS — the
vector side just reports `unavailable` until they're met.

- **llama.cpp libs**: `make llamacpp` (also run as part of
  `make build`; a fetch failure there only warns) downloads the pinned
  prebuilt release into `bin/llamacpp/` next to the binary (override
  with `index.local.libDir`).
- **Model**: downloaded automatically into `<data-dir>/index/models/`
  on first boot (639 MB, progress in the server log; resumable, never
  blocks boot — vector search reports `unavailable` until it lands).
- **Linux**: a system `libffi.so.8` must be loadable (preinstalled on
  mainstream distros; on NixOS use `nix develop` — the repo flake's
  dev shell puts libffi and libstdc++/libgomp on `LD_LIBRARY_PATH`).

### `index.embedder: auto` (online primary + local fallback)

Prefers an online OpenAI-compatible API for speed and falls back to the
in-process local model during an outage, so vector search stays fresh
instead of pausing. The online primary is configured by the `openai`
block (`baseUrl` / `model` / `apiKey`); the fallback by the `local` block
(auto-downloaded at boot regardless, so it's ready). A circuit breaker
skips the primary for a cooldown after repeated failures, then re-probes.

**Both sides must be the SAME embedding model** — the index stores one
vector space and one dimension; mixing models yields incoherent
similarity. The supported pairing is one model served two ways, e.g.
`index.openai.model: Qwen/Qwen3-Embedding-0.6B` (a host that serves it,
e.g. DeepInfra) with the default local Qwen3-Embedding-0.6B. fp16-vs-Q8
drift is negligible.

The passkey is the one secret the server may need at boot. Accepted
sources:

1. Env var named by `auth.passkeyEnv` (default `ANY_WALLET_PASSKEY`).
2. If `--passkey-stdin` is passed on the command line, read from stdin
   (single line).
3. Otherwise, no passkey — the wallet must be plain.

No interactive TTY prompt. Passkey arrives via env or stdin so the
server can run under a supervisor / shell pipeline.

## Server flags

```
--config <path>
--data-dir <path>
--account <accountId>        # selector when the root holds several
--addr <host:port>           # must be loopback in v1
--wallet <path>
--passkey-stdin
--log-level <debug|info|warn|error>
```

## CLI flags

```
--addr <host:port>           # defaults to 127.0.0.1:7001
--timeout <duration>         # request timeout, default 30s
--verbose                    # log HTTP exchange to stderr
```

## First run

With no config file and no data dir, `any init` does:

1. Create `~/.any/` (mode 0700).
2. Generate an account and write
   `~/.any/<accountId>/wallet.key` — plain unless
   `ANY_WALLET_PASSKEY` is set. With `--mnemonic`/`--mnemonic-stdin`
   the account is derived from the supplied phrase instead (restore /
   second device; fresh device key either way).
3. Print the mnemonic to stderr (generation only), with a prominent
   warning to back it up.

`any run` does NOT create wallets: on a fresh root it starts
unauthorized and waits for `POST /v1/auth` (which can also generate or
restore the account — the HTTP flavor of init for UI onboarding). See
`02-server.md` § Startup and `03-api.md` § Auth.
