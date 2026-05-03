# PR #1 — Foundations

> Spec for the first frontend PR. Code lands only after this is
> approved. After this PR merges, every subsequent feature PR has the
> same guardrails (lint, types, tests, snapshots, a11y, CI).

## Goal

Stand up the SPA project, design system primitives, theming, and the
Go embed pipeline. Prove the dev loop and the production build path
with a single placeholder page that pings `GET /v1/health`.

**Zero product features in this PR.** Spaces, objects, editor, etc.
all come later. The only screen here is a placeholder Health page.

## Non-goals

- Multi-pane layout (PR #2).
- Real data fetching beyond `/v1/health` (PR #3+).
- Light/dark *content* design polish — only the toggle and the tokens
  are in scope. Each later PR refines its own surfaces in both modes.
- Icons / illustrations beyond what `lucide-react` ships.

## Scope

### Project & tooling

- `web/app/` — Vite + React 18 + TypeScript with `strict: true` and
  `noUncheckedIndexedAccess: true`.
- Package manager: **pnpm**. Lockfile committed.
- Tailwind v4 (via `@tailwindcss/vite`), tokens.css with the 6-color
  palette + alpha steps + dark-mode block from the frontend agent
  contract in `docs/agents/README.md`.
- Component-variant stack: `class-variance-authority`, `clsx`,
  `tailwind-merge`. A `cn()` helper at `src/lib/cn.ts`.
- Linting: ESLint with `@typescript-eslint`, `eslint-plugin-react`,
  `eslint-plugin-react-hooks`, `eslint-plugin-jsx-a11y`. Prettier for
  formatting. ESLint config rejects `console.log`, unused imports,
  any `any`, and a11y violations.
- Pre-commit: `husky` + `lint-staged` — runs eslint + prettier + tsc
  on staged files.
- Unit/component tests: **Vitest** + `@testing-library/react` +
  `@testing-library/user-event` + `vitest-axe` for a11y assertions.
- E2E + visual regression: **Playwright** with `@playwright/test`.
  Visual snapshots stored under `e2e/snapshots/`.
- UI catalog: **Storybook 8** with the `@storybook/react-vite` builder.
  One story file per primitive showing every variant + state.

### Design system primitives (`src/components/ui/`)

Each primitive ships with: component, story, test (incl. axe-core),
and an entry in `components/ui/index.ts`.

- `Button` — cva variants: `primary | ghost | danger`; sizes: `sm | md`.
- `Input`, `Textarea` — bare; consumes `--color-foreground-10` for
  borders, `--color-accent` for focus ring.
- `Label` — `@radix-ui/react-label`.
- `Dialog` — `@radix-ui/react-dialog` with header/body/footer slots.
- `DropdownMenu` — `@radix-ui/react-dropdown-menu`.
- `ContextMenu` — `@radix-ui/react-context-menu`.
- `Popover` — `@radix-ui/react-popover`.
- `Tooltip` — `@radix-ui/react-tooltip`, 200 ms delay default.
- `Toast` — `sonner` mounted in `<App/>`; thin wrapper exporting
  `toast.success / error / info` typed against our tokens.

### State plumbing

- TanStack Query `<QueryClientProvider>` at the app root, with a
  shared `queryClient` exported from `src/lib/queryClient.ts` (default
  staleTime 30 s, retry 1).
- Jotai `<Provider>` at the app root.
- One real atom: `themeAtom` at `src/atoms/theme.ts` — `'light' | 'dark' | 'system'`,
  persisted to localStorage; resolves to a concrete mode with a
  derived `resolvedThemeAtom`. Applies `data-theme="dark"` to `<html>`.

### API client (`src/lib/api/`)

- `client.ts` — `apiFetch<T>(path, init)` doing `fetch`, JSON
  serialization, error envelope decoding (`{error:{code,message,details?}}`
  per `docs/06-errors.md`). Throws a typed `ApiError` carrying `code`,
  `message`, `status`, `details`.
- `meta.ts` — exports `getHealth()` returning the typed `HealthResponse`.
  No other endpoints in this PR.
- Types live next to handlers; no shared schema generator yet (deferred).

### Health page (`src/pages/HealthPage.tsx`)

The only product surface. Placeholder for the layout shell that
arrives in PR #2. Renders three states cleanly.

```
┌──────────────────────────────────────┐
│ any                          [☾ / ☀] │   ← title + theme toggle
│                                      │
│ ●  Server: ok                        │
│    Account:  A9WYn…UJ99jgXJ          │
│    Version:  any dev (commit none)   │
│    Started:  2026-05-01 19:08:36 UTC │
│                                      │
└──────────────────────────────────────┘
```

States:

- **Loading** — skeleton lines for the four labelled rows.
- **Error** — the row pattern collapses to a single error card using
  the `destructive` token; shows `error.code` and `error.message`.
- **OK** — as sketched. Status dot colored with `success` token.

### Go embed pipeline

- `internal/server/webapp/dist/` — destination for built SPA. Gitignored;
  a `.gitkeep` keeps the directory.
- `internal/server/webapp.go` — `//go:embed all:webapp/dist` plus a
  small handler that serves `index.html` for `/ui` and `/ui/*`, and
  built assets for `/ui/assets/*`. `Cache-Control: no-cache` on
  `index.html`, immutable on hashed asset paths.
- `Makefile` — adds `web` target: `cd web/app && pnpm install --frozen-lockfile && pnpm build && rm -rf ../../internal/server/webapp/dist && cp -r dist ../../internal/server/webapp/dist`.
- The legacy `internal/server/web/index.html` stays untouched; PR #2
  remaps the new SPA to `/ui` and the legacy page to `/ui-legacy`.

### CI (`.github/workflows/ci.yml`)

Single job, `ubuntu-latest`, Node 20, pnpm 9, Go 1.26.2. Steps:

1. `pnpm install --frozen-lockfile`
2. `pnpm -C web/app lint`
3. `pnpm -C web/app typecheck`
4. `pnpm -C web/app test --run`
5. `pnpm -C web/app build`
6. `pnpm -C web/app build-storybook`
7. `make web && go build ./cmd/any && go test ./...`
8. `pnpm -C web/app exec playwright install --with-deps chromium`
9. `pnpm -C web/app test:e2e` — boots `./any run` against a temp data
   dir + the bundled stage1 nodeconf, navigates `/ui`, runs the
   Playwright spec.
10. Upload `playwright-report/` and `e2e/snapshots/` as artifacts.

CI fails closed: any non-zero step blocks the PR.

## Acceptance criteria (testable)

A PR reviewer can verify each of these by running the listed command.

1. `pnpm -C web/app dev` boots Vite at `127.0.0.1:5173` with `/v1/*`
   proxied to `127.0.0.1:7001`. Visiting `/` shows the Health page.
2. `pnpm -C web/app build && make web && go build ./cmd/any && ./any run`
   serves the **same** Health page at `http://127.0.0.1:7001/ui`.
3. `pnpm -C web/app test --run` is green; coverage report generated.
4. `pnpm -C web/app test:e2e` is green; visual snapshots match.
5. `pnpm -C web/app lint` and `pnpm -C web/app typecheck` exit 0.
6. `pnpm -C web/app storybook` renders a story for **every** primitive
   listed above; each story shows all variants and states.
7. The theme toggle persists across reloads (localStorage). Initial
   theme follows `prefers-color-scheme` until the user overrides.
8. Every primitive's component test calls `expect(await axe(...)).toHaveNoViolations()`.
9. Tabbing through the Health page reaches the theme toggle and any
   actionable element with a visible `:focus-visible` ring (the
   `--color-accent` token).
10. CI on a fresh branch runs all of the above and blocks merge on
    failure.

## Test plan

- **Unit** — `apiFetch` decodes both success and error envelopes;
  unknown shapes produce a generic `ApiError` with `code: "client.bad_response"`.
- **Component** — every `ui/` primitive renders correctly, has no
  axe-core violations, supports keyboard interaction (Enter/Space on
  buttons, Esc on dialogs/popovers).
- **Component** — `HealthCard` renders all three states from injected
  query state (mock `useQuery`).
- **E2E** — Playwright: start the binary, GET `/ui`, assert
  "Server: ok" appears within 3 s, snapshot the rendered card in both
  light and dark modes.
- **Visual regression** — `HealthCard` OK state, light + dark.

## Files added (approximate)

```
web/app/
  package.json              pnpm-lock.yaml
  tsconfig.json             tsconfig.node.json
  vite.config.ts            vitest.config.ts
  playwright.config.ts
  .eslintrc.cjs             .prettierrc
  .storybook/main.ts        .storybook/preview.tsx
  index.html
  src/
    main.tsx                App.tsx
    pages/HealthPage.tsx (+ .test.tsx)
    components/
      ui/Button.tsx (+ .stories.tsx + .test.tsx)
      ui/Input.tsx + Textarea.tsx (+ stories + tests)
      ui/Label.tsx (+ stories + tests)
      ui/Dialog.tsx (+ stories + tests)
      ui/DropdownMenu.tsx (+ stories + tests)
      ui/ContextMenu.tsx (+ stories + tests)
      ui/Popover.tsx (+ stories + tests)
      ui/Tooltip.tsx (+ stories + tests)
      ui/Toast.tsx (+ stories + tests)
      ui/index.ts
      health/HealthCard.tsx (+ stories + tests)
    atoms/theme.ts (+ test)
    lib/
      api/client.ts (+ test)
      api/meta.ts
      cn.ts
      queryClient.ts
    styles/tokens.css   styles/globals.css
    test/setup.ts       test/a11y.ts
e2e/
  health.spec.ts        snapshots/...
.github/workflows/ci.yml
.husky/pre-commit
internal/server/
  webapp/dist/.gitkeep
  webapp.go (+ test)
Makefile (`web` target added)
.gitignore (+ web/app/node_modules, web/app/dist, internal/server/webapp/dist)
```

Estimated diff: 30–40 files, ~1,500 lines, mostly skeleton + config.
Large for a single PR but **must** land together — CI doesn't work
until everything is wired. The PR description lists each section
above as a top-level heading so the reviewer can scan it.

## Out of scope (next PR or later)

- Layout shell (3 panes) — PR #2.
- Wouter routing beyond a single route (`/`) — PR #2.
- BlockNote — PR #5.
- Storybook deploy / hosted preview — post-v1.
- Sentry / error tracking — post-v1.
- i18n — post-v1.

## Open questions

- **Storybook version.** v8 ships stable; v9 is in beta. Default v8.
- **Playwright matrix.** Chromium only in this PR; Firefox/WebKit
  added in PR #2 once the layout exists.
- **CI Go cache.** GitHub-hosted runners with `go-version: 1.26.2`
  and `actions/setup-go` should be enough; revisit if cold builds
  exceed 5 min.

## Approval

Sign off on this spec, then I open PR #1 with the implementation. The
spec PR (this branch) merges first; the implementation PR follows,
referencing this doc.
