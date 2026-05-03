# PR #24 — Multi-select in the pages tree

> Shift-click range selection + Cmd/Ctrl-click toggle, the way Finder
> and every file system does it. Bulk delete on a single confirm.

## v1 scope

- **Plain click**: single-select + open in pane 3 (current behaviour).
- **⌘/Ctrl + click**: toggle the row in / out of the selection set.
  Doesn't change which object is open.
- **Shift + click**: select the range from the anchor to the clicked
  row, **within the same parent**. Cross-parent shift+click falls
  back to single-toggle (cleaner than computing a flat traversal
  across collapsed/expanded folders).
- **Shift + ArrowUp / ArrowDown**: from the focused tree row, extend
  the range to the previous / next visible row and move focus with it.
  This uses rendered tree order rather than query-cache sibling lists
  so it works inside expanded folders even when object records carry
  partial `nav` data.
- **Esc**: clears multi-selection.
- **Delete / Backspace** on a row that's part of a multi-selection
  → bulk-delete dialog with the count. Otherwise single-row
  delete dialog (current).
- **Drag a selected row onto a folder** when ≥2 rows are selected:
  moves the whole selected set into that folder, preserving tree order
  and clearing the selection after success. Dropping a multi-selection
  on a non-folder is rejected with a toast so reordering semantics stay
  simple.
- **Selected rows** get a softer background tint than the active
  row so the two states are distinguishable.
- **Click empty area below the list**: clears multi-selection.

## Decisions

- **Shift-click range select is scoped to siblings only.** Cross-parent ranges
  need a flat visual ordering that depends on which folders are
  expanded. Punted — the user can ⌘-click across parents.
- **Keyboard range select follows visible rows.** This matches file-manager
  keyboard selection and avoids stale/missing `nav.parentId` fields in
  nested query records.
- **Keyboard range keeps a separate moving edge.** Reversing direction
  after extending upward/downward shrinks from the focused edge even if
  Chromium continues delivering repeat key events to the row that started
  the gesture.
- **Plain click clears the multi-selection.** Notion and Finder
  both do this. Cmd-click is the explicit additive gesture.
- **Multi-select doesn't change `activeObjectId`.** Opening is a
  separate intent. Single-click is the only gesture that opens.
- **Selection state lives in Jotai atoms**, not in TanStack Query
  cache. It's UI state, not server state.
- **Bulk delete / move are N sequential mutations.** The SDK doesn't
  expose batch tree operations; sequential is fine at the handful scale
  and keeps error handling aligned with the existing single-object APIs.

## Files

```
docs/specs/PR-024-tree-multi-select.md
web/app/src/atoms/tree-selection.ts                  (new — set + anchor)
web/app/src/components/tree/ObjectRow.tsx            (shift/cmd click + highlight)
web/app/src/components/tree/ObjectTree.tsx           (Esc clear, click-empty clear, bulk dialog mount)
web/app/src/components/objects/BulkDeleteObjectsDialog.tsx (new)
```

## Acceptance criteria

1. Click row A, Shift-click row C in the same parent → A, B, C
   highlighted.
2. ⌘/Ctrl-click toggles a row in / out without changing the
   open object.
3. Esc clears the highlight.
4. Click empty area in the tree clears the highlight.
5. With ≥ 2 rows selected, Delete opens a bulk-delete dialog
   ("Delete N objects?"). Confirm runs N deletes; each failure
   toasts but the rest continue.
6. Dragging a selected row onto a folder moves every selected row
   into that folder.
7. Focus row A, press Shift+ArrowDown twice → A, B, C highlighted
   and focus lands on C; Shift+ArrowUp shrinks the range back to A-B.
8. Existing single-click + drag-and-drop + rename + single-delete
   flows unchanged.

## Test plan

- **Atom unit**: shift+click within siblings selects the range;
  cmd+click toggles; clear empties.
- **Component**: ObjectRow click semantics with each modifier.
- **Component**: Shift+Arrow extends and shrinks a sibling range while
  focus follows the selected edge.
- **Manual**: shift+click across folders falls back; Esc clears; drag
  a multi-selection onto a folder moves the batch.

## Out of scope

- Range select across collapsed/expanded folder boundaries.
- Bulk-rename / bulk-edit of a property across selected objects.
