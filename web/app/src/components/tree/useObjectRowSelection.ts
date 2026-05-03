import { useMemo, type MouseEvent } from 'react';
import { useAtomValue, useSetAtom, useStore } from 'jotai';
import { selectAtom } from 'jotai/utils';
import { useQueryClient } from '@tanstack/react-query';
import {
  activeObjectIdAtom,
  objectTitleDraftKey,
  objectTitleDraftsAtom,
  rangeIds,
  renamingObjectIdAtom,
  selectedTreeIdsAtom,
  treeSelectionAnchorAtom,
  treeSelectionKeyboardEdgeAtom,
} from '@/atoms';
import {
  NAV_ROOT_PARENT_ID,
  type ObjectRecord,
} from '@/lib/api/objects';

export function useObjectRowSelection({
  spaceId,
  obj,
  renderedParentId,
  isFolder,
  onToggleFolder,
}: {
  spaceId: string;
  obj: ObjectRecord;
  renderedParentId: string | undefined;
  isFolder: boolean;
  onToggleFolder: () => void;
}) {
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const setRenamingId = useSetAtom(renamingObjectIdAtom);
  const setSelectedIds = useSetAtom(selectedTreeIdsAtom);
  const setAnchor = useSetAtom(treeSelectionAnchorAtom);
  const setKeyboardEdge = useSetAtom(treeSelectionKeyboardEdgeAtom);

  const activeAtom = useMemo(
    () => selectAtom(activeObjectIdAtom, (id) => id === obj.id),
    [obj.id],
  );
  const selectedAtom = useMemo(
    () => selectAtom(selectedTreeIdsAtom, (set) => set.has(obj.id)),
    [obj.id],
  );
  const renamingAtom = useMemo(
    () => selectAtom(renamingObjectIdAtom, (id) => id === obj.id),
    [obj.id],
  );
  const liveTitleDraftAtom = useMemo(
    () =>
      selectAtom(
        objectTitleDraftsAtom,
        (drafts) => drafts[objectTitleDraftKey(spaceId, obj.id)],
      ),
    [obj.id, spaceId],
  );

  const active = useAtomValue(activeAtom);
  const selected = useAtomValue(selectedAtom);
  const renaming = useAtomValue(renamingAtom);
  const liveTitleDraft = useAtomValue(liveTitleDraftAtom);
  const store = useStore();
  const qc = useQueryClient();
  const parentId = renderedParentId ?? obj.nav?.parentId ?? NAV_ROOT_PARENT_ID;

  const startRename = () => setRenamingId(obj.id);
  const stopRename = () => setRenamingId(null);

  const onTitleClick = (e: MouseEvent<HTMLButtonElement>) => {
    if (e.shiftKey) {
      const anchor = store.get(treeSelectionAnchorAtom);
      if (anchor && anchor.parentId === parentId) {
        const siblings =
          qc.getQueryData<ObjectRecord[]>([
            'objects',
            spaceId,
            'children',
            parentId,
          ]) ?? [];
        const ids = rangeIds(siblings, anchor.id, obj.id);
        setSelectedIds(new Set(ids));
        setKeyboardEdge(obj.id);
        return;
      }
    }
    if (e.metaKey || e.ctrlKey) {
      const cur = store.get(selectedTreeIdsAtom);
      const next = new Set(cur);
      if (next.has(obj.id)) next.delete(obj.id);
      else next.add(obj.id);
      setSelectedIds(next);
      setAnchor({ id: obj.id, parentId });
      setKeyboardEdge(obj.id);
      return;
    }

    setSelectedIds(new Set([obj.id]));
    setAnchor({ id: obj.id, parentId });
    setKeyboardEdge(obj.id);
    if (isFolder) onToggleFolder();
    else setActiveObjectId(obj.id);
  };

  return {
    active,
    selected,
    renaming,
    liveTitleDraft,
    parentId,
    store,
    setActiveObjectId,
    setSelectedIds,
    setAnchor,
    setKeyboardEdge,
    startRename,
    stopRename,
    onTitleClick,
  };
}
