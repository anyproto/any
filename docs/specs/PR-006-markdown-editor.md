# PR #6 — Markdown editor (BlockNote)

> Spec for the sixth frontend PR. Pane 3 stops being a placeholder:
> selecting an object renders a real, editable BlockNote document
> backed by `GET/PUT /v1/spaces/:s/objects/:o/markdown`.

## Goal

- Pane 3 shows the current object's body as a rich-text editor.
- Edits round-trip through the markdown endpoints (the server diffs
  per-block and emits the upsert/delete batches — see
  `internal/server/handlers_markdown.go`).
- Saves are debounced (~800 ms after the last keystroke).
- A small status indicator in the header shows **Saved / Saving… /
  Error** based on the editor's state machine.
- Switching objects flushes the pending save before loading the new
  document.

After this PR the app is meaningfully usable: create a page, name it,
write in it, switch, come back, the content is still there.

## Non-goals

- File / image uploads. SDK defers files to v1.1.
- Slash-menu customization beyond what BlockNote ships.
- Collaborative editing presence (cursors, remote selections).
- Undo across the network. We rely on the editor's local undo stack;
  remote conflict surface is the `'conflict'` state below, displayed
  but not auto-merged in v1 — the "next save wins" pattern.
- Object metadata editing (title, properties). Title rename is in
  PR #5; properties are PR #7.

## API surface used

| Method | Path | Purpose |
|--------|------|---------|
| `GET`  | `/v1/spaces/:s/objects/:o/markdown` | Read body. Returns `{content: string}`. |
| `PUT`  | `/v1/spaces/:s/objects/:o/markdown` | Replace body. Body `{content: string}`. Server diffs per-block; returns `{inserted, updated, deleted, unchanged}`. |

Both routes are real, no `501`. Per-block diff means concurrent edits
in different blocks merge cleanly server-side; same-block edits are
"last write wins."

## Decisions

### Editor: BlockNote, paired with `@blocknote/mantine` for the UI

The architecture doc explicitly rejected `@blocknote/mantine`. After
investigation that turns out to be impractical for v1 — without a
prebuilt UI package (`@blocknote/mantine`, `@blocknote/shadcn`, or
`@blocknote/ariakit`), we'd be implementing the slash menu, formatting
toolbar, link popover, and side menu by hand. That's a multi-week
detour, not PR #6.

**Pivot from the architecture doc:** install `@blocknote/mantine` for
the prebuilt UI surfaces, then layer our `tokens.css` over its CSS
variables (BlockNote exposes `--bn-colors-editor-text`,
`--bn-colors-menu-background`, etc.). The result is BlockNote
scaffolding with our token palette, no Mantine visual signature.

If/when we want to ship our own slash menu / formatting toolbar,
we swap the UI package for the headless `BlockNoteViewRaw` and
re-implement the menus — a separate PR, not a precondition.

I'll amend `docs/08-app-architecture.md` to reflect this decision in
the same commit.

### Save state machine

A discriminated union driven by `useReducer` (no XState yet — first
real machine in this codebase, per the architecture doc):

```ts
type SaveState =
  | { kind: 'loading' }
  | { kind: 'load_error'; error: ApiError }
  | { kind: 'idle';   savedContent: string }                                 // matches server
  | { kind: 'dirty';  savedContent: string; nextContent: string; since: number }
  | { kind: 'saving'; savedContent: string; inflightContent: string }
  | { kind: 'save_error'; savedContent: string; nextContent: string; error: ApiError };
```

Transitions:
- `loading → idle` on first GET success.
- `loading → load_error` on GET fail.
- `idle → dirty` on local change.
- `dirty → saving` after debounce (800 ms).
- `saving → idle` on save success when nothing changed during flight.
- `saving → dirty` on save success when the editor changed during flight.
- `saving → save_error` on failure.
- `save_error → saving` on retry / next change after backoff.
- `(any with savedContent) → loading` on object switch.

### Debounce / batching

- 800 ms after the last `editor.onChange`. (Ad-hoc default —
  matches Notion-ish feel.)
- One in-flight save at a time; subsequent changes mark the doc
  dirty again and schedule another save after the current one
  resolves.

### Switching objects

Object switch happens when `activeObjectIdAtom` changes.
`MarkdownEditor` is keyed by object id so it remounts; before
unmounting, we **flush** any pending save synchronously (await the
mutation). If the save fails, we surface a toast — switching
proceeds either way (we don't block navigation).

### Save status surface

A small text indicator in the pane 3 header, right side. States:
- `loading` → "Loading…"
- `idle`    → "Saved" (faint)
- `dirty`   → "Editing…"
- `saving`  → "Saving…"
- `save_error` → "Save failed — Retry" (button)
- `load_error` → render an error card *instead of* the editor.

## Files changed / added

```
docs/specs/PR-006-markdown-editor.md
docs/08-app-architecture.md            (BlockNote/mantine pivot note)
web/app/package.json                   (+ @blocknote/{core,react,mantine})
web/app/src/lib/api/markdown.ts        + .test.ts
web/app/src/components/editor/
  MarkdownEditor.tsx                   BlockNote wrapper
  saveMachine.ts                       reducer + types  + .test.ts
  blocknote-theme.css                  CSS variable overrides
web/app/src/components/layout/
  ObjectView.tsx                       (renders MarkdownEditor + status)
  ObjectView.test.tsx                  (rewrite)
```

## Acceptance criteria

1. Selecting an object triggers GET `…/markdown`. Loading state shows briefly.
2. Server returns content → BlockNote loads it as blocks; cursor in the doc.
3. Typing → status changes from "Saved" → "Editing…" within one paint.
4. After ~800 ms of no typing → status goes "Saving…" → "Saved".
5. Continuing to type during a save schedules another save after the one in flight.
6. Switching to a different object flushes the pending save.
7. Killing the server then editing → "Save failed — Retry"; reading network requests shows the failed PUT.
8. The editor honours light & dark mode (CSS variables).
9. CI green; no a11y regressions.

## Test plan

- **Unit** (`markdown.ts`): GET decodes `{content}`, PUT shape, error envelope passthrough.
- **Unit** (`saveMachine.ts`): all transitions in the table above.
- **Component** (`MarkdownEditor`):
  - Renders content from initial fetch.
  - `onChange` updates state to `dirty`; advances time → mutation called.
  - Server error surfaces `'save_error'`.
- **axe**: BlockNote ships its own a11y; we assert no new violations
  on our wrapper (header / status indicator / error card).

## Open questions

- **Conflict handling.** The PR #6 spec ships "last save wins"; the
  architecture doc's full state machine includes a `'conflict'` state
  feeding `@pierre/diffs`. That's a follow-up — flag it in the doc.
- **Save-on-blur vs only on debounce.** Default debounce-only. Many
  editors also save on blur (window blur, tab change). Easy follow-up.
- **Editor width / typography.** Default to a 760 px max-width with
  the same 15 px / 28 px line height we used in the placeholder so
  the visual transition is gentle.
