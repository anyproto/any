import type { MouseEvent } from 'react';
import { Plus, Pencil, Trash2 } from 'lucide-react';
import {
  ContextMenuItem,
  ContextMenuSeparator,
} from '@/components/ui';
import { cn } from '@/lib/cn';

export function ObjectRowActions({
  isFolder,
  expanded,
  renaming,
  title,
  creating,
  onCreateChild,
}: {
  isFolder: boolean;
  expanded: boolean;
  renaming: boolean;
  title: string;
  creating: boolean;
  onCreateChild: (e: MouseEvent<HTMLButtonElement>) => void;
}) {
  if (!isFolder || !expanded || renaming) return null;
  return (
    <button
      type="button"
      aria-label={`New page inside ${title}`}
      title="New page inside"
      disabled={creating}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={onCreateChild}
      className={cn(
        'ml-auto inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-foreground/45',
        'opacity-0 transition-opacity hover:bg-foreground/8 hover:text-foreground',
        'group-hover:opacity-100',
        'focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        creating && 'pointer-events-none opacity-50',
      )}
    >
      <Plus className="h-3.5 w-3.5" aria-hidden />
    </button>
  );
}

export function ObjectRowContextActions({
  onRename,
  onDelete,
}: {
  onRename: () => void;
  onDelete: () => void;
}) {
  return (
    <>
      <ContextMenuItem onSelect={onRename}>
        <Pencil className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
        Rename
      </ContextMenuItem>
      <ContextMenuSeparator />
      <ContextMenuItem onSelect={onDelete} className="text-destructive">
        <Trash2 className="h-3.5 w-3.5" aria-hidden />
        Delete…
      </ContextMenuItem>
    </>
  );
}
