# PR #3 — Spaces (real)

> Spec for the third frontend PR. Replaces the mock spaces in pane 1
> with `/v1/spaces`, adds create + delete flows. Per
> docs/08-app-architecture.md roadmap.

## Goal

Make pane 1 real:

- Render `GET /v1/spaces` results in the rail (no more mock list).
- Create a space via the **+** button → `POST /v1/spaces`.
- Delete a space via context menu on its icon → `DELETE /v1/spaces/:id`.
- First-load auto-pick the first space if none is active; persist
  active space id across reloads.
- Surfaces loading / empty / error states cleanly.

After this PR, mock data is gone from pane 1 (and from the app
entirely — pane 2 still uses mock sections, those go in PR #4).

## Non-goals

- Joining someone else's space (`/v1/spaces/join` returns 501).
- Members / ACL (501 across the board).
- Sync status indicator (501).
- Editing space metadata (no `PATCH /v1/spaces/:id` server-side; deferred).
- Space-derive / one-to-one (501).

## API surface used

| Method | Path | Purpose |
|--------|------|---------|
| `GET`  | `/v1/spaces` | List my spaces. Returns `{spaces: SpaceInfo[]}`. |
| `GET`  | `/v1/spaces/:id` | Single space (used after create to confirm). |
| `POST` | `/v1/spaces` | Create. Body `{name, description?, iconCid?, spaceType?}` → 201 + SpaceInfo. |
| `DELETE` | `/v1/spaces/:id` | Delete. → 204. Permanent. |

`SpaceInfo` fields (from `internal/api/spaces.go`):

```
{ id, type?, name?, description?, iconCid?, status, ownRole, createdAt }
```

`status` is one of: `unknown | active | joining | leaving | deleted | remote_dead`. We render only `active` spaces in v1; non-active get a muted treatment (greyed icon, tooltip notes the status).

## Decisions

- **Hooks layer** (`web/app/src/lib/api/spaces.ts`):
  - `useSpaces()` — `queryKey: ['spaces']`, default options.
  - `useCreateSpace()` — invalidates `['spaces']` on success; sets the
    new id as active.
  - `useDeleteSpace()` — invalidates `['spaces']`; if the deleted id
    was active, clears `activeSpaceIdAtom` so AppShell auto-picks the
    next available.
- **Auto-pick.** When `useSpaces` resolves with ≥ 1 space and
  `activeSpaceIdAtom` is null *or* points at a space no longer in the
  list, set it to `spaces[0].id`. Logic lives in `AppShell`'s effect
  (replaces today's mock-driven version).
- **Color tone.** With no real avatars yet, derive a stable token
  from the space id (xxhash → 5 bins → one of accent/success/info/destructive/foreground).
  Glyph: first letter of `name` uppercased; falls back to "·".
- **Create dialog.** Single text input ("Name" — required) + optional
  description Textarea. Submit on Enter or button click. Toast on
  success ("Created Foo") and error (envelope code + message).
- **Delete confirm.** Right-click on a space icon opens a context
  menu with "Delete…". Selecting it opens a Dialog with the space
  name and a destructive button. No optimistic delete (irreversible).
- **Empty state.** When `useSpaces` returns 0 spaces: rail shows just
  the + button, pane 2 shows a "No spaces yet — create one" prompt
  pointing at the +.
- **Error state.** When `useSpaces` fails: rail shows a retry button
  (disabled icon + tooltip with the error code). Pane 2 + pane 3
  hide their content; pane 3 shows an error card.

## Files changed / added

```
web/app/src/lib/api/spaces.ts                + spaces.test.ts
web/app/src/components/layout/SpacesRail.tsx (rewrite — uses useSpaces)
web/app/src/components/layout/SpacesRail.test.tsx (rewrite)
web/app/src/components/spaces/CreateSpaceDialog.tsx + .test.tsx + .stories.tsx
web/app/src/components/spaces/DeleteSpaceDialog.tsx + .test.tsx
web/app/src/components/layout/AppShell.tsx (effect uses useSpaces, not MOCK_SPACES)
web/app/src/lib/mock-data.ts (drop MOCK_SPACES + tone map; keep section / object mocks for PR #4)
docs/specs/PR-003-spaces.md
```

## Acceptance criteria

1. `GET /v1/spaces` is called once on app mount; result populates the rail.
2. Each space icon shows the first letter of its name on a token-colored background.
3. Hovering an icon shows a tooltip with the full name.
4. Clicking an icon updates `activeSpaceIdAtom`; pane 2 reacts.
5. Reloading the page restores the last active space (still in localStorage).
6. If the persisted active space no longer exists in the list, the rail picks the first available.
7. **+** opens the create dialog. Submitting calls `POST /v1/spaces`; on success the new space is appended, becomes active, and a "Created" toast appears.
8. Right-clicking an icon opens a context menu with "Delete…". Confirming calls `DELETE /v1/spaces/:id`; the icon disappears; if it was active, the rail picks the next.
9. Network errors render an error card in pane 3 with the code from the envelope.
10. CI green: lint, typecheck, vitest, build. Existing tests stay green.

## Test plan

- **Unit** (lib/api/spaces.test.ts):
  - `listSpaces` decodes a sample envelope.
  - `createSpace` POSTs JSON with the right Content-Type.
  - `useCreateSpace` seeds the `['spaces']` cache with the created
    space before invalidating. AppShell validates `activeSpaceId`
    against that cache, so this prevents the new space selection from
    being snapped back while the refetch is still in flight.
  - `deleteSpace` issues DELETE with no body.
  - 404 on `getSpace` produces an `ApiError` with code from envelope.
- **Component** (SpacesRail.test.tsx):
  - Loading state shows skeletons.
  - Empty state shows just the + button + a hint.
  - Error state shows the retry affordance.
  - Populated state renders one button per space; click updates atom.
- **Component** (CreateSpaceDialog.test.tsx):
  - Submit calls the mutation with `{name}` (required validation).
  - Toast on success/error.
- **axe-core**: no violations on any state.

E2E spec stays where it is for now (it pings `/v1/health`); a richer
spaces-flow E2E lands in PR #4 once the tree is in to assert
end-to-end navigation.

## Open questions

- **Tone palette stability.** Hashing the id is deterministic but
  changes if the id format changes. If the user has strong feelings
  about a specific space being purple, we'll add a `iconCid` lookup
  later.
- **Soft-delete confirm copy.** Delete is destructive and irreversible
  (per CLAUDE.md / SDK behavior). Default copy: "Delete *Foo*?
  Objects in this space will become inaccessible. This cannot be
  undone." Strong wording on purpose.
