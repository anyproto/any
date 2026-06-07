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

- **Properties are per-type namespaced, keyed by xKey.** An object can carry
  multiple types; each type owns its property bag. Dotted paths use the
  type's **xKey** (a stable snake_case slug of the name — `"Agent Skill"` →
  `agent_skill`; builtins use their id) plus the property xKey:
  `getProp(obj,"agent_skill.agent_skill_name")`. `createType` derives+returns
  `type.xKey` (or pass `opts.xKey`); the xKey survives display-name renames
  (needs SDK `add-type-xkey`; the type meta stores `any.xkey`). anyHelper
  resolves name/xKey/id for the `typeKey` *argument*, but dotted *paths*
  (reads, filter, sort) must use the xKey — reads come back keyed by it.
  **Property writes are nested type groups mirroring the read shape**:
  `createObject("book", { name, book: { author: "..." } })` /
  `updateObject(id, { book: { rating: 9 } })` — top-level data keys other
  than `name`/`body`/`markdown`/`types`/`space` must resolve to a type
  xKey/id or the call errors (`data.properties` and dotted write keys are
  gone; a silently-dropped misplaced key once lost a whole batch). Builtin
  namespaces stay literal (`obj.name`, `obj.any.types`, `obj.nav.parentId`,
  `obj.program.name`); `nav` writes are just the `nav` group. Writes go one
  `properties/:objId/base/:typeId` PATCH per type; unknown-prop / wrong-kind
  writes fail loudly (client resolver + server validation). No
  flatten-to-top-level, no first-type guessing (both were legacy
  anytypeHelper hacks).
- **Programs** — type `program` (built-in), datasets
  `program_source` (record `main`, field `code`), `program_description`
  (record `main`, field `text` — the `## Tool Description` BODY only),
  and `program_methods` (one record per method: id = bare method name,
  fields `{name, kind, text, pos}` where `name` is the heading with
  signature but WITHOUT the kind tag, e.g. `createType(opts)`; renderers
  reconstruct `### name [kind]` + blank line + text). Registered in
  `internal/program/program.go`. Boolean property `program.any_tool`
  marks toolhood: set true by the writers iff the docs carry a non-empty
  description AND ≥1 method; `anyHelper.getTools` filters STRICTLY on
  it (description presence no longer implies toolhood). Readers use
  `anyHelper.getToolDocs(toolId)` → `{description, methods}`; the
  toolcall_core boot builds prompts and `describeMethod` from that.
  Writers are `sync.go::upsertProgram` (Go, splits via
  `toolmd.go::splitToolMarkdown`) and `anyPrograms.saveProgram` (JS,
  `_splitToolMarkdown` — keep the two splitters in sync); both
  reconcile stale method records on re-save. `saveProgram`/`saveTool`
  are GONE from anyHelper — program writes live in anyPrograms only;
  `editProgram` is deliberately source-only (round-tripping markdown
  through a tool save would wipe `program_methods`).
- **Mini apps** — type `Mini App` (`mini_app`, built-in,
  `internal/miniapp/miniapp.go`), dataset `mini_app` with one `main`
  record `{source, state, readme}`. `programs/miniapp.js` reads it via
  `anyHelper.getObjects({objectId, dataset})` and writes via `setRecord`
  (per-field atomic) — no markdown-block parsing.
- **Skills** — type `Agent Skill`, property `agent_skill_name` (under
  the `Agent Skill` namespace). Content stored via `editor/markdown`.
- **Tool descriptions** — `cmd/bobrik-watch/tool-descriptions/*.md`,
  SPLIT into `program_description` + `program_methods` at sync time
  (see Programs above). Authored as one file: a `## Tool Description`
  section plus a `## Tool Schema` section with `### method(sig) [kind]`
  subsections (kind ∈ getter|mutator|setup|program, default getter).
  Both sections are required for toolhood; `anyPrograms.saveProgram`
  rejects markdown missing either.
