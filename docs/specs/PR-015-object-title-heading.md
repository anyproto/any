# PR #15 — Object title as a heading in the editor

> Show the object's name as a Notion-style title above the BlockNote
> body, inline-editable, kept in sync with the sidebar tree.

## Goal

Today the open object pane shows only the body (BlockNote editor).
The object's name lives in the sidebar tree but isn't visible — and
isn't editable — at the top of the document. This PR adds a tall
inline title that:

- Reads the object's `any.name`.
- Renders as an H1-sized, borderless inline input above the editor.
- Edits commit to `setObjectProperty(typeId='any', patch={name})`.
- Stays in sync both ways: renaming in the tree updates the title;
  editing the title updates the tree and pane header immediately
  while typing, before blur/save.

## Decisions

- **Title is its own field, not the editor's first H1.** Notion does
  this for two reasons: (a) parsing markdown to extract / inject an
  H1 is fragile; (b) the user can still write H1s in the body for
  in-document headings without colliding with the title. Anytype
  agrees. We follow the same pattern.
- **Live navigation draft, no save state machine for the title.** The body has the
  debounced save state (Loading / Editing / Saving / Saved / Save
  failed). For the title we keep it simpler: blur or Enter commits;
  failures toast. While the user types, `objectTitleDraftsAtom`
  feeds the sidebar row and pane header so navigation chrome feels
  immediate without turning every keypress into a server write.
- **Cache patch + invalidation breadth.** A successful title edit
  patches mounted `['objects', spaceId, ...]` caches, then invalidates
  them so the sidebar tree, type tables and the relation picker all
  reflect the new name. Cheap because TanStack Query refetches lazily
  — only mounted queries pay.
- **Empty title = "Untitled" placeholder.** The placeholder is
  display-only; the underlying value stays empty, matching the
  sidebar's "Untitled" rendering.
- **Tab moves into the body.** Pressing Tab from the title focuses
  the BlockNote editor. Pressing Shift+Tab from the first body
  block returns to the title (BlockNote default; we don't fight it).

## Files

```
docs/specs/PR-015-object-title-heading.md
web/app/src/lib/api/objects.ts                    (useObjectName hook)
web/app/src/components/editor/ObjectTitle.tsx     (new)
web/app/src/components/editor/MarkdownEditor.tsx  (renders <ObjectTitle/> above the editor)
```

## Acceptance criteria

1. Opening an object renders the name as a large title above the
   body.
2. Editing the title updates the sidebar tree and pane header while
   typing; pressing Enter (or blurring) commits.
3. Renaming via the sidebar context menu updates the editor title
   without a page refresh.
4. Empty name shows "Untitled" as placeholder.
5. Tab from the title focuses the body editor.
6. Existing tests stay green.

## Test plan

- **Component**: ObjectTitle commits on Enter and blur, shows
  placeholder when empty, propagates failures via toast.
- **Manual**: rename via tree → title updates; edit title → tree
  updates; edit title in one tab → second tab refreshes on focus
  (TanStack Query refetchOnWindowFocus default).
