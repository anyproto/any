# Desktop shell contract: LISTENING announce, desktop-origin CORS, embedded nodeconf

## Overview
- The any-ui desktop shell (any-ui PR #162, spec PR-095 there) spawns `any run
  --addr 127.0.0.1:0` as a Tauri sidecar. It needs three small server-side
  contracts; until they land, the shell times out by design.
- Problem 1: the startup log prints the CONFIG addr (`"addr": "127.0.0.1:0"`),
  not the kernel-resolved port — the shell has no way to learn where to
  connect. Fix: an explicit listener + a machine-parseable `LISTENING <addr>`
  stdout line (the Chrome `DevTools listening on ws://…` pattern).
- Problem 2: the webview's page origins (`tauri://localhost` on macOS/Linux,
  `http://tauri.localhost` on Windows; Vite dev origins under `tauri dev`)
  are cross-origin to the loopback API, and v1 ships no CORS. Fix: a fixed,
  hardcoded allowlist — this AMENDS the documented "no CORS in v1" stance as
  a named local-origin exception; the loopback-only listen posture is
  unchanged (CORS never gated local processes; it gates browser pages, and
  no remote page can carry these origins — custom schemes are unclaimable
  and RFC 6761 pins `.localhost` to loopback).
- Problem 3: the nodeconf fallback `../test-etc/staging.yml` is CWD-relative
  — nonexistent for a packaged app (verified live: fresh data dir + non-repo
  CWD fails with `open ../test-etc/staging.yml`). Fix: vendor the staging
  nodeconf into the package and `go:embed` it as the fallback — fulfilling
  nodeconf.go's own "packaged default before v1 ships" promise. Dev behavior
  unchanged (same network); the flip to a production-network default stays a
  separate release decision.

## Context (from discovery)
- `internal/server/server.go:99` — `lg.Info("listening", zap.String("addr",
  cfg.Listen.Addr), …)` logs the config value; echo binds internally, no
  explicit listener. Insertion point for `net.Listen` + announce.
- `internal/server/routes.go:11-46` — `buildEcho` middleware chain
  (Recover → RequestID → BodyLimit → httpLog); CORS slots after RequestID.
  Echo's CORS middleware is a no-op without an Origin header (curl/CLI/
  same-origin untouched) and answers preflight OPTIONS itself; it runs
  before the SSE handlers so streams carry the header too.
- `internal/config/nodeconf.go` — `DefaultNodeconfPath = "../test-etc/staging.yml"`
  with the "will be replaced by a packaged default before v1 ships" comment;
  precedence inline `network.nodeconf` → `network.nodeconfPath` → fallback.
  `go:embed` can't reach outside the package dir → vendor a copy as
  `internal/config/nodeconf-staging.yml` (provenance comment; drift accepted,
  the staging conf rarely changes).
- Verified non-changes: `--addr`/port-0 already work (live probe 2026-06-11);
  `POST /v1/shutdown` exists (the shell consumes it, expects 2xx/204);
  `server.pid` lock already refuses a second instance per data dir.
- Handler tests build the app via `buildEcho(deps)` + httptest — the CORS
  matrix tests follow that pattern.

## Steps
1. `internal/config/nodeconf-staging.yml`: vendored copy of
   `../test-etc/staging.yml` + `//go:embed` fallback in `LoadNodeconf`
   (delete the CWD-relative read; keep override precedence). Test: empty
   `Network{}` returns non-empty YAML with no file on disk.
2. `internal/server/server.go`: `ln, err := net.Listen("tcp", cfg.Listen.Addr)`
   → `e.Listener = ln`; print `LISTENING <ln.Addr()>` to stdout (plain
   `fmt.Printf`, NOT zap — log level/format must not break the contract)
   with a "do not change — the desktop shell parses this" comment; zap
   lines switch to the resolved addr. Test: port-0 run announces a
   non-zero port (or unit-test the announce helper).
3. `internal/server/routes.go`: `middleware.CORSWithConfig` — AllowOrigins
   `tauri://localhost`, `http://tauri.localhost`, `http://localhost:5173`,
   `http://127.0.0.1:5173`; AllowHeaders Content-Type, Accept. Tests:
   preflight OPTIONS echoes the origin; GET with desktop Origin carries
   ACAO; no-Origin and unlisted-Origin requests get no ACAO. SSE shares
   the same middleware chain (covered structurally; verified e2e by the
   any-ui tauri-dev smoke).
4. Docs sweep: `docs/02-server.md` (listen contract, LISTENING line, the
   desktop shell's reliance on POST /v1/shutdown), `docs/03-api.md` "No
   CORS" line + `CLAUDE.md` "no CORS in v1" line (named exception),
   `internal/server/web.go` same-origin comment, `docs/05-config.md`
   (embedded fallback).
5. Acceptance: `go test ./...` green; `./any run --addr 127.0.0.1:0
   --data-dir <tmp>` from any CWD prints `LISTENING 127.0.0.1:<port>`;
   end-to-end: `pnpm tauri dev` from the any-ui worktree boots to a
   working window (unblocks any-ui PR #162's smoke).
