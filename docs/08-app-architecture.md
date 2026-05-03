# App architecture

> Status: **living architecture**. The Vite app under `web/app/` is the
> main frontend. Update this file in the same change as code that
> contradicts it — same rule as the other `docs/NN-*.md` files.

## What we're building

A minimal-design end-user app on top of `any`'s HTTP/JSON API, modelled
after Anytype but ruthlessly scoped down. v1 is a **multi-space,
single-user notes/objects app — your own spaces only, no sharing**:

- list & switch between your spaces; create / delete them,
- list & navigate the per-space object tree (`nav.parentId` / `nav.pos`),
- create / rename / delete objects,
- view & edit a markdown body via `GET/PUT /v1/spaces/:s/objects/:o/markdown`,
- read & write structured properties on objects via the typed
  property endpoints,
- pick from / create types & properties.

Joining someone else's space (`/spaces/join`), members, ACL, sync-status
UI, and files are explicitly **not v1** — they map onto endpoints that
today return `501 sdk.not_implemented` (see "API surface" below).

The product framing is "Anytype 2.0, minimal" — same domain model
(spaces / objects / types / properties), much smaller surface, room
to grow features in self-contained slices.

## What this doc decides

1. **Frontend stack** — what we build the app *with*.
2. **State model** — server state, client state, complex flows.
3. **UI kit & design tokens** — Radix primitives + in-repo wrappers
   + a 6-color semantic palette.
4. **Project layout** — where the code lives, how it relates to the
   Go binary.
5. **Dev loop** — how I edit, you reload.
6. **Build / ship pipeline** — how the app gets into the single
   `any` binary so deployment stays one file.
7. **Module boundaries** — the seams that make features additive.
8. **Feature roadmap** — the next ~8 PRs, mapped onto the API.

Sign off on these (or push back) before any code lands.

## API surface — what's actually usable today

Pulled from `docs/03-api.md` and `internal/server/handlers_*.go`. The
app v1 is bounded by what's *not* `501`.

### Real (handler exists, calls SDK)

- Meta: `GET /v1/health`, `POST /v1/shutdown`.
- Account: `GET /v1/account`.
- Spaces: `POST /v1/spaces`, `GET /v1/spaces`, `GET /v1/spaces/:s`,
  `DELETE /v1/spaces/:s`.
- Objects: `POST /v1/spaces/:s/objects` (server has `nav`
  auto-stamp; the app still sends an explicit `nav` home),
  `POST /v1/spaces/:s/objects/derive`,
  `POST /v1/spaces/:s/objects/query` (cross-object, the tree query),
  `DELETE /v1/spaces/:s/objects/:o`,
  `GET/PUT /v1/spaces/:s/objects/:o/markdown`.
- Data plane: `POST /v1/spaces/:s/query` (per-object dataset),
  `POST /v1/spaces/:s/modify`, `POST /v1/spaces/:s/delete-records`.
- Types: `GET /v1/spaces/:s/types`, `POST /v1/spaces/:s/types`,
  `GET /v1/spaces/:s/types/:t`,
  `GET /v1/spaces/:s/types/:t/properties`,
  `POST /v1/spaces/:s/types/:t/properties`.
- Properties on objects: `GET /v1/spaces/:s/properties/:o`,
  `POST /v1/spaces/:s/properties/:o/base/:t`.

### Stubbed (`501 sdk.not_implemented`)

- `PUT /v1/account/metadata`.
- `POST /v1/spaces/{join,derive,one-to-one}`.
- `DELETE /v1/spaces/:s/types/:t`,
  `DELETE/PATCH /v1/spaces/:s/types/:t/properties/:p`.
- `POST /v1/spaces/:s/properties/:o/{account,device,attach,detach}/:t`.
- All of `/members`, `/acl/*`.
- All of `/sync-status*`.

