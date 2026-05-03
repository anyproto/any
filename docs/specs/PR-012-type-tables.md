# PR #12 — Type tables (database view)

> Click a custom type in pane 2 → pane 3 shows a database surface
> where rows are objects of that type and properties can render as a
> spreadsheet table or a readable list. Inline-edit cells in table
> mode, add rows (= new objects), add columns (= new properties),
> filter, sort.

## Goal

Make custom types behave like databases:

- **Click a type row** in pane 2's Types section → pane 3 switches
  from the editor to a table view scoped to that type.
- **Each row** is an object stamped with the type. The first column
  is `any.name` (the title); each remaining column is a type property.
- **Inline edit** any cell to update the underlying property.
- **Add a row** from the toolbar's `New` button or the bottom `+ New`
  row — creates an object with the type stamped and opens it.
- **Add a column** via a `+` header at the right edge → opens a
  popover that names a new property (kind: text / number / yes-no)
  and runs `addPropertyToType`.
- **Filter** by name through the database toolbar.
- **Sort** through the database toolbar or by clicking a column header
  — column headers cycle asc → desc → default order.
- **Show / hide properties** locally through the database toolbar.
- **Switch layout** locally between `Table`, `List`, and `Gallery`.
  Table remains the editable spreadsheet surface; List reuses the same
  rows, property order, filter, sort, paging, and virtualization, but
  renders each object as a readable row with property previews.
  Gallery reuses the same data/property model and renders a virtualized
  responsive card grid for visual browsing.
- **Rename the list title and set an emoji icon** locally from the
  table header. The same `atoms/type-meta.ts` override is used by the
  sidebar row and `+ New` menu.
- **Scale** through paged queries and row virtualization. The database
  surface must not render all records in a type at once.

## Non-goals

- **Delete or rename a column.** Server returns `501` for both
  (`DELETE/PATCH /v1/spaces/:s/types/:t/properties/:p`). Hidden until
  the SDK lands. The edit-property panel can show metadata, but cannot
  persist rename/type/delete changes yet.
- **Saved views / multiple named views per type** (Notion-style view
  tabs with independent table / board / calendar / list configs).
  v1 has one local `All` view with a persisted layout choice
  (`Table`, `List`, or `Gallery`). Toolbar filter/sort/property
  visibility is local UI state, not persisted view configuration.
- **Multi-property filters / formulas / relations / rollups.**
- **Multi-select / bulk delete** in the table.
- **Server-backed list rename/icon/delete.** Type metadata write and
  delete endpoints are still `501`; the table header only writes local
  UI metadata.
- **Persisted column order.** Users can reorder user-property columns
  locally, but the order resets when the view reloads until we have a
  saved-view or type-metadata storage shape.
- **Persisted column widths.** Header-edge drag resize and header
  context-menu auto-fit are in scope for spreadsheet feel. Widths are
  persisted locally per type/property until saved views or type
  metadata can own that configuration.
- **Server-backed layout persistence.** The Table/List layout choice is
  persisted locally in `any.tables.viewLayouts.v1` per type. It is not
  a server object and should be migrated when a saved-view API exists.
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
| `POST` | `/v1/spaces/:s/objects` | Add a row. Body `{nav:{type:1,parentId:""}, types:[typeId]}`: hierarchy home first, list membership second. |
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
  | { kind: 'object'; objectId: string; typeId?: string | null }
  | { kind: 'type-table'; typeId: string };

