# PR #8 — Type & property creation

> Spec for the eighth frontend PR. Users can define a custom type
> with named properties and create objects of it.

## Goal

Three things ship together:

1. **Types API client** — list / get / create types and add properties.
2. **Create-Type wizard** — multi-step Dialog flow modelled by a
   state machine (the second hand-rolled machine in the codebase,
   per `docs/08-app-architecture.md`).
3. **+ New dropdown surfaces user types** — once a user creates a
   "Recipe" type, the menu shows "New Recipe" alongside "New page" /
   "New folder".

After this PR the user can model their own object kinds — recipes,
contacts, books, whatever — and create instances from the same +
New menu they've been using.

## Non-goals

- **Server-backed editing** of types or properties (no API for that
  yet — `Types.UpdatePropertyMeta` returns `501`). The app may layer
  local per-device list metadata for display names/icons while the SDK
  gap exists.
- **Server-backed deleting** of types or properties (also `501`).
  Hiding/removing a list from the local UI is allowed, but it must be
  presented as local-only and must not delete objects.
- **Editing property values on objects** — that's a property panel
  in pane 3, separate PR.
- **Uploaded/media icons** for types in the menu. Emoji list icons may
  be stored as local UI metadata.
- **Validating property values** client-side (server is source of truth).

## API surface used

| Method | Path | Purpose |
|--------|------|---------|
| `GET`  | `/v1/spaces/:s/types` | List types. Returns `{types: TypeInfo[]}` (includes the built-in `nav`). |
| `GET`  | `/v1/spaces/:s/types/:t` | Get one type. |
| `POST` | `/v1/spaces/:s/types` | Create. Body `{name, description?, iconCid?}` → 201 + `{typeId}`. |
| `GET`  | `/v1/spaces/:s/types/:t/properties` | List a type's property defs. |
| `POST` | `/v1/spaces/:s/types/:t/properties` | Add a property. Body `{name, kind, description?, xKey?}` → 201 + `{propId}`. |

`PropertyKind` values from `internal/api/types.go` v1: `string`, `number`,
`boolean`, `null`, `array`, `object`. We surface the first three in the
UI (most common); the rest stay in the type but aren't presented as
dropdown choices in v1 (no harm, easy to add later).

## Decisions

### Wizard, not single-form

A single-form dialog would work but the architecture doc earmarked
this for "second state machine," and the wizard pattern actually
matches the SDK's two-step nature (create type → add each property).
Three steps:

1. **Name** — name (required), description (optional).
2. **Properties** — list of property rows: name + kind dropdown
   (string / number / boolean for v1). Add / remove rows.
3. **Confirm** — summary + Create button.

State machine (`components/types/typeWizard.ts`):

```ts
type WizardState =
  | { kind: 'name';        name: string; description: string }
  | { kind: 'properties';  name: string; description: string; properties: Draft[] }
  | { kind: 'confirm';     name: string; description: string; properties: Draft[] }
  | { kind: 'creating';    progress: number; total: number }
  | { kind: 'done';        typeId: string }
  | { kind: 'create_error'; error: ApiError; partial?: { typeId?: string; createdProps: number } };

type Draft = { name: string; kind: 'string' | 'number' | 'boolean' };
```

The `creating` state walks: createType → addProperty[0] → … → addProperty[N].
On error mid-walk, `create_error.partial` records what landed (the type
itself, plus how many properties succeeded). The user can:
- **Retry** — replays the missing addProperty calls.
- **Cancel** — closes; the type and any added properties survive.

### + New dropdown

Today: `New page`, `New folder`. After this PR:

- `New page`
- `New folder`
- *(separator if there are user types)*
- `New {TypeName}` for each non-builtin type (sorted by name).
- *(separator)*
- `Create type…` → opens the wizard.

`useCreateObject` extended to pass explicit hierarchy `nav` plus
`types: [typeId]` in the request body. The `nav` block is the object's
home; the type id is only membership in that list/database view.

