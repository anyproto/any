# PR #14 — Relation property + per-type UI plan

Two things in one PR, because they're tightly coupled — the new
relation type forces us to think about what "appropriate UI" means
for every property kind, not just the new one.

## Part 1 — Relation property

### Goal

A column whose cells link to other objects in the same space. Notion
calls this a "Relation". Display shows the linked object's name as a
clickable chip; clicking it opens the object in pane 3. Editing opens
a search popover that lists objects you can pick from.

### Wire layer

The SDK has no native relation kind. We follow PR #13's convention
and layer the UI sub-kind on top of a storage kind via `xKey`:

```jsonc
{ "name": "Author", "kind": "string", "xKey": "relation" }
```

Stored value is the linked object's id as a plain string:

```jsonc
{ "<typeId>": { "<propId>": "obj_abc123" } }
```

`uiKind()` already returns `'string'` for unknown xKeys; we add the
`relation` branch so it returns `'relation'` instead.

### Decisions (v1)

- **Single target only.** No multi-relation in v1. If we need it later
  the wire shape becomes `kind: 'array', xKey: 'relations'` and the
  cell goes from one chip to N chips, like TagsCell.
- **No target-type constraint.** The picker lists every object in the
  current space. Anytype/Notion both let you constrain a relation to
  a given type — we'll add that as a follow-up because it needs UI on
  the column-creation side too (a second dropdown in the popover).
- **Display fallback.** If the stored id no longer resolves (object
  deleted), show a muted "Missing object" chip rather than throwing.
- **Click-through navigation.** Clicking a chip in display mode sets
  `activeViewAtom = { kind: 'object', objectId }` so pane 3 swaps to
  the linked object. The chip shows a tiny arrow icon to hint this.
- **Picker.** Click the cell → popover with a search input + a
  scrollable list of up to 50 matches. Default sort: most recent.
  Filter is plain `name.includes(query)` client-side over the cached
  objects-by-space list. Enter selects the first match.

### Edge cases

- The source object itself shows up in the picker. Filtered out so
  you can't link an object to itself.
- The picker lists objects across types. If the user wants Author to
  only allow People, they pick wrong; nothing breaks but the cell
  shows the name of whatever they picked. Acceptable for v1.
- Renaming the linked object reflects on next refresh of the table —
  TanStack Query will revalidate when the by-space list refetches.
  No real-time push.

### Files

```
docs/specs/PR-014-relation-property-and-ui-plan.md
web/app/src/lib/api/types.ts                              (uiKind/toAddPropertyParts: relation branch)
web/app/src/lib/api/objects.ts                            (useObjectsBySpace hook for the picker)
web/app/src/components/tables/cells/RelationCell.tsx      (new)
web/app/src/components/tables/TableView.tsx               (dispatch on 'relation')
web/app/src/components/tables/AddColumnPopover.tsx        (9th option: Relation → object)
```

### Acceptance criteria

1. The Add-column popover lists 9 kinds; "Relation" picks
   `kind=string, xKey=relation`.
2. A relation cell shows "—" when empty.
3. Clicking the cell opens a popover with a search box. Typing
   filters the list; Enter selects the top match. Esc cancels.
4. Selecting an object commits its id; the cell now shows the
   object's name as a chip.
5. Clicking the chip in display mode opens that object in pane 3.
6. Deleting the linked object → cell shows "Missing object" rather
   than crashing.

## Part 2 — Per-type UI plan

This section documents the cell affordance for every property type —
what's shipped today, what the upgrade vector is, and the rationale.
The bar is "what would Notion or Anytype do here, minimally?"

The legend: ✅ shipped / 🟡 partial / ⏳ planned follow-up.

### Text (`string`)
- ✅ **Single-line input.** Click to edit, Enter commits, Esc cancels,
  blur commits, autosizes to row height. Display: truncate with
  ellipsis on overflow.
- ⏳ Inline expand on focus when value is long? Skipped — Long text
  exists for that case.

### Long text (`string + xKey=longtext`)
- ✅ **Auto-growing textarea**, Shift+Enter inserts a newline, Enter
  commits, Esc cancels. Display: single-line truncated.
- ⏳ Click → modal/dialog with a full editor surface (BlockNote-lite
  or just a tall textarea). Keeps the row tidy for content-heavy
  cells.

### Number (`number`)
- ✅ **Right-aligned numeric input**, parses on commit; bad values
  become `null` (cleared).
- ⏳ Format hints — currency / percent / decimals — exposed via a
  per-column configuration popover. Stored on the property's xKey
  suffix (e.g. `xKey=number:currency`).

### Yes / No (`boolean`)
- ✅ **Checkbox** centered in the cell, click toggles, no edit mode.
- ⏳ Tri-state when value is null (unchecked vs unset). Today null
  reads as false; visually identical.

### Date (`string + xKey=date`)
- 🟡 **HTML5 `<input type=date>`.** Browser owns the picker; we get a
  tiny calendar drop-down for free, but it's the OS chrome rather
  than something we control.
- ⏳ Custom mini-calendar popover (react-day-picker or hand-rolled
  Radix). Lets us:
  - Show "today" highlight + locale week start.
  - Add a "Clear" button.
  - Eventually: time-of-day, recurring, ranges.
- ⏳ Display upgrade — render relative dates ("Tomorrow", "in 3 days")
  for near-future, absolute for further.

### URL (`string + xKey=url`)
- ✅ **Text input** + small ↗ icon visible only when value parses as
  a URL; click opens in a new tab.
- ⏳ Auto-fetch favicon + page title for richer chip ("Wikipedia —
  Lasagna"). Same wire shape.
- ⏳ Embed previews on hover (cards) for known providers.

### Email (`string + xKey=email`)
- ✅ **Text input** + ✉︎ icon when value matches `\S+@\S+\.\S+`;
  click opens `mailto:`.
- ⏳ Auto-link to a Person object if one with that email exists in
  the space — closes the loop with relations.

### Tags (`array + xKey=tags`)
- ✅ **Comma-separated input → string[]**, dedup + trim. Display:
  small grey pill chips.
- ⏳ Per-tag color (`tagcolor` map on the property). Stable colors
  derived from the tag string for v1.5; full picker later.
- ⏳ Combobox in edit mode — typeahead suggesting existing tags from
  the same column.

### Relation (`string + xKey=relation`) — **new in this PR**
- 🟡 **Search popover** with name filter, no type constraint, single
  target. Display: clickable chip with name + tiny arrow icon.
- ⏳ Constrain by type — second dropdown in the column-creation
  popover; xKey becomes `relation:<typeId>`. Picker filters by type.
- ⏳ Multi-relation — N chips, comma-separated picker, `array+relations`.
- ⏳ Inline create — "Add new …" at the bottom of the picker creates
  the target object on the fly.

### Common upgrades (apply to multiple cells)
- ⏳ **Resize** column widths; persist per-(space, type) in
  localStorage.
- ⏳ **Reorder** columns via drag of the header.
- ⏳ **Hide / show** columns from a "View options" popover.
- ⏳ **Per-column filter** chips above the table (Notion-style).

## Roadmap (suggested ordering after this PR ships)

1. PR #15 — mini-calendar for Date + relative date display.
2. PR #16 — type-constrained Relation, multi-relation, inline create.
3. PR #17 — column resize + reorder + hide/show.
4. PR #18 — per-column filters.
5. PR #19 — number formats + tag colors.
