# Architecture evaluation — agent-friendly modularity

> Audit of the `any` codebase as of PR #27. Goal: surface the
> structural weak spots that make it dangerous for AI coding agents
> to extend, and lay out a target architecture they can land on
> safely.

The data below is from a code-survey pass — file paths and counts
are concrete, recommendations are prioritised by impact-vs-cost.

## Current implementation status — 2026-05-03 (re-audit)

This document started as a PR #27 audit. A re-audit confirms strong
progress on the objects API, public barrels, and shared property
editing. The frontend is materially easier to extend, but the big
architecture risks are now concentrated in execution gaps: component
tests still lag the decomposition, SPA e2e coverage is absent, and the
production build still has large chunks to tune.

### ✅ Landed

- **`lib/api/objects.ts` is a barrel** over six focused modules
  (`model.ts`, `queries.ts`, `mutations.ts`, `transport.ts`,
  `cache.ts`, `index.ts`). Public surface is what gets re-exported.
- **`ObjectModel` + `readObjectProp` / `patchObjectProp`** facade is
  in use. TableView, TableRows, and ObjectTypeBar no longer cast
  per-type namespaces directly. The remaining `as Record<string,
  unknown>` casts are centralized in `lib/api/objects/model.ts` and
  `lib/api/client.ts`.
- **ESLint blocks deep imports** into `@/lib/api/objects/*` — the
  object barrel is enforced, not aspirational.
- **ESLint also blocks legacy property editor imports** from
  `@/components/tables/PropertyCell` and
  `@/components/tables/cells/*`.
- **Collision-safe UI keys are centralized in `shared/keyOf`**.
  Type metadata, table column widths, object title drafts, Add
  Property list keys, and view module keys no longer join opaque ids
  with `:`.
- **`cache.ts` exists** with `objectKeys` factories and predicate
  helpers (`isObjectsQueryForSpace`,
  `isObjectsByTypeQueryForSpace`). Mutations now invalidate via
  these helpers instead of inline `predicate: q => …`.
- **Per-resource query-key factories are the intended cache shape.**
  `objectKeys`, `spaceKeys`, and `typeKeys` keep cache ownership near
  each API resource. This is preferable to one global `cache.x.y`
  namespace because adding a resource does not require editing a mega
  registry.
- **Optimistic table cell commits use `objectKeys` root factories**
  instead of hardcoded `['objects', …]` fragments.
- **Object transport validates response shapes** for query/create
  responses and throws `client.bad_response` instead of treating bad
  200s as empty data.
- **Sidebar type counts paginate** instead of silently truncating at
  5000 objects.
- **Property rendering switches are exhaustive** via `assertNever`.
- **Lazy view modules now sit behind an ErrorBoundary** so failed
  chunks render a failure state instead of staying in Suspense
  fallback forever.
- **Cold navigation now seeds from existing sidebar/tree caches.**
  Type table titles merge sparse detail responses with the cached
  type list, object titles initialize from cached tree/table records,
  and view modules preload on hover/focus to reduce first-open visual
  jumps.
- **Space switching is warmed and layout-stable.** Space detail views
  seed from the spaces rail list, rail rows prefetch the target
  space's detail/types/root-tree caches plus the pane-3 view that will
  be restored for that space, and cold Lists/tree/table loading states
  now stay visually quiet instead of flashing transient skeleton bars.
- **Object title drafts are bounded and cleaned up on delete.** The
  live draft map keeps at most 100 entries, single/bulk/orphan delete
  flows clear deleted object drafts, and keys remain collision-safe.
- **Public barrels exist for the main API/component folders touched
  so far**: `ui/`, `atoms/`, `lib/api/`, `lib/api/objects/`,
  `types/`, `editor/`, `tree/`, `layout/`, `spaces/`, `objects/`,
  `tables/`, `health/`, `settings/`, `properties/`, and `shared/`.
