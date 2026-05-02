import { useState } from 'react';
import { useAtom, useSetAtom } from 'jotai';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/Dialog';
import { Button } from '@/components/ui/Button';
import { toast } from '@/components/ui/Toast';
import { deleteObject } from '@/lib/api/objects';
import { ApiError } from '@/lib/api/client';
import { activeObjectIdAtom } from '@/atoms/selection';
import { clearTreeSelectionAtom } from '@/atoms/tree-selection';
import { useQueryClient } from '@tanstack/react-query';

interface Props {
  spaceId: string;
  /** ids to delete; null/empty = closed. */
  ids: string[] | null;
  onClose: () => void;
}

/**
 * Bulk-delete N objects in one confirm. Sequential calls because the
 * SDK has no batch endpoint; a failure on row K toasts but the
 * remaining ones still go through. After completion the tree caches
 * for the space refresh and the multi-selection clears.
 */
export function BulkDeleteObjectsDialog({ spaceId, ids, onClose }: Props) {
  const [activeObjectId, setActiveObjectId] = useAtom(activeObjectIdAtom);
  const clearSelection = useSetAtom(clearTreeSelectionAtom);
  const qc = useQueryClient();
  const [pending, setPending] = useState(false);

  const open = ids != null && ids.length > 0;
  const count = ids?.length ?? 0;

  const onConfirm = async () => {
    if (!ids || ids.length === 0) return;
    setPending(true);
    let succeeded = 0;
    const failures: { id: string; msg: string }[] = [];
    for (const id of ids) {
      try {
        await deleteObject(spaceId, id);
        succeeded++;
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        const code = err instanceof ApiError ? err.code : 'unknown';
        failures.push({ id, msg: `${code}: ${msg}` });
      }
    }
    setPending(false);

    if (activeObjectId && ids.includes(activeObjectId)) {
      setActiveObjectId(null);
    }
    clearSelection();

    // Refresh every children-of-parent cache for this space — cheap
    // (lazy refetch only mounted ones).
    void qc.invalidateQueries({
      predicate: (q) => {
        const k = q.queryKey;
        return Array.isArray(k) && k[0] === 'objects' && k[1] === spaceId;
      },
    });

    if (failures.length === 0) {
      toast.success(`Deleted ${succeeded} object${succeeded === 1 ? '' : 's'}`);
    } else if (succeeded === 0) {
      toast.error(`Failed to delete: ${failures[0]!.msg}`);
    } else {
      toast.error(
        `Deleted ${succeeded}, ${failures.length} failed (first: ${failures[0]!.msg})`,
      );
    }
    onClose();
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !pending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Delete {count} object{count === 1 ? '' : 's'}?
          </DialogTitle>
          <DialogDescription>
            These objects and any descendants become inaccessible. This cannot
            be undone.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="ghost" disabled={pending}>
              Cancel
            </Button>
          </DialogClose>
          <Button
            type="button"
            variant="danger"
            disabled={pending}
            onClick={() => void onConfirm()}
          >
            {pending ? 'Deleting…' : `Delete ${count}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
