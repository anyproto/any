import { atom } from 'jotai';

/**
 * Multi-selection state for the pages tree. Shift+click range
 * selection + Cmd/Ctrl+click toggle. See
 * docs/specs/PR-024-tree-multi-select.md.
 *
 * Two atoms instead of one struct so a row can subscribe to just the
 * selected-set without re-rendering on anchor moves.
 */

/** Set of object ids currently in the multi-selection. */
export const selectedTreeIdsAtom = atom<Set<string>>(new Set<string>());

/**
 * Anchor for shift+range selection — the row that started the
 * current selection. parentId stays alongside the id so we can
 * reject cross-parent shift+clicks (we only support same-parent
 * ranges in v1).
 */
export interface TreeSelectionAnchor {
  id: string;
  parentId: string;
}

export const treeSelectionAnchorAtom = atom<TreeSelectionAnchor | null>(null);

/** Convenience write-only atom: clears both selected ids and anchor. */
export const clearTreeSelectionAtom = atom(null, (_get, set) => {
  set(selectedTreeIdsAtom, new Set<string>());
  set(treeSelectionAnchorAtom, null);
});

/**
 * When non-null, the BulkDeleteObjectsDialog is shown for these ids.
 * A row sets this when Delete is pressed and the current row is in
 * a multi-selection of ≥2 items; ObjectTree renders the dialog.
 */
export const pendingBulkDeleteAtom = atom<string[] | null>(null);

/**
 * Compute the inclusive range of ids between two siblings, sorted by
 * `nav.pos` (the same order the tree renders them in).
 *
 * `siblings` is the cached children-of-parent list. `fromId` is the
 * anchor; `toId` is the row just clicked. Returns the list of ids
 * spanning the two, in render order.
 */
export function rangeIds(
  siblings: { id: string; nav?: { pos?: string } }[],
  fromId: string,
  toId: string,
): string[] {
  const sorted = [...siblings].sort((a, b) =>
    (a.nav?.pos ?? '') < (b.nav?.pos ?? '') ? -1 : 1,
  );
  const fi = sorted.findIndex((s) => s.id === fromId);
  const ti = sorted.findIndex((s) => s.id === toId);
  if (fi === -1 || ti === -1) return [toId];
  const [start, end] = fi < ti ? [fi, ti] : [ti, fi];
  return sorted.slice(start, end + 1).map((s) => s.id);
}
