# PR #26 — Lexical editor spike

> Add Lexical as a second object-page editor engine without removing
> the existing BlockNote path. The goal is to prove the migration
> boundary, not to recreate the full Lexical Playground in one pass.

## Goal

- Install Lexical's React, rich-text, markdown, list, link, code,
  selection, and utility packages.
- Keep `MarkdownEditor` as the public app boundary.
- Add a persisted settings flag:
  - `any.editor.engine = "blocknote" | "lexical"`
  - default is `"lexical"` after the spike passes verification.
- Render BlockNote or Lexical behind that flag.
- Make the Lexical path load and save through the same markdown API:
  - `GET /v1/spaces/:s/objects/:o/markdown`
  - `PUT /v1/spaces/:s/objects/:o/markdown`
- Reuse the existing object title, type bar, save machine, and header
  status reporting.

## Decisions

### Editor boundary

`web/app/src/components/editor/MarkdownEditor.tsx` remains the only
component imported by view modules. Internally it reads
`editorEngineAtom` and delegates to the existing BlockNote
implementation or to `LexicalMarkdownEditor`.

This keeps the product shell and object view stable while the editor
engine evolves.

### Lexical scope

The first Lexical slice includes:

- paragraphs
- headings
- quote blocks
- bullet, numbered, and checklist lists
- code blocks and inline code
- bold, italic, strikethrough
- markdown import/export and markdown shortcuts
- a minimal slash menu
- a minimal block menu with an insert-below button and drag handle.
  The drag targeter is app-local instead of Lexical's experimental
  draggable block plugin because checklist/list items must move as
  independent blocks, not as one parent list node. The visible grip now
  drives the move with pointer events, while Lexical drop commands and a
  native captured drop fallback stay in place for browser drag/drop
  compatibility. All paths route through the same list-aware move helper.

The initial compact local toolbar was removed after UX review because
it made the page feel too heavy. Inline formatting now lives in a
contextual selection/right-click plugin, not as permanent editor
chrome.

Markdown persistence remains the limiting line: bold, italic,
strikethrough, inline code, links, and text-case changes round-trip
through markdown. Underline, subscript, and superscript are available
as Lexical editor formats for the live editor surface but are not
lossless across reload until we add custom markdown/HTML transformers
or move object bodies to richer editor storage.

Checklist markdown uses a local `MARKDOWN_TRANSFORMERS` list that puts
a checklist transformer before unordered lists. Lexical's default
transformer set does not include checklists, and ordering matters
because `- [ ]` otherwise imports as a plain bullet whose text starts
with markdown syntax. The local transformer also accepts an empty
checkbox line at end-of-line and normalizes empty checklist export to
`- [ ]` / `- [x]` without relying on trailing spaces.

The following remain explicitly out of scope for this first spike:

- tables
- images/files
- comments
- collaboration
- custom object/property blocks
- full Playground feature parity

Follow-up plugin choices are tracked in
`docs/specs/PR-027-lexical-plugin-roadmap.md`.

### Persistence

Lexical is still persisted as markdown, not Lexical JSON. This keeps
the backend contract unchanged and lets the user switch between
BlockNote and Lexical while the migration is evaluated.

Hydration is scoped to object identity. `LexicalHydrationPlugin` and
the BlockNote fallback import markdown when an object is opened or
switched, but ignore same-object query-cache refreshes caused by
autosave. Re-importing saved markdown for the object currently being
edited rebuilds the editor tree and moves the user's selection to the
beginning of the page, so live same-object updates must wait for a
richer incremental/collaborative editing path.

The Lexical path also keeps the latest markdown payload in a ref as
soon as `OnChangePlugin` reports it, and markdown saves update the
TanStack Query cache optimistically in `onMutate`. This protects
structural edits such as empty checkbox blocks when the user switches
away and back before the debounced save round-trip finishes. Markdown
queries are treated as locally authoritative for the app session
(`staleTime: Infinity`) for the same reason: an eager background GET
after remount can otherwise race the pending save and overwrite the
cache with older server content.

### Settings

The editor engine flag is local UI preference state, like theme. It
uses `appStorage()` so browser, tests, and constrained storage
contexts share the same Jotai storage contract.

## Files

```
docs/specs/PR-026-lexical-editor-spike.md
docs/agents/README.md
web/app/package.json
web/app/pnpm-lock.yaml
web/app/src/atoms/settings.ts
web/app/src/components/settings/AppSettingsDialog.tsx
web/app/src/components/editor/MarkdownEditor.tsx
web/app/src/components/editor/LexicalMarkdownEditor.tsx
web/app/src/components/editor/LexicalBlockMenu.tsx
web/app/src/components/editor/editorHydration.ts
web/app/src/components/editor/lexicalMarkdownTransformers.ts
web/app/src/components/editor/lexical-theme.css
```

## Acceptance criteria

1. Settings exposes an **Editor engine** choice with BlockNote and
   Lexical.
2. Lexical is the default and BlockNote remains available as a
   fallback in settings.
3. Switching to Lexical renders the same object title and type bar.
4. Lexical loads existing markdown content.
5. Editing Lexical content marks the document dirty, debounces saves,
   and reports status to the object header.
6. Switching objects flushes pending Lexical saves.
7. Hovering a top-level block shows the insert-below and drag controls.
8. `pnpm typecheck`, `pnpm lint`, and focused tests stay green.
