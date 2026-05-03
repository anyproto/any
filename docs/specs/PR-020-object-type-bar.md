# PR #20 — Object type bar (chips + expandable properties)

> Below the title, show what lists this object belongs to and let the
> user expand any one of them inline to view + edit that list's
> property values for this object.

## Goal

Anytype-style affordance: a row of chips under the page title — one
per type / list the object is stamped with. Clicking a chip toggles
a panel below it that shows that list's properties as label/value
rows, editable in place. Same as how Anytype shows "Books / Dune /
Online Shop / Products" with "Author / Genre / Description"
expanding underneath.

## v1 scope (this PR)

- One chip per typeId in `object.any.types`. Skip empty / nav and
  lists hidden locally through `atoms/type-meta.ts`.
- Each chip = local emoji list icon override or small Sparkles icon +
  local display-name override or server type name. The picker itself
  lives in the sidebar/table list metadata UI.
- Click toggles. **At most one chip expanded at a time.** Re-clicking
  the active chip collapses it.
- Below: a vertical list of `{label, value}` rows, one per
  user-property of that type. The `name` field is skipped (it's the
  page title; not a type property anyway).
- Each value cell reuses an existing cell component
  (TextCell / NumberCell / DateCell / etc.) so editing semantics
  match the table view.
- A "+" chip can attach an existing non-hidden list to this object or create a
  new list. When creating from this bar, the dialog runs in quick-create
  mode: Name → Create, skipping Properties and Confirm. The new list is
  attached to the current object automatically and pane 3 returns to
  the object.
- No primary-vs-secondary chip styling. Deferred.
- No "Show More" / collapse-after-N rows. List is always full.

## Decisions

- **Expanded state stays local.** No persistence — re-open the
  object and nothing is expanded. Cheap to undo, easy to upgrade
  later if users want it.
- **One expanded at a time.** Keeps the page tidy; matches the
  Anytype gesture that shows one card under the chips.
- **No optimistic updates.** Property writes go through a small
  `useSetObjectProperty` hook that POSTs and then invalidates the
  per-object cache. Round-trip is fast (<50 ms locally) so the UI
  feels live without us building rollback paths.
- **Skip the `name` property.** The title sits above the bar.
  Showing `name` again as a row would be confusing and double-edit.
- **Properties layout — two columns, fixed left.** Notion + Anytype
  both do this. Left column: ~140 px label + small icon. Right:
  the value.

## Files

```
docs/specs/PR-020-object-type-bar.md
web/app/src/lib/api/objects.ts                         (useObject + useSetObjectProperty)
web/app/src/components/editor/ObjectTypeBar.tsx        (new — chips + expandable panel)
web/app/src/components/editor/MarkdownEditor.tsx       (mount the bar between title and body)
```

## Acceptance criteria

1. Open an object that's stamped with one or more user types — a
   row of chips appears between the title and the editor body.
2. Click a chip: a panel expands below the bar with that type's
   properties as label/value rows.
3. Editing a value commits and persists across refresh.
4. Clicking the expanded chip again collapses the panel.
5. Clicking a different chip swaps the panel to that type's props.
6. An object with no user types shows nothing (no empty bar).
7. "+" chip opens list search; picking a list attaches it to the object.
8. "Create new list…" creates the list, attaches it to this object,
   closes the dialog, and leaves pane 3 on the object editor.
9. Existing tests stay green.

## Test plan

- Manual: create a Book object, add Author / Genre / Description,
  confirm chip + panel + commit round-trip.
- Component: ObjectTypeBar renders one button per type and toggles
  expansion on click.
- Hook: useObject returns the record by id.

## Out of scope (deferred)

- Detach type chip (×) on each chip.
- Primary type styling (the first chip rendered larger / coloured).
- Uploaded/image list icons (emoji overrides are supported locally).
- Per-property reorder / hide.
