import { useAtom } from 'jotai';
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
import { useDeleteObject, type ObjectRecord } from '@/lib/api/objects';
import { ApiError } from '@/lib/api/client';
import { activeObjectIdAtom } from '@/atoms/selection';

interface Props {
  spaceId: string;
  /** Object pending deletion; null = closed. */
  obj: ObjectRecord | null;
  parentId: string;
  onClose: () => void;
}

/**
 * Confirm dialog for deleting an object. Strong copy because the
 * SDK doesn't support undelete.
 */
export function DeleteObjectDialog({ spaceId, obj, parentId, onClose }: Props) {
  const [activeObjectId, setActiveObjectId] = useAtom(activeObjectIdAtom);
  const deleteMutation = useDeleteObject(spaceId);
  const open = obj !== null;
  const title =
    obj?.any?.name?.trim() || (obj ? `Untitled (${obj.id.slice(0, 6)}…)` : 'this object');

  const onConfirm = async () => {
    if (!obj) return;
    try {
      await deleteMutation.mutateAsync({ objectId: obj.id, parentId });
      toast.success(`Deleted “${title}”`);
      if (obj.id === activeObjectId) setActiveObjectId(null);
      onClose();
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to delete object';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete “{title}”?</DialogTitle>
          <DialogDescription>
            This object and any descendants become inaccessible. This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="ghost">
              Cancel
            </Button>
          </DialogClose>
          <Button
            type="button"
            variant="danger"
            disabled={deleteMutation.isPending}
            onClick={() => void onConfirm()}
          >
            {deleteMutation.isPending ? 'Deleting…' : 'Delete'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
