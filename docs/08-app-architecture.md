# App architecture (proposal)

> Status: **proposal**, not yet implemented. This doc is the gate for
> the first PR. The current `internal/server/web/index.html` is a dev
> harness; "the app" is a separate, real frontend that will replace it
> over time. Update this file in the same change as code that contradicts
> it — same rule as the other `docs/NN-*.md` files.

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
- Objects: `POST /v1/spaces/:s/objects` (with `nav` auto-stamp),
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

Markdown editing: **BlockNote** (`@blocknote/core`, `@blocknote/react`).
Block-based by design — closest match to Anytype's data model — with
built-in markdown round-trip (`blocksToMarkdown`, `tryParseMarkdownToBlocks`)
that lines up with our `markdown` endpoints. Sits on TipTap/ProseMirror
under the hood so we keep that ecosystem available if we ever need to
drop down a level. Theming is via CSS variables, so the BlockNote UI
inherits our tokens.css; we **do not** import `@blocknote/mantine` —
its visual opinions would clash with our design system.

Tree: **`@headless-tree/react`** (Lukas Bach; the official successor
to `react-complex-tree`). Truly headless — we own all DOM rendering,
which matches our object-row needs (custom icon + title + property
preview + sync dot per row, not a generic file row). Built-in keyboard
drag-and-drop lines up with the "keyboard-first from PR #1" commitment.
Falls back to **dnd-kit** only if a feature it doesn't cover surfaces.

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

- Atoms live under `web/app/src/atoms/`, one file per concern
  (`atoms/selection.ts`, `atoms/tree.ts`, `atoms/search.ts`).
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

- **Pane 1 (~80 px, fixed-min)** — vertical strip of spaces. Pinned
  spaces at top, scrollable list, profile/help anchored bottom.
- **Pane 2 (~280 px, resizable)** — current space contents. Header
  with space name + actions; sectioned list (categories, Unread auto,
  Favorites pinned, Types).
- **Pane 3 (fills, resizable)** — the open object. Header with
  back/forward/breadcrumb, body, footer when applicable.

Decisions:

- **Resizable panes** — drag handles between pane 1↔2 and 2↔3. Min
  widths enforced (pane 1 = 60 px collapsed, 80 px default; pane 2 =
  220 px min). Widths persisted per user via a Jotai atom +
  localStorage.
- **Light + dark mode from PR #1.** Theme atom + system-preference
  detection by default; manual override persisted. Tokens.css ships
  both palettes from day one; every component must work in both.
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
            ├── pages/               top-level routes (Tree, Object, Settings)
            ├── components/
            │   ├── ui/              Radix wrappers (Button, Dialog, …)
            │   ├── tree/            object tree
            │   ├── editor/          markdown editor
            │   └── properties/      property panel
            ├── atoms/               Jotai atoms (selection, tree, search …)
            ├── hooks/               TanStack Query hooks (useSpaces, …)
            ├── lib/
            │   ├── api/             typed client, one file per
            │   │                    endpoint group (spaces, objects…)
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

CI: one job runs `make web && go build && go test ./...`. Adding a
release job that produces darwin-arm64 / darwin-amd64 / linux-amd64
binaries follows the same pattern but is out of scope for this PR.

## Module boundaries

Three layers, each independently swappable.

1. **Transport** (`web/app/src/lib/api/`). One `client.ts` doing
   `fetch` + error envelope decoding (the `{error:{code,message,details}}`
   shape from `docs/06-errors.md`). One file per endpoint group with
   typed `request → response` functions. **No React in this layer.**
2. **Data** (`web/app/src/hooks/` + `web/app/src/atoms/`). TanStack
   Query hooks for server state; Jotai atoms for client state.
   Optimistic updates and cache invalidation live here. Components
   consume hooks/atoms, never raw `fetch`.
3. **View** (`web/app/src/components/`, `web/app/src/pages/`). Pure
   presentation; gets data from hooks/atoms. `components/ui/` is the
   primitive kit; the rest of `components/` is feature-shaped widgets
   composed into `pages/`.

Why this matters for "extensible": a new screen is one file in
`pages/`, plus zero or more new files under `components/`, plus zero or
more new hooks/atoms, plus zero or one new file under `lib/api/`. No
cross-screen imports; shared utilities live in `lib/` or
`components/ui/`.

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
   `nav.pos`. Lazy-loaded children. No DnD yet.
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

## Not in this PR

- Any code under `web/app/`.
- Any change to the Go binary.
- Any UI tests.

This PR is the doc only. If the doc is wrong, fixing words is cheap;
fixing words after the scaffold is shipped is not.
