# bobrik-watch

A JS-powered chat agent that runs on top of the `any` HTTP API. It
subscribes to a chat in a space and responds to human messages using
the [anytype-agent-runtime](https://github.com/anyproto/anytype-agent-runtime)
(Sobek JS engine).

The watched space is the agent's **private home** (chat, programs,
skills, memory, debug logs), but the agent is not confined to it: every
anyHelper method takes a `space` option accepting any space id, plus
`listSpaces()` / `createSpace()` / `getUIContext()` — the latter reads
the `ui-context` pointer object the web UI keeps updated with the
space/object the user is currently viewing, so "summarize this page"
works from the one chat the UI surfaces everywhere. See "Cross-space
model" in [CLAUDE.md](CLAUDE.md).

## How it works

On startup bobrik-watch:

1. Ensures a space and chat object exist (creates them if missing).
2. Creates the `Program` and `Agent Skill` types with their properties.
3. Ensures the "System Bobrik Files" nav folder; everything synced
   below is parented under it so `--bootstrap` (SIGHUP) can wipe it
   for a clean refresh.
4. Syncs JS programs from `cmd/bobrik-watch/programs/` (stored in the
   `program_source` dataset) plus the sibling `anyHelper.js`. For
   each tool, the matching `tool-descriptions/<name>.md` is SPLIT on
   write: the `## Tool Description` body goes to `program_description`,
   each `### method(sig) [kind]` subsection of `## Tool Schema` becomes
   one `program_methods` record (`{name, kind, text, pos}`, id =
   method name), and `program.any_tool` is set true. Programs without
   a description file get `any_tool: false` and are not tools.
5. Syncs agent skills from `cmd/bobrik-watch/skills/` (stored as
   editor/markdown content on skill objects).
6. Ensures a `Debug` nav folder nested under "System Bobrik Files"
   (so `--bootstrap`/SIGHUP wipes and recreates it with everything
   else). Its id is passed to the agent runtime as
   `env.ANY_DEBUG_FOLDER_ID`.
7. Writes its PID to `./.bobrik-pid` and subscribes to the chat's
   `chat_messages` SSE stream.

On each human message (no `agent` field):

1. Creates a fresh Sobek JS runtime with `SetupAnySDKDirtyRuntime`.
2. Evaluates a wrapper that imports `private:init_agent@v1`, which
   bootstraps types, loads skills, boots the LLM kernel
   (`toolcall_core@v1`), and generates a response.

Replies are posted through the native chat API
(`anyHelper.sendChatMessage`) from inside the agent — the chat id is threaded
in as `args.chatId`. There is no host-side `chatReply` effect; the only
Go-side post is `chatSend`, a fallback used when the runtime itself fails to
start (so the typing indicator still resolves).

The kernel's debug collector (`dcInit` in `toolcall_core@v1.js`) creates
one `Agent Debug Log` page per invocation and appends each turn live.
When `env.ANY_DEBUG_FOLDER_ID` is set, the page is filed under the
`Debug` nav folder (`client.addToCollection`); otherwise it stays at
root. Those pages are wiped on the next `--bootstrap` refresh along with
the rest of "System Bobrik Files".

## Build

```sh
make build
# produces ./bin/bobrik-watch
```

## Run

Start the `any` server first:

```sh
./bin/any run
```

Then in another terminal:

```sh
./bin/bobrik-watch
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `127.0.0.1:7001` | `any` server address (host:port) |
| `--space` | `bao` | Space name (created if missing) |
| `--agent-name` | `bao` | `agent.name` display label on replies |
| `--programs-dir` | `cmd/bobrik-watch/programs` | Directory with .js program files |

There is no `--chat` flag: the watched chat is the space's chat object named
**`general`**, found-or-created by name + chat type. Clients (Desktop UI, etc.)
create a `general` chat in each space by convention; bobrik watches that one,
or mints it when it owns the space (e.g. the dev `bao` space has no other
client to create it). This replaced the earlier deterministic-derive scheme
(the `btoa('any-ui/primary-chat/v1')` seed), which coupled bobrik to a UI
internal constant.

### Pointing at a different server

```sh
./bin/bobrik-watch --addr 127.0.0.1:7002 --space myspace
```

## Architecture

```
cmd/bobrik-watch/
  main.go          — startup, SSE subscribe loop, message dispatch
  runtime.go       — SetupAnySDKDirtyRuntime (effects, globals, module resolver)
  anyloader.go     — module resolver: queries program objects via any API
  sync.go          — syncs .js programs and .md skills into the space
  anyHelper.js     — embedded JS library (drop-in for anytypeHelper)
  programs/        — JS agent programs (copied from assistantjs, imports rewritten)
  skills/          — agent skill markdown files
```

### Key components

- **`anyHelper.js`** — JS library with the same `createClient()` API surface
  as `anytypeHelper.js`, backed by the `any` HTTP API instead of the Anytype
  API. Read from disk at sync time (sibling of `--programs-dir`) so edits
  take effect on `--bootstrap` without rebuilding the binary.

- **Module resolver** (`anyloader.go`) — resolves `import "name@version"`
  by querying program objects in the space (`program.name` + `program.version`
  properties), then reading source from the `program_source` dataset.

- **Program storage** — each program is an object of type `program` with
  `name` and `version` properties. Source code lives in the `program_source`
  dataset (not in markdown/editor blocks), avoiding the 65KB per-block limit.

- **Skill storage** — each skill is an object of type `Agent Skill` with
  a `agent_skill_name` property. Content is stored as markdown via
  the `editor/markdown` endpoint.

- **Conversation history + memory** (`convmemory@v1.js`,
  docs/11-agent-memory.md) — structured server-side datasets, no
  markdown transcripts:
  - `agent_turns` on the chat object — one append-only record per
    agent invocation (user text, reply bubbles, effects, debugRef →
    debug page, llm scalars). Kept forever; the boot context is a
    bounded `sort:["-seq"] limit:N` query, not a trimmed file.
  - `agent_chunks` on the chat object — LLM-compressed summaries
    carrying explicit `fromSeq..toSeq` pointers, so any summary the
    agent sees expands back to the exact raw turns
    (`convmemory.expandChunk`).
  - `agent_memory_items` on the per-space brain object
    (`GET /v1/spaces/:id/agent/brain`, seed-derived) — typed memory
    items (category/context/body, tag/entity/keyword arrays,
    confidence/importance/salience).

## What's missing

- **Vector-backed semantic search** — externalized to a separate
  vector-search service that does not exist yet (docs/07-roadmap.md
  §9). `convmemory.search` runs in degraded mode (period / category /
  recency via indexed queries); similarity ranking, dedup, and the old
  link-gen/evolution/reflect/decay passes are dormant until the
  service lands. No vectors are stored in the datasets. Interim
  semantic recall is the `search` tool (RLM loop over the datasets,
  docs/12-rlm-search.md) — slower and token-priced, but functional.
- **FTS** — there is no full-text indexer in the `any` API.
  `anyHelper.search()` is REMOVED (a stub returning `[]` that agents
  kept reaching for and silently getting nothing — see the VM-boot
  debug log analysis). Use the `search` tool, or `getObjects` with a
  `$regex` filter for exact substrings.
- **Tags** — `addTag`, `listTags`, etc. return errors. No select/multi_select
  property format in the `any` API yet.
