# PR #4 — Object tree (real)

> Spec for the fourth frontend PR. Pane 2 stops being mock: the
> sectioned list is replaced by a tree backed by
> `POST /v1/spaces/:s/objects/query`.

## Goal

- Render the active space's root-level objects in pane 2 as a tree.
- Each row shows an icon (folder vs item) + title.
- Folder rows expand on click → lazy-fetch their children.
- Selecting a row writes `activeObjectIdAtom` (pane 3 reacts).
- A header **+ New** button creates a root Page via
  `POST /v1/spaces/:s/objects` (server stamps `nav` defaults).
- Loading / empty / error states for both root and children.

After this PR, the only mock left in the app is the placeholder body
inside pane 3 (PR #5 wires CRUD; PR #6 brings the real editor).

## Non-goals

- Inline rename, delete, drag-and-drop — PRs #5, #5, #7 respectively.
- Folder creation (only items in PR #4; folder creation is
  trivially "+" with `nav.type=2` but UX-wise it pairs with rename).
- Properties / typed columns — PR #6.
- Real titles. Until PR #5 wires rename, objects show their `any.name`
  if present, else `Untitled (id…)`.

## API surface used

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/v1/spaces/:s/objects/query` | Cross-object query against the per-space `objects` collection. Body `{filter, sort, limit, offset}`. |
| `POST` | `/v1/spaces/:s/objects` | Create. Empty body `{}` is accepted by the API, but the app sends explicit hierarchy homes: `{nav:{type:1,parentId:""}}` for a root item, `{nav:{type:2,parentId:""}}` for a root folder. Response: `{objectId}`. |

### Query body for tree fetches

For root: `{ "filter": { "nav.parentId": "" }, "sort": ["nav.pos"] }`
For a folder: `{ "filter": { "nav.parentId": "<id>" }, "sort": ["nav.pos"] }`

The cross-object collection rows have this shape (per
`internal/nav/nav.go`):

```jsonc
{
  "id":  "obj_abc",
  "any": { "name": "...", "types": ["nav", ...] },
  "nav": {
    "type":     1,        // 1 = item, 2 = folder
    "parentId": "obj_root_or_other",
    "pos":      "<lexid>"
  }
}
```

We render `any.name` (truncated) as the row title; fallback
`Untitled (${id.slice(0,6)}…)`. Icon is folder for `nav.type === 2`,
file for `1`.

## Decisions

- **No tree library.** A 60-line recursive component is enough for
  the v1 tree (lazy-loaded children, expand/collapse local state,
  click to select). `@headless-tree/react` lands in PR #7 alongside
  drag-and-drop, where its keyboard-DnD shines.
- **Per-folder query keys.** `['objects', spaceId, 'children', parentId]`.
  Each folder is a separate `useQuery` — natural lazy loading;
  collapsed folders aren't fetched until the user opens them; cache
  is per-folder so re-expanding is instant.
- **Bulk expand/collapse signal.** The space-name Pages header can
  broadcast expand-all or collapse-all. Rows keep local expanded state
  for normal interaction and only respond to the shared nonce signal,
  so regular clicks do not force the whole tree through a central
  expansion map.
- **Invalidations.** Creating an object invalidates the children of
  its `nav.parentId` (root by default). Renaming (PR #5) invalidates
  the same. Deleting invalidates the children list AND removes the
  per-object cache. PR #4 only creates root items, so the parent is
  always `""`.
- **Selection vs expansion.** Clicking a row always *selects* it
  (sets `activeObjectIdAtom`). The expansion chevron is a separate
  hit target on folder rows so opening a folder doesn't jump pane 3.
  Items don't have a chevron.
- **+ button placement.** The header gets `+ New` as the single
  root-page fast path. The hierarchy section `+` is reserved for
  new folders, expanded folder rows get their own child-page `+`,
  and the Lists section owns new-list/list-entry actions. Toast on
  error.
- **Polling vs realtime.** Until `/subscribe` lands, the tree
  refetches on window focus (TanStack Query default) and after our
  own mutations. Acceptable for v1.

## Files added / changed

```
docs/specs/PR-004-object-tree.md
web/app/src/lib/api/objects.ts            + objects.test.ts
web/app/src/components/tree/ObjectTree.tsx
web/app/src/components/tree/ObjectRow.tsx + .test.tsx
web/app/src/components/layout/SpaceContents.tsx (rewrite — tree + + button)
web/app/src/components/layout/SpaceContents.test.tsx (rewrite)
web/app/src/lib/mock-data.ts (DELETED — no more mock)
```

## Acceptance criteria

1. Selecting a space triggers `POST /v1/spaces/:s/objects/query` for the root.
2. Empty space shows an empty state ("No objects yet — create one with **+ New**").
3. Each root object renders as a row with an icon + title.
4. Folder rows have a chevron; clicking the chevron expands and fetches children.
5. Clicking the row body (not chevron) selects the object — pane 3 reacts.
6. Reload preserves which folders were expanded *only* if their state was persisted (we explicitly do **not** persist this in v1; reload collapses everything).
7. **+ New** creates an object; the new row appears at the top (or wherever its `nav.pos` lands), gets selected, and pane 3 opens it.
8. A failed query renders an error card; the rest of the rail and pane 1 stay usable.
9. Existing tests still pass; CI green.

## Test plan

- **Unit** — `objects.ts`: queryObjects POST shape, low-level
  createObject POST `{}`, app create-body builder emits explicit
  hierarchy `nav`, error envelope passes through.
- **Component** — `ObjectRow`:
  - Item row: clicking selects.
  - Folder row: clicking chevron expands, fetches children.
  - Title fallback when `any.name` is absent.
- **Component** — `ObjectTree`:
  - Loading skeleton, empty state, populated.
  - Selecting cascades to `activeObjectIdAtom`.
- **Component** — `SpaceContents`: + button calls POST and selects the new id.
- **axe**: no violations on each state.

## Open questions

- **What counts as a folder visually?** v1 = chevron + folder icon
  for `nav.type === 2`. Items (`type === 1`) get no chevron even if
  they have children downstream (the data model allows it; the UI
  doesn't surface it until we have a clearer use case).
- **Sort direction.** `sort: ["nav.pos"]` is ascending. Anytype's
  current UI puts newest at top; v1 puts whatever the lexid order
  produces. Revisit when we have user feedback.
- **Search / filter.** Out of scope for PR #4. Will land alongside
  `/` shortcut in a later PR.
