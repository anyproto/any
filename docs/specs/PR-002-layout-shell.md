# PR #2 — Layout shell

> Spec for the second frontend PR. Approval gate: doc merges (or
> reviewer signs off in the PR), then implementation lands on the
> same branch. Per docs/08-app-architecture.md § Layout & interaction
> model.

## Goal

Stand up the **3-pane desktop chrome** modelled on the Anytype reference
screenshot. **All data is mocked.** No `/v1/...` calls. No business
logic. The point is to lock the layout, the resize behavior, the
keyboard navigation, the empty states, and the light/dark token
behavior — *before* real data introduces noise.

After this PR a reviewer can:

- Open `/ui` and see a 3-pane layout that looks like the reference.
- Drag the dividers between pane 1↔2 and 2↔3 to resize.
- Hit `⌘1` / `⌘2` / `⌘3` to focus the corresponding pane.
- Toggle light/dark from anywhere via the existing theme toggle.
- Resize the window below 800 px and see a "use a wider window" notice.
- Reload the page and have pane widths persisted.

## Non-goals

- No `/v1/spaces`, `/v1/objects/query`, or any real API call.
  PR #3 wires real spaces; PR #4 wires the object tree.
- No tree drag-and-drop; PR #7.
- No editor; PR #5.
- No object create / rename / delete flows; PR #4.
- No command palette (`⌘K`); deferred to v1.1.
- No `?` keyboard-help overlay; deferred.

## Layout

```
┌────┬──────────────────┬───────────────────────────────────┐
│ 80 │       280        │            fills                  │
│    │                  │                                   │
│ ◯  │  Anytype Team ▾  │  ←  →  · Foo / Bar / Baz          │
│ ◯  │  ─────────────── │                                   │
│ ●  │  ⌖  Chats        │  Selected object body             │
│ ◯  │  🎲 Random       │                                   │
│ ◯  │  🔒 Security     │  (mock — placeholder block of     │
│ +  │  🤖 AI           │   text rendered with token colors)│
│    │  …               │                                   │
│    │                  │                                   │
│ ⚙  │  See All ▾       │                                   │
└────┴──────────────────┴───────────────────────────────────┘
  P1          P2                       P3
```

(Approximation. Pane 2 sections, list items, and pane 3 body all
mock data — see `src/lib/mock-data.ts`.)

### Pane 1 — Spaces rail

- Current implementation is an expanded spaces list by default
  (~280 px at 1280 px viewport), with a persisted close/open toggle
  that animates pane 1 to width 0.
- Open mode shows a header, filter input, rounded-square space
  avatars, labels, and trailing row metadata.
- Closed mode removes pane 1 from the interactive surface.
- Pane 1 is toggled from the object-view header, next to Back/Forward.
- Active space gets token-driven selection treatment while the pane is
  open.

### Pane 2 — Space contents

- ~280 px wide. Resizable 220–480; default 280.
- Can close to width 0. Pane 2 is toggled from the object-view header,
  next to Back/Forward, and reopens to the previous expanded width.
- Top: space header (avatar, name, ▾ menu — menu shows mock items).
- Body: sectioned list. Sections in mock:
  - Top-level chats / pages.
  - "Unread" (auto, mock badges).
  - "My Favorites" (pinned by user — mock 3 items).
  - "Types" (system list of types — mock 4 entries).
- Each row: leading icon, title, optional trailing badge.
- Selecting a row updates `activeObjectIdAtom`; pane 3 reflects it.

### Pane 3 — Object view