- **TableView decomposed into a data-view module.** `TableView.tsx`
  is now a composition shell around `useTableViewController`
  (queries, filter, sort, paging, virtualization, creation),
  `useTableProperties` (type properties, visibility, order, drag,
  widths), `TableToolbar`, `TableColumnHeader`, `TableSettingsPanel`,
  `ListRowsView`, and `TableRows`. Nested settings routes live under
  `components/tables/settings/`, so adding new list/view settings does
  not grow the root table component. Shared property editing remains in
  `components/properties/PropertyCell.tsx` plus
  `components/properties/cells/*`.
- **View state is consolidated in `atoms/view-state.ts`.** Active
  space, pane-3 view/history, focused pane, tree multi-selection,
  inline rename state, and pending bulk delete now have one model
  (`viewStateAtom`) plus derived per-slice atoms for existing call
  sites. `selection.ts`, `focus.ts`, and `tree-selection.ts` remain as
  tiny compatibility wrappers.
- **CI workflow exists.** `.github/workflows/ci.yml` runs the frontend
  gates, production SPA build, Storybook build, Go vet/build/test, and
  the Playwright command. It opts JavaScript actions into Node 24 and
  skips Playwright cleanly when the private stage1 nodeconf is absent,
  so missing E2E fixtures no longer surface as curl exit 7. The repo
  now enforces the discipline instead of relying on local habit.
- **High-risk component test backfill landed.** Component tests now
  cover space create/edit/delete dialogs, object create-folder/delete/
  bulk-delete dialogs, list rename/delete dialogs, layout header/avatar
  primitives, and the editable property cells. The backfill also caught
  and fixed an invalid nested-button DOM structure in `RelationCell`.
- **Direct table-module tests landed.** The table refactor is now
  verified at the new module boundaries: `useTableViewController`,
  `useTableProperties`, `TableToolbar`, `TableRows`, and
  `TableSettingsPanel` have focused tests in addition to the broader
  `TableView` integration tests.
- **`SpaceContents.tsx` is composition only.** Space sidebar behavior is
  now split across `useSpaceContentsController`, `SpaceHeader`,
  `HierarchySection`, and `ListsSection`, so adding hierarchy actions or
  list-specific behavior no longer grows one sidebar container.
- **`ObjectRow.tsx` is split by responsibility.** Selection state,
  keyboard range behavior, rename input rendering, hover actions, and
  context-menu actions live in `useObjectRowSelection`,
  `useObjectRowKeyboard`, `ObjectRowRename`, and `ObjectRowActions`.

### Intentional tradeoffs

- **ESLint deep-import rules now enforce the public barrels for the
  major frontend modules.** Rules cover atoms, objects API, shared
  utilities, `ui/`, `types/`, `health/`, `settings/`, `tree/`,
  `properties/`, `spaces/`, `objects/`, `tables/`, and legacy
  table-owned property editor paths. The editor module still keeps
  targeted deep imports for lazy chunk entrypoints and focused test
  mocks.
- **Feature folders are optional, not a correctness gap.** The current
  enforced shape (`atoms/`, `lib/api/<resource>`, `components/<area>`)
  is coherent. Move to `features/<name>/` only when a product slice
  grows enough to own UI, local state, and tests end-to-end.

### ✗ Still open

- **A few heavy UI surfaces still need focused tests.** The table module
  split now has direct coverage, but `ListRowsView`, `CreateTypeDialog`,
  `AddColumnPopover`, `ObjectTypeBar`, and `MarkdownEditor` still rely
  mostly on broader integration-style coverage.
- **Large build chunks remain.** BlockNote is already lazy-loaded into
  the editor chunk, but the async editor chunk is still large and the
  main app chunk is just over Vite's default warning threshold.
- **No SPA e2e spec files, no feature flags, no spec-link CI check**.

## TL;DR (post-refactor)

