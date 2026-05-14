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
2. Resolve the data dir (default `~/.any/`).
3. Open the wallet (`auth.FileProvider`) at `<data-dir>/wallet.key`.
   - If absent: generate, write, print the mnemonic to stderr with a
     "back this up" warning.
4. Open the SDK (`anysyncsdk.Open(ctx, cfg, provider)`).
5. Bind HTTP listener on `127.0.0.1:<port>` (default `7001`) synchronously
   via `net.Listen`. Listen errors (port in use, EACCES, non-loopback)
   surface to the caller of `server.Run` before serving begins.
6. Serve.

## Listen address

- **Default**: `127.0.0.1:7001`. Plain HTTP, no TLS, no auth.
- **Configurable**: `listen.addr` in config or `--addr host:port` flag.
- The server refuses to bind anything other than a loopback address in
  v1. If you pass `--addr 0.0.0.0:7001` it errors out clearly with
  "remote access is not supported in v1". (Keeps the security model
  honest.)
- Pass `127.0.0.1:0` to let the OS pick a free port; embedders read the
  actually-bound address back via the `RunOptions.Ready` hook (see
  below). The startup logs always print the resolved address.

## Embedding (RunWith)

`server.Run(ctx, cfg)` is the CLI entry point and blocks. Embedders that
need to know the bound address synchronously — e.g. the gomobile wrapper
in `mobile/` — call `server.RunWith(ctx, cfg, RunOptions{Ready: fn})`.
`Ready` fires once on the calling goroutine after the listener has
bound, before Echo starts serving; `fn` receives the resolved
`host:port`. Wallet / SDK / listen failures still return from `RunWith`
as ordinary errors, so callers can `select` on a ready channel vs the
`Run` error channel to surface a real startup error instead of a
swallowed nil.

## Shutdown

Two ways the server stops:

1. `SIGINT` / `SIGTERM` — graceful: stop accepting new requests, drain
   in-flight with a 10s deadline, close SDK, exit 0.
2. `POST /v1/shutdown` — same path, triggered over HTTP. (Localhost-only
   is enforced by the listen address.)

After the graceful timeout, in-flight requests are aborted and the
process exits with 0.

## Single-instance lock

The server writes a PID lock file at `<data-dir>/server.pid` on
startup. If the lock is held by a live PID, startup fails with a
clear message. Stale locks (PID no longer exists) are reclaimed.

## Data dir layout

```
<data-dir>/
├── wallet.key          # auth.FileProvider wallet (mode 0600)
├── server.pid          # lock file
├── config.yaml         # optional, if not passed via --config
└── sdk/                # any-store DB(s) — owned by the SDK
```

The SDK's `config.Storage.DataDir` points at `<data-dir>/sdk/`.
Server-specific files live directly under `<data-dir>/`.

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
  "status":    "ok",
  "version":   "any v0.1.0 (sdk v0.0.0)",
  "startedAt": "2026-04-23T18:12:00Z",
  "account":   "A3...accountId..."
}
```

Does not require SDK state beyond the server being up and the wallet
loaded. Used by `any status` and by supervisors once we add
install/service files.

## One server = one account

v1 is deliberately single-account per process. Two accounts → two
data dirs, two `any run` processes on different ports. Multi-account
is deferred.