- **Debug logs** — `Agent Debug Log` is a server **built-in** type
  (`internal/agentdebug`, registered in `internal/server/sdk.go`) with a
  structured `agent_debug_log` dataset — NOT a runtime-`createType`'d type
  and no longer a markdown page. The JS kernel's debug collector (`dcInit`/
  `dcLogInitialContext`/`dcLogTurn`/`dcFlush` in `toolcall_core@v1.js`)
  creates one object per agent invocation (named after the prompt, empty
  body) and writes an ordered **array** of entry records into the dataset via
  `anyHelper.setRecord` — one record per entry, each with a monotonic `seq`
  and a `kind` (`boot` | `system_prompt` | `turn` | `done`). Read back sorted
  by `seq` (or by the zero-padded record id) via `getObjects({objectId,
  dataset:"agent_debug_log", sort:["seq"]})`. A `turn` record holds the
  extracted scalars (`stopReason`/`inTokens`/`outTokens`/`durationMs`),
  `cells[]` (`{code, result, isError, executed}`), and the raw `response{}` —
  one message object, small, and the only home for the per-turn assistant
  narration text, cache counters (`cache_read_input_tokens` & co), and
  tool_use ids. The full `messages[]` window is intentionally NOT stored
  (repeats the whole conversation every turn → quadratic growth;
  reconstructible from chat history + prior turn records).
  The Go side ensures a **root-level** `Debug` nav folder
  (`ensureDebugFolder` — find-or-create only: an existing folder is
  reused untouched, never reparented or recreated) and passes its id
  to the runtime as `env.ANY_DEBUG_FOLDER_ID` (`runtime.go`/`runAgent`).
  `init_agent.js` forwards it to `createClient`
  (→ `client.config.debugFolderId`), and `dcInit` files the page there
  via `client.addToCollection`. Don't add a second, Go-side debug
  writer — that just duplicates the page. The folder deliberately sits
  OUTSIDE "System Bobrik Files" so `--bootstrap` (SIGHUP) refreshes —
  which wipe the system folder's children — never delete it; traces
  accumulate across refreshes. (It used to be nested inside; every
  refresh orphaned the traces.) The folder id is stable, but SIGHUP
  still re-runs bootstrap and rewrites `debugFolderID`, so it stays
  guarded by `debugFolderMu` (signal goroutine writes, subscribe loop
  reads). Folder find/create is shared via `ensureNavFolder` /
  `findNavFolder`.

## Naming

- `anyHelper.js` replaces `anytypeHelper.js`, same method surface.
- Property-key prefixes are dropped: legacy `__anytype_`/`__any_`/`__amemory_`
  prefixes existed to avoid collisions in a flat namespace; per-type
  namespacing makes them redundant (`agent_skill.agent_skill_name`, not
  `__any_agent_skill_name` — the first segment is the type xKey).
  Migrated: init_agent, toolcall_core, miniapp.
- **Memory + chat history are server-side built-in datasets now**
  (`convmemory@v1.js` over `internal/agentlog` + `internal/agentmem` —
  see docs/11-agent-memory.md). `agent_turns`/`agent_chunks` live on the
  chat object (append-only turn records; chunks carry `fromSeq..toSeq`
  drill-down pointers); `agent_memory_items` lives on the seed-derived
  per-space brain object with a real `category` field (the old
  category-in-`tags[0]` convention is gone, as are the hex vector
  properties, the `_main` anchor object, and the rolling-markdown
  transcript). amemory@v2 and memory-bootstrap are DELETED. Semantic
  recall is a non-functional TODO until the external vector-search
  service lands; `convmemory.search` falls back to indexed
  period/category/recency reads.
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
offenders (legacy data) and `anyPrograms.saveProgram` rejects them on
write, so the bad name never reaches storage from inside the agent.

## Build / run

```
make build                                        # builds any, bobrik-watch, any-agent-runtime
./bin/bobrik-watch                                # default: space=bobrik; watches the DERIVED primary chat
                                                  # (same seed as any-ui: btoa('any-ui/primary-chat/v1') —
                                                  #  TEMP shared-seed convention, contract TBD)
./bin/bobrik-watch --addr 127.0.0.1:7002          # point at a different server
./bin/bobrik-watch --bootstrap                    # SIGHUP a running instance
./bin/any-agent-runtime -e .env script.js k=v     # run one JS file with PRODUCTION module
                                                  # resolution (imports resolve from the `any`
                                                  # space via internal/anyrt — same loader as
                                                  # bobrik-watch; -m adds a file-dir fallback)
```

The runtime wiring (anySDK module loader + effect/global setup) lives in
`internal/anyrt/` and is shared by bobrik-watch, `cmd/any-agent-runtime`,
and (via the latter) the JS integration tests — one loader, three
consumers, no test/prod resolution drift. Module imports are resolved
per-import over HTTP with NO caching, by design: editing a program in the
space (anyPrograms.saveProgram / editProgram) is live on the next message;
disk edits land via SIGHUP re-sync. Don't add a cache here.
