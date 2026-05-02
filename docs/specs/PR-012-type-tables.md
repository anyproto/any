# PR #12 — Type tables (database view)

> Click a custom type in pane 2 → pane 3 shows a table where rows
> are objects of that type and columns are the type's properties.
> Inline-edit cells, add rows (= new objects), add columns
> (= new properties), filter, sort.

## Goal

Make custom types behave like databases:

- **Click a type row** in pane 2's Types section → pane 3 switches
  from the editor to a table view scoped to that type.
- **Each row** is an object stamped with the type. The first column
  is `any.name` (the title); each remaining column is a type property.
- **Inline edit** any cell to update the underlying property.
- **Add a row** at the bottom of the table — creates an object with
  the type stamped. Click into the name cell to name it.
- **Add a column** via a `+` header at the right edge → opens a
  popover that names a new property (kind: text / number / yes-no)
  and runs `addPropertyToType`.
- **Filter** by name (text input above the table).
- **Sort** by clicking a column header — cycles asc → desc → none.

## Non-goals

- **Delete or rename a column.** Server returns `501` for both
  (`DELETE/PATCH /v1/spaces/:s/types/:t/properties/:p`). Hidden until
  the SDK lands.
- **Saved views / multiple views per type** (Notion-style table /
  board / calendar / list). Only the default table for v1.
- **Multi-property filters / formulas / relations / rollups.**
- **Multi-select / bulk delete** in the table.
- **Reordering columns** (drag headers).
- **Custom cell renderers beyond text / number / yes-no.** SDK has
  `array` and `object` kinds — they fall back to a read-only JSON
  display for v1.

## API surface used

All real handlers (no `501`s in scope):

| Method | Path | Purpose |
|---|---|---|
| `GET`  | `/v1/spaces/:s/types/:t/properties` | Column definitions. |
| `POST` | `/v1/spaces/:s/types/:t/properties` | Add a column. |
| `POST` | `/v1/spaces/:s/objects/query` | Rows — filter `{"any.types": typeId}`, sort `["<typeId>.<propId>"]` or `["any.name"]`. |
| `POST` | `/v1/spaces/:s/objects` | Add a row. Body `{types: [typeId]}`. |
| `POST` | `/v1/spaces/:s/properties/:o/base/:t` | Edit a cell. Body `{patch: {<propId>: <value>}}` for the type-namespace; `{patch: {name: <string>}}` for the name column. |

Cross-object record shape — namespaced by typeId, per
`internal/nav/nav.go`:

```jsonc
{
  "id": "obj_abc",
  "any": { "name": "Spaghetti", "types": ["any", "nav", "t_recipe"] },
  "nav": { "type": 1, "parentId": "", "pos": "PPQR" },
  "t_recipe": { "p_servings": 4, "p_vegan": false }
}
```

## Decisions

### Single view atom

Replace `activeObjectIdAtom` with a discriminated union at
`atoms/selection.ts`:

```ts
type ActiveView =
  | { kind: 'empty' }
  | { kind: 'object'; objectId: string }
  | { kind: 'type-table'; typeId: string };

const activeViewAtom = atom<ActiveView>({ kind: 'empty' });
```

Keep the existing `activeObjectIdAtom` as a derived atom over
`activeViewAtom` for back-compat — every existing read/write site
keeps working unchanged. Add a new `activeTypeIdAtom` derived the
same way for the type-table side.

### Pane 2 — type rows become navigable

The type row in `<TypesSection>` was a no-op. Now it sets
`activeViewAtom` to `{kind: 'type-table', typeId}` and gets the same
`aria-current="page"` treatment objects have when active.

### Pane 3 — switch on view.kind

`<ObjectView>` reads `activeViewAtom`:
- `empty`     → existing empty state.
- `object`    → existing markdown editor.
- `type-table`→ new `<TableView typeId={…}/>`.

### Cell components

Three primitives: `<TextCell>`, `<NumberCell>`, `<BoolCell>`. Each:

