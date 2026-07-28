# Server

## HTTP framework

Use **[`github.com/labstack/echo`](https://echo.labstack.com/)** (v4).
Middleware stack, routing groups, and binding helpers line up well with
the endpoint catalog. No other framework should be introduced without a
reason.

Grouping routes per SDK section uses `echo.Group`:

```
e := echo.New()
v1 := e.Group("/v1")
v1.GET("/health", ...)
spaces := v1.Group("/spaces")
spaces.POST("", ...)
spaces.GET("", ...)
spaces.GET("/:spaceId", ...)
// ...
```

The `/v1` prefix is **not** optional in v1 — every route ships under
it from day one so the next iteration doesn't have to break paths.

## Command

```
any run [--config PATH]
```

Foreground process. Stops on Ctrl-C (SIGINT) or `any stop`. No
self-daemonization, no `--detach` — run under a terminal, `tmux`,
`systemd --user`, `nohup`, whatever the operator prefers.

## Startup

1. Load config (file → env var overrides → flags). See `05-config.md`.
2. Resolve the data-dir ROOT (default `~/.any/`) and pick the account
   to boot (`internal/server/identity.go`):
   - `auth.walletPath` / `--wallet` set → that wallet, data flat at the
     root (manual mode).
   - `account:` / `ANY_ACCOUNT` / `--account` set → `<root>/<id>/` if
     present, else the root `wallet.key` (the derived id must match,
     verified after opening).
   - No selector: a root `wallet.key` (legacy flat layout) is the
     default account; else a sole `<root>/<id>/` dir; else **no
     account**.
3. With an account: boot its engine — pid lock in the account dir, open
   the wallet, derive the account id, open the SDK and the indexer —
   before the listener binds, so boot FAILURES surface immediately.
   `run` does NOT auto-generate a wallet anymore; create accounts with
   `any init` or over HTTP.
   SDK `Open` returns after local wiring only: eager space loading and
   offline catch-up replay run on one SDK-owned serial background pass,
   so the server serves as soon as the listener binds. Until a space's
   turn in the pass, reads against it serve the pre-offline state.
   `GET /v1/health` reports the pass via `bootstrapping` (see § Health);
   per-space convergence stays on `/sync-status`.
4. Without an account: start **unauthorized**. Every `/v1` route except
   `/v1/health`, `/v1/shutdown`, `/v1/openapi.json` and `/v1/auth`
   returns `401 auth.required` until `POST /v1/auth` creates / restores
   / selects an account and boots the engine in place (no restart).
   See `03-api.md` § Auth.
5. Bind HTTP listener on `127.0.0.1:<port>` (default `7001`).
6. Serve.

Opening the SDK also auto-starts the **one-to-one inbox notifier**
(`anysyncsdk.Open` → `StartOneToOneInbox`): when a coordinator is
configured it watches the coordinator inbox for incoming 1-1 (direct)
space requests and surfaces each as a `one_to_one_pending` row in the
space list — no server-side wiring or config knob. With no coordinator
the notifier is simply off and incoming 1-1s arrive only via the
out-of-band `POST /v1/spaces/one-to-one/register-incoming` path. See
`03-api.md` § Spaces and the SDK's `docs/13-one-to-one-spaces.md`.

## Listen address

- **Default**: `127.0.0.1:7001`. Plain HTTP, no TLS, no auth.
- **Ephemeral port**: `--addr 127.0.0.1:0` asks the kernel for a free port.
  The server prints `LISTENING <resolved-addr>` as a plain stdout line
  before serving — a machine-parseable contract the any-ui desktop shell
  uses as its port handshake + readiness gate (it also relies on
  `POST /v1/shutdown` for graceful quit). Do not change that line's shape.
- **Configurable**: `listen.addr` in config or `--addr host:port` flag.
- The server refuses to bind anything other than a loopback address in
  v1. If you pass `--addr 0.0.0.0:7001` it errors out clearly with
  "remote access is not supported in v1". (Keeps the security model
  honest.)

## Shutdown

Two ways the server stops:

1. `SIGINT` / `SIGTERM` — graceful: stop accepting new requests, drain
   in-flight with a 10s deadline, close SDK, exit 0.
2. `POST /v1/shutdown` — same path, triggered over HTTP. (Localhost-only
   is enforced by the listen address.)

After the graceful timeout, in-flight requests are aborted and the
process exits with 0.

## Single-instance lock

The server writes a PID lock file at `<account-dir>/server.pid` when
the account's engine boots (the root itself for the legacy flat
layout). If the lock is held by a live PID, the boot fails with a
clear message — `409 auth.account_in_use` when it happens via
`POST /v1/auth`. Stale locks (PID no longer exists) are reclaimed.
One lock per ACCOUNT: two servers may share a root as long as they
serve different accounts (on different ports). An unauthorized server
holds no lock until it boots an account.

## Data dir layout

`dataDir` is a ROOT that can hold several accounts:

```
<root>/                          # dataDir, default ~/.any
├── config.yaml                  # optional, if not passed via --config
├── models/                      # shared embedder model cache (all accounts)
├── wallet.key                   # LEGACY flat layout = the DEFAULT account;
├── server.pid                   #   its data stays directly at the root
├── sdk/  index/                 #   exactly as before (no migration)
└── <accountId>/                 # every account created since
    ├── wallet.key               # auth.FileProvider wallet (mode 0600)
    ├── server.pid               # per-account lock file
    ├── sdk/                     # any-store DB(s) — owned by the SDK
    ├── files/                   # file content (one CARv2 per rootCid) — owned
    │                            #   by the SDK (files v2, docs/17-files.md)
    └── index/                   # local search index (index.db) — owned by the indexer
```

The SDK's `config.Storage.DataDir` points at `<account-dir>/sdk/`; the
SDK derives `<account-dir>/files/` next to it for file bytes. A durable
file's bytes are a cache (reclaimable via `/v1/files/cache/*` or
per-file offload); a non-durable file's bytes are the ONLY copy —
deleting `files/` by hand loses them.
The search index (`docs/13-index.md`) is derived state: removing
`<account-dir>/index/` is safe but re-indexes only content changed
afterwards ("index from the next change"). The embedder model cache is
shared at `<root>/models/` — one ~600MB download per root, not per
account (a model already sitting in a legacy `<account-dir>/index/models/`
keeps being used from there).

## Logging

Uses `any-sync/app/logger`. The config's `log` block is applied
globally at startup. Default level `info`; colorized output to stderr.
Each major package pulls a named logger (e.g. `logger.NewNamed("http")`).

Echo's own logger is wired to the same backend — one log stream for
the whole process.

## Health

`GET /v1/health` returns:

```json
{
  "status":        "ok",
  "version":       "any v0.1.0 (sdk v0.0.0)",
  "startedAt":     "2026-04-23T18:12:00Z",
  "account":       "A3...accountId...",
  "bootstrapping": false
}
```

Does not require SDK state — on an unauthorized server `account` is
`""` and everything else is live. Used by `any status` and by
supervisors once we add install/service files.

`bootstrapping` is `true` while a booted engine's SDK background boot
pass (eager space loading + offline catch-up) is still running: the
server is serving, catch-up happens in the background. `false` when
unauthorized and once the pass completes. Per-space convergence stays
on `/sync-status` — this flag only reports the one-shot boot pass.

## One server = one account

v1 is deliberately single-account per process. Two accounts → two
`any run` processes on different ports (they may share one data-dir
root — each account dir carries its own pid lock). Switching the
account of a RUNNING server is not supported: stop it and start with
`--account <id>` (or let `POST /v1/auth` pick on an unauthorized
server). Multi-account per process is deferred.