- Fills remaining width. No left edge resize (the divider is owned
  by pane 2's right edge).
- Top: back/forward buttons (no-op for now), breadcrumb of selection
  (mock).
- Body: when nothing selected → empty state ("Pick an item from the
  list"). When something selected → mock title + a few paragraphs of
  filler text rendered with the body type ramp.
- Bottom: nothing in v1; the chat composer in the screenshot is
  out of scope (no chats in our model).

## Decisions baked into PR #2

- **Library for splitter**: [`react-resizable-panels`](https://github.com/bvaughn/react-resizable-panels)
  (~6 kB). Native min/max, persisted via callback, accessible
  (keyboard resize). Avoids rolling our own drag handlers.
- **Persistence**: expanded pane widths persisted under
  `any.layout.widths.v3` via Jotai `atomWithStorage`. Pane-1 closed
  state persists separately under `any.layout.spacesRailClosed.v1` so
  the spaces rail can fully close. Pane-2 closed state persists
  separately under `any.layout.spaceContentsClosed.v1` for the same
  reason. When a closed left pane is reopened from the object header
  toggle, it starts at its minimum practical open width (pane 1 = 14 %,
  pane 2 = 17 %) instead of restoring an oversized old width; the user
  can resize wider after reopening. Pane widths are persisted
  independently whenever that specific pane is open, even if the other
  left pane is closed, so reopening pane 1 does not mutate pane 2's
  current compact width. Persisted widths seed the initial render; after
  mount, live refs updated only from real resize-handle drags are
  authoritative. `PanelGroup.onLayout` events emitted by programmatic
  toggles are ignored because the splitter redistributes freed space
  into sibling panes before the desired layout is applied; persisting
  those transient sizes is what makes pane 1 and pane 2 codependent. The
  toggle path applies the corrected `PanelGroup` layout in a layout
  effect so the fix lands before paint. The storage key is bumped if the
  panel count or order changes, or if a prior key could contain
  splitter-redistributed widths from a buggy toggle path.
- **Pane 1 performance**: space rows are virtualized once the list is
  large enough, row components are memoized, and per-row metadata reads
  use `selectAtom` so unrelated space metadata edits do not wake every
  row.
- **Min window width**: 800 px. Below that, the layout collapses to
  a centered notice ("Cowork is desktop-only in v1 — open a wider
  window"). No re-flow.
- **Keyboard shortcuts** (added to `docs/08-app-architecture.md`'s
  table):
  - `⌘1` / `⌘2` / `⌘3` — move browser focus to the first focusable
    element of pane 1 / 2 / 3.
  - Existing focus-ring rules apply.
- **Mock data lives in `src/lib/mock-data.ts`** and is imported only
  by feature components. PR #3 deletes this file when the real space
  list lands.

## Project layout (additions)

```
web/app/src/
├── atoms/
│   ├── layout.ts           pane width atoms (persisted)
│   ├── focus.ts            focused-pane atom (1|2|3)
│   └── selection.ts        active space + active object (mock ids)
├── components/
│   └── layout/
│       ├── AppShell.tsx          root: PanelGroup + window-size guard
│       ├── SpacesRail.tsx        pane 1
│       ├── SpaceContents.tsx     pane 2
│       ├── ObjectView.tsx        pane 3
│       ├── ResizeHandle.tsx      thin styled drag affordance
│       └── MinWidthGuard.tsx     <800 px notice
├── lib/
│   └── mock-data.ts        spaces, sections, items, sample bodies
└── pages/
    └── HealthPage.tsx      kept; mounted under pane 3 when no
                             selection so the existing /v1/health
                             card is still visible (will move to a
                             dedicated /settings page in PR #3+).
```

`App.tsx` renders `<AppShell />` instead of `<HealthPage />`.

## Acceptance criteria

A reviewer can verify each by hand on `pnpm dev` (`:5173`) or the
embedded binary at `:7001/ui`.

1. Visiting `/` shows the 3-pane layout — strip, sidebar, main area.
2. Both dividers drag to resize. Min/max enforced (cursor changes;
   panel won't shrink past min). Closed sidebars animate to width 0.
3. Reloading restores the last divider positions and pane-1 / pane-2
   closed state.
4. Click a space icon → its mocked contents render in pane 2.
5. Click an item in pane 2 → its mocked body renders in pane 3.
6. With nothing selected, pane 3 shows the empty state and (until
   PR #3) the existing health card below it.
7. `⌘1` / `⌘2` / `⌘3` move focus to the first interactive element
   of the corresponding pane. Visible focus ring.
8. Window resized below 800 px → centered notice, no overflow.
9. Light + dark mode both render correctly. Theme toggle works
   from the new pane 1 settings cog (or wherever it lives).
10. CI green: lint, typecheck, vitest, build.

## Test plan

- **Component**: SpacesRail / SpaceContents / ObjectView each render
  the expected mock shape; selection updates the relevant atom.
- **Component**: MinWidthGuard appears below 800, hidden above.
- **Atom**: pane widths atom round-trips localStorage.
- **Keyboard**: pressing `⌘1/2/3` (synthesized event) focuses the
  expected pane.
- **axe-core**: no violations on AppShell or any of the panes.
- **Visual snapshot** (Playwright): light + dark snapshot of the
  shell at default widths.
- **E2E**: existing `health.spec.ts` updated to navigate via the
  new shell (or relaxed if the page no longer matches the old DOM).

## Files added

```
docs/specs/PR-002-layout-shell.md
web/app/src/atoms/layout.ts                      + .test.ts
web/app/src/atoms/focus.ts                       + .test.ts
web/app/src/atoms/selection.ts
web/app/src/lib/mock-data.ts
web/app/src/components/layout/AppShell.tsx       + .stories.tsx
web/app/src/components/layout/SpacesRail.tsx     + .test.tsx + .stories.tsx
web/app/src/components/layout/SpaceContents.tsx  + .test.tsx + .stories.tsx
web/app/src/components/layout/ObjectView.tsx     + .test.tsx + .stories.tsx
web/app/src/components/layout/ResizeHandle.tsx
web/app/src/components/layout/MinWidthGuard.tsx  + .test.tsx
```

Estimated diff: ~25 files, ~1,200 lines.

## New runtime dep

- `react-resizable-panels` (^2)

## Open questions

- **Settings cog placement.** Resolved: pane 1 bottom opens the
  app-level Settings dialog. `Cmd+,` opens the same dialog even when
  the user is focused elsewhere.
- **Active state indicator on pane 1.** An accent-color *ring* around
  the icon (Anytype reference) vs a *bar* on the left edge. Default:
  ring; revisit when we have real space avatars.
- **Health card placement.** Until PR #3 lands a real Settings
  surface, the health card lives in pane 3's empty state for
  reviewability. Drop it the moment a Settings page exists.
