import { useSetAtom } from 'jotai';
import { useQueryClient } from '@tanstack/react-query';
import { Trash2 } from 'lucide-react';
import { Button, toast } from '@/components/ui';
import {
  objectKeys,
  useDeleteObject,
  type ObjectRecord,
  NAV_ROOT_PARENT_ID,
} from '@/lib/api/objects';
import { ApiError } from '@/lib/api/client';
import { activeObjectIdAtom, clearObjectTitleDraftAtom } from '@/atoms';

interface Props {
  spaceId: string;
  objectId: string;
}

/**
 * Surfaced inside MarkdownEditor's load-error branch. Lets the user
 * purge an orphan projection row (the tree behind it is gone but the
 * row still appears in queries).
 *
 * Why: docs/03-api.md § Object deletion explains how an orphan can
 * exist. The server's DELETE handler still writes the tombstone even
 * when the tree-delete fails, so the row drops out of subsequent
 * queries.
 */
export function OrphanCleanup({ spaceId, objectId }: Props) {
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const clearTitleDraft = useSetAtom(clearObjectTitleDraftAtom);
  const qc = useQueryClient();
  const del = useDeleteObject(spaceId);

  const onClick = async () => {
    const parentId = lookupParentId(qc, spaceId, objectId);
    try {
      await del.mutateAsync({ objectId, parentId });
      toast.success('Removed from list');
      clearTitleDraft({ spaceId, objectId });
      setActiveObjectId(null);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to remove';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <Button
      variant="danger"
      size="sm"
      disabled={del.isPending}
      onClick={() => void onClick()}
    >
      <Trash2 className="h-3.5 w-3.5" aria-hidden />
      {del.isPending ? 'Removing…' : 'Remove from list'}
    </Button>
  );
}

/**
 * Walk the children-of-parent caches looking for the row. Returns
 * its `nav.parentId`, or root if we don't find it (cache cold).
 *
 * `useDeleteObject` only uses parentId for cache invalidation, so a
 * wrong guess just causes one extra invalidation — never a wrong
 * server call.
 */
function lookupParentId(
  qc: ReturnType<typeof useQueryClient>,
  spaceId: string,
  objectId: string,
): string {
  const all = qc.getQueriesData<ObjectRecord[]>({
    queryKey: objectKeys.childrenRoot(spaceId),
  });
  for (const [, list] of all) {
    if (!list) continue;
    const hit = list.find((r) => r.id === objectId);
    if (hit) return hit.nav?.parentId ?? NAV_ROOT_PARENT_ID;
  }
  return NAV_ROOT_PARENT_ID;
}
