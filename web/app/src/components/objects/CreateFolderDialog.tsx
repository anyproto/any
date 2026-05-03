import { useEffect, useState } from 'react';
import { FolderPlus } from 'lucide-react';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui';
import { Button, Input, Label } from '@/components/ui';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  isPending?: boolean;
  onCreate: (name: string) => void | Promise<void>;
}

export function CreateFolderDialog({ open, onOpenChange, isPending = false, onCreate }: Props) {
  const [name, setName] = useState('');
  const trimmedName = name.trim();
  const canSubmit = trimmedName.length > 0 && !isPending;

  useEffect(() => {
    if (!open) setName('');
  }, [open]);

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    void onCreate(trimmedName);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <form onSubmit={onSubmit}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <FolderPlus className="h-4 w-4 text-foreground/55" aria-hidden />
              New folder
            </DialogTitle>
            <DialogDescription>
              Folders organize pages in the sidebar. They do not open as editable documents.
            </DialogDescription>
          </DialogHeader>

          <div className="mt-4 flex flex-col gap-1.5">
            <Label htmlFor="new-folder-name">Name</Label>
            <Input
              id="new-folder-name"
              // eslint-disable-next-line jsx-a11y/no-autofocus -- Radix focus trap
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Marketing"
              aria-invalid={name.length > 0 && trimmedName.length === 0}
            />
          </div>

          <DialogFooter className="mt-6">
            <DialogClose asChild>
              <Button type="button" variant="ghost" disabled={isPending}>
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={!canSubmit}>
              {isPending ? 'Creating…' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
