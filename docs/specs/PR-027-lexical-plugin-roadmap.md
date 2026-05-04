# PR #27 — Lexical plugin roadmap

> Keep the object editor plugin-oriented. This file lists the Lexical
> plugins we can use, which ones are active now, and which product
> plugins should be built on top of Lexical rather than inside the app
> shell.

## Goal

- Give future agents a clear menu of editor plugins.
- Keep `MarkdownEditor` as the stable public boundary.
- Keep `LexicalMarkdownEditor` as a composition layer, not a giant
  feature container.
- Add editor features in small plugin slices that can be tested,
  flagged, and removed independently.

## Sources

- Lexical's React docs
  (`https://lexical.dev/docs/getting-started/react` and
  `https://lexical.dev/docs/packages/lexical-react`):
  `@lexical/react` exposes editor features as JSX-composed plugins,
  and plugins can be lazy-loaded when their cost should not hit the
  default editor path.
- Local install: `web/app/package.json` currently includes
  `lexical`, `@lexical/react`, `@lexical/markdown`,
  `@lexical/rich-text`, `@lexical/list`, `@lexical/link`,
  `@lexical/code`, `@lexical/selection`, and `@lexical/utils`.
- Current migration spec:
  `docs/specs/PR-026-lexical-editor-spike.md`.

## Current editor plugins

`web/app/src/components/editor/LexicalMarkdownEditor.tsx` currently
uses these official plugins:

| Plugin | Purpose | Status |
| --- | --- | --- |
| `RichTextPlugin` | Main editable rich text surface. | Active |
| `HistoryPlugin` | Undo and redo. | Active |
| `ListPlugin` | Bullet and numbered lists. | Active |
| `CheckListPlugin` | Checkbox lists. | Active |
| `LinkPlugin` | Link node behavior. | Active |
| `MarkdownShortcutPlugin` | Markdown-style shortcuts while typing. | Active |
| `OnChangePlugin` | Editor-state listener for markdown save bridge. | Active |
| `DraggableBlockPlugin_EXPERIMENTAL` | Root block drag/reorder primitive. | Active behind local `LexicalBlockMenu`; keep wrapped because the API is experimental. |

The editor also has these local plugins/components:

| Local plugin | Purpose | Direction |
| --- | --- | --- |
| `LexicalHydrationPlugin` | Converts markdown from the API into Lexical nodes. | Keep local while persistence is markdown. |
| `LexicalMarkdownChangePlugin` | Converts Lexical state back to markdown for saves. | Keep local while persistence is markdown. |
| `LexicalBlockMenu` | Left-side insert-below control and drag handle for top-level blocks. | Keep local, small, and replace internals if Lexical stabilizes the draggable API. |
| `LexicalSelectionMenu` | Floating selection/right-click formatting menu with inline formats, link/unlink, case transforms, and clear formatting. | Keep local and move under `lexical/plugins/` when the plugin folder is split out. Underline/script formats are live-editor only until richer persistence lands. |
| `LexicalSlashMenu` | Minimal command menu. | Replace with official typeahead primitives. |

## Official plugins available now

These are present in the installed `@lexical/react` package and can be
added without changing the app's editor boundary. Some may still need
matching node packages or custom UI.

| Plugin | Use it for | Notes |
| --- | --- | --- |
| `LexicalTypeaheadMenuPlugin` | Slash commands, mentions, object search. | Best next step; replaces the fixed local slash menu. |
| `LexicalNodeMenuPlugin` | Node-anchored menus. | Useful for block insert menus and embed menus. |
| `LexicalNodeContextMenuPlugin` | Right-click node menus. | Good fit for block-level actions. |
| `LexicalNodeEventPlugin` | Node event wiring. | Use for hover/selection behavior without broad DOM listeners. |
| `LexicalDraggableBlockPlugin` | Block drag handles and reorder. | Experimental API; wrap behind a feature flag. |
| `LexicalAutoLinkPlugin` | Turn typed URLs into links. | Pair with `AutoLinkNode` and URL matchers. |
| `LexicalClickableLinkPlugin` | Open links while not editing. | Add with clear edit vs open interaction rules. |
| `LexicalHorizontalRulePlugin` | Divider blocks. | Low risk and markdown-friendly. |
| `LexicalTabIndentationPlugin` | Tab indentation. | Useful after keyboard behavior is specified. |
| `LexicalTableOfContentsPlugin` | Page outline from headings. | Good for right-side outline or command palette. |
| `LexicalAutoFocusPlugin` | Focus editor on mount. | Only for flows where focus should be stolen intentionally. |
| `LexicalEditorRefPlugin` | Parent access to editor instance. | Prefer local hooks first; use for integration boundaries. |
| `LexicalClearEditorPlugin` | Clear editor command handling. | Mostly useful for tests or reset flows. |
| `LexicalCharacterLimitPlugin` | Character budget UI. | Use only for constrained fields, not object pages. |
| `LexicalHashtagPlugin` | Hashtag token behavior. | Lower priority than object mentions. |
| `LexicalAutoEmbedPlugin` | URL/embed insertion flow. | Wait for file/embed API and card design. |
| `LexicalTablePlugin` | Table behavior. | Needs a table-node persistence decision. |
| `LexicalCollaborationPlugin` | Collaborative editing. | Defer until realtime/storage model exists. |
| `LexicalPlainTextPlugin` | Plain text editor surface. | For titles/comments/short fields, not object body. |