- displays the current value (read mode by default).
- enters edit mode on click / Enter / F2.
- commits on Enter / blur (Esc cancels).
- writes via `setObjectProperty(spaceId, objectId, typeId, {<propId>: value})`.

Optimistic update pattern reused from `useRenameObject`: patch the
row in cache, rollback on error.

### Add-row affordance

A trailing "+ New row" row at the bottom of the table body. Click it
→ `useCreateObject({typeIds: [typeId]})` → focus the new row's name
cell in edit mode.

### Add-column affordance

A `+` header cell at the right edge of the column header. Click it →
small popover with name input + kind dropdown → `addPropertyToType`.
Same flow as the wizard's properties step but inline and one-shot.

### Filter + sort

- **Filter**: a text input above the table, debounced 200ms, filters
  rows whose `any.name` contains the value (case-insensitive).
  Server-side via `{filter: {"any.types": typeId, "any.name": {"$regex": "..."}}}`
  — see `internal/server/handlers_query.go` for the supported
  operators. If `$regex` isn't supported v1, fall back to client-side.
- **Sort**: click a column header → cycle through asc / desc / none.
  Default is `["nav.pos"]` (creation order). Sort updates query key
  so `useObjectsByType` refetches.

## State machine?

Per the architecture doc this would be the third hand-rolled machine
(after editor save + type wizard). It's borderline — the table view
mostly composes existing mutations. We do *not* introduce an XState
migration in this PR; the existing per-cell edit pattern is enough.
If a fourth machine arrives we re-evaluate.

## Files added / changed

```
docs/specs/PR-012-type-tables.md
web/app/src/atoms/selection.ts                   (rewrite — view atom)
web/app/src/atoms/selection.test.ts              (new)
web/app/src/lib/api/objects.ts                   (useObjectsByType helper)
web/app/src/components/tables/
  TableView.tsx                                  (root)
  TableHeader.tsx                                (column headers + sort)
  TableRow.tsx                                   (data row)
  cells/TextCell.tsx
  cells/NumberCell.tsx
  cells/BoolCell.tsx
  AddColumnPopover.tsx                           (inline column add)
  TableView.test.tsx
web/app/src/components/layout/
  ObjectView.tsx                                 (switch on view.kind)
  SpaceContents.tsx                              (TypeRow → set view)
```

## Acceptance criteria

1. Clicking a type row in pane 2 opens its table in pane 3.
2. Active type row shows the same active-state styling as objects.
3. Header has Name + one column per type property + a `+` column at right.
4. Body shows one row per object filtered by `any.types`.
5. Filter input narrows visible rows by name (≥200 ms debounce).
6. Clicking a column header cycles sort asc / desc / off; the column
   gets a triangle indicator.
7. Clicking a cell enters edit mode; Enter / blur saves; Esc cancels.
8. The Name cell saves to `any.name`; property cells save to
   `<typeId>.<propId>`.
9. **+ New row** at the bottom creates an object with the type and
   focuses its name cell.
10. **+** header opens a popover; submitting adds a column and the
    table re-fetches the type's properties.
11. Errors revert optimistic updates and toast the API code.
12. Existing tests stay green.

## Test plan

- **Unit** (`selection.ts`): activeViewAtom transitions; derived
  activeObjectIdAtom round-trips.
- **Component** (`TableView`):
  - renders header + rows from injected query data.
  - clicking a name cell + typing + Enter calls setObjectProperty
    with the right body shape.
  - clicking a property cell does the same with namespaced patch.
  - clicking + New row calls createObject with `{types: [typeId]}`.
  - filter narrows visible rows.

## Open questions

- **Server `$regex` support.** Need to confirm by reading
  `handlers_query.go`. If unsupported, do client-side filter — small
  regression on large tables (everything fetches), revisit.
- **Cell width strategy.** Default to `min-content` per column with
  text-overflow ellipsis on the name. Drag-resize columns is out of
  scope.
- **Read-only fallback for array / object kinds.** v1 shows a small
  `<code>` block with the JSON; not editable.
