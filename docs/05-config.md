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
# Data root. Contains wallet.key, server.pid, storage/.
dataDir: ~/.any

# HTTP server listen address. Loopback only in v1.
listen:
  addr: 127.0.0.1:7001

# Auth — wallet location, optional passkey env var name.
auth:
  walletPath: ~/.any/wallet.key       # default: <dataDir>/wallet.key
  passkeyEnv: ANY_WALLET_PASSKEY      # env var name to read passkey from

# any-sync network. Path to a nodeconf YAML, or inline.
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

# Local search index (docs/11-index.md). FTS needs no external
# dependency; vector search activates when an embedder is configured.
index:
  enabled: true                       # default true; false disables the indexer + /search
  embedder: ""                        # "" (FTS-only) | ollama | openai
  ollama:
    url: http://localhost:11434       # default
    model: embeddinggemma             # default
  openai:                             # any OpenAI-compatible /embeddings API
    baseUrl: https://api.openai.com/v1
    model: text-embedding-3-small     # required when embedder: openai
    apiKey: sk-...                    # sent as Bearer; never logged
  vector:
    dim: 0                            # 0 = probe the embedder at boot

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
ANY_LISTEN_ADDR=127.0.0.1:7002
ANY_WALLET_PATH=/var/lib/any/wallet.key  # overrides auth.walletPath
ANY_WALLET_PASSKEY=...                # read directly
ANY_LOG_LEVEL=debug                   # shorthand for log.defaultLevel

ANY_INDEX_ENABLED=false               # index.enabled
ANY_INDEX_EMBEDDER=ollama             # index.embedder
ANY_INDEX_OLLAMA_URL=http://localhost:11434
ANY_INDEX_OLLAMA_MODEL=embeddinggemma
ANY_INDEX_OPENAI_BASE_URL=https://api.openai.com/v1
ANY_INDEX_OPENAI_MODEL=text-embedding-3-small
ANY_INDEX_OPENAI_API_KEY=sk-...
ANY_INDEX_VECTOR_DIM=768              # index.vector.dim (0 = probe)
```

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

With no config file and no data dir, `any run` does:

1. Create `~/.any/` (mode 0700).
2. Generate a wallet.key — plain unless `ANY_WALLET_PASSKEY` is set.
3. Print the mnemonic to stderr, with a prominent warning to back it
   up.
4. Start serving on `127.0.0.1:7001`.

`any init` runs steps 1–3 and exits — gives the operator a moment to
copy the mnemonic before the server binds.
