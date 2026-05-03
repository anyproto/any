import { useCallback, useState } from 'react';
import { useAtomValue, useSetAtom } from 'jotai';
import {
  activeObjectIdAtom,
  activeSpaceIdAtom,
  focusedPaneAtom,
} from '@/atoms';
import {
  useCreateObject,
  useRenameObjectAnywhere,
} from '@/lib/api/objects';
import { useEnsurePagesList } from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';
import { toast } from '@/components/ui';

export function useSpaceContentsController() {
  const activeSpaceId = useAtomValue(activeSpaceIdAtom);
  const setFocused = useSetAtom(focusedPaneAtom);
  const [hierarchyOpen, setHierarchyOpen] = useState(true);
  const [listsOpen, setListsOpen] = useState(true);

  return {
    activeSpaceId,
    focusPane: () => setFocused(2),
    hierarchyOpen,
    toggleHierarchy: () => setHierarchyOpen((v) => !v),
    listsOpen,
    toggleLists: () => setListsOpen((v) => !v),
  };
}

export function useRootObjectActions(spaceId: string) {
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const createMutation = useCreateObject(spaceId);
  const renameMutation = useRenameObjectAnywhere(spaceId);
  const pagesListId = useEnsurePagesList(spaceId);

  const createPage = useCallback(async () => {
    try {
      const opts: { typeIds?: string[] } = {};
      if (pagesListId) opts.typeIds = [pagesListId];
      const { objectId } = await createMutation.mutateAsync(opts);
      setActiveObjectId(objectId);
      toast.success('Created page');
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to create object';
      toast.error(`${code}: ${msg}`);
    }
  }, [createMutation, pagesListId, setActiveObjectId]);

  const createTypedObject = useCallback(
    async (typeIds: string[], label: string) => {
      try {
        const { objectId } = await createMutation.mutateAsync({ typeIds });
        setActiveObjectId(objectId);
        toast.success(`Created ${label}`);
      } catch (err) {
        const code = err instanceof ApiError ? err.code : 'unknown';
        const msg = err instanceof Error ? err.message : 'Failed to create object';
        toast.error(`${code}: ${msg}`);
      }
    },
    [createMutation, setActiveObjectId],
  );

  const createFolder = useCallback(
    async (name: string) => {
      try {
        const { objectId } = await createMutation.mutateAsync({ folder: true });
        await renameMutation.mutateAsync({ objectId, name });
        toast.success(`Created folder “${name}”`);
      } catch (err) {
        const code = err instanceof ApiError ? err.code : 'unknown';
        const msg = err instanceof Error ? err.message : 'Failed to create folder';
        toast.error(`${code}: ${msg}`);
        throw err;
      }
    },
    [createMutation, renameMutation],
  );

  return {
    createPage,
    createTypedObject,
    createFolder,
    isPending: createMutation.isPending || renameMutation.isPending,
  };
}
