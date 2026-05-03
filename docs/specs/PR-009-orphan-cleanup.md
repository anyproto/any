# PR #9 — Orphan cleanup in the editor's load-error state

> Small follow-up. The markdown editor already shows an error card
> when `GET /v1/spaces/:s/objects/:o/markdown` fails. This PR makes
> that card actionable when the failure is the well-known "tree does
> not exist" condition (an orphan projection row).

## Background

Per `docs/03-api.md` § Object deletion, `Objects.Delete` only drops
the any-sync tree; the per-space `objects` projection row is killed
by a separate tombstone write. The server's `DELETE /v1/spaces/:s/objects/:o`
handler runs both in order — first the tombstone, then the tree — so
this is normally consistent.

Existing rows from earlier experimentation (or any future order
inversion) can still leave **orphans**: a row in `objects` whose
underlying tree is gone. They appear in the tree query, but loading
their markdown produces:

```
internal — markdown: List: query: query: spaceobjects: BuildTree
<id>: tree does not exist
```

## Goal

When the editor surfaces a load error, give the user a one-click
**Remove from list** action that:

1. Issues `DELETE /v1/spaces/:s/objects/:o` — the server writes the
   tombstone (succeeds) and then tries the tree delete (fails
   silently, already gone).
2. The next children-query refetch drops the row.
3. The active object selection is cleared.

## Decisions

- **Button visible for *all* load errors**, not only the
  "tree does not exist" one. Reasoning: the user's recourse is the
  same — purge the row from their tree — and gating on a
  message-text match would be brittle.
- **Lookup parentId from cache.** Walk the children-of-parent caches
  the same way `ObjectTree.findObjectInCache` does. If we don't find
  it (cache cold) fall back to root parent — `useDeleteObject` only
  uses the parent for cache invalidation, so a wrong guess just makes
  one extra invalidation and is harmless.
- **Toast and clear selection on success.** Same pattern as
  `DeleteObjectDialog`.

## Files changed

```
docs/specs/PR-009-orphan-cleanup.md
web/app/src/components/editor/MarkdownEditor.tsx (load-error branch)
web/app/src/components/editor/OrphanCleanup.tsx + .test.tsx
```

## Acceptance criteria

1. With a known orphan id selected, the editor renders the error card
   plus a destructive "Remove from list" button.
2. Clicking it issues `DELETE /v1/spaces/:s/objects/:o`; toast confirms.
3. The row vanishes from pane 2 on the next refetch.
4. `activeObjectIdAtom` becomes null; pane 3 returns to the empty state.
5. Existing tests stay green.

## Test plan

- **Component** (`OrphanCleanup.test.tsx`): clicking the button calls
  the delete mutation with the object's id; success clears the active
  selection.
- **Re-test** existing `MarkdownEditor` flows aren't disturbed by the
  new branch (load-error path only).