### Cache + invalidation

- `useTypes(spaceId)` — `queryKey: ['types', spaceId]`.
- `useCreateType` seeds the created type into that key, then
  invalidates it on success.
- `useAddPropertyToType` invalidates `['types', spaceId, typeId, 'properties']`.
- Every active space auto-ensures one default user list named `Pages`.
  `useEnsurePagesList` uses module-level per-space in-flight and
  created-id guards so Header / sidebar callers, React StrictMode, and
  stale refetch windows cannot race-create duplicates. Existing
  duplicate `Pages` records are collapsed in visible list surfaces; the
  first non-builtin `Pages` id remains the canonical id for new plain
  pages.
- `CreateTypeDialog` accepts an optional `onCreated(typeId)` callback.
  Generic callers ignore it; object-scoped callers use it to attach
  the newly created list to the current object before the dialog closes.
- `CreateTypeDialog` also accepts `quickCreate`. Object-scoped callers
  use it so the flow is Name → Create, skipping the Properties and
  Confirm steps; properties can be added later from the object type bar.
- List display metadata lives in `atoms/type-meta.ts` under
  `any.type.meta.v1`. It is keyed by `spaceId:typeId` and can override
  the display name, emoji icon, or hidden state for a list. These
  overrides are used by pane-2 list rows, the `+ New` menu, and the
  table page title. They are intentionally per-device until the server
  exposes type rename/icon/delete endpoints.

## Files added / changed

```
docs/specs/PR-008-type-creation.md
web/app/src/lib/api/types.ts                    + .test.ts
web/app/src/components/types/
  CreateTypeDialog.tsx                          (the wizard host)
  ListMetaControls.tsx                          (rename/icon/delete UI)
  typeWizard.ts                                 state machine + reducer
  typeWizard.test.ts
  CreateTypeDialog.test.tsx
web/app/src/atoms/type-meta.ts                  (+ local list metadata)
web/app/src/components/layout/SpaceContents.tsx (+ types in dropdown)
web/app/src/components/tables/TableView.tsx     (+ editable list title/icon)
web/app/src/lib/api/objects.ts                  (useCreateObject supports `types`)
```

## Acceptance criteria

1. `+ New → Create type…` opens the wizard at step Name.
2. Empty name disables Next.
3. Step Properties: add rows (max 10 in v1), each with name + kind.
   Remove via × button. Empty-name rows blocked from Next.
4. Step Confirm: shows the planned type + properties.
5. Create button → POST /types, then sequential POST /properties; on
   completion the dialog closes and a toast confirms.
6. Mid-flight failure surfaces in the dialog with Retry / Cancel.
7. After success the new type appears in the + New dropdown as
   `New {TypeName}`.
8. Selecting `New {TypeName}` POSTs /objects with
   `{nav:{type:1,parentId:""}, types:[typeId]}`; the new object lands
   in the hierarchy root, gets selected, opens in pane 3, and also
   appears in that list.
9. CI green; existing tests stay green.

## Test plan

- **Unit** (`types.ts`): list/create/addProperty URL/method/body shape;
  error envelope passes through.
- **Unit** (`typeWizard.ts`): every transition; partial-failure path.
- **Component** (`CreateTypeDialog`): three-step navigation,
  validation gates, sequence of mutations on Create.
- **Integration** (`SpaceContents`): user type appears in the dropdown
  after a successful create.
- **axe**: dialog passes in every step.

## Open questions

- **Property kinds beyond string / number / boolean.** v1 caps at
  three because the SDK's `array` and `object` kinds need more UX
  (nested schemas). Easy to extend.
- **Required vs optional properties.** SDK supports `Required`; v1
  ignores it (every property is optional). Adding a checkbox is a
  small follow-up.
- **Type icons.** Built-in types have no icons either; defer to a
  later PR that adds emoji/lucide pickers.
