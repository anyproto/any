# Agent playbooks

This folder collects per-task playbooks for AI coding agents working
in this repo. They are deliberately short, repo-specific, and pointed
at concrete files.

When using Claude Code or the Claude Agent SDK, copy the relevant
playbook into your prompt (or symlink the folder to `.claude/skills/`
if you want them auto-loaded).

| Playbook                                           | When to use                                            |
| -------------------------------------------------- | ------------------------------------------------------ |
| [`frontend-component.md`](./frontend-component.md) | Adding a UI primitive to `web/app/src/components/ui/`. |
| [`frontend-feature.md`](./frontend-feature.md)     | Adding a user-visible feature (page, screen, flow).    |
| [`bug-fix.md`](./bug-fix.md)                       | Fixing a bug. Reproducer test first, then fix.         |

## Conventions every playbook assumes

- TypeScript strict mode, including `noUncheckedIndexedAccess` and
  `exactOptionalPropertyTypes`. No `any` without a comment justifying it.
- Imports use the `@/` alias for `web/app/src/*`.
- Tailwind v4. Styling via tokens in `src/styles/tokens.css`. No
  per-component colors, no hex codes.
- Components forward refs.
- Server state through TanStack Query hooks in `src/lib/api/<group>.ts`.
- Client state through Jotai atoms in `src/atoms/`.
- Tests via Vitest + React Testing Library + `vitest-axe`.
- Stories via Storybook 8, one story file alongside the component.
- Per-PR specs in `docs/specs/PR-NNN-*.md` are the contract for any
  non-trivial PR. Read the spec before writing code.

## Frontend architecture contract

- Keep the shell small: `main.tsx`, `App.tsx`, app shell, `shared/`,
  and `components/ui/` should not learn product-specific rules about
  spaces, objects, types, editors, or tables.
- Product behavior belongs in feature modules, API resource modules,
  and registries such as `web/app/src/app/viewModules.tsx`.
- Server state goes through typed TanStack Query hooks. Components do
  not call `fetch` directly.
- Persisted Jotai atoms use `appStorage()` from `@/shared/storage`, so
  Vitest and constrained browsers get the same storage contract.
- Structured object reads/writes go through the public resource barrels
  (`@/lib/api`, `@/lib/api/objects`, `@/components/properties`, etc.).
  Do not deep-import private resource modules unless a barrel explicitly
  exposes no suitable API.
- Every object has one canonical home in the hierarchy via `nav`.
  Lists/types are optional memberships layered on top; list/table row
  creation must preserve hierarchy home and add membership separately.
- Data views are modular: a root view component composes controller
  hooks, property state hooks, toolbar, row renderers, and settings
  screens. New layouts should reuse the data controller and property
  model instead of forking queries or cache behavior. See
  `docs/specs/PR-012-type-tables.md` for the current table/list view
  contract.
- Object-page editors are selected behind `editorEngineAtom`, not by
  changing app-shell imports. `MarkdownEditor` is the stable boundary;
  Lexical is the default plugin-oriented engine and BlockNote remains
  the fallback engine documented in
  `docs/specs/PR-026-lexical-editor-spike.md`. Before adding editor
  plugins, read `docs/specs/PR-027-lexical-plugin-roadmap.md` and keep
  new Lexical behavior isolated as plugins rather than growing one
  editor component.
- Prefer one or two local reducer state machines for complex flows.
  If a third independent machine appears, adopt a proper state-machine
  library instead of growing bespoke reducers.

## Stop and ask the human if

- The change conflicts with this frontend contract or the active
  per-PR spec.
- A new dependency would be needed (especially component libraries,
  state libraries, or anything overlapping the existing design system).
- The change touches `internal/server/handlers_*.go` or anything else
  that crosses the HTTP API contract.
