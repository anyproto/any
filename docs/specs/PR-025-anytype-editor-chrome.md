# PR #25 — Anytype-inspired editor chrome

> Bring the BlockNote interaction layer closer to Anytype: quiet
> side controls, a calm slash menu, compact floating toolbars, and
> consistent block-menu panels. This is aesthetic reference work,
> not a source-code port.

## Source of truth

- Reference product: Anytype desktop / `anyproto/anytype-ts`.
- Public design files inspected for behavior and visual language:
  `src/scss/component/editor.scss`, `src/scss/block/text.scss`,
  `src/scss/block/common.scss`, `src/scss/menu/block/common.scss`.
- Implementation stays original and local to our BlockNote adapter.
  No Anytype source code, selector trees, or assets are copied into
  this repo.

## Goal

The editor should feel like an Anytype block editor without becoming
Anytype's editor implementation:

- Side menu appears beside the active block with a subtle plus button
  and drag handle.
- Slash menu has restrained sizing, typography, icon cells, hover
  states, and shadow.
- Formatting/link/drag-handle menus share one panel language.
- Selected blocks, empty placeholders, checklist boxes, and nested
  indent guides fit the same quiet system.
- The text column keeps the PR-019 704 px measure and typography.

## Decisions

### Keep BlockNote

We continue using `@blocknote/mantine` as the concrete BlockNote UI
adapter because it supplies the slash menu, formatting toolbar, side
menu, and block drag handles through BlockNote's default controller
surface. Replacing it with a custom adapter is a later modularity
step, not needed for this visual pass.

### Make editor chrome explicit

`MarkdownEditor.tsx` now passes the default UI flags explicitly:
`formattingToolbar`, `linkToolbar`, and `slashMenu`.
This prevents a future BlockNote upgrade or wrapper refactor from
accidentally hiding block-based controls again.

Emoji picker and comments are disabled for now because the app has no
product surface for them. File/image upload remains deferred by the
v1 API scope, but the core BlockNote adapter can still evolve there
later.

`sideMenu` is disabled on the default UI and re-mounted through a
local `SideMenuController` wrapper. The wrapper still uses BlockNote's
real side-menu extension and drag-handle button, but replaces the
stock add button so the whole 24 px control opens the slash menu
instead of only the inner icon being clickable.

### Scope Mantine

Mantine remains an editor-internal adapter only. All overrides live in
`web/app/src/components/editor/blocknote-theme.css` under the
`anytype-block-editor` class. The app UI kit continues to be Radix +
in-repo primitives.

BlockNote renders menus and toolbars through a portal whose root gets
`bn-root anytype-block-editor`, while the editable surface gets
`bn-container anytype-block-editor`. Any editor chrome variable used
by popovers must be defined on both roots; otherwise menu backgrounds
and shadows become invalid and the dropdown appears naked over the
document.

### Visual language

The new chrome uses local editor variables:

- `--any-editor-panel` for floating surfaces.
- `--any-editor-panel-border` for 1 px separators.
- `--any-editor-panel-shadow` for soft floating depth.
- `--any-editor-hover` / `--any-editor-active` for grey hover and
  selected states instead of purple menu fills.
- `--any-editor-muted` / `--any-editor-subtle` for icon and helper
  text.

These variables map to our six-token palette; no new global colors
are introduced.

### Slash menu performance

The slash menu uses BlockNote's real suggestion-menu extension and
default command items, but the React renderer is local. It is
intentionally not a native scroll container: BlockNote tracks document
`scroll` while a suggestion menu is open, so scrolling a native menu
can force repeated FloatingUI/state work. The local renderer shows a
small window of rows and handles wheel movement itself, which keeps
block insertion behavior intact without emitting menu scroll events.

Keep the renderer lightweight: only the visible rows mount, row hover
has no transitions, and command badges/icons are fixed-size cells.
Keyboard selection still follows BlockNote's controller and the
window snaps to the selected row.

## Files

```
docs/specs/PR-025-anytype-editor-chrome.md
docs/agents/README.md
web/app/src/components/editor/MarkdownEditor.tsx
web/app/src/components/editor/blocknote-theme.css
```

## Acceptance criteria

1. Hovering a block shows the plus and drag handle next to the
   writing column.
2. Clicking plus opens the slash menu.
3. Drag handle still opens the block actions menu and can start block
   drag/drop.
4. The slash menu and formatting toolbar use grey Anytype-like
   surfaces, not Mantine's blue/purple defaults.
5. Text measure and PR-019 typography remain unchanged.
6. `pnpm typecheck`, `pnpm lint`, `pnpm test:run`, and `pnpm build`
   stay green.

## Out of scope

- Reimplementing Anytype's editor.
- Copying Anytype code or assets.
- Custom block schemas beyond BlockNote defaults.
- File/image upload panels.
- Collaborative selections, comments, or Anytype-specific block
  types.
