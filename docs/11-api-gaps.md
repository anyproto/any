# API gaps (single-player)

What the API/SDK is missing for the single-player app to stop relying on
local-storage workarounds and feel complete. Sharing and collaboration are
deliberately out of scope — those come later.

Priorities reflect impact on the current product:

- **P0** — UI already exists and lies without it, or cheats with `localStorage`.
  Fixing these deletes code.
- **P1** — Core features that are absent entirely, blocking the next round of
  work.
- **P2** — Sync, scale, polish.

---

## P0 — fix the lies

These are gaps where the SPA already ships a feature that pretends to work but
is actually scoped to a single browser tab via `localStorage`. Each one maps
to a file or code path we delete the day it lands.

**Rename / icon / description for spaces.** Every browser keeps its own
private name and icon for the same space. Two devices disagree.
→ kills `web/app/src/atoms/space-meta.ts`.

**Rename / icon / hidden flag for types (lists).** Same pattern. The
"hide from sidebar" toggle doesn't propagate across devices.
→ kills `web/app/src/atoms/type-meta.ts`.

**Delete a type.** Returns `501 sdk.not_implemented` today; the destructive
menu can't be wired.

**Rename a property.** Column header rename can't persist. 501 stub at
`PATCH /v1/spaces/:spaceId/types/:typeId/properties/:propId`.

**Delete a property.** Column delete can't persist. 501 stub at
`DELETE /v1/spaces/:spaceId/types/:typeId/properties/:propId`.

**Edit a property's options / tags.** Renaming a select option, recoloring a
tag, removing a tag — limited or missing.

**Per-type views — filter, sort, layout, column order, column widths.** None
of this syncs. Different device, different table layout.
→ kills the per-type half of `web/app/src/atoms/table.ts`.

**Account display name and avatar.** The SDK shape exists but isn't wired,
so the account settings dialog can't save anything.

---

## P1 — missing core features

Things that aren't in the API at all yet, which the single-player app needs
before the next round of UI work makes sense.

**Account creation, recovery, login.** No real auth flow. A wallet is created
on first run, but there's no account-recovery story, no way to log into the
same account from a second device, no "I lost my mnemonic" path.

**Workspace / account primitive.** A clean separation between "the local
account" and "the spaces it owns." Currently entangled.

**Search.** No global search and no real per-space search beyond exact-match
name filtering on the client.

**Personal favourites.** No way to pin objects or types to the top of the
sidebar. Standard Anytype behaviour, currently absent.

**File upload (blob → CID).** Blocks object cover images, custom space icons
(so icons can only be emoji today), profile avatars, attachments in the
editor, and image blocks in BlockNote.

**Icons on properties.** Property labels are text-only.

**Icons on lists / types.** Overlaps with the rename/icon ask in P0 — flagged
separately because it's a distinct piece of metadata even after rename ships.

**Grouping.** Board-style "group by status," collapsible sections in lists,
group headers in tables.

**Restore deleted space or object.** Delete is one-way. Trash exists, but
there's no undo (`SetLocalStatus(StatusActive)` not exposed).

**Chat API.** Per-object discussion / comments thread.

**Public / external API surface.** A documented, stable API for third parties,
separate from the internal SDK calls.

---

## P2 — sync, scale, polish

**Subscribe to changes (websocket / event stream).** The big one. The client
polls today. Edit a row in one tab, the other tab won't see it until refocus.
Across a user's own devices the app feels stale until manually refreshed.
Every "live" feature is downstream of this.

**Sync status.** No way to ask "is this object synced to my other devices?
am I online?" — so the synced / syncing / offline dot can't be built.

**Bulk move / bulk delete.** Dragging 50 rows is 50 HTTP requests today.
Bulk delete is N sequential calls. Works, but slow, and any one of them can
fail halfway through.

**Server-side filter by parent.** The sidebar tree filters children
client-side. Fine for hundreds of objects, will drag at thousands. Needs an
indexed `nav.parentId` filter with `nav.pos` ordering.

---

## Headline

We can read and create. We can barely change metadata. Changes don't propagate
live between a user's own devices.

Fixing the **P0 rename/icon** items alone deletes three `localStorage` shadow
files (`space-meta.ts`, `type-meta.ts`, the per-type half of `table.ts`).
Adding **subscriptions** makes the app feel real across laptop and phone
instead of needing a manual refresh. Everything else builds on top of those.
