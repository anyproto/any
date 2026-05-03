# PR #7 — Tree drag-and-drop

> Spec for the seventh frontend PR. Reorder objects in the tree and
> move them into folders by dragging.

## Goal

- Drag any row and drop it:
  - **on a folder** → it becomes the last child of that folder.
  - **between two siblings** → it lands at that exact position.
  - **at the root** → moves to top-level.
- The new `nav.pos` is computed client-side via a TS port of
  `github.com/anyproto/lexid` so we don't round-trip just to figure
  out the lexid.
- The move uses the existing `POST /v1/spaces/:s/properties/:o/base/nav`
  with `{patch: {parentId, pos}}` — both fields land in one DAG change.
- Optimistic update: the row jumps to its new place immediately;
  rolls back on error.

After this PR the tree is a real org tool — make folders, drag pages
into them, reorder.

## Non-goals

- Arbitrary multi-select reorder. Batch drag is supported only for
  dropping selected rows onto a folder.
- Keyboard-driven move. (Architecture doc commits to this eventually
  via `@headless-tree/react`'s keyboard DnD; deferred — see "Library
  pivot" below.)
- Cross-space drag.
- Auto-expand a hovered folder during drag (nice-to-have follow-up).

## API surface used

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/v1/spaces/:s/properties/:o/base/nav` | Set nav properties. Body `{patch: {parentId, pos}}`. Server commits both fields in one change. |
| `POST` | `/v1/spaces/:s/objects/query` | Used to find a folder's max `nav.pos` when computing a drop-at-end position. Already cached per-folder by `useObjectChildren`. |

All real handlers; nothing 501.

## Decisions

### Library pivot: dnd-kit, not @headless-tree/react

The architecture doc picked `@headless-tree/react` because it ships
with keyboard DnD and a complete tree state machine. After PR #4
shipped a hand-rolled tree (also justified at the time as "simpler
for v1"), swapping to headless-tree now means rewriting the tree
*and* learning a DnD API.

Pragmatic choice: **pair our hand-rolled tree with `@dnd-kit/core`**
just for the DnD primitives (`DndContext`, `useDraggable`,
`useDroppable`). Mouse DnD ships now; keyboard DnD lands either as a
follow-up (small dnd-kit modifier work) or as part of a future
headless-tree migration if we hit limits.

I'll amend `docs/08-app-architecture.md` with this decision in the
same commit, mirroring the BlockNote/mantine pivot.

### Lexid port

The lexid algorithm lives at `github.com/anyproto/lexid`; the server
uses `(CharsAllNoEscape, blockSize=4, stepSize=100)`. The TS port
must agree byte-for-byte with the Go side or pos values diverge.

We port only the operations the client needs:
- `Middle()` — first pos in an empty folder.
- `Next(prev)` — append at the end (used on drop-at-end).
- `NextBefore(prev, before)` — between two siblings; covers
  drop-at-start (prev = "").

Tests assert against fixtures generated from the Go side
(`docs/fixtures/lexid.json`, committed). If the Go-side lexid version
ever changes we re-generate.

### Drop semantics

Three logical drop targets:

| Drop target           | parentId        | pos formula                                        |
|-----------------------|-----------------|----------------------------------------------------|
| On a folder row       | folder.id       | `Next(maxPosOf(folder))` or `Middle()` if empty.   |
| Above a sibling row   | sibling.parentId| `NextBefore(prevSibling.pos, sibling.pos)`         |
| Below a sibling row   | sibling.parentId| `NextBefore(sibling.pos, nextSibling.pos)` or `Next(sibling.pos)` if it's the last. |

To support drop-above / drop-below we render thin **drop bars**
between rows; dragging into the top half of a row → drop above; bottom
half → drop below; centre → drop on (folders only).

Edge cases:
- **Drop a row onto its own subtree** is rejected (shows a forbidden
  cursor; no mutation).
- **Drop where pos doesn't change** (e.g. drop on its current
  position) is a no-op.
- **Multi-selected rows dropped on a folder** move as a batch. The
  selected records are sorted by their current tree order, appended to
  the target folder with fresh lexid positions, and moved through
  sequential `useMoveObject` mutations. Dropping a multi-selection on a
  non-folder is rejected with a toast.

### Optimistic update

On drop we:
1. Synchronously patch the source parent's children query (remove
   the row) and the target parent's children query (insert at
   position) in the TanStack Query cache.
2. Fire the mutation.
3. On error, restore both query caches and surface a toast.

### Files added / changed

```
docs/specs/PR-007-tree-dnd.md
docs/fixtures/lexid.json                 (Go-generated test fixtures)
docs/08-app-architecture.md              (dnd-kit pivot note)
web/app/package.json                     (+ @dnd-kit/core)
web/app/src/lib/lexid.ts                 + .test.ts
web/app/src/lib/api/objects.ts           (+ useMoveObject mutation)
web/app/src/components/tree/
  ObjectRow.tsx                          (useDraggable + useDroppable)
  ObjectTree.tsx                         (DndContext, drop computation)
  DropBar.tsx                            (between-row affordance)
```

## Acceptance criteria

1. Hovering a row mid-drag shows a top/bottom drop bar; folder rows also show a "drop on" highlight.
2. Releasing on a folder → row moves into the folder, becomes its last child.
3. Releasing on a drop bar between two siblings → row lands between them; the order persists across reload.
4. Dragging a row onto itself or its descendants is rejected — no mutation.
5. Save errors restore the pre-drop layout and toast the API code.
6. Lexid TS port matches the Go-generated fixtures byte-for-byte.
7. CI green; no a11y regressions on the tree.

## Test plan

- **Unit (lib/lexid.ts)**: Middle, Next×N, NextBefore against fixtures.
- **Unit (lib/api/objects.ts)**: `useMoveObject` POSTs the right body shape, invalidates source + target.
- **Component**: drop on folder triggers mutation with the right
  `{parentId, pos}` (mock the API). Rejection of self-drop.
- **axe**: drag handles get role/label appropriately.

## Open questions

- **Touch DnD.** dnd-kit supports it but desktop-only is the v1 stance,
  so we don't wire touch sensors yet.
- **Multi-block drag preview.** Default ghost suffices; custom drag
  preview is a follow-up if we want it.
- **Auto-expand on hover.** Defer until users ask for it.