No `/subscribe` — push isn't in v1 server-side either. The app polls
or refetches on focus / mutation; the data layer hides this so swapping
in WebSockets later is one PR.

## Decision: frontend stack

**Pick: Vite + React 18 + TypeScript + Tailwind v4 + Radix UI primitives
+ class-variance-authority + clsx + tailwind-merge.**

Why React over Svelte / Solid / vanilla:

- We will need a real text editor and a real tree control. The mature
  options (TipTap / Lexical / ProseMirror; react-arborist; dnd-kit) are
  React-first.
- Hiring / external contribution surface is biggest in React.
- Anytype's own desktop client is React; conceptual reuse is non-zero.

Why Vite over Next.js / Remix / CRA:

- We're shipping a single-page app served by a Go binary, not an SSR
  app. Next/Remix solve problems we don't have.
- Vite's dev loop (HMR, ~50 ms reloads) is the best in the ecosystem
  and integrates cleanly with a backend-as-API-only setup via its dev
  proxy.
- CRA is unmaintained.

TypeScript, not JS — the API has enough shapes (filter / modify ops /
property values) that types pay for themselves on the first refactor.

Tailwind v4, **not** v3 — and not "no Tailwind." v4 is CSS-first: theme
tokens are CSS variables, no PostCSS pipeline, no `tailwind.config.js`.
Pairing v4 with class-variance-authority keeps variants in the
component definition (not in JSX), which avoids the long-class-string
problem that plagued v3. Reference: `craft-agents-oss/packages/ui/styles/index.css`
is a clean working example of "minimal design + Tailwind v4."

Routing: **wouter**. ~2 kB, hooks-based, sufficient for our route
graph (no nested layouts in v1).

Markdown editing: **BlockNote** (`@blocknote/core`, `@blocknote/react`,
`@blocknote/mantine`).
Block-based by design — closest match to Anytype's data model — with
built-in markdown round-trip (`blocksToMarkdown`, `tryParseMarkdownToBlocks`)
that lines up with our `markdown` endpoints. Sits on TipTap/ProseMirror
under the hood so we keep that ecosystem available if we ever need to
drop down a level. Theming is via CSS variables, so the BlockNote UI
inherits our tokens.css.

Important editor exception: `@blocknote/react` provides the editor core
and default controllers, but not the concrete component adapter that
renders the slash menu, formatting toolbar, side menu, and block drag
handles. Until we build our own adapter on `ComponentsContext`, the
editor module uses `@blocknote/mantine` for those BlockNote-specific
surfaces. Keep it lazy-loaded inside the editor module and themed by
`blocknote-theme.css`; do not use Mantine for the app UI kit.

The editor chrome follows `docs/specs/PR-025-anytype-editor-chrome.md`:
Anytype is the aesthetic reference for block controls and floating
menus, but the implementation stays original and scoped to our
BlockNote adapter. `MarkdownEditor.tsx` must pass the block UI flags
explicitly (`formattingToolbar`, `linkToolbar`, `slashMenu`) so
package upgrades cannot silently remove the block menu surface.
`sideMenu` is mounted through our local `SideMenuController` wrapper
so the plus button has a full-size hit target while BlockNote still
owns block drag/drop and the drag-handle menu. Keep
menu/toolbar/side-menu styling under the `anytype-block-editor` class
in `blocknote-theme.css`.

Tree: custom React rows backed by TanStack Query caches and
**dnd-kit** for drag/drop. The important invariant is still headless
rendering: we own the DOM for object rows, selection, context menus,
and lazy folder children.

The lexid allocator already needs a JS port (see `docs/03-api.md`
§ Moves) — we keep it in `web/app/src/lib/lexid.ts` and unit-test it
against fixtures generated by the Go side.

Considered and rejected for the tree:

- `@pierre/trees` (trees.software) — beautifully built, but
  *file-tree purpose-built* (git status badges, file-extension icons,
  flatten-empty-directories). We'd be fighting its defaults to render
  object rows. Beta status (1.0.0-beta.3) is a secondary concern.
