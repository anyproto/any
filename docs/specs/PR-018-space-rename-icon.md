# PR #18 — Rename a space + set an icon (client-side override)

> Notion-/Anytype-style edit affordances for space metadata. Stored
> client-side until the SDK exposes a server-side update path.

## Goal

Today the only way to set a space's name is at create time, and
there's no icon picker at all. This PR adds:

- **Rename** — change a space's display name from the rail's row
  context menu (right-click) or the pane 2 header.
- **Change icon** — pick an emoji that replaces the auto-generated
  initial-glyph in `SpaceAvatar`. Quick-pick row of common choices
  + free-form input for any single emoji.
- **Reset** — clear the override so the space falls back to the
  server's stored name and the auto-glyph.

## SDK constraint (the reason this is client-side)

`any-sync-sdk` v0.0.0-20260501 exposes:

```go
type Service interface {
    Create(ctx, CreateRequest) (Space, error)
    Get(ctx, spaceId) (Space, error)
    List(ctx) ([]SpaceInfo, error)
    Delete(ctx, spaceId) error          // soft-delete
    // …Join / Derive / OneToOne / Subscribe
}
```

No `Update`, no `SetMetadata`. The internal `techspace.Service.Add`
is upsert-capable (PR-016 dug into this) but it lives under
`internal/` and isn't reachable from us. The closest existing
mutator is `SetLocalStatus`, which only flips the status field.

That leaves three options:

1. Patch the SDK locally to add a metadata-update method.
2. Wait for upstream to ship one.
3. Apply overrides on the client and migrate later.

We pick (3) for v1 because:

- The user wants this **now** as a UX gap, not a sync-correctness
  feature.
- Storage shape mirrors what an eventual server PATCH body would
  carry (`{name?, icon?}`), so swapping to server-side later is one
  function-body change behind `useSpaceMeta`.
- An emoji icon doesn't need to round-trip through `iconCid` (a CID
  pointing at uploaded image data). Emojis are short strings; we
  can store them in any text field.

## Storage

Atom: `spaceMetaOverridesAtom` in `web/app/src/atoms/space-meta.ts`,
backed by `atomWithStorage` under the localStorage key
`any.space.meta.v1`:

```ts
type SpaceMetaOverride = {
  name?: string;
  icon?: string; // single emoji or short text
};
type SpaceMetaOverrides = Record<string /* spaceId */, SpaceMetaOverride>;
```

Helper: `useSpaceMeta(spaceId)` returns
`{ overrideName, overrideIcon, set, clear }`. Call sites compute the
**effective** name as `overrideName ?? server.name ?? fallback` and
pass the icon through to `SpaceAvatar`'s new `iconOverride` prop.

## UI

### Where you can edit

- **Pane 1 (SpacesRail)** — right-click on a space row. The
  context menu gains "Edit space…" above "Delete…" (currently the
  only entry). One menu item, not two — opens a dialog with both
  fields.
- **Pane 2 (SpaceContents Header)** — clicking the existing
  space-name button (which today only does nothing useful — there's
  a chevron but no menu wired) opens the same dialog. This is the
  Notion gesture: click the workspace name to edit it.

### The dialog

Single modal with:

- **Name** input (defaults to the current effective name).
- **Icon** — one row of quick-pick emojis (📝 📚 🎯 🍳 🎨 ✈️ 💼 🏠
  🎮 🎵 🌱 ❤️) plus a free text input where any emoji can be pasted.
  Empty = no override (falls back to auto-glyph).
- **Reset to default** ghost button (clears both fields' overrides).
- **Cancel** + **Save** in the footer.

### Where it shows up

`SpaceAvatar` learns an optional `iconOverride` prop. When set, it
renders the emoji centered in the rounded square (still backed by
the spaceId-derived tone color so the row's color identity stays
consistent across renames). When unset, behaviour is unchanged
(initial glyph from name+id).

`SpacesRail.SpaceRow`, `SpaceContents.Header`, and any future
surface (Relation picker if we ever surface space chips, etc.) read
the effective name + override icon via `useSpaceMeta`.

## Files

```
docs/specs/PR-018-space-rename-icon.md
web/app/src/atoms/space-meta.ts                            (new — atom + helper hook)
web/app/src/atoms/space-meta.test.ts                       (new)
web/app/src/components/spaces/EditSpaceDialog.tsx          (new — name + icon)
web/app/src/components/spaces/EditSpaceDialog.test.tsx     (new)
web/app/src/components/layout/SpaceAvatar.tsx              (iconOverride prop)
web/app/src/components/layout/SpacesRail.tsx               (context menu entry)
web/app/src/components/layout/SpaceContents.tsx            (header click → dialog)
```

## Acceptance criteria

1. Right-click on a space row → "Edit space…" → dialog opens with
   the current name + icon (if any).
2. Save updates the rail row's label and avatar emoji immediately.
3. The pane 2 header reflects the same change without refresh.
4. Reload preserves the rename + icon (localStorage).
5. Deleting a space with an override should clear the override
   too — no zombies in localStorage. (Wired into `useDeleteSpace`'s
   onSuccess.)
6. "Reset to default" clears the override and the avatar returns to
   the auto-glyph.
7. Existing tests stay green; new tests cover the atom hook and the
   dialog's submit/reset paths.

## Migration plan (when the SDK ships UpdateMetadata)

1. Add `useUpdateSpaceMetadata` mutation hitting the new server
   endpoint.
2. Inside `useSpaceMeta.set`, write to **both** localStorage and the
   server (best-effort upgrade) until we're confident in the
   round-trip.
3. After a release, drop the localStorage write and read directly
   from `useSpace(...).data.name / iconCid`.
4. Add a one-shot migration on app boot that, for any localStorage
   entry whose spaceId we can resolve, fires the server update and
   clears the local entry on success.

## Test plan

- **Atom unit**: `useSpaceMeta.set` writes to the atom and persists;
  `clear` removes the entry.
- **Dialog component**: renders current name/icon, Save commits the
  override, Reset clears both fields, Cancel discards changes.
- **Manual**: rename a space → reload page → name persists. Set an
  emoji → it shows in pane 1 and pane 2. Reset → falls back to
  auto-glyph.
