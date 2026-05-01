# Agent playbooks

This folder collects per-task playbooks for AI coding agents working
in this repo. They are deliberately short, repo-specific, and pointed
at concrete files.

When using Claude Code or the Claude Agent SDK, copy the relevant
playbook into your prompt (or symlink the folder to `.claude/skills/`
if you want them auto-loaded).

| Playbook | When to use |
|----------|-------------|
| [`frontend-component.md`](./frontend-component.md) | Adding a UI primitive to `web/app/src/components/ui/`. |
| [`frontend-feature.md`](./frontend-feature.md) | Adding a user-visible feature (page, screen, flow). |
| [`bug-fix.md`](./bug-fix.md) | Fixing a bug. Reproducer test first, then fix. |

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

## Stop and ask the human if

- The change conflicts with `docs/08-app-architecture.md` or the
  active per-PR spec.
- A new dependency would be needed (especially component libraries,
  state libraries, or anything overlapping the existing design system).
- The change touches `internal/server/handlers_*.go` or anything else
  that crosses the HTTP API contract.