## Packages to add only when needed

Do not install these preemptively. Add them in the PR that uses them
and record the storage/UI consequence in that PR spec.

| Package | Enables | Decision needed first |
| --- | --- | --- |
| `@lexical/table` | Real table nodes and table commands. | Markdown round-trip will lose structure; decide JSON or HTML storage. |
| `@lexical/hashtag` | Hashtag nodes. | Decide whether hashtags are just text or object/tag relations. |
| `@lexical/html` | HTML import/export. | Decide where HTML fits next to markdown API. |
| `@lexical/yjs` | Yjs collaboration. | Requires realtime lifecycle and conflict strategy. |
| `@lexical/file` | File nodes. | Requires file upload/download API. |
| `@lexical/mark` | Text marks/comments. | Requires comment/annotation model. |
| `@lexical/overflow` | Overflow-aware inline UI. | Only after toolbar/link UI requires it. |

## Product plugins to build locally

These should live under an editor plugin folder, not in `App.tsx`,
layout components, or shared UI primitives.

Suggested future shape:

```
web/app/src/components/editor/lexical/
  AnyLexicalEditor.tsx
  nodes/
  plugins/
  markdown/
  toolbar/
```

| Product plugin | What it does | Foundation |
| --- | --- | --- |
| `ObjectMentionPlugin` | `@` or `[[...]]` search for objects and inserts links/backlinks. | `LexicalTypeaheadMenuPlugin`, `LinkNode`, object query hooks. |
| `PropertyBlockPlugin` | Shows editable object properties inline inside a page. | Custom decorator nodes plus property resource hooks. |
| `DataViewEmbedPlugin` | Embeds a list/table/gallery view as a page block. | Custom decorator nodes plus data-view registry. |
| `BlockMenuPlugin` | Extend the current plus button and drag handle with block actions, duplicate/delete, and keyboard affordances. | Current `LexicalBlockMenu`, then `NodeEventPlugin`, `NodeContextMenuPlugin`, draggable block plugin. |
| `ContextualFormatMenuPlugin` | Richer selection/right-click formatting menu in our visual style. | Current local `LexicalSelectionMenu`, then `NodeContextMenuPlugin`, `NodeEventPlugin`, formatting commands. |
| `CalloutPlugin` | Callout blocks with icon/color. | Custom element node; markdown fallback required. |
| `ToggleHeadingPlugin` | Toggle headings and collapsible sections. | Custom element nodes or markdown-compatible transform. |
| `FileMediaPlugin` | Image/file blocks. | File API and card/preview design. |
| `TemplateInsertPlugin` | Insert object templates/snippets. | Typeahead menu plus template API. |
| `OutlinePlugin` | Page outline from headings. | `LexicalTableOfContentsPlugin`. |

## Recommended order

1. **Stabilize the plugin boundary.**
   Extract the local toolbar, slash menu, markdown bridge, and hydration
   into `components/editor/lexical/plugins/` before adding more
   visible editor features.
2. **Replace the slash menu with typeahead.**
   Use `LexicalTypeaheadMenuPlugin` so command search, object mentions,
   and later templates share the same menu primitive.
3. **Add low-risk markdown-friendly plugins.**
   Auto-link, clickable links, horizontal rule, and tab indentation can
   ship while persistence remains markdown.
4. **Expand block chrome carefully.**
   The minimal plus/drag block chrome is active. Add richer block
   actions, destructive actions, and mobile-specific behavior behind a
   feature flag until pointer, keyboard, and touch behavior are verified.
5. **Add Anytype-native plugins.**
   Object mentions, property blocks, and data-view embeds are the real
   product value; build them after the plugin folder exists.
6. **Defer storage-heavy plugins.**
   Tables, embeds, files, comments, and collaboration should wait for
   explicit API/storage decisions.

## Acceptance criteria for each plugin PR

- The plugin is isolated in its own component or hook.
- `MarkdownEditor` remains the only imported editor boundary outside
  the editor folder.
- If the plugin affects persistence, the spec states whether markdown
  round-trip is complete, lossy, or blocked.
- Risky plugins get a setting or feature flag.
- Component tests cover the plugin interaction, and markdown bridge
  tests cover persistence behavior when relevant.
