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
any run [--config PATH] [--mode standalone|managed]
```

Foreground process. Stops on Ctrl-C (SIGINT), `any stop` (a signal —
see § Shutdown) or, on a managed server, its host's `POST
/v1/shutdown`. No self-daemonization, no `--detach` — run under a
terminal, `tmux`, `systemd --user`, `nohup`, whatever the operator
prefers.

## Modes

`mode` (`--mode`, `ANY_MODE`, config `mode:`, `embedded.Options.Mode`)
declares **who owns this server**, and key custody, account selection,
logout and shutdown rights follow from it:

|                    | `standalone` (default)                  | `managed`                                              |
|--------------------|-----------------------------------------|--------------------------------------------------------|
| Owner              | the user (terminal, `any stop`)         | the spawning host (desktop shell, mobile app)          |
| Account key        | `wallet.key` on disk, optional passkey  | never on disk — the host supplies it on every boot     |
| Account choice     | resolved from disk (§ Startup)          | stated by the host over `POST /v1/auth`, every boot    |
| Logout / switch    | refused                                 | `DELETE /v1/auth`, `POST /v1/auth {…, replace: true}`  |
| `POST /v1/shutdown`| refused (`403 shutdown.not_managed`)    | allowed, control-token gated                           |

Mode is fixed at launch and **unreachable over HTTP** — that is what
makes the standalone refusals enforceable rather than advisory. A
managed server refuses the standalone-only inputs (`account:`,
`auth.walletPath`) at config load. Clients never branch on the mode
string: `GET /v1/auth` reports the operations the server accepts as
`capabilities` bits (`03-api.md` § Auth).

**Control token.** A managed server accepts `POST`/`DELETE /v1/auth`
and `POST /v1/shutdown` only with the `X-Any-Control-Token` header —
otherwise any same-user process on the loopback could log it into a
different account or stop it. A CLI-spawned managed server mints the
token and prints it as the second line of the stdout handshake
(`CONTROL_TOKEN <hex>`, right after `LISTENING`); only the spawning
parent owns that pipe. An in-process host passes its own through
`embedded.Options.ControlToken` (required in managed). The token is
never logged. It expresses ownership, not authentication: loopback is
still the trust boundary.

**Device identity is server-cached.** A managed login carries the
account key only; the device key — what the peerId derives from — is
minted once per `(root, account)` at `<root>/<accountId>/device.key`
and reused on every later login, so a host that logs in on every app
launch does not leave a new row in the synced devices registry each
time. Two hosts that must be two devices for one account (an app and
its share extension) use two roots. The cache is never portable.

No migration between the modes: a managed boot never reads a
`wallet.key`, a standalone boot never reads a `device.key`. Data lives
under `<root>/<accountId>/` in both, so the same account reached under
either custody finds its existing SDK state; the legacy flat-root
account is standalone-only — a managed login with its phrase lands in
`<root>/<id>/` fresh, with a new peerId, and re-syncs from the network.

## Startup

1. Load config (file → env var overrides → flags). See `05-config.md`.
2. **Managed**: skip account resolution entirely — start unauthorized
   (step 4) and wait for the host's `POST /v1/auth`.
   **Standalone**: resolve the data-dir ROOT (default `~/.any/`) and
   pick the account to boot (`internal/server/identity.go`):
   - `auth.walletPath` / `--wallet` set → that wallet, data flat at the
     root (manual mode).
   - `account:` / `ANY_ACCOUNT` / `--account` set → `<root>/<id>/` if
     present, else the root `wallet.key` (the derived id must match,
     verified after opening).
   - No selector: a root `wallet.key` (legacy flat layout) is the
     default account; else a sole `<root>/<id>/` dir; else **no
     account**.
3. With an account: boot its engine — take the instance lock in the
   account dir, open
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
5. Bind HTTP listener on `127.0.0.1:<port>` (default `7001`). Print
   `LISTENING <addr>` (and, managed with a minted token,
   `CONTROL_TOKEN <hex>`) on stdout; record the address in the
   account dir's `server.addr` once an engine is up.
6. Serve.

The **engine** — the instance lock, the SDK, the indexer, the push
service and every goroutine and stream working against them — is
separable from the listener: a managed server tears it down in place on
`DELETE /v1/auth` and replaces it on a `replace` switch, returning to
step 4 without a restart (`03-api.md` § Auth). Every request and
stream runs inside an engine gate; a teardown flips the server
unauthorized, cancels the engine's context (streams end with
`closed{reason: deauthorized}`), drains the gate (10s bound), joins the
engine's goroutines, drops the account-bound in-memory state, closes
the resources and clears the fields — so a later boot in the same
process is the same code path as the first.

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
  uses as its port handshake + readiness gate; a managed server with a
  minted control token follows it with `CONTROL_TOKEN <hex>` (§ Modes),
  which gates the shell's auth and graceful-quit calls. Do not change
  either line's shape. The address is also recorded in the account
  dir's `server.addr`, which the CLI reads when `--addr` is not given.
- **Configurable**: `listen.addr` in config or `--addr host:port` flag.
- The server refuses to bind anything other than a loopback address in
  v1. If you pass `--addr 0.0.0.0:7001` it errors out clearly with
  "remote access is not supported in v1". (Keeps the security model
  honest.)

## Shutdown

Session and lifetime are separate axes: `POST`/`DELETE /v1/auth` change
which account the server serves, the entries below end the process.
One teardown core — tear the engine down (streams get their terminal
frame, in-flight requests drain, 10s bound), close the listener, exit
0 — and three entry points, gated by ownership (§ Modes):

| Entry                        | standalone                       | managed                          |
|------------------------------|----------------------------------|----------------------------------|
| `SIGINT` / `SIGTERM`         | yes                              | yes                              |
| `POST /v1/shutdown`          | `403 shutdown.not_managed`       | yes, needs the control token     |
| `embedded.Stop` / `StopNow`  | n/a                              | yes — the in-process host's path |

`any stop` sends no HTTP: it finds the server serving the data dir's
account by its **held** instance lock (`FindRunning` — proof of life,
never `server.pid` alone), sends it `SIGTERM` and waits for the lock to
be released. That works against a wedged server and one on an unknown
ephemeral port, and it needs no token; a managed server can be stopped
the same way. An unauthorized standalone server holds no account lock
yet — stop it with Ctrl-C. Windows has no signal to send; there `any
stop` errors and stopping is the host's job.

`POST /v1/shutdown` stays outside the auth guard so an unauthorized
managed server is still stoppable by its host. After the graceful
timeout, in-flight requests are aborted and the process exits with 0.

## Single-instance lock

When the account's engine boots, the server takes an exclusive OS file
lock on `<account-dir>/server.lock` (the root itself for the legacy flat
layout) — `flock(2)` on unix, `LockFileEx` on Windows. A held lock fails
the boot with a clear message: `409 auth.account_in_use` when it happens
via `POST /v1/auth`.

The kernel releases the lock when the holder exits by any means, so
there is nothing stale to reclaim — a crashed server blocks nobody, and
neither file below is removed on release or on crash. `server.lock`
itself is empty; the holder's pid goes in `<account-dir>/server.pid`
right after acquiring, purely to name it in the error
(`details.pid` — best-effort, and absent if that file is unreadable),
and its bound address in `<account-dir>/server.addr` once the listener
is up. Treat both as labels, never as proof a server is running: the
CLI (`any stop`, `--addr` discovery) probes the lock itself and reads
the files only for the holder it found.

One lock per ACCOUNT: two servers may share a root as long as they
serve different accounts (on different ports). An unauthorized server
holds no lock until it boots an account; a managed server releases it
on `DELETE /v1/auth` and takes the next account's on a switch.

## Data dir layout

`dataDir` is a ROOT that can hold several accounts:

```
<root>/                          # dataDir, default ~/.any
├── config.yaml                  # optional, if not passed via --config
├── models/                      # shared embedder model cache (all accounts)
├── wallet.key                   # LEGACY flat layout = the DEFAULT account;
├── server.lock                  #   its data stays directly at the root
├── server.pid                   #   exactly as before (no migration)
├── sdk/  index/
└── <accountId>/                 # every account created since
    ├── wallet.key               # STANDALONE: auth.FileProvider wallet (mode 0600)
    ├── device.key               # MANAGED: cached device key (mode 0600, minted
    │                            #   once, never portable — § Modes); the account
    │                            #   key is never written
    ├── server.lock              # per-account single-instance lock (OS file lock)
    ├── server.pid               # holder's pid, for error messages only
    ├── server.addr              # holder's bound address — CLI convenience only
    ├── sdk/                     # any-store DB(s) — owned by the SDK; sdk.db ALSO
    │                            #   holds the local store's l_* collections
    ├── files/                   # file content (one CARv2 per rootCid) — owned
    │                            #   by the SDK (files v2, docs/17-files.md)
    └── index/                   # local search index (index.db) — owned by the indexer