| # | Weak spot | Status | Severity | Next step |
|---|---|---|---|---|
| 1 | Hook sprawl in `lib/api/objects.ts` | ✅ split into 6 modules behind a barrel | resolved | — |
| 2 | Selection state fragmented across 9 atoms | ✅ consolidated in `atoms/view-state.ts` | resolved | keep new view state in the model |
| 3 | No public API barrels | ✅ main barrels exist and major imports are lint-enforced | resolved | keep new modules behind barrels |
| 4 | Cache invalidation is ad-hoc | ✅ per-resource factories (`objectKeys`, `spaceKeys`, `typeKeys`) | resolved | keep cache keys beside each API resource |
| 5 | Test coverage | ◐ dialogs/cells and core table modules have direct coverage | LOW | backfill `ListRowsView` and heavy editor/type surfaces |
| 6 | scattered `as Record<string, unknown>` casts | ✅ removed from table/editor surfaces; centralized in model/client | resolved | — |
| 7 | No feature-folder boundaries | ◐ optional; current barrels are enforced | LOW | pilot only when a slice grows large |
| 8 | Specs drift from code | ✗ | MEDIUM | spec-link CI check |
| 9 | No SPA e2e test files | ✗ command is in CI, tests still missing | MEDIUM | Playwright smoke per feature |
| 10 | No feature flags / kill switches | ✗ | LOW | flags atom |
| 11 | ESLint barrel rule only covers a few high-risk paths | ✅ major frontend barrels enforced | resolved | add rules with new barrels |
| 12 | Property editors lived under tables but were used by editor too | ✅ moved to `components/properties` | resolved | — |
| 13 | Opaque ids joined with `:` for UI keys | ✅ replaced with `shared/keyOf` at known sites | resolved | — |
| 14 | Bad object API 200 responses could become empty data | ✅ guarded in objects transport | resolved | — |
| 15 | Lazy chunk failure looked like endless loading | ✅ view ErrorBoundary added | resolved | — |
| 16 | Large build chunks | ✗ editor async chunk + app chunk need tuning | MEDIUM | manual chunks / dependency audit |

## 1. Hook sprawl — `lib/api/objects.ts`

**Evidence (audit)**: 13 exported hooks in one file. Three of them
hit the same endpoint with subtly different semantics:

- `useObject(spaceId, id)` — single record by id
- `useObjectName(spaceId, id)` — single record's name only
- `useObjectsByType(...)` — all records of a type
- `useObjectsByTypeInfinite(...)` — paged variant
- `useObjectsBySpace(spaceId)` — every item in space (relation picker)
- `useObjectChildren(spaceId, parentId)` — tree view
- `useTypeObjectCounts(spaceId, typeIds)` — sidebar counts

Plus mutations: `useCreateObject`, `useDeleteObject`, `useRenameObject`,
`useRenameObjectAnywhere`, `useMoveObject`, `useSetObjectProperty`.

**Why it bites agents**: an agent landing on "I need to read an
object" has to read 200 lines of file to know which hook to pick —
and several pairs (`useObject` vs `useObjectName`,
`useRenameObject` vs `useRenameObjectAnywhere`) differ only in
cache invalidation strategy. Picking the wrong one silently leaks
stale data.

**Fix**:

1. Split by intent into three modules:
   - `lib/api/objects/queries.ts` — read hooks, deduped to four:
     `useObject`, `useObjectsByType`, `useObjectsBySpace`,
     `useObjectChildren`. (Counts and infinite variants stay as
     options on the same hook.)
   - `lib/api/objects/mutations.ts` — write hooks. Collapse the
     two rename hooks into one with optional `parentId`.
   - `lib/api/objects/index.ts` — public barrel: re-exports the
     trimmed surface; nothing else.
2. Mark the now-private hooks `@internal` JSDoc + don't re-export.
3. Add a one-paragraph "which hook should I use?" preamble at top
   of the barrel.

## 2. Selection state consolidated in `atoms/view-state.ts`

**Status (2026-05-03)**: resolved for user-facing view state.
`activeSpaceIdAtom`, `activeViewAtom`, `activeObjectIdAtom`,
`activeTypeIdAtom`, `focusedPaneAtom`, `renamingObjectIdAtom`,
`selectedTreeIdsAtom`, `treeSelectionAnchorAtom`,
`treeSelectionKeyboardEdgeAtom`, and `pendingBulkDeleteAtom` are now
owned by `web/app/src/atoms/view-state.ts`.

