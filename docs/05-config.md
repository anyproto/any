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
