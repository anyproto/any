import { useEffect, useState } from 'react';
import { useSetAtom } from 'jotai';
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
import { Input, Textarea } from '@/components/ui/Input';
import { Label } from '@/components/ui/Label';
import { toast } from '@/components/ui/Toast';
import { useCreateSpace } from '@/lib/api/spaces';
import { ApiError } from '@/lib/api/client';
import { activeSpaceIdAtom } from '@/atoms/selection';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * Dialog for POST /v1/spaces. Name is required (server accepts empty
 * but the rail looks broken with no name); description is optional.
 */
export function CreateSpaceDialog({ open, onOpenChange }: Props) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const setActiveSpaceId = useSetAtom(activeSpaceIdAtom);
  const createMutation = useCreateSpace();

  // Reset form when the dialog closes (or before it next opens).
  // `createMutation` is a fresh object every render, so it MUST NOT
  // be in the deps — that would loop forever.
  const reset = createMutation.reset;
  useEffect(() => {
    if (!open) {
      setName('');
      setDescription('');
      reset();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentional; see comment above
  }, [open]);

  const trimmedName = name.trim();
  const canSubmit = trimmedName.length > 0 && !createMutation.isPending;

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    try {
      const trimmedDescription = description.trim();
      const req = trimmedDescription
        ? { name: trimmedName, description: trimmedDescription }
        : { name: trimmedName };
      const created = await createMutation.mutateAsync(req);
      setActiveSpaceId(created.id);
      toast.success(`Created “${created.name ?? 'space'}”`);
      onOpenChange(false);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to create space';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={(e) => void onSubmit(e)}>
          <DialogHeader>
            <DialogTitle>New space</DialogTitle>
            <DialogDescription>
              Spaces are independent containers for your objects. You can create as many
              as you like.
            </DialogDescription>
          </DialogHeader>

          <div className="mt-4 space-y-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-space-name">Name</Label>
              <Input
                id="new-space-name"
                // eslint-disable-next-line jsx-a11y/no-autofocus -- inside a Radix Dialog focus trap; autoFocus just lands on the name field
                autoFocus
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="My space"
                aria-invalid={trimmedName.length === 0 && name.length > 0}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-space-description">Description (optional)</Label>
              <Textarea
                id="new-space-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="What's this space for?"
                rows={3}
              />
            </div>
          </div>

          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="ghost">
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={!canSubmit}>
              {createMutation.isPending ? 'Creating…' : 'Create space'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
