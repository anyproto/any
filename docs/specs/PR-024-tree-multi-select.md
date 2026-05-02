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
- **Esc**: clears multi-selection.
- **Delete / Backspace** on a row that's part of a multi-selection
  → bulk-delete dialog with the count. Otherwise single-row
  delete dialog (current).
- **Selected rows** get a softer background tint than the active
  row so the two states are distinguishable.
- **Click empty area below the list**: clears multi-selection.

## Decisions

- **Range select scoped to siblings only.** Cross-parent ranges
  need a flat visual ordering that depends on which folders are
  expanded. Punted — the user can ⌘-click across parents.
- **Plain click clears the multi-selection.** Notion and Finder
  both do this. Cmd-click is the explicit additive gesture.
- **Multi-select doesn't change `activeObjectId`.** Opening is a
  separate intent. Single-click is the only gesture that opens.
- **Selection state lives in Jotai atoms**, not in TanStack Query
  cache. It's UI state, not server state.
- **Bulk delete is N sequential `useDeleteObject` calls.** The SDK
  doesn't expose batch delete; sequential is fine at the handful
  scale and gives us per-object error reporting.

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
6. Existing single-click + drag-and-drop + rename + single-delete
   flows unchanged.

## Test plan

- **Atom unit**: shift+click within siblings selects the range;
  cmd+click toggles; clear empties.
- **Component**: ObjectRow click semantics with each modifier.
- **Manual**: shift+click across folders falls back; Esc clears.

## Out of scope

- Drag-and-drop of a multi-selection (single-row drag still works).
- Bulk move into a folder.
- Range select across collapsed/expanded folder boundaries.
- Bulk-rename / bulk-edit of a property across selected objects.
