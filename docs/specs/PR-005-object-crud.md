# PR #5 — Object create / rename / delete

> Spec for the fifth frontend PR. Folder creation, inline rename, and
> destructive delete on top of the PR #4 tree.

## Goal

Make the tree usable as a real organisational surface:

- Create objects of two flavours: **page** (item) or **folder** (with children).
- Inline-rename rows (`F2` or right-click → Rename).
- Delete rows (right-click → Delete… with destructive confirm).
- All mutations invalidate the right TanStack Query keys so the tree updates.

## Non-goals

- Drag-and-drop reordering / moving — PR #7.
- Multi-select. (Right-click acts on the row you right-clicked.)
- Undo. The SDK doesn't expose undelete in v1; we lean on the destructive confirm.
- Object icon picker / emoji.
- Tree search.

## API surface used

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/v1/spaces/:s/objects` | Create. Body `{nav: {type: 1\|2}}` to pick item vs folder. |
| `POST` | `/v1/spaces/:s/properties/:o/base/:t` | Set base properties. Used for rename via `t = "any"`, body `{patch: {name: "new name"}}`. |
| `DELETE` | `/v1/spaces/:s/objects/:o` | Permanent delete. Returns 204. |

Everything is real handler code already (no `501` here); nav stamping
happens server-side per `internal/nav/nav.go`.

## Decisions

- **Rename UX.** Inline. Triggered by `F2` on the focused row or
  `Rename` in the context menu. The title swaps for an `<Input>`,
  text auto-selected. Enter saves; Escape cancels; blur saves
  (industry standard).
- **Save semantics.** Save sends `POST /properties/:o/base/any` with
  `{patch: {name: "..."}}`. Empty / unchanged value → no-op (no
  request, exit edit mode). Optimistic update on the
  `useObjectChildren` cache; roll back on error with toast.
- **Delete UX.** Right-click → `Delete…`. Opens a destructive Dialog
  with the object title + the irreversibility note. No optimistic
  delete (irreversible).
- **Create.** The header `+ New` is a single fast-path action:
  create a root Page. No menu, no choice. It POSTs `/objects` with
  explicit root item home `{nav:{type:1,parentId:""}}` and selects the
  new id so pane 3 opens. Structural creation lives in the relevant
  section: the hierarchy section `+` opens the new-folder dialog;
  Lists `+` creates a new list; list rows expose their own `+` for
  new entries. Folder rows reveal a `+` only while expanded; it POSTs
  `{nav:{type:1,parentId:<folderId>}}`, expands the folder, selects
  the new page, and opens it in pane 3.
- **Folder semantics.** A folder is `nav.type === 2`. Visual: folder
  icon + chevron in the row. Children fetched lazily (already PR #4).
- **Keyboard map.**
  - `F2` — rename focused row (when it has focus).
  - `Esc` — cancel rename.
  - `Enter` — save rename (or, on a folder row not in edit mode, expand).
  - `Backspace` / `Delete` — open the delete dialog (focused row).
- **Cache key strategy.** `useRenameObject` invalidates
  `['objects', spaceId, 'children', parentId]` for the row's parent.
  `useDeleteObject` does the same and removes the row from cached
  arrays where it appears.

## Files added / changed

```
docs/specs/PR-005-object-crud.md
web/app/src/lib/api/objects.ts            (extend: setObjectProperty,
                                          deleteObject, useRenameObject,
                                          useDeleteObject; useCreateObject
                                          accepts {parentId, folder?})
web/app/src/lib/api/objects.test.ts       (extend)
web/app/src/components/tree/ObjectRow.tsx (rename inline, context menu,
                                          delete trigger, F2 + Esc + Backspace
                                          handling, edit-mode atom)
web/app/src/components/tree/ObjectRow.test.tsx
web/app/src/components/layout/SpaceContents.tsx
                                          (top + New fast path;
                                          section creation actions)
web/app/src/components/objects/DeleteObjectDialog.tsx + .test.tsx
web/app/src/atoms/edit.ts                 (renamingObjectIdAtom)
```

## Acceptance criteria

1. The top `+ New` button directly creates a root page; no menu opens.
2. Top `+ New` → POSTs `{nav:{type:1,parentId:""}}`; appears in tree, selected.
3. Hierarchy section `+` opens the folder dialog; submit POSTs
   `{nav:{type:2,parentId:""}}` and the folder appears with chevron + folder icon.
4. Right-click on any row → context menu: Rename, Delete….
5. `F2` while a row is focused enters rename mode (cursor in title input, all-selected).
6. Enter saves; the row title updates immediately (optimistic) and persists.
7. Esc cancels (no API call).
8. Blurring the input also saves.
9. Selecting `Delete…` opens a dialog with the title + irreversibility copy.
10. Confirming sends `DELETE /v1/spaces/:s/objects/:o`; the row vanishes; toast confirms.
11. Errors revert optimistic updates and surface a toast with the API code.
12. `Backspace` / `Delete` on a focused row opens the delete dialog.
13. Hovering/focusing an expanded folder row reveals a right-edge `+`;
   clicking it creates a child page inside that folder and opens it.
14. Existing tests stay green; CI green.

## Test plan

- **Unit** (`objects.ts`):
  - `setObjectProperty` POSTs the right URL + body shape.
  - `deleteObject` issues DELETE.
  - 404 / envelope passes through as `ApiError`.
- **Component** (`ObjectRow`):
  - Rename via context menu → input shown → Enter calls mutation.
  - Esc cancels (no mutation call).
  - Delete via context menu → dialog opens.
  - F2 on focused row enters rename mode.
  - Folder row `+` creates a child object with the folder id as
    `nav.parentId`, expands the folder, and selects/opens the new id.
- **Component** (`SpaceContents`):
  - + dropdown shows two options; clicking each calls create with the right body.
- **axe**: no violations on row in normal + rename modes; on the delete dialog.

## Open questions

- **Save on blur vs explicit Enter only.** Default to *save on blur*
  to match Anytype / Notion. If feedback says it's surprising, switch
  to "blur cancels" via a single toggle in `ObjectRow`.
- **Folder creation parent.** Root folder creation stays in the section
  header. Child page/object creation is available from a folder row's
  hover `+`; nested folder creation can be added separately if needed.
- **Delete confirmation suppression.** No "don't ask again" in v1.
  Easy to add later as a `localStorage` flag if it becomes annoying.
