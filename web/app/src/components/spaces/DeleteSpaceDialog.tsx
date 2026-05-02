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
import { useDeleteSpace, type SpaceInfo } from '@/lib/api/spaces';
import { ApiError } from '@/lib/api/client';
import { activeSpaceIdAtom } from '@/atoms/selection';

interface Props {
  /** The space pending deletion; null = closed. */
  space: SpaceInfo | null;
  onClose: () => void;
}

/**
 * Destructive confirm. The wording leans on the irreversibility — the
 * SDK doesn't support undelete in v1.
 */
export function DeleteSpaceDialog({ space, onClose }: Props) {
  const [activeSpaceId, setActiveSpaceId] = useAtom(activeSpaceIdAtom);
  const deleteMutation = useDeleteSpace();
  const open = space !== null;
  const name = space?.name?.trim() || 'this space';

  const onConfirm = async () => {
    if (!space) return;
    try {
      await deleteMutation.mutateAsync(space.id);
      toast.success(`Deleted “${name}”`);
      // If the deleted space was active, clear the selection so
      // AppShell's auto-pick effect picks the next available.
      if (space.id === activeSpaceId) setActiveSpaceId(null);
      onClose();
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to delete space';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete “{name}”?</DialogTitle>
          <DialogDescription>
            Objects in this space will become inaccessible. This cannot be undone.
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
