# Authoring brief for the any docs website (read fully before writing)

You are writing pages of the public documentation website for **any** — an open source,
reactive, local-first, CRDT, end-to-end-encrypted database with Mongo-style queries, built-in
chat and editor CRDTs, and (via **anybao / anyrt**) a sandboxed Python runtime for scheduled
jobs and agents that live inside the user's own encrypted data. The site is modelled on
docs.convex.dev (tone, structure, depth): technical but accessible, one concept per page,
every feature shown with a runnable example, and short callouts explaining *why* a design
differs from hosted backends.

Repos: any server = /home/zarkone/any/any (docs in docs/*.md, CLAUDE.md is an accurate
status index — READ the relevant docs/NN-*.md for every page; they are the source of
truth). anybao/anyrt = /home/zarkone/anybao (docs/adr/*.md are canonical contracts;
README.md, CLAUDE.md, docs/debugging.md, docs/effects/syscalls.md, repos/CLAUDE.md,
repos/_agent/programs, repos/_connectors/programs). Never invent behavior: if a source doc
says something, say that; if it isn't documented, don't claim it. Be accurate about wire
shapes (paths, JSON fields, error codes) — copy them from the docs.

## Hard rules
- Files: `website/<NN-slug>/<page>.md`, FLAT (no subfolders). `index.md` in each section is
  the section Overview. Front-matter on every page:
  ```
  ---
  title: Reading data
  description: One sentence, used in the index and llms.txt.
  order: 30
  ---
  # Reading data
  ```
  `order` = position in the section (10, 20, 30…; index.md has order 0).
- First paragraph after the H1 = a 1–3 sentence lead (rendered larger).
- Present tense; describe current behavior only. No history ("used to", "now", "was
  replaced", "moved from"), no ticket ids (SYN-xx / PRO-xx / ADR numbers) in prose — the
  site is for users, not maintainers. No "v1 prototype" hedging.
- Links to other pages: relative `.html` links, e.g. from `03-database/objects.md` to
  `../realtime/subscribe.html` or same-section `reading-data.html`. Section slugs are the
  folder name minus the `NN-` prefix. Only link to pages in the inventory below.
- Examples: prefer `curl` against `http://127.0.0.1:7001/v1/...` with JSON bodies, plus
  the matching `any …` CLI line when one exists; for anybao, Python program snippets and
  `anyrt …` lines. Keep JSON examples small and correct.
- Callouts: `> **Why it matters.** …` for the local-first/encrypted/on-device advantage;
  `> **Note.** …` for caveats. Use them, but max ~2 per page.
- Use GFM tables for enumerations (fields, error codes, flags). Code blocks with language.
- Page length: 60–250 lines. Overviews (index.md) 40–120 lines and MUST end with a
  `<div class="cards">` block linking each page of the section:
  `<a href="page.html"><strong>Title</strong><span>one line</span></a>`.
- Raw HTML allowed. No images (none exist). ASCII diagrams in code fences are fine.
- Don't write README.md or files starting with `_`.

## Site inventory (folder → pages). Write ONLY your assigned section(s); link to others freely.
01-understanding/  index, local-first, encryption, crdt-and-consistency, zen-of-any, dev-workflow, best-practices, programs-and-effects
02-quickstart/     index, install, curl, cli, javascript, python, android, ios, anyrt, networks
03-tutorial/       index, objects, properties, datasets, apps
04-database/       index, spaces, objects, types-and-properties, data-types, reading-data, writing-data, indexes, runtime-datasets, upsert, aggregation, version-history, markdown-import-export, system-fields, derived-objects
05-realtime/       index, subscribe, sync-status, space-list, event-bus
06-auth/           index, accounts, devices, identities, profile
07-collaboration/  index, members-and-roles, invites, acl, one-to-one, derived-spaces, bundles
08-types/          index, chat, editor, page, links
09-files/          index, uploading, downloading, status-and-durability, cache, deleting
10-search/         index, full-text, vector, hybrid, indexing, embedders, evaluation
11-notifications/  index, push, processes
12-programs/       index, effects, writing-a-program, modules-and-overlays, traces-and-replay, credentials, testing, limits
13-scheduling/     index, cron, once, event-triggers, device-pins, runs-and-monitoring
14-agents/         index, conversations, tools, memory, subagents, connectors, agent-data, progress-and-ui, embedding-anyrt
15-operations/     index, server, configuration, data-dir, networks, security-model, builds-and-ci, debugging
16-testing/        index, any-e2e, anybao-harness
17-reference/      index, http-api, cli, events, errors, config, anyrt-cli, effects-catalog, trigger-schema, anybao-toml, glossary
roadmap.md (root)
