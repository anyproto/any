# PR #22 — Reuse property shape from another list

> When adding a column to a list, give the user a quick way to grab
> the same field they already defined elsewhere — name, kind and
> xKey copied — rather than retyping.

## Goal

Today the column-add popover only creates brand-new properties:
type a name, pick a kind, submit. After a few lists you've defined
"Author" / "Tags" / "Status" repeatedly. Notion and Anytype both
let you pick from "properties used elsewhere".

## v1 scope

A second tab in the existing `AddColumnPopover`:

- **New** (default) — current behaviour.
- **From another list** — searchable list of every user property
  defined in any other list in this space. Pick a row → fires the
  same `addPropertyToType` mutation with the picked property's
  `{name, kind, xKey}`.

That's it. No batching, no migration, no cross-type linkage.

## What this is not (Option B, deferred)

Truly *shared* properties — one propId living on multiple lists with
values that flow across — is a different feature that needs SDK
support to model "a property def attached to N types". Worth doing
later but out of scope here.

The "copy the shape" approach has a clear migration story to Option
B: when the SDK exposes shared props we walk our existing copies,
detect dupes by name+kind, and offer to merge.

## Decisions

- **Don't deduplicate within the menu.** If two lists each define
  "Author" with the same kind, both rows show. Hiding dupes asks us
  to pick a winner; better to surface and let the user choose.
- **Don't hide props already on the current list.** Picking a name
  that collides causes `addPropertyToType` to either fail (server
  rejects duplicate names) or land a second column — surface the
  error rather than pre-filter. Keeps the menu honest.
- **Aggregate via `useQueries`.** TanStack Query's batched-hooks
  primitive — call N `useTypeProperties` queries in a stable order.
  Reuses the per-type cache the table view already populates.
- **No explicit "copy" verb in the wire layer.** This is purely a
  client convenience — the server sees the same `POST .../properties`
  it'd see if the user typed the values manually.

## Files

```
docs/specs/PR-022-reuse-property-shape.md
web/app/src/lib/api/types.ts
  - useAllPropertiesInSpace(spaceId)        (new — useQueries-based aggregator)
  - PropertyDefWithOwner                    (new — adds owning typeId/typeName)
web/app/src/components/tables/AddColumnPopover.tsx
  - tab toggle: New / From another list
  - reuse panel reads useAllPropertiesInSpace, filters by query, renders
    rows; pick → addPropertyToType with the original (name, kind, xKey)
```

## Acceptance criteria

1. Open the AddColumn popover on a list — two tabs at the top:
   "New" (default, current behaviour) and "From another list".
2. Switch to "From another list" → see every user property defined
   in any other list, with `<list name> · <kind>` next to the
   property name.
3. Filter input narrows the list by property name.
4. Picking a row fires `addPropertyToType` and closes the popover —
   the same outcome as filling out "New" by hand.
5. The current list's own properties don't appear (you'd just be
   adding it twice).
6. Empty state: "No properties defined in other lists yet." with a
   hint pointing to other lists.
7. Existing flow unchanged; existing tests stay green.

## Test plan

- **Unit**: `useAllPropertiesInSpace` returns the flat list with
  owning typeId.
- **Component**: AddColumnPopover renders the tab toggle, switching
  swaps the body, picking a row fires the right mutation.
- **Manual**: define "Author / string" on Books, switch to Movies,
  use "From another list" to add Author there in one click.

## Out of scope (deferred)

- Truly shared properties with cross-type values (Option B).
- Bulk import: "give me all of Books's properties on Movies".
- Inline preview of an example value next to each candidate.
- Reorder by frequency-of-use.