- `react-arborist` — stable and capable; lower setup cost than
  headless-tree, less flexible. Reasonable fallback if headless-tree
  gives us trouble.

Icons: **lucide-react** — large, consistent, tree-shakeable.

## Decision: state model

### Object home vs list membership

Every object has one canonical home in the hierarchy: its `nav`
record (`nav.type`, `nav.parentId`, `nav.pos`). The tree/sidebar is
the source of truth for where the object lives. Lists are optional
views/memberships layered on top through `any.types` / user type ids;
they never decide the object's home.

Frontend creation paths must use `buildCreateObjectBody` /
`useCreateObject`, which always sends an explicit `nav` block. A list
or table row creation sends both parts: root or folder `nav` for the
home, plus `types: [typeId]` for membership in that database view.
This keeps future plugin/module surfaces honest: modules can add
membership, properties, or rendering, but not silently steal object
ownership from the hierarchy.

Three layers, three tools, kept separate.

### Server state — TanStack Query

Caching, invalidation, optimistic updates, refetch on focus, request
deduping. Every component that wants data from `/v1/...` does it
through a hook in `web/app/src/hooks/` (e.g. `useSpaces`,
`useObjectTree(spaceId)`, `useObjectMarkdown(spaceId, objectId)`).
Components never call `fetch` directly.

When `/subscribe` lands server-side, query invalidation becomes
WS-driven inside these hooks; consumers don't change.

### Client state — Jotai

Lots of small, distributed UI state — current selection, expanded
folders, search input, view mode, drag state per row, draft text per
object. Jotai's atoms model that naturally: each piece is independent,
re-renders are minimal by default, and derived state is a single
`atom((get) => …)` away.

Conventions:

- Atoms live under `web/app/src/atoms/`, one file per concern.
  Navigation, pane focus, tree selection, inline rename state, and
  pending bulk delete all belong to `atoms/view-state.ts`; the older
  `selection.ts`, `focus.ts`, and `tree-selection.ts` files are
  compatibility wrappers only.
- Per-object atoms use `atomFamily` keyed by object id; we wire a
  cleanup hook on object delete so families don't leak.
- `atomWithQuery` (jotai-tanstack-query) is the seam where server state
  becomes a derivable atom — used sparingly, only when a server value
  needs to feed into client-side derived atoms.

