import type { Dispatch, KeyboardEvent, SetStateAction } from 'react';
import type { Atom } from 'jotai';
import {
  selectedTreeIdsAtom,
  treeSelectionAnchorAtom,
  treeSelectionKeyboardEdgeAtom,
} from '@/atoms';

const KEYBOARD_RANGE_PARENT_ID = '__visible_tree_range__';

function visibleTreeRowsFrom(row: HTMLElement): HTMLElement[] {
  const root = row.closest('[role="tree"]') ?? row.closest('ul') ?? document;
  return Array.from(root.querySelectorAll<HTMLElement>('[data-tree-row-id]'));
}

function visibleRangeIds(rowIds: string[], fromId: string, toId: string): string[] {
  const fi = rowIds.indexOf(fromId);
  const ti = rowIds.indexOf(toId);
  if (fi === -1 || ti === -1) return [toId];
  const [start, end] = fi < ti ? [fi, ti] : [ti, fi];
  return rowIds.slice(start, end + 1);
}

function focusTreeRow(row: HTMLElement) {
  row.focus({ preventScroll: true });
  if ('scrollIntoView' in row) {
    row.scrollIntoView({ block: 'nearest' });
  }
}

export function useObjectRowKeyboard({
  objId,
  isFolder,
  renaming,
  store,
  setAnchor,
  setSelectedIds,
  setKeyboardEdge,
  setPendingBulkDelete,
  setPendingDelete,
  startRename,
  toggleFolder,
}: {
  objId: string;
  isFolder: boolean;
  renaming: boolean;
  store: { get: <Value>(atom: Atom<Value>) => Value };
  setAnchor: (anchor: { id: string; parentId: string }) => void;
  setSelectedIds: (ids: Set<string>) => void;
  setKeyboardEdge: (id: string) => void;
  setPendingBulkDelete: (ids: string[]) => void;
  setPendingDelete: Dispatch<SetStateAction<boolean>>;
  startRename: () => void;
  toggleFolder: () => void;
}) {
  const extendKeyboardRange = (direction: -1 | 1, currentRow: HTMLElement) => {
    const rows = visibleTreeRowsFrom(currentRow);
    const rowIds = rows
      .map((row) => row.dataset.treeRowId)
      .filter((id): id is string => typeof id === 'string' && id.length > 0);
    const storedEdgeId = store.get(treeSelectionKeyboardEdgeAtom);
    const currentEdgeId =
      storedEdgeId && rowIds.includes(storedEdgeId) ? storedEdgeId : objId;
    const currentIndex = rowIds.indexOf(currentEdgeId);
    if (currentIndex === -1) return;

    const nextRow = rows[currentIndex + direction];
    const nextId = nextRow?.dataset.treeRowId;
    if (!nextRow || !nextId) return;

    const anchor = store.get(treeSelectionAnchorAtom);
    const anchorId =
      anchor?.parentId === KEYBOARD_RANGE_PARENT_ID && rowIds.includes(anchor.id)
        ? anchor.id
        : currentEdgeId;
    if (
      !anchor ||
      anchor.parentId !== KEYBOARD_RANGE_PARENT_ID ||
      !rowIds.includes(anchor.id)
    ) {
      setAnchor({ id: currentEdgeId, parentId: KEYBOARD_RANGE_PARENT_ID });
    }

    setSelectedIds(new Set(visibleRangeIds(rowIds, anchorId, nextId)));
    setKeyboardEdge(nextId);
    focusTreeRow(nextRow);
  };

  return (e: KeyboardEvent<HTMLLIElement>) => {
    if (renaming) return;
    if (e.shiftKey && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      e.preventDefault();
      extendKeyboardRange(e.key === 'ArrowDown' ? 1 : -1, e.currentTarget);
      return;
    }
    if (e.key === 'F2') {
      e.preventDefault();
      startRename();
    } else if (e.key === 'Backspace' || e.key === 'Delete') {
      e.preventDefault();
      const cur = store.get(selectedTreeIdsAtom);
      if (cur.size > 1 && cur.has(objId)) {
        setPendingBulkDelete(Array.from(cur));
      } else {
        setPendingDelete(true);
      }
    } else if (e.key === 'Enter' && isFolder) {
      e.preventDefault();
      toggleFolder();
    }
  };
}

export { KEYBOARD_RANGE_PARENT_ID };
