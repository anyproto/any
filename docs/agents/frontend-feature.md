# Playbook: Add a frontend feature

For user-visible features (a new page, panel, or end-to-end flow).
For new design-system primitives, use
[`frontend-component.md`](./frontend-component.md) instead.

## Step 0 — Spec first

Don't write code without an approved spec at
`docs/specs/PR-NNN-<slug>.md`. The spec defines:

- Goal (one sentence).
- Acceptance criteria as testable bullets.
- Layout sketch (ASCII art or screenshot reference).
- States (loading / empty / error / populated / disabled).
- Keyboard interactions.
- Test plan.

If no spec exists, **stop and ask the human** — start there.

## Layered structure

A feature spans up to four layers. Add the smallest set you need.

| Layer       | Path                                       | What lives here                                                  |
|-------------|--------------------------------------------|------------------------------------------------------------------|
| Transport   | `web/app/src/lib/api/<group>.ts`           | Typed `apiFetch` wrappers for one endpoint group.               |
| Server data | `web/app/src/lib/api/<group>.ts` (hooks)   | TanStack Query hooks (`useFoo()`, `useCreateFoo()`).            |
| Client data | `web/app/src/atoms/<concern>.ts`           | Jotai atoms — UI state, selection, filters, drafts.             |
| View        | `web/app/src/components/<feature>/`        | Feature-specific components.                                     |
|             | `web/app/src/pages/<Feature>Page.tsx`      | Top-level routes assembled from feature components + ui kit.    |

A new feature touches at most one or two new files in each layer.
**No cross-feature imports**: if `components/tree/` needs something
from `components/editor/`, the shared piece moves to `components/ui/`.

## Patterns

**Server state.** Typed hook in the same file as the API function:

```ts
// src/lib/api/objects.ts
export async function listObjectsByParent(spaceId: string, parentId: string) { ... }

export function useObjectsByParent(spaceId: string, parentId: string) {
  return useQuery({
    queryKey: ['objects', spaceId, 'by-parent', parentId],
    queryFn: ({ signal }) => listObjectsByParent(spaceId, parentId).catch(rethrow(signal)),
  });
}
```

**Client state.** One atom per concern, one file per concern:

```ts
// src/atoms/selection.ts
export const selectedObjectIdAtom = atom<string | null>(null);
```

For per-id state, use `atomFamily` and clean up on delete:

```ts
export const objectDraftAtom = atomFamily((id: string) => atom<string>(''));
```

**Error states.** Errors come from `ApiError` (`@/lib/api/client`).
Surface `error.code` + `error.message` to the user; never `JSON.stringify`
the whole envelope.

**Loading states.** Always render a skeleton or spinner. Never
condition on `data &&` such that the UI shifts when data arrives.

**Optimistic mutations.** Use TanStack Query's `onMutate` /
`onError` / `onSettled` to roll back. Don't optimistically mutate
across query keys.

## Required deliverables for a feature PR

1. **Spec.** `docs/specs/PR-NNN-*.md` — merged before code.
2. **Layered code** as above. Each new component has a story.
3. **Tests:**
   - Unit: pure logic in `lib/`.
   - Component: each new component, including all rendered states.
   - E2E: at least one Playwright spec covering the happy path.
   - axe-core assertion in every component test.
4. **Visual snapshot.** Update Playwright snapshots in light + dark.
5. **PR description** linking the spec and showing screenshots in
   both modes.

## Stop and ask if

- The feature needs an endpoint that returns `501 sdk.not_implemented`
  today. Check `internal/server/handlers_unimplemented.go` and
  `docs/03-api.md`. If so, the feature is blocked on SDK work.
- The change requires modifying `docs/03-api.md` or any
  `internal/server/handlers_*.go` file — that's a server-side PR,
  separate from the frontend.
- The change touches more than one feature folder. That's a sign the
  shared piece needs to move into `components/ui/` first.
