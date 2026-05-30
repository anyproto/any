# bobrik-watch

A JS-powered chat agent that runs on top of the `any` HTTP API. It
subscribes to a chat in a space and responds to human messages using
the [anytype-agent-runtime](https://github.com/anyproto/anytype-agent-runtime)
(Sobek JS engine).

## How it works

On startup bobrik-watch:

1. Ensures a space and chat object exist (creates them if missing).
2. Creates the `Program` and `Agent Skill` types with their properties.
3. Ensures the "System Bobrik Files" nav folder; everything synced
   below is parented under it so `--bootstrap` (SIGHUP) can wipe it
   for a clean refresh.
4. Syncs JS programs from `cmd/bobrik-watch/programs/` (stored in the
   `program_source` dataset) plus the sibling `anyHelper.js`. For
   each tool, the matching `tool-descriptions/<name>.md` is written
   to `program_description` (must contain a `## Tool Description`
   section).
5. Syncs agent skills from `cmd/bobrik-watch/skills/` (stored as
   editor/markdown content on skill objects).
6. Ensures a `Debug` nav folder nested under "System Bobrik Files"
   (so `--bootstrap`/SIGHUP wipes and recreates it with everything
   else). Its id is passed to the agent runtime as
   `env.ANY_DEBUG_FOLDER_ID`.
7. Writes its PID to `./.bobrik-pid` and subscribes to the chat's
   `chat_messages` SSE stream.

On each human message (no `fromAgent` field):

1. Creates a fresh Sobek JS runtime with `SetupAnySDKDirtyRuntime`.
2. Registers a `chatReply` effect that posts replies back to the chat.
3. Evaluates a wrapper that imports `private:init_agent@v1`, which
   bootstraps types, loads skills, boots the LLM kernel
   (`toolcall_core@v1`), and generates a response.

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
| `--space` | `bobrik` | Space name (created if missing) |
| `--chat` | `bobrik` | Chat object name (created if missing) |
| `--agent-name` | `bobrik` | `fromAgent` tag on replies |
| `--programs-dir` | `cmd/bobrik-watch/programs` | Directory with .js program files |

### Pointing at a different server

```sh
./bin/bobrik-watch --addr 127.0.0.1:7002 --space myspace --chat mychat
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
  a `__any_agent_skill_name` property. Content is stored as markdown via
  the `editor/markdown` endpoint.

## What's missing

- **Search** — `anyHelper.search()` returns empty. The `any` API has no
  full-text search indexer yet.
- **Tags** — `addTag`, `listTags`, etc. return errors. No select/multi_select
  property format in the `any` API yet.
