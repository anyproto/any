# PR #13 — Extended property types

> Adds Long text, Date, URL, Email, and Tags to the type-table column
> dropdown. Existing Text / Number / Yes-No still work.

## Goal

The SDK exposes only six storage kinds (`string`, `number`, `boolean`,
`null`, `array`, `object`). To give users richer column types
(Date, URL, Tags, etc.) we layer a UI sub-kind on top using the
property's `xKey` field, then dispatch to a more specific cell.

After this PR the `+ Add column` dropdown lists:

- Text          → kind=string
- Long text     → kind=string, xKey="longtext"
- Number        → kind=number
- Yes / No      → kind=boolean
- Date          → kind=string, xKey="date"
- URL           → kind=string, xKey="url"
- Email         → kind=string, xKey="email"
- Tags          → kind=array,  xKey="tags"

## Non-goals

- Single-select / multi-select with user-defined options. Needs
  schema for option lists which the SDK doesn't expose yet.
- Relations / rollups / formulas / file uploads.
- Server-side validation of formats (URL/email/date). The cell does
  best-effort parsing; bad values still save.
- Migrating existing string / array properties to new sub-kinds — no
  PATCH endpoint.

## Wire layer

`AddPropertyRequest` already accepts `xKey`. We use that as the UI
sub-kind hint:

```jsonc
{ "name": "Released", "kind": "string", "xKey": "date" }
```

`PropertyDef` carries `xKey` back unchanged on read. A small `uiKind`
helper in `lib/api/types.ts` maps `(kind, xKey) → UIPropertyKind`.

## Decisions

- **xKey as the hint, not xKind.** `xKind` is set server-side; we
  control `xKey` from the request. Slight semantic drift from the
  SDK's "natural key" usage but the practical value is high and
  there's no other writable extension slot today.
- **Storage shapes per UI kind:**
  - `Date`: ISO 8601 day string, `"YYYY-MM-DD"`. No time/timezone in
    v1 — adds two more popovers worth of decisions.
  - `URL`: raw string, no normalisation.
  - `Email`: raw string.
  - `Tags`: JSON array of trimmed non-empty strings, deduped.
  - `Long text`: raw string with newlines preserved.
- **Display fallbacks.** When a value doesn't parse, show it as plain
  text. We never throw away user input.
- **Edit ergonomics.**
  - Date: HTML `<input type="date">`. Browser owns the picker.
  - URL / Email: text input + small "Open" / "Email" link in display
    mode (only when the value is well-formed).
  - Long text: textarea, Shift+Enter inserts a newline, Enter
    commits, Esc cancels. Displayed truncated to one line.
  - Tags: comma-separated input → array. Display as small chips.

## Files added / changed

```
docs/specs/PR-013-property-types.md
web/app/src/lib/api/types.ts                            (uiKind helper, UIPropertyKind type)
web/app/src/components/tables/cells/
  LongTextCell.tsx
  DateCell.tsx
  UrlCell.tsx
  EmailCell.tsx
  TagsCell.tsx
web/app/src/components/tables/TableView.tsx             (PropertyCell dispatch by uiKind)
web/app/src/components/tables/AddColumnPopover.tsx      (expanded dropdown; sends xKey)
```

## Acceptance criteria

1. The Add-column popover shows 8 kind options.
2. Creating a Date column → date inputs in the cells. Saving an ISO
   date persists; reload shows the same date in the user's locale.
3. URL column → text input. Display mode renders a link if value
   parses as URL.
4. Email column → text input. Display mode renders a `mailto:` link
   when the value contains an `@`.
5. Long text column → textarea, multi-line accepted; Shift+Enter
   newline; Enter commits; truncated single-line in display.
6. Tags column → comma input → chip display. Empty entries are
   dropped; whitespace trimmed; duplicates dropped.
7. Existing Text / Number / Yes-No still work unchanged.
8. CI green.

## Test plan

- **Unit** (`uiKind`): every (kind, xKey) → expected UI kind.
- **Unit** (TagsCell parse): `"a, b, ,a"` → `["a", "b"]`.
- **Component**: AddColumnPopover sends the right body for each kind.
- **Component**: each cell renders + commits the right value shape.

## Open questions

- **Date with time.** Skipped in v1 (one more set of decisions:
  timezone, locale display, parsing rules).
- **Tag color / order.** Tags render in input order; no per-tag color
  in v1.
- **Migration.** Existing string columns won't auto-become Date if
  values look like dates — too risky without an explicit user choice.