Why not Zustand: an Anytype-style app has 50+ small pieces of UI state,
not a few clear domains. Atoms compose better than stores at that
granularity, and selector discipline (Zustand's main tax) is a non-issue
in Jotai because subscriptions are per-atom.

### Complex flows — hand-rolled state machines, XState later

Some flows have meaningful *transitions* and benefit from explicit
states rather than ad-hoc booleans. We model them as TypeScript
discriminated unions driven by `useReducer`:

```ts
type EditorState =
  | { kind: 'idle' }
  | { kind: 'dirty';     text: string; since: number }
  | { kind: 'saving';    text: string }
  | { kind: 'saved';     versionId: string }
  | { kind: 'error';     text: string; error: ApiError }
  | { kind: 'conflict';  local: string; remote: string }
```

Same shape as a state machine, no library. If we end up with three or
four such flows, we adopt **XState** and migrate them; the seam is
clean because the state union shape doesn't change.

The flows we expect to use this pattern:

1. **Object editor** (PR #5) — first instance.
2. **Space sync status** (post-v1, when the SDK sync routes land).
3. **Type creation wizard** (PR #8).

## Decision: UI kit & tokens

A small in-repo design system at `web/app/src/components/ui/`, built on
Radix primitives and styled with Tailwind v4 + cva. It *is* our design
system — we just don't import someone else's.

### Primitives wrapped (PR #1)

| Primitive   | Radix package                        | Notes                                |
|-------------|--------------------------------------|--------------------------------------|
| Button      | (no Radix; native)                   | cva variants: `primary`, `ghost`, `danger`; sizes `sm`, `md` |
| Input       | (no Radix; native)                   | base text input + `Textarea` variant |
| Dialog      | `@radix-ui/react-dialog`             | for confirmations, type creation     |
| DropdownMenu| `@radix-ui/react-dropdown-menu`      | row context menu, header menu        |
| ContextMenu | `@radix-ui/react-context-menu`       | right-click on tree rows             |
| Popover     | `@radix-ui/react-popover`            | property edit popovers               |
| Tooltip     | `@radix-ui/react-tooltip`            | keyboard hints, sync state           |
| Toast       | `sonner`                             | save errors, undo affordance         |
| Label       | `@radix-ui/react-label`              | form labels                          |

Deferred but known: `cmdk` (command palette, post-v1), `motion`
(animation, when needed), **`@pierre/diffs`** (markdown diff visualisation
for the editor's `'conflict'` state in PR #5 — Pierre's library is
purpose-built for this and renders cleanly with our tokens).

### Design tokens

Lifted in shape from `craft-agents-oss/packages/ui/styles/index.css`,
trimmed to the 6 semantic colors. `web/app/src/styles/tokens.css`:

```css
@import "tailwindcss";

@theme {
  /* === 6 BASE COLORS (light mode) === */
  --color-background:  oklch(0.99  0.003 265);
  --color-foreground:  oklch(0.18  0.01  270);
  --color-accent:      oklch(0.58  0.22  293);  /* purple */
  --color-info:        oklch(0.75  0.16   70);  /* amber */
  --color-success:     oklch(0.55  0.17  145);  /* green */
  --color-destructive: oklch(0.58  0.24   28);  /* red   */

  /* === ALPHA STEPS (used for borders, hovers, overlays) === */
  --color-foreground-50:  color-mix(in oklab, var(--color-foreground) 50%,
                                    var(--color-background));
  --color-foreground-10:  color-mix(in oklab, var(--color-foreground) 10%,
                                    var(--color-background));
}

.dark {
  --color-background:  oklch(0.13  0.01  270);
  --color-foreground:  oklch(0.95  0.005 265);
  --color-accent:      oklch(0.72  0.22  293);
  --color-info:        oklch(0.82  0.16   70);
  --color-success:     oklch(0.70  0.17  145);
  --color-destructive: oklch(0.72  0.24   28);
}
```

Six colors, two states (light/dark) — that's the whole palette. Every
new component picks from these tokens; no per-component colors. If a
component wants e.g. a "panel" surface, it composes
`bg-foreground-10` rather than introducing a new token.

## Layout & interaction model

The app is a **3-pane desktop layout**, modelled 1:1 on the reference
screenshot in the proposal thread (Anytype's existing client) for v1,
diverging as features land:

- **Pane 1 (~280 px expanded, 0 px closed)** — spaces list with a
  persisted close/open toggle. Open mode shows search + labels;
  closed mode removes the pane entirely. Profile/help stay anchored
  bottom while open.
- **Pane 2 (~280 px, resizable)** — current space contents. Header
  with space name + actions; sectioned list (categories, Unread auto,
  Favorites pinned, Types). This pane can also close to 0 px.
- **Pane 3 (fills, resizable)** — the open object. Header with
  back/forward/breadcrumb, body, footer when applicable.

Decisions:

- **Resizable panes** — drag handles between pane 1↔2 and 2↔3. Min
  widths enforced (pane 1 = 0 % when closed, 14–28 % when open; pane
  2 = 0 % when closed, 17–38 % when open). Default expanded widths are
  the minimum useful open sizes (pane 1 = 14 %, pane 2 = 17 %) and are
  persisted per user via a Jotai atom + localStorage. Pane-1 and
  pane-2 closed state are stored separately. Reopening a closed left
  pane restores it to its minimum practical open width first (pane 1 =
  14 %, pane 2 = 17 %) so both sidebars do not return oversized on
  wide screens; users can resize wider after opening. Each open left
  pane persists its own width even when the other left pane is closed,
  so reopening the spaces rail does not inflate the current-space
  sidebar. Persisted widths seed the first render only; during a live
  session, refs updated from real resize-handle drags are the source of
  truth. `PanelGroup.onLayout` events caused by programmatic open/close
  are intentionally ignored because the splitter temporarily
  redistributes freed width into sibling panes; recording that
  redistribution makes the left panes codependent. Toggle paths set the
  intended `PanelGroup` layout in a layout effect before paint.
  Close/open uses a short flex-grow transition so the main area glides
  into place instead of snapping. The object-view header owns two
  Anytype-style
  buttons next to Back/Forward: one toggles the spaces rail, one
  toggles the current-space object sidebar. They remain visible in
  every open/closed combination.
- **Pane navigation state** — pane 3 keeps a lightweight history stack
  of `{spaceId, activeView}` entries, so Back/Forward can move across
  opened objects, type-table views, and space switches. Space changes
  also save the current pane-3 view into a per-space cache and restore
  it when the user comes back to that space; boot hydration restores
  the persisted active space's last pane without creating a Back entry.
- **Large-list performance** — shell lists should keep row work local:
  virtualize when item counts can grow, memoize row components, pass
  stable callbacks, and use `selectAtom` for row-specific slices of
  shared Jotai state.
- **Light + dark mode from PR #1.** Theme atom + system-preference
  detection by default; manual override persisted. Tokens.css ships
  both palettes from day one; every component must work in both. The
  app-level Settings dialog exposes the persisted preference as
  `System`, `White`, and `Dark`, and the pane-1 settings cog plus
  `Cmd+,` open that same dialog.
- **Desktop-only in v1.** Min window width 800 px. Below that we show
  a "use a wider window" notice rather than re-flow the layout.
  Mobile / responsive is a v2 concern.
- **Keyboard-first from PR #1.** A small, opinionated default set,
  expanded as features land:

  | Shortcut          | Action                                 |
  |-------------------|----------------------------------------|
  | `Cmd+1` / `2` / `3` | focus pane 1 / 2 / 3                |
  | `Cmd+K`           | command palette (cmdk; deferred to v1.1) |
  | `Cmd+,`           | open settings                          |
  | `Cmd+N`           | new object in active space             |
  | `Cmd+Shift+N`     | new space                              |
  | `↑` / `↓`         | navigate within focused list           |
  | `Enter`           | open focused item in pane 3            |
  | `/`               | focus search/filter in current pane    |
  | `Esc`             | close popover / return focus to list   |

  Discoverable via tooltips and a `?` overlay (deferred to v1.1).

## Project layout

Modelled on `craft-agents-oss/apps/electron/src/renderer/`, trimmed:

```
any/
├── cmd/any/                         (unchanged)
├── internal/
│   ├── server/
│   │   ├── web.go                   serves embedded SPA assets at /ui
│   │   ├── web/                     legacy dev harness — kept until
│   │   │   └── index.html           parity, then deleted
│   │   └── webapp/                  NEW: built SPA assets, embedded
│   │       └── dist/                copied here by `make web` / CI
│   └── …                            (unchanged)
└── web/
    └── app/                         NEW: the SPA source
        ├── package.json
        ├── tsconfig.json
        ├── vite.config.ts
        ├── index.html
        └── src/
            ├── main.tsx             Vite entry, mount <App/>
            ├── App.tsx              router + layout shell
            ├── app/                 tiny shell registries (view modules)
            ├── pages/               top-level routes (Tree, Object, Settings)
            ├── shared/              tiny reusable core utilities
            ├── components/
            │   ├── ui/              Radix wrappers (Button, Dialog, …)
            │   ├── tree/            object tree
            │   ├── editor/          markdown editor
            │   └── properties/      property panel
            ├── atoms/               Jotai atoms (selection, tree, search …)
            ├── lib/
            │   ├── api/             typed client, one file per
            │   │                    endpoint group (spaces, objects…);
            │   │                    currently also hosts query hooks
            │   ├── lexid.ts         JS port, tested against Go fixtures
            │   └── nav.ts           tree helpers (parent/pos/type)
            └── styles/
                ├── tokens.css       6-color palette, alpha steps
                └── globals.css      reset + typography
```

The Go binary keeps embedding HTML — but instead of one file it
embeds a *directory*:

```go
//go:embed all:webapp/dist
var webappFS embed.FS
```

`registerUIRoutes` serves the SPA's `index.html` for `/ui` and any
unknown path under `/ui/*`, and serves built assets (`/ui/assets/*`)
from the embedded FS. Same-origin requirement from CLAUDE.md is
preserved — the SPA fetches `/v1/...` on the same host.

## Dev loop

Two terminals on the developer's Mac:

```sh
# terminal 1 — backend
cd any && ./any run                              # 127.0.0.1:7001

# terminal 2 — frontend
cd any/web/app && pnpm dev                       # 127.0.0.1:5173
```

`vite.config.ts` proxies `/v1/*` → `http://127.0.0.1:7001`, so the
SPA, while served from Vite, makes same-origin-style requests against
the running Go server. HMR; type errors stream to the terminal.

Open `http://127.0.0.1:5173` for the in-development UI; the
production-style URL `http://127.0.0.1:7001/ui` continues to work and
serves the last-built SPA.

`pnpm` (not npm/yarn) — fastest installs, biggest pnpm-store dedup
once `web/app/node_modules/` starts to grow. Bun is the natural
alternative if we want a single tool for TS+package management later;
pnpm is the conservative pick for now.

## Build / ship pipeline

```sh
make web                    # cd web/app && pnpm install && pnpm build
                            # → web/app/dist
                            # → cp -r web/app/dist internal/server/webapp/dist

go build ./cmd/any          # embeds the SPA via //go:embed
```

CI lives at `.github/workflows/ci.yml`. It installs web dependencies,
runs frontend lint/typecheck/unit tests, builds the SPA and Storybook,
syncs the SPA into the Go embed directory, then runs `go vet`,
`go build`, `go test`, and the Playwright command. The Playwright
command is wired; meaningful e2e spec files should be added as app
flows stabilize.

Adding a release job that produces darwin-arm64 / darwin-amd64 /
linux-amd64 binaries follows the same pattern but is out of scope for
this PR.

## Module boundaries

The target shape is a **minimum core** plus feature modules. Core owns
process-wide providers, shell layout, storage/test adapters, query
client defaults, and registries. Everything product-specific lives in
a module that can be added, removed, or lazy-loaded without changing
the app shell.

Current core:

- `web/app/src/main.tsx` — process providers.
- `web/app/src/App.tsx` and `components/layout/AppShell.tsx` — the
  three-pane shell, including persisted pane widths and side-panel closed
  state.
- `web/app/src/app/viewModules.tsx` — registry for pane-3 view modules.
- `web/app/src/shared/` — tiny reusable utilities such as test-safe
  storage and virtual scrolling.
- `web/app/src/components/ui/` — primitive UI kit only.

Feature modules are still stored in `components/<feature>/` and
`lib/api/<group>.ts` while the app is small. Public barrels plus ESLint
deep-import rules make that shape safe today. A move to
`features/<name>/index.ts` is optional and should happen only when a
slice grows large enough to own UI, local state, and tests end-to-end.

Three layers, each independently swappable:

1. **Transport** (`web/app/src/lib/api/`). One `client.ts` doing
   `fetch` + error envelope decoding (the `{error:{code,message,details}}`
   shape from `docs/06-errors.md`). One file per endpoint group with
   typed `request → response` functions. **No React in this layer.**
2. **Data** (`web/app/src/lib/api/` today, `features/*/queries.ts`
   as modules grow, plus `web/app/src/atoms/`). TanStack Query hooks
   for server state; Jotai atoms for client state. Optimistic updates
   and cache invalidation live here. Components consume hooks/atoms,
   never raw `fetch`.
3. **View** (`web/app/src/components/`, `web/app/src/pages/`). Pure
   presentation; gets data from hooks/atoms. `components/ui/` is the
   primitive kit; the rest of `components/` is feature-shaped widgets
   composed into `pages/`.

Why this matters for "extensible": a new screen or pane-3 mode should
register one module entry point, then keep transport, state, and view
code inside that feature. No cross-feature imports; shared utilities
move to `shared/` or `components/ui/`.

### Type table module today

The current `type-table` pane-3 module is intentionally one local
`All` database view, not a saved-view system. It uses only the API
surface that exists now: object query pages, type properties, object
creation, and property patches. The local view can render as either
`Table` or `List`; that layout choice is stored per type in
`any.tables.viewLayouts.v1` and should be migrated when a real saved
view API exists.

The collection owns a light local toolbar with a Table/List segmented
control plus `Filter`, `Sort`, `Settings`, and `New` actions. Property
creation lives in `Settings` → `Properties` → `Add`, not as a separate
toolbar plus.
The list title and list icon are editable in the table header; those
values are local `type-meta` overrides keyed by `spaceId:typeId`, not
server writes. The same override powers pane-2 list rows and `+ New`
labels. Right-clicking a list row can rename, change the emoji icon, or
delete the list; delete currently means "hide locally" and never
removes objects, because `/types/:t` deletion still returns `501`.
The table surface follows the Anytype collection rhythm: a page-scale
list title, an `All` view tab, quiet icon+text toolbar controls on the
same line, and full-width horizontal table rules so sparse one-column
lists still feel like a real collection. Rows use soft 42 px rhythm,
subtle hover, and an icon-only open affordance; the add-row footer is
part of the table, not a detached CTA. Creating a table row creates a
normal hierarchy object first (explicit root `nav` unless another home
is supplied) and only then stamps the table's type id as list
membership.
The List layout uses the same query, filter, sort, property order, and
virtualizer as Table, but renders taller readable rows with an object
icon, title, up to four property preview pills, and a row-wide open
target with a visual arrow affordance. Do not fork data loading by
layout; layout is only presentation.
`Settings` opens a
right-side panel that behaves like a small navigation stack for
view/list configuration: layout, property visibility, filter, sort,
collection settings, templates, and future per-list options. Clicking
a settings row opens a full detail screen inside that panel with a
back affordance; do not use inline accordion sections for these
screens. Filter text and hidden columns are client UI state and may
reset on reload. Property column order is also local UI state: the
`Property visibility` detail screen can reorder user-property columns
by pointer-driven drag handles with live row shifting, and the table
header exposes matching hover/focus drag handles so users can reorder
columns in-place. `Name` is pinned first. Header edges expose
spreadsheet-style column resize handles;
widths are local persisted UI state keyed by type/property, while the
trailing add-column slot absorbs extra empty width so table rules still
run to the edge of the view. Sort maps to the existing query sort key. The `Properties`
detail screen is a list of Name plus user properties; `Add` opens an
in-panel create-property screen with a name input and type list, and
existing property rows open a metadata screen. Property
rename/type/delete remain read-only until the server exposes non-501
endpoints for those operations. Do not add Notion-style saved table /
board / calendar views until the app has an explicit view module
contract backed by either a server endpoint or a documented
type-metadata storage shape.

## Feature roadmap (proposed, ~8 PRs)

Each line ≈ one PR. Stops where the API does.

1. **Skeleton + UI kit + tokens** — Vite project, Tailwind v4 + Radix
   wrappers (Button, Input, Dialog, DropdownMenu, ContextMenu, Popover,
   Tooltip, Toast, Label), tokens.css, Go embed wiring, `/v1/health`
   ping rendered, CI green. *(this branch)*
2. **Spaces** — list spaces, create one, delete one, switch active
   space. Active space id persisted in `localStorage` via a Jotai atom.
3. **Object tree** — left sidebar tree of objects in the active space,
   built from `objects/query` filtered by `nav.parentId`, ordered by
   `nav.pos`. Lazy-loaded children. The space-name section header has
   fast actions for new root page, new root folder, and
   expand/collapse-all folders; the bulk toggle broadcasts a signal so
   folder rows can stay locally stateful for normal clicks. Folder
   creation uses a name dialog and does not select/open the folder,
   because folders are containers rather than block-editable pages.
   Folder rows reveal a hover `+` action on the right edge to create,
   select, and open a child object inside that folder.
   Shift/Cmd multi-select mirrors file managers, including
   Shift+ArrowUp/Down range extension through visible rendered rows so
   expanded folders work even with partial `nav` records; dragging a
   selected row onto a folder moves the whole selected set into that
   folder. Arbitrary multi-row reorder remains out of scope.
   Each space auto-ensures one default user list named `Pages`; new
   plain pages are stamped with that list id. The ensure path must be
   idempotent across multiple React hook callers and StrictMode
   remounts, and the sidebar collapses legacy duplicate `Pages` type
   records to one visible row.
4. **Object create/rename/delete** — round-trip through
   `POST/DELETE /objects` and the `properties .../base/:typeId`
   endpoints; optimistic updates via TanStack Query.
5. **Markdown editor** — TipTap, debounced PUT; first hand-rolled
   state machine (idle/dirty/saving/saved/error/conflict).
6. **Property panel** — render a typed object's properties, edit them
   in place via `properties .../base/:t`. Read type definitions from
   `types/:t/properties`.
7. **Tree DnD** — port lexid to TS, fixtures from Go; dnd-kit drop
   zones; reorder via `properties .../base/nav` patch.
8. **Type & property creation** — minimal flow to create a new type,
   add properties to it, and create objects of it. Second state
   machine (multi-step wizard).

After (8) we re-evaluate. Members/ACL/Sync status PRs are blocked on
SDK work flagged in `docs/07-roadmap.md`. If we're at 3+ state
machines by then, we adopt **XState** and migrate.

## Risks / open questions

- **Realtime.** Polling is fine for v1 but won't survive multi-device
  use. The right answer is the deferred `/subscribe` WebSocket. The
  data layer hides this; switching is mostly inside `hooks/` + `atoms/`.
- **Editor choice.** BlockNote was picked for block-based editing with
  built-in markdown round-trip. If a deeper Anytype block-model
  alignment is needed later, the seam is `components/editor/` and we
  can drop down to ProseMirror via BlockNote's TipTap layer without
  rewriting the host component.
- **Shipping the legacy `/ui`.** Plan: keep both for one release. The
  current dev harness moves to `/ui-legacy`, the SPA takes `/ui`. We
  drop `/ui-legacy` once the SPA covers the same query/object/markdown
  exercises.
- **Auth.** Currently none — the server is loopback-only (`docs/02-server.md`).
  When the remote-access story (v2) lands, the SPA needs an auth header
  flow. The transport layer is the right place for that to live.
- **Tailwind v4 maturity.** v4 is recent. If we hit a wall (missing
  plugin, broken IDE tooling), the fallback is plain CSS modules + the
  same tokens — no other layer changes.

## Maintenance rule

When a frontend change adds a new feature surface, prefer a module
entry point plus lazy registration over another direct import in the
shell. When a feature grows enough to need its own transport, queries,
state, and views, graduate it from `components/<feature>/` into
`features/<feature>/` and leave only shared primitives in `shared/`
or `components/ui/`.
