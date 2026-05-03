import { useEffect, useState } from 'react';
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
import { useSpaceMeta } from '@/atoms';
import { cn } from '@/lib/cn';

interface Props {
  /** The space being edited. `null` ⇒ dialog stays closed. */
  spaceId: string | null;
  /** Server-side name, shown as the placeholder so users see what
   *  the value will fall back to when they clear the override. */
  serverName?: string | undefined;
  onClose: () => void;
}

const QUICK_PICK_ICONS = ['📝', '📚', '🎯', '🍳', '🎨', '✈️', '💼', '🏠', '🎮', '🎵', '🌱', '❤️'];

/**
 * Edit a space's display name + icon. Both fields are stored in
 * a localStorage-backed Jotai atom (no server round-trip yet —
 * see docs/specs/PR-018-space-rename-icon.md for the SDK gap).
 */
export function EditSpaceDialog({ spaceId, serverName, onClose }: Props) {
  const open = spaceId != null;
  const { overrideName, overrideIcon, set, clear } = useSpaceMeta(spaceId);

  const [name, setName] = useState('');
  const [icon, setIcon] = useState('');

  // Hydrate the form when the dialog opens for a new space.
  useEffect(() => {
    if (!open) return;
    setName(overrideName ?? '');
    setIcon(overrideIcon ?? '');
    // We deliberately depend only on `open` so re-renders during
    // editing don't clobber the user's draft.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    set({ name, icon });
    onClose();
  };

  const onReset = () => {
    clear();
    setName('');
    setIcon('');
    onClose();
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <DialogContent>
        <form onSubmit={onSubmit}>
          <DialogHeader>
            <DialogTitle>Edit space</DialogTitle>
            <DialogDescription>
              Change how this space is shown in your sidebar. These
              settings stay on this device until cross-device sync
              ships.
            </DialogDescription>
          </DialogHeader>

          <div className="mt-4 space-y-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="edit-space-name">Name</Label>
              <Input
                id="edit-space-name"
                // eslint-disable-next-line jsx-a11y/no-autofocus -- Radix focus trap
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={serverName?.trim() || 'Untitled'}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="edit-space-icon">Icon</Label>
              <div className="flex flex-wrap gap-1">
                {QUICK_PICK_ICONS.map((emoji) => (
                  <button
                    type="button"
                    key={emoji}
                    aria-label={`Use ${emoji}`}
                    onClick={() => setIcon(emoji)}
                    className={cn(
                      'inline-flex h-8 w-8 items-center justify-center rounded-md text-base',
                      'hover:bg-foreground/5',
                      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
                      icon === emoji && 'bg-foreground/10 ring-1 ring-foreground/20',
                    )}
                  >
                    {emoji}
                  </button>
                ))}
              </div>
              <Input
                id="edit-space-icon"
                value={icon}
                onChange={(e) => setIcon(e.target.value)}
                placeholder="Or paste any emoji"
                maxLength={4}
                className="mt-1"
              />
              <p className="text-xs text-foreground/50">
                Leave blank to use the auto-generated initial.
              </p>
            </div>
          </div>

          <DialogFooter className="mt-6 flex items-center justify-between gap-2 sm:justify-between">
            <Button
              type="button"
              variant="ghost"
              onClick={onReset}
              className="text-xs text-foreground/60"
            >
              Reset to default
            </Button>
            <div className="flex gap-2">
              <DialogClose asChild>
                <Button type="button" variant="ghost">
                  Cancel
                </Button>
              </DialogClose>
              <Button type="submit">Save</Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