The old `selection.ts`, `focus.ts`, and `tree-selection.ts` files are
compatibility wrappers so local tests and any short-lived direct
imports keep working. The public import path remains `@/atoms`.

**Current shape**:

```ts
export type ViewState = {
  activeSpaceId: string | null;
  activeView:
    | { kind: 'empty' }
    | { kind: 'object'; objectId: string }
    | { kind: 'type-table'; typeId: string };
  focusedPane: 1 | 2 | 3;
  treeSelection: {
    ids: ReadonlySet<string>;
    anchor: { id: string; parentId: string } | null;
    keyboardEdgeId: string | null;
  };
  renamingObjectId: string | null;
  pendingBulkDelete: readonly string[] | null;
};
```

Derived per-slice atoms remain exported because they preserve cheap
row subscriptions (`selectAtom(selectedTreeIdsAtom, ...)`) and keep
call sites readable. New state that answers "what is the user looking
at or doing in the panes?" should extend `view-state.ts`; table layout,
type metadata, storage-backed preferences, and object title drafts stay
in their own files because they are not pane/view state.

## 3. No public API barrels (`index.ts`) outside `components/ui/`

**Evidence (audit)**: only `components/ui/` has an `index.ts`.
Atoms, lib/api, layout, tree, editor, etc. are all deep-imported
across the codebase.

**Why it bites agents**: refactoring requires editing every import
site. Agents avoid refactors that touch >10 files, so the structure
calcifies.

**Fix**:

1. Add `index.ts` barrels to: `atoms/`, `lib/api/`, `lib/api/objects/`,
   `components/spaces/`, `components/objects/`, `components/types/`,
   `components/editor/`, `components/tables/`, `components/tree/`,
   `components/layout/`.
2. Add an ESLint `no-restricted-imports` rule to forbid deep
   imports across module boundaries:
   ```
   "@/lib/api/objects/*" → error, use "@/lib/api/objects"
   "@/atoms/*"           → error, use "@/atoms"
   "@/components/X/*"    → error from outside components/X/
   ```
3. Within a folder, deep imports stay legal (they're the same
   module).

## 4. Cache ownership via per-resource key factories

**Status (2026-05-03)**: resolved as an intentional architecture.
Objects use `objectKeys` from `lib/api/objects/cache.ts`; spaces use
`spaceKeys` from `lib/api/spaces.ts`; types use `typeKeys` from
`lib/api/types.ts`.

This per-resource pattern is preferred over one global `cache.x.y`
namespace. It keeps query-key ownership next to the hooks and
mutations that use it, avoids a central mega-registry, and still gives
agents one obvious rule: never write raw query-key arrays in consumers
or mutations when a resource key factory exists.

The remaining cleanup is local, not architectural: if another mutation
needs the same optimistic by-type patching loop that `TableRows` uses,
extract that loop into an object-cache helper beside `objectKeys`.

## 5. Test coverage gaps

**Evidence (audit)**: the original pass found 42 of ~64 components
with no test sibling. The current tree has direct coverage for the
high-risk dialog and editable-cell flows, plus focused tests for the
core table modules introduced by the data-view split:
`useTableViewController`, `useTableProperties`, `TableToolbar`,
`TableRows`, and `TableSettingsPanel`. Remaining gaps are narrower:
`ListRowsView`, plus heavier surfaces like `CreateTypeDialog`,
`AddColumnPopover`, `ObjectTypeBar`, and `MarkdownEditor`.

**Why it bites agents**: an agent making "innocuous" UI changes
breaks the dialog chain or a cell editor and ships green.

**Fix (process, not code)**:

1. Per-PR rule (already in CLAUDE.md, restate explicitly): every
   new component lands with a `.test.tsx` covering the happy path
   + one edge case.
2. Keep the cell harness in `src/test/cellHarness.tsx` as the pattern
   for future property editors: render, enter edit mode, commit, assert
   the value handed to `onCommit`.
3. Next targeted backfill: direct `ListRowsView` tests, then one smoke
   each for `CreateTypeDialog`, `AddColumnPopover`, `ObjectTypeBar`,
   and `MarkdownEditor`.

