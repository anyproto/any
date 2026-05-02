# PR #19 — Editor surface 1:1 with Anytype

> Bring the BlockNote editor into pixel-parity with `anytype-ts` for
> the things a writer notices: title size, body font + measure,
> heading rhythm, list indent, code/quote treatment.

## Source of truth

Numbers extracted from `anyproto/anytype-ts` SCSS files (PR-011
used the same approach for the side panels). Cited inline below
with `<file>:<line>` so they're verifiable.

## Mapping

| Anytype token | Anytype source | Our application |
|---|---|---|
| Editor measure: **704 px** | `src/scss/_vars.scss:55 (--editor-width)` | `<MarkdownEditor>` outer wrapper → `max-w-[704px]` |
| Title: **36 px / 40 line-height / 700** | `src/scss/_vars.scss:34-36` | `<ObjectTitle>` textarea typography |
| Paragraph: **16 / 24 / -0.2 letter-spacing** | `src/scss/_vars.scss:33` | `.bn-editor p` |
| H1: **28 / 32 / 700, 22 px top** | `src/scss/_vars.scss:40-41`, `block/text.scss:103` | `.bn-editor h1` |
| H2: **22 / 28 / 700, 12 px top** | `src/scss/_vars.scss:43-44`, `block/text.scss:110` | `.bn-editor h2` |
| H3: **18 / 26 / 700, 12 px top** | `src/scss/_vars.scss:46-47`, `block/text.scss:117` | `.bn-editor h3` |
| List indent: **24 px / level**, bullet 6 px | `block/text.scss:17-22` | `.bn-editor ul, ol` and `li` |
| Quote: **1.5 px left border, 28 px padding-left, 6 + 6 px margin** | `block/text.scss:128-142` | `.bn-editor blockquote` |
| Code block: **mono 14 / 22, 16 px padding, 8 px radius, shape-highlight-light bg** | `block/text.scss:156-164` | `.bn-editor pre` |
| Inline code: **14 px, 1×4 padding, 4 px radius, shape-secondary bg** | `block/markup.scss:6` | `.bn-editor code` |
| Divider: **1 px shape-primary** | `block/div.scss:17` | `.bn-editor hr` |
| Block-to-block: **2 px bottom margin**, headings provide their own top | `block/common.scss:4` | `.bn-block` |

## Decisions

- **Font stack — system, not Inter.** Anytype bundles Inter; we keep
  the existing system stack (`system-ui, -apple-system, …`). On
  macOS this resolves to SF Pro Text, which reads like Inter at
  these sizes. We avoid pulling Google Fonts (the binary is
  offline-first) and skip adding `@fontsource/inter` for now. If we
  ever want true Inter, one `pnpm add @fontsource/inter` plus an
  import in this CSS file.
- **Mono stack — system, not IBM Plex.** Same reason. Mac → SF Mono.
- **Color tokens.** Anytype's `--color-shape-highlight-light` and
  `--color-shape-secondary` map cleanly to our existing
  `color-mix(foreground / N% / transparent)` recipe — see the
  `--bn-color-*-light` aliases below.
- **Hard-reset BlockNote/Mantine defaults.** BlockNote's CSS is
  scoped under `.bn-container` with element selectors; matching
  specificity (one class + one element) is enough — no `!important`.
- **Title not part of the editor.** Confirmed in PR-015. The title's
  36 px applies to `<ObjectTitle>`, not to a body H1.

## Files

```
docs/specs/PR-019-editor-design-1to1.md
web/app/src/components/editor/blocknote-theme.css   (major expansion)
web/app/src/components/editor/ObjectTitle.tsx       (36 / 40 / 700)
web/app/src/components/editor/MarkdownEditor.tsx    (max-w-2xl → max-w-[704px])
```

## Acceptance criteria

1. Side-by-side with anytype.io, the title and a paragraph land on
   the same baseline at the same size.
2. Pressing `# ` followed by text turns into an H1 sized 28 / 32.
3. Pressing `> ` produces a quote with a thin left border 28 px from
   the text.
4. A code block looks like Anytype's grey rounded-corner panel.
5. The first body block hugs the title (no awkward gap).
6. Existing tests stay green; no new ones needed (visual change,
   covered by manual side-by-side).

## Test plan

- Manual visual diff with anytype.io on the same content.
- `pnpm typecheck`, `pnpm lint`, `pnpm test:run`, `pnpm build` all
  clean.

## Out of scope

- Bundling Inter / Plex.
- Per-block colour callouts (we don't expose them yet).
- Toggle blocks (BlockNote ships this; visual parity later).
- Image, embed, file blocks (Anytype-specific surfaces).
