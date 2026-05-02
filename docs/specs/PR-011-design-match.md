# PR #11 — Side panels, design match

> Match Anytype's sidebar design more faithfully in both panes.
> Measurements extracted from `anytype-ts` (Any Source Available
> License — used here as visual reference; this PR does not copy any
> code or stylesheet content into our tree).

## Why this exists

PR #10 standardised our two panes around our token system but didn't
match the reference faithfully — pane 1 is a slim icon rail; the
reference is a wider list with rows that carry an avatar, a name,
and trailing meta (pinned / unread). Pane 2's tree rows are also
spacing-different from the reference. This PR closes that gap.

## Goals

1. **Pane 1 widens** to a list. Each row is **avatar + name +
   trailing meta**. Pinned spaces sit at the top with a pin marker;
   the rest scroll. A filter input sits above the list. Bottom anchor
   stays — account avatar + globe + help.
2. **Pane 2 row treatment** matches the reference: 28 px-tall rows
   with 6 px gap to the leading icon, 6 px-radius hover surface,
   trailing meta when present.
3. **Section headers** read smaller and tighter — same chevron
   pattern but the type ramp matches the reference.

## Measurements (extracted as numerical facts)

Distilled from `anytype-ts/src/scss/component/sidebar/page/vault.scss`,
`anytype-ts/src/scss/widget/{common,object,tree}.scss`. Re-implemented
in our codebase via Tailwind utilities + our tokens; nothing copied.

### Pane 1 — spaces list

| Element | Measurement |
|---|---|
| Pane width | 280 px (was 80 px). Persisted via existing `paneWidthsAtom`. |
| Filter input height | 32 px |
| Row height | 52 px (with subtitle) / 36 px (compact, our default for v1) |
| Row padding (left, right) | 12 px |
| Avatar size | 36 px square, 8 px corner radius |
| Avatar → text gap | 10 px |
| Row hover surface | full row, 8 px corner radius |
| Pin / unread trailing icon | 16 px |
| Account avatar at bottom | 28 px circle |

### Pane 2 — sectioned list

| Element | Measurement |
|---|---|
| Pane width | 320 px default, 220–480 range (unchanged) |
| Section header height | 28 px, padding 0 8 px |
| Section header type | uppercase, 11 px, tracking 0.05 em, foreground/40 |
| Item row height | 28 px |
| Item padding (left, right) | 8 px |
| Item leading icon | 20 px |
| Icon → label gap | 6 px |
| Hover surface | 6 px corner radius, foreground/5 |
| Active surface | 6 px radius, foreground/8 |
| Trailing badge / count | foreground/40, tabular-nums |

### Type ramp

| Token | Value |
|---|---|
| Body small | 12 px / 16 px line-height |
| Body | 13 px / 20 px line-height |
| Body emphasis | 13 px / 20 px / 500 weight |
| Section label | 11 px / 14 px / 500 / uppercase / 0.05 em tracking |

## Decisions

- **Pane 1 shape change.** Row layout, not the slim rail. The rail
  was an interesting compact form but didn't match the reference.
  We keep the *concept* of a profile/account anchor at the bottom.
- **Filter input ships now.** It only does client-side filter against
  cached `useSpaces()` for v1 (a search across all objects is a
  later PR).
- **Pinned spaces.** No backend support yet (no `space.pinned`
  property). UI shows a thin row separator after the pinned section
  but we render *all* spaces in one list until pinning lands. The
  separator pattern + pin indicator are pre-built so adding the
  feature is one prop.
- **No "channels" abstraction.** The reference uses "channels" =
  spaces; we keep our existing language — the user-facing string is
  "Space" — but the *visual* matches the channel-list pattern.
- **Pane 1 width.** Increases from 80 px to 280 px. The existing
  `paneWidthsAtom` storage key is bumped to `v2` so a previous user's
  saved 80 px doesn't get persisted into the new design.

## Files changed / added

```
docs/specs/PR-011-design-match.md
web/app/src/atoms/layout.ts            (bump storage key + new defaults)
web/app/src/components/layout/SpacesRail.tsx  (rewrite as <SpacesList>)
web/app/src/components/layout/SpacesRail.test.tsx  (rewire)
web/app/src/components/layout/SpaceContents.tsx    (row treatment)
web/app/src/components/layout/SectionHeader.tsx    (type ramp tweak)
web/app/src/components/layout/AccountAvatar.tsx    (28px circle)
```

## Acceptance criteria

1. Pane 1 is now a 280 px list with avatar + name rows.
2. A filter input above the list filters spaces by name (case-insensitive).
3. Each row has hover + active surfaces matching the measurements.
4. Pane 2 row heights / paddings match the table; no visual drift
   from the reference at default sizes.
5. Light + dark mode both render.
6. Existing tests stay green; CI green.

## Test plan

- **Component** (`SpacesList`): rows render with avatar + name;
  filter narrows the list; clicking selects.
- **Component** (`SpaceContents`): item row dimensions match (snapshot
  of the rendered classes is fine — exact pixels aren't asserted).
- **axe**: no violations after the rewrite.

## Open questions

- **Pinning UI.** We surface a "pin to top" affordance once the
  backend has `space.pinned`. Today's pin indicator is rendered only
  if a row has `pinned: true` in the (currently always-empty) source.
- **Search across objects.** Future PR; pane 1's filter only matches
  space names today.
- **Animation / transitions.** Refernce has subtle transitions
  ($transitionCommon ≈ 150 ms). I match the duration (`transition`
  + `duration-150`) without copying the named ease curves.