## 6. Type-unsafe data access

**Evidence (audit)**: 8 occurrences of `as Record<string, unknown>`
or `as any`, hot in `TableView.tsx` (3) and `ObjectTypeBar.tsx`
(2). All for reading per-type property namespaces off
`ObjectRecord`.

**Why it bites agents**: the cast hides which fields are accessed.
Renaming a property doesn't trigger a type error; the bug surfaces
at runtime.

**Fix**: an `ObjectModel` facade:

```ts
export class ObjectModel {
  constructor(private record: ObjectRecord) {}
  name(): string                          { … }
  types(): readonly string[]              { … }
  prop(typeId: string, propId: string): unknown { … }
  setProp(typeId, propId, value): ModifyBatch { … }
}
```

Every read of per-type data goes through `model.prop(typeId,
propId)` — one cast, one type-checked path. Same for writes.

## 7. Feature-folder boundaries are optional

**Evidence**: a feature like "object type bar" lives across:
- `components/editor/ObjectTypeBar.tsx`
- `lib/api/objects.ts` (`useObject`, `useSetObjectProperty`)
- `lib/api/types.ts` (`useType`, `useTypeProperties`)
- `components/tables/cells/*` (reused cells)
- `components/tables/AddColumnPopover.tsx` (reused popover)

**Why it is not urgent**: public barrels plus ESLint now make the
current folder shape predictable. Agents do not need to deep-import
across modules, and the shared state/API surfaces are enforced.

**Optional longer-term move**: introduce `features/` next to
`components/` when a slice grows large enough that UI, local state, and
tests should move together:

```
src/
  features/
    object-type-bar/
      ObjectTypeBar.tsx
      api.ts          # re-exports the API hooks this feature uses
      atoms.ts        # local UI state
      __tests__/
      index.ts        # public surface (the component)
    tree/
    table-view/
    editor/
    space-edit/
    multi-select/
  components/ui/      # cross-feature primitives only
  lib/api/            # raw network layer
  atoms/              # cross-feature shared state only
```

Migration is opt-in per feature. This is a maintainability preference,
not a current correctness risk.

## 8. Build chunk size

**Evidence**: production builds pass, but Vite warns on large chunks.
The editor is already split into a lazy `MarkdownEditor` chunk, so this
is not a single 1.8 MB initial bundle. The remaining issue is bundle
shape: the async editor chunk is large because of BlockNote and its
dependencies, and the main app chunk is slightly above Vite's default
warning threshold.

**Fix**:

1. Audit static imports from the app shell into editor/table/icon-heavy
   modules so lazy boundaries stay clean.
2. Consider Rollup `manualChunks` for BlockNote, React/vendor, and the
   large icon catalog if measurements show better cache behavior.
3. Keep feature code behind `viewModules.tsx` lazy boundaries whenever
   it is not needed for the first paint.

## 9. Spec drift

Specs (`docs/specs/PR-NNN-*.md`) are the design source of truth at
PR time but nothing keeps them aligned with shipped code.
Already-stale: PR-013 doesn't mention Relation; PR-018 talks about
"per-device only" but PR-024 added bulk delete with no spec update.

**Fix**: a Markdown lint check in CI that:

1. Each PR-NNN-*.md must reference at least one file under `web/`
   or `internal/`.
2. A footer table `| File | Last verified |` must list every file
   the spec covers, with a date / commit. (Lightweight; we update
   on touch.)

## 10. No SPA e2e tests

`internal/e2e/e2e_test.go` covers the API. The SPA has unit tests
only. Multi-select range, drag-and-drop reorder, dialog confirms —
all unverified end-to-end.

**Fix**: add actual Playwright spec files. The CI command already
exists; it needs meaningful smoke coverage for the app shell, space
switching, object open/edit, table open, and one dialog flow.

## 11. Feature flags

**Evidence**: nothing. Every change ships everywhere.

**Fix**: `featureFlagsAtom` backed by localStorage with a tiny
`<FlagGuard flag="x">` component. New experimental work hides
behind a flag (`?flags=x` enables) until promoted.

## Proposed target architecture