const activeViewAtom = atom<ActiveView>({ kind: 'empty' });
```

Keep the existing `activeObjectIdAtom` as a derived atom over
`activeViewAtom` for back-compat — every existing read/write site
keeps working unchanged. Add a new `activeTypeIdAtom` derived the
same way for the type-table side. The optional `typeId` on object
views is transient navigation context used when an object is opened
from table/list/gallery; it lets the editor expand the source list
chip without changing generic object navigation.

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

### Database toolbar + settings panel

The first shipped type table should feel useful before we introduce a
saved view model. The table header owns a compact database toolbar,
and heavier configuration lives in a right-side settings panel with a
small navigation stack:

- `Filter` toggles a name filter input. Filtering runs against loaded
  rows on the client because server-side regex support is not a stable
  v1 contract.
- `Sort` opens a menu for default order, `Name`, or any user property,
  plus ascending / descending direction. Column headers keep the quick
  cycle interaction for power users.
- The compact Table/List/Gallery segmented control in the toolbar
  switches the local layout immediately. `Settings` → `Layout` exposes
  the same choice from the side panel. The choice is stored in
  `atoms/table.ts` under `any.tables.viewLayouts.v1`, keyed by type id.
- Gallery is implemented as its own renderer (`GalleryRowsView`) with a
  small `GalleryViewConfig` surface (`minCardWidth`, `cardHeight`,
  `gap`, `previewPropertyLimit`, `showPropertyLabels`). Future card
  cover fields, density controls, and per-view card property choices
  should extend that config rather than forking data loading.
- `Settings` opens the table settings panel. The panel is the
  home screen for view/list controls: layout, property visibility,
  filter, sort, properties, and future per-list options. Each row
  opens a full detail screen inside the same panel with back / close
  controls; it is not an accordion.
- In the `Property visibility` detail screen, users show / hide user
  property columns and drag user properties by their grip handles to
  reorder them. `Name` is always shown and pinned before user
  properties. The grip drag is pointer-based, not native HTML5 drag:
  the lifted row follows the pointer and neighboring rows animate into
  the prospective drop slot. Visibility and order are local state only;
  refreshing returns to the server property order with all properties shown.
- In the table header, user-property columns also expose drag handles
  on hover/focus. Dropping one column onto another applies the same
  local property order immediately.
- Right-clicking a table column header opens a native app context menu.
  The first command is `Auto-fit column width`, which measures the
  loaded row content and header label, resizes the column, and persists
  the width through the same local column-width store as manual resize.
  `Auto-fit all columns` applies that same measurement pass to `Name`
  and every visible property column.
- In the `Properties` detail screen, users see the type's properties
  as a simple list. Clicking a property opens an `Edit property`
  detail screen for its metadata. `Add` opens an `Add property` detail
  screen inside the same panel; users enter a name and choose a kind
  from the create list. Suggested properties are intentionally not
  implemented yet.
- `New` creates an object stamped with the active type and selects it.
- The toolbar does not show a separate property `+`; property creation
  lives in `Settings` → `Properties` → `Add`. The table header may
  still expose the narrow add-column affordance at the right edge.

Do not add saved view persistence until the API has an explicit view
resource or a product decision assigns view state to type metadata.

### Add-row affordance

A toolbar `New` button and a trailing "+ New row" row at the bottom
of the table body both call `useCreateObject({typeIds: [typeId]})`.
After creation the object is selected so the user can edit the full
object screen.

### Add-column affordance

A `+` header cell at the right edge of the column header. Click it →
small popover with name input + kind dropdown → `addPropertyToType`.
Same flow as the wizard's properties step but inline and one-shot.

### Filter + sort

- **Filter**: a toolbar-controlled text input, debounced 200ms, filters
  loaded rows whose `any.name` contains the value (case-insensitive).
  Keep this client-side until the query API exposes a stable text
  operator.
- **Sort**: click a column header → cycle through asc / desc / none.
  The toolbar sort menu and settings panel can also choose `Name`, any
  visible or hidden property, and direction. Default is `["nav.pos"]`
  (creation order). Sort updates query key so
  `useObjectsByTypeInfinite` refetches from the first page.

### Paging + virtualization

Rows use `POST /objects/query` with `limit` and `offset`. The
frontend requests pages through `useObjectsByTypeInfinite`, asking for
one extra row as a cheap "has more" probe because the response does
not expose a total count. The table body renders only the visible
window via `shared/virtual/useVirtualRows.ts`, with spacer rows
preserving scroll height. The List layout uses the same query and
virtualizer with a taller row estimate; do not fork data loading for
layout-specific rendering. The Gallery layout uses the same query and
property state, but owns width-aware grid virtualization so card
columns can respond to panel width without rendering every object.

Type counts in pane 2 are batched: one query reads object records for
the space and derives counts from `any.types`, instead of one query
per type.

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
web/app/src/atoms/table.ts                       (local widths + layout)
web/app/src/lib/api/objects.ts                   (paged type-row helpers)
web/app/src/components/tables/
  TableView.tsx                                  (composition root only)
  useTableViewController.ts                      (query/filter/sort/paging)
  useTableProperties.ts                          (visibility/order/widths)
  useDataViewLayout.ts                           (current local renderer)
  TableToolbar.tsx                               (view tab + actions)
  TableColumnHeader.tsx                          (sort/drag/resize header)
  TableSettingsPanel.tsx                         (settings navigation shell)
  settings/*.tsx                                 (nested settings screens)
  TableRows.tsx                                  (table renderer rows)
  ListRowsView.tsx                               (list renderer rows)
  tableObjectValues.ts                           (row value helpers)
  AddColumnPopover.tsx                           (inline column add)
  TableView.test.tsx
web/app/src/components/layout/
  ObjectView.tsx                                 (switch on view.kind)
  SpaceContents.tsx                              (TypeRow → set view)
```

