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

- **Programs** — type `program` (built-in), datasets
  `program_source` (code), `program_description` (tool docs), and
  `program_methods` (per-method docs, currently unused). Registered
  in `internal/program/program.go`.
- **Skills** — type `Agent Skill`, identified by
  `__any_agent_skill_name`. Content stored via `editor/markdown`.
- **Tool descriptions** — `cmd/bobrik-watch/tool-descriptions/*.md`,
  written to `program_description` at sync time. MUST contain a
  `## Tool Description` heading; `anyHelper.saveProgram` rejects
  markdown without it.

## Naming

- `anyHelper.js` replaces `anytypeHelper.js`, same method surface.
- All `__anytype_` prefixes renamed to `__any_`.
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
`syncPrograms` + `syncSkills` — so the next agent run picks up the
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
