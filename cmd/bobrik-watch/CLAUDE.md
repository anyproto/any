# bobrik-watch — guidance for Claude Code

User-facing overview lives in [`BOBRIK.md`](BOBRIK.md). This file is the
implementation-status / gotchas surface for Claude Code working in
this directory.

## What it is

JS-powered chat agent (`cmd/bobrik-watch/`). Embeds the
`anytype-agent-runtime` (Sobek JS engine) and runs the full
assistantjs stack (init_agent → toolcall_core → LLM) against the
`any` HTTP API.

## Storage shape

- **Properties are per-type namespaced.** An object can carry multiple
  types; each type owns its property bag. anyHelper resolves readable
  `"<Type>.<prop>"` keys to the server's internal ids (catalog-backed)
  on write, and reverse-maps records to nested readable form on read
  (`obj["Type"].prop`, via `getProp(obj,"Type.prop")`). Builtin
  namespaces stay literal (`obj.name`, `obj.any.types`,
  `obj.nav.parentId`, `obj.program.name`). Writes go one
  `properties/:objId/base/:typeId` PATCH per type; unknown-prop /
  wrong-kind writes fail loudly (server validation). No flatten-to-top-
  level, no first-type guessing (both were legacy anytypeHelper hacks).
- **Programs** — type `program` (built-in), datasets
  `program_source` (code), `program_description` (tool docs), and
  `program_methods` (per-method docs, currently unused). Registered
  in `internal/program/program.go`.
- **Mini apps** — type `Mini App` (`mini_app`, built-in,
  `internal/miniapp/miniapp.go`), dataset `mini_app` with one `main`
  record `{source, state, readme}`. `programs/miniapp.js` reads/writes
  it via `anyHelper.getRecord`/`setRecord` (per-field atomic) — no
  markdown-block parsing.
- **Skills** — type `Agent Skill`, property `agent_skill_name` (under
  the `Agent Skill` namespace). Content stored via `editor/markdown`.
- **Tool descriptions** — `cmd/bobrik-watch/tool-descriptions/*.md`,
  written to `program_description` at sync time. MUST contain a
  `## Tool Description` heading; `anyHelper.saveProgram` rejects
  markdown without it.
- **Debug pages** — the JS kernel's debug collector (`dcInit` in
  `toolcall_core@v1.js`) creates one `Agent Debug Log` page per agent
  invocation. The Go side ensures a `Debug` nav folder
  (`ensureDebugFolder`, nested under "System Bobrik Files") and passes
  its id to the runtime as `env.ANY_DEBUG_FOLDER_ID`
  (`runtime.go`/`runAgent`). `init_agent.js` forwards it to
  `createClient` (→ `client.config.debugFolderId`), and `dcInit` files
  the page there via `client.addToCollection`. Don't add a second,
  Go-side debug writer — that just duplicates the page. Because the
  folder is wiped/recreated on every `--bootstrap` refresh, its id
  changes, so `debugFolderID` is guarded by `debugFolderMu` (signal
  goroutine writes, subscribe loop reads). Folder find/create is
  shared via `ensureNavFolder` / `findNavFolder`.

## Naming

- `anyHelper.js` replaces `anytypeHelper.js`, same method surface.
- Property-key prefixes are dropped: legacy `__anytype_`/`__any_`/`__amemory_`
  prefixes existed to avoid collisions in a flat namespace; per-type
  namespacing makes them redundant (`Agent Memory.chat_id`, not
  `__any_chat_id`; `Agent Memory.vector`, not `__amemory_vector`).
  Migrated: init_agent, toolcall_core, miniapp, amemory@v2.
- amemory categories live in the bare `tags` array on the `Agent Memory`
  type (no per-tag prefix). Category filtering is server-side via the
  any-store array filter (`getObjects("Agent Memory", {filter:{"Agent
  Memory.tags":{$in:[...]}}})` — scalar = "contains", `$in` = "intersects");
  `_buildCategoryFilter` still enforces exact `m.category` afterwards. FTS
  (`client.search`) is still a stub — the keyword half of recall is inert,
  vector similarity carries.
- Env vars in JS programs (`env.ANYTYPE_API_URL`, etc.) keep their
  original names — they come from the runtime's `args` injection.

## Source on disk, not embedded

`anyHelper.js`, `skills/`, and `tool-descriptions/` are read live
from the parent of `--programs-dir` (defaults to `cmd/bobrik-watch/`)
at sync time — edits to those files take effect on the next refresh
without rebuilding the binary. That was the whole point of
SIGHUP-driven bootstrap; embedding pinned the JS to the binary
timestamp.

## Refresh via SIGHUP

On startup writes its PID to `./.bobrik-pid`.
`kill -HUP $(cat .bobrik-pid)` — or, equivalently,
`bobrik-watch --bootstrap` — deletes the "System Bobrik Files"
folder and every object parented under it (children first, then the
folder), then re-runs the bootstrap — `ensureSystemFolder` +
`syncPrograms` + `syncSkills` + `ensureDebugFolder` — so the next agent run picks up the
latest `anyHelper.js`, programs, skills, and tool descriptions from
disk. Subscribe loop stays up across the refresh; types (`Program`,
`Agent Skill`) are not recreated. SIGINT/SIGTERM remove the PID file
before exit.

## Program name validity

A program name must be a valid JS identifier
(`[A-Za-z_$][A-Za-z0-9_$]*`). The agent's boot prelude emits
`var <name>;` per tool, so a `-`/`.`/leading-digit name would crash
bootstrap with `Unexpected token`. `getTools()` silently skips
offenders (legacy data) and `anyHelper.saveProgram` rejects them on
write, so the bad name never reaches storage from inside the agent.

## Build / run

```
make build                                        # builds both any and bobrik-watch
./bin/bobrik-watch                                # default: space=bobrik, chat=bobrik
./bin/bobrik-watch --addr 127.0.0.1:7002          # point at a different server
./bin/bobrik-watch --bootstrap                    # SIGHUP a running instance
```
