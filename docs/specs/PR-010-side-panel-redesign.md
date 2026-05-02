# PR #10 — Side panels redesign

> Both side panes (1 and 2) get a visual + structural refresh.
> Pane 3 stays document-focused and isn't touched.

## Goal

Move from "functional but plain" to a layout that reads like a real
notes app's sidebar:

- **Pane 1** uses rounded-square space avatars (~36 px) instead of
  circles, with the user's account avatar pinned at the bottom.
- **Pane 2** gets a header with a small space avatar matching
  pane 1's tone, then two collapsible sections: **Pages** (the
  existing object tree) and **Types** (real `/v1/types`, user types
  only).

The layout pattern is inspired by Anytype's existing client (the
reference screenshot you shared); implementation is independent and
uses our own tokens / lucide icons / spacing scale.

## Non-goals

- Unread / Favorites / Mentions sections — no backend support yet.
  When per-object "last seen" or `any.favorite` lands, those sections
  drop in next to Pages cleanly.
- Account-name / avatar editing — `PUT /v1/account/metadata` returns
  `501`.
- Type editor / type detail panel. Clicking a type row in v1 is a
  no-op (no destination); we revisit when there's a real Settings
  page or a per-type filter.
- Pane 3 changes.

## Decisions

### Pane 1 — space avatars

- Shape: **rounded square** (`rounded-lg`, ~6 px corner radius).
- Size: **36 px × 36 px** (was 36 px circle; the move is purely
  shape).
- Background: token-mapped from `tone(spaceId)` — same hash we use
  today, so an existing space keeps its color.
- Glyph: first letter of the space name uppercased (existing
  `glyph()` helper).
- Active state: 2 px accent ring with offset (unchanged).
- Muted state (non-active status): 50 % opacity (unchanged).
- Spacing: 6 px gap between avatars (was 8 px) so the rail reads as
  "denser app launcher" rather than "icon list."

### Pane 1 — account avatar at the bottom

A circle (not square — visually distinguishes "you" from "spaces")
with the user's initial. Driven by `useHealth().data.account` for
now (no avatar API). Click → no-op in this PR; placeholder for the
account / settings menu in a later PR.

### Pane 2 — header

Small (16 px) avatar matching the active space's tone, then the
space name, then a chevron-down to indicate the menu trigger. The
right side keeps the existing **+ New** dropdown and the
Members / More icons.

### Pane 2 — sections

Two collapsible sections, each with the same header pattern:

- A small header row: chevron (▶ / ▼), label uppercased + tracking,
  optional trailing count.
- Body indented one chevron-width.

**Pages** (default expanded): renders the existing `<ObjectTree>` —
no behavior change, just nested under a section header.

**Types** (default collapsed): pulls `useTypes(spaceId)` filtered to
non-builtin. Each row: type icon (lucide `Sparkles` for now, until
we have a per-type icon), type name, and a `(N)` count of objects of
that type — count via the same cross-object query the tree uses,
filtered by `any.types` containing the type id. Clicking a row is a
no-op in v1.

## Files changed / added

```
docs/specs/PR-010-side-panel-redesign.md
web/app/src/components/layout/SpacesRail.tsx        (rewrite)
web/app/src/components/layout/SpacesRail.test.tsx   (extend)
web/app/src/components/layout/SpaceContents.tsx     (sections + new header)
web/app/src/components/layout/SpaceContents.test.tsx (extend)
web/app/src/components/layout/SectionHeader.tsx     (new shared piece)
web/app/src/components/layout/AccountAvatar.tsx     (new)
web/app/src/components/layout/SpaceAvatar.tsx       (new — extracted from SpacesRail; reused in pane 2 header)
```

## Acceptance criteria

1. Pane 1 shows rounded-square space avatars (no longer circles).
2. The account avatar (a circle with the user's initial) sits at the
   bottom of pane 1, above the existing settings cog.
3. Pane 2's header shows a small space avatar next to the name.
4. Pane 2 has two visible sections: **Pages** (expanded) and
   **Types** (collapsed).
5. Expanding **Types** lists user types with an object count next to
   each name; clicking a row does nothing (no-op, no error).
6. Light + dark mode both render correctly. Existing focus rings
   intact.
7. Existing tests still pass; CI green.

## Test plan

- **Component** (`SpacesRail`): account avatar rendered when health
  succeeds; rounded-square classes applied; existing populated /
  empty / error states still pass.
- **Component** (`SpaceContents`): both section headers render;
  Types section collapsed by default; expanding fetches types and
  shows non-builtin only.
- **axe**: no violations on either pane.

## Open questions

- **Object count per type.** v1 issues one cross-object query per
  visible user type to compute counts. With a handful of types
  that's fine; if the user creates 30 types we batch later. Flag in
  the spec.
- **Account avatar source.** `useHealth().data.account` is a base58
  string — we hash it to pick a tone and take the first character as
  the glyph. Replace with real avatar when the SDK exposes one.