```
web/app/src/
├── features/                     # each owns its files end-to-end
│   ├── tree/
│   │   ├── ObjectTree.tsx
│   │   ├── ObjectRow.tsx
│   │   ├── selection.ts          # selectedIds + anchor (local)
│   │   ├── BulkDeleteDialog.tsx
│   │   ├── __tests__/
│   │   └── index.ts              # exports <ObjectTree/>
│   ├── editor/
│   │   ├── MarkdownEditor.tsx
│   │   ├── ObjectTitle.tsx
│   │   ├── ObjectTypeBar.tsx
│   │   ├── __tests__/
│   │   └── index.ts
│   ├── tables/
│   ├── space-rail/
│   ├── space-contents/
│   └── …
├── components/ui/                # cross-feature primitives
│   ├── Button.tsx
│   ├── Dialog.tsx
│   ├── Popover.tsx
│   └── index.ts
├── components/properties/        # reusable property value editors
│   ├── PropertyCell.tsx
│   ├── cells/
│   └── index.ts
├── lib/
│   ├── api/
│   │   ├── client.ts             # apiFetch + ApiError
│   │   ├── objects/
│   │   │   ├── queries.ts
│   │   │   ├── mutations.ts
│   │   │   ├── model.ts          # ObjectModel facade
│   │   │   └── index.ts          # public surface
│   │   ├── types/
│   │   │   └── … (same shape)
│   │   └── cache.ts              # named query-key recipes
│   └── …
├── atoms/
│   ├── view-state.ts             # pane/view/focus/tree selection model
│   ├── theme.ts
│   ├── flags.ts
│   └── index.ts
├── shared/
│   └── storage.ts
└── app/                          # composition root only
```

Boundaries enforced by `eslint-plugin-boundaries` or
`eslint-plugin-import` `no-restricted-imports`.

## Migration order (refreshed)

Done so far: PR #28 (objects barrel + expanded ESLint rules), PR #29
(`view-state.ts` consolidation), PR #30 (per-resource cache key
factories), PR #31 (`ObjectModel` facade), CI setup, and a
property-editor extraction into `components/properties`.
Next, ranked by leverage:

1. **Targeted test backfill.** Direct tests for `ListRowsView` /
   `TableRows`, then one smoke each for `CreateTypeDialog`,
   `AddColumnPopover`, `ObjectTypeBar`, and `MarkdownEditor`.
2. **Build chunk optimization.** Audit static imports, then consider
   Rollup `manualChunks` for BlockNote/vendor/icon-heavy code if the
   measured cache behavior improves.
3. **Playwright smoke harness.** The command is wired in CI; add real
   spec files for the core app paths.
4. **Feature flags atom + `<FlagGuard>`.**
5. **Spec-link CI check.**
6. **Optional feature-folder pilot.** Try it only when a slice grows
   large enough to justify moving UI, state, and tests together.

Each PR remains small and independent.

## Agent guidelines (excerpt — copy into prompts)

When asking an agent to add a feature, include this in the prompt:

> - **Where to put files**: use the existing public barrels first:
>   product UI under `components/<area>/`, shared primitives under
>   `components/ui/`, and shared cross-feature state under
>   `web/app/src/atoms/`. Use `features/<name>/index.ts` only when a
>   slice is large enough to own UI, local state, and tests end-to-end.
> - **API access**: only via `lib/api/<resource>` barrels. No deep
>   imports. If a hook you need doesn't exist, add it to the barrel
>   with an `@internal` JSDoc if it's only for one feature.
> - **Cache invalidation**: use the owning resource's key factory
>   (`objectKeys`, `spaceKeys`, `typeKeys`, etc.). Don't write raw
>   query-key arrays in consumers or mutations.
> - **Selection / view state**: only via `@/atoms`; extend
>   `atoms/view-state.ts` instead of adding a new selection atom.
> - **Tests**: every component or hook lands with a sibling
>   `.test.tsx` / `.test.ts`. The test must run in `vitest run`
>   without a server.
> - **Specs**: PR-NNN-*.md describing the change must list every
>   file it touches in a footer table.