```

**Upgrading is one-way.** any-store v2.0.0 renamed the database header
magic; it still reads databases written by earlier v2 builds, but once a
v2.0.0-era server has opened a data dir the header carries the new magic
and a build pinned to an older any-store refuses it outright
(`btree: database is corrupt` — the data is intact, the old code just
doesn't recognize the file). Verified by rolling a server back after an
upgrade. Keep a copy of the data dir if you need the option to downgrade.

The SDK's `config.Storage.DataDir` points at `<account-dir>/sdk/`; the
SDK derives `<account-dir>/files/` next to it for file bytes. A durable
file's bytes are a cache (reclaimable via `/v1/files/cache/*` or
per-file offload); a non-durable file's bytes are the ONLY copy —
deleting `files/` by hand loses them.
`sdk/sdk.db` is the SDK's replay cache of the DAGs **plus the local
store** (`docs/26-local-store.md`): the `l_*` collections there are
device-local data with no DAG behind them and no backup. The SDK's
own re-index paths rebuild only CRDT collections and leave them alone,
but removing `sdk/` by hand loses them.
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

A process serves one account **at a time**. Two accounts at once → two
`any run` processes on different ports (they may share one data-dir
root — each account dir carries its own instance lock). Switching the
account of a RUNNING server is an ownership right (§ Modes): a managed
server's host switches in place with `POST /v1/auth {…, "replace":
true}` or signs out with `DELETE /v1/auth`; a standalone server refuses
both — stop it and start with `--account <id>`, or let `POST /v1/auth`
pick on an unauthorized one. Multi-account per process is deferred.