## Acceptance criteria

1. Clicking a type row in pane 2 opens its table in pane 3.
2. Active type row shows the same active-state styling as objects.
3. Header has a Table/List layout switch, then `Filter`, `Sort`,
   `Settings`, and `New`.
4. Table layout has Name + one column per type property + a `+` column at right.
5. Body shows one row per object filtered by `any.types`.
6. List layout renders the same objects as readable rows with up to
   four property previews, an object icon, and a visual open affordance;
   clicking anywhere on a row opens that object. The bottom row creates
   `New <type>`.
7. Filter input narrows loaded rows by name (≥200 ms debounce).
8. Toolbar sort and settings-panel sort can select default order,
   Name, each user property, and asc / desc direction.
9. Clicking a column header cycles sort asc / desc / off; the column
   gets a triangle indicator.
10. Settings panel opens and closes from the toolbar.
11. Settings-panel Layout can switch between Table and List and matches
    the toolbar segmented control state.
12. Settings-panel property visibility can hide and restore user
   property columns; Name stays visible.
13. Settings rows open full detail screens in the panel with a back
   affordance, not inline expanders.
14. Dragging property visibility grip handles reorders the user
   property columns in the table immediately.
15. Dragging user-property table headers reorders those columns with
   the same local order state.
16. Properties detail screen lists Name plus user properties, has an
   `Add` row, and opens property detail screens in the same panel.
17. Add-property detail screen creates a property with a name and kind
   through `POST /types/:t/properties`.
18. Clicking a cell enters edit mode; Enter / blur saves; Esc cancels.
19. The Name cell saves to `any.name`; property cells save to
   `<typeId>.<propId>`.
20. Toolbar **New** and bottom **+ New row** create an object with the
   type and select it.
21. **+** header opens a popover; submitting adds a column and the
    table re-fetches the type's properties.
22. Errors revert optimistic updates and toast the API code.
23. Existing tests stay green.
24. Large lists remain responsive because both layouts render only a
    virtual row window and load more records by page.

## Test plan

- **Unit** (`selection.ts`): activeViewAtom transitions; derived
  activeObjectIdAtom round-trips.
- **Component** (`TableView`):
  - renders header + rows from injected query data.
  - clicking a name cell + typing + Enter calls setObjectProperty
    with the right body shape.
  - clicking a property cell does the same with namespaced patch.
  - clicking + New row calls createObject with
    `{nav:{type:1,parentId:""}, types:[typeId]}`.
  - filter narrows visible rows.
  - toolbar controls render.
  - switching to List layout persists `any.tables.viewLayouts.v1`,
    renders readable rows with property previews, and can open a row.
  - settings panel opens from the toolbar, a row opens a nested detail
    screen, and back returns to the settings home screen.
  - property visibility detail screen hides and restores a property column.
  - dragging property visibility handles reorders table columns.
  - dragging table-header property handles reorders table columns.
  - Properties → Add opens an in-panel create screen and posts the
    selected kind/name.
  - toolbar New creates and selects an object.

## Open questions

- **Server `$regex` support.** Need to confirm by reading
  `handlers_query.go`. If unsupported, do client-side filter — small
  regression on large tables (everything fetches), revisit.
- **Cell width strategy.** Default Name/property widths are explicit,
  user-resizable from header edges, auto-fit for one or all visible
  columns from the column header context menu, and clamped to a
  practical spreadsheet range. The trailing add-column slot absorbs
  remaining empty width so horizontal table rules continue to the
  viewport edge.
- **Read-only fallback for array / object kinds.** v1 shows a small
  `<code>` block with the JSON; not editable.
