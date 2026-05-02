import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useAtom } from 'jotai';
import { useDraggable, useDroppable } from '@dnd-kit/core';
import { ChevronRight, ChevronDown, FileText, Folder, Pencil, Trash2 } from 'lucide-react';
import {
  useObjectChildren,
  useRenameObject,
  type ObjectRecord,
  NAV_FOLDER,
  NAV_ROOT_PARENT_ID,
} from '@/lib/api/objects';
import { activeObjectIdAtom } from '@/atoms/selection';
import { renamingObjectIdAtom } from '@/atoms/edit';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/ContextMenu';
import { Input } from '@/components/ui/Input';
import { toast } from '@/components/ui/Toast';
import { ApiError } from '@/lib/api/client';
import { DeleteObjectDialog } from '@/components/objects/DeleteObjectDialog';
import { cn } from '@/lib/cn';

interface ObjectRowProps {
  spaceId: string;
  obj: ObjectRecord;
  /** Visual depth — 0 for root, +1 per level. Drives left padding. */
  depth: number;
}

/**
 * One row in the object tree. Folders get a chevron + lazy-loaded
 * children; items don't.
 *
 * Click anywhere on the row body → select.
 * Click chevron (folders only) → expand/collapse, no selection change.
 * F2 / context-menu Rename → inline rename input.
 * Backspace/Delete / context-menu Delete → confirm dialog.
 */
export function ObjectRow({ spaceId, obj, depth }: ObjectRowProps) {
  const [activeObjectId, setActiveObjectId] = useAtom(activeObjectIdAtom);
  const [renamingId, setRenamingId] = useAtom(renamingObjectIdAtom);
  const [expanded, setExpanded] = useState(false);
  const [pendingDelete, setPendingDelete] = useState(false);
  const renameMutation = useRenameObject(spaceId);

  const isFolder = obj.nav?.type === NAV_FOLDER;
  const active = activeObjectId === obj.id;
  const renaming = renamingId === obj.id;
  const parentId = obj.nav?.parentId ?? NAV_ROOT_PARENT_ID;
  const title = obj.any?.name?.trim() || `Untitled (${obj.id.slice(0, 6)}…)`;

  // DnD: row is both a draggable source and a droppable target.
  // The drop target id must equal the object id so ObjectTree's
  // onDragEnd reducer can look it up directly.
  const drag = useDraggable({ id: obj.id, disabled: renaming });
  const drop = useDroppable({ id: obj.id });
  const isDropTarget = drop.isOver && drop.active && drop.active.id !== obj.id;

  const startRename = () => setRenamingId(obj.id);
  const stopRename = () => setRenamingId(null);

  const commitRename = async (next: string) => {
    const trimmed = next.trim();
    stopRename();
    if (trimmed === '' || trimmed === (obj.any?.name ?? '')) return;
    try {
      await renameMutation.mutateAsync({ objectId: obj.id, parentId, name: trimmed });
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to rename';
      toast.error(`${code}: ${msg}`);
    }
  };

  const onRowKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (renaming) return; // rename input owns keys
    if (e.key === 'F2') {
      e.preventDefault();
      startRename();
    } else if (e.key === 'Backspace' || e.key === 'Delete') {
      e.preventDefault();
      setPendingDelete(true);
    } else if (e.key === 'Enter' && isFolder) {
      e.preventDefault();
      setExpanded((v) => !v);
    }
  };

  return (
    <li
      ref={drop.setNodeRef}
      role="treeitem"
      aria-selected={active}
      aria-expanded={isFolder ? expanded : undefined}
      tabIndex={0}
      onKeyDown={onRowKeyDown}
      className="focus-visible:outline-none"
      style={
        drag.transform
          ? { transform: `translate3d(${drag.transform.x}px, ${drag.transform.y}px, 0)`, opacity: drag.isDragging ? 0.4 : 1 }
          : undefined
      }
    >
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            ref={drag.setNodeRef}
            {...drag.listeners}
            className={cn(
              'group flex items-center gap-1 rounded-md px-1.5 py-1 text-sm text-foreground',
              'hover:bg-foreground/5',
              active && 'bg-foreground/8 font-medium',
              isDropTarget && (isFolder ? 'bg-accent/15 ring-1 ring-accent/40' : 'ring-1 ring-accent/30'),
              'group-focus-visible:ring-2 group-focus-visible:ring-accent',
            )}
            style={{ paddingLeft: `${0.375 + depth * 1}rem` }}
          >
            {isFolder ? (
              <button
                type="button"
                aria-label={expanded ? 'Collapse' : 'Expand'}
                aria-expanded={expanded}
                onClick={() => setExpanded((v) => !v)}
                className={cn(
                  'inline-flex h-4 w-4 items-center justify-center rounded text-foreground/50 hover:text-foreground',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
                )}
              >
                {expanded ? (
                  <ChevronDown className="h-3 w-3" aria-hidden />
                ) : (
                  <ChevronRight className="h-3 w-3" aria-hidden />
                )}
              </button>
            ) : (
              <span className="inline-block h-4 w-4" aria-hidden />
            )}

            {isFolder ? (
              <Folder className="h-3.5 w-3.5 shrink-0 text-foreground/60" aria-hidden />
            ) : (
              <FileText className="h-3.5 w-3.5 shrink-0 text-foreground/60" aria-hidden />
            )}

            {renaming ? (
              <RenameInput
                initial={obj.any?.name ?? ''}
                onCommit={(v) => void commitRename(v)}
                onCancel={stopRename}
              />
            ) : (
              <button
                type="button"
                onClick={() => setActiveObjectId(obj.id)}
                onDoubleClick={startRename}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'flex-1 truncate text-left',
                  'focus-visible:outline-none rounded',
                )}
              >
                {title}
              </button>
            )}
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onSelect={startRename}>
            <Pencil className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
            Rename
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            onSelect={() => setPendingDelete(true)}
            className="text-destructive"
          >
            <Trash2 className="h-3.5 w-3.5" aria-hidden />
            Delete…
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      {/* Lazy-loaded children */}
      {isFolder && expanded && (
        <ChildrenList spaceId={spaceId} parentId={obj.id} depth={depth + 1} />
      )}

      <DeleteObjectDialog
        spaceId={spaceId}
        obj={pendingDelete ? obj : null}
        parentId={parentId}
        onClose={() => setPendingDelete(false)}
      />
    </li>
  );
}

/**
 * The inline rename input. Auto-focuses + selects on mount; commits
 * on Enter or blur, cancels on Escape.
 */
function RenameInput({
  initial,
  onCommit,
  onCancel,
}: {
  initial: string;
  onCommit: (next: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initial);
  const inputRef = useRef<HTMLInputElement>(null);
  // Suppress the very next blur after Escape so cancel doesn't re-commit.
  const cancelledRef = useRef(false);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  return (
    <Input
      ref={inputRef}
      className="h-6 flex-1 px-1.5 text-sm"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          onCommit(value);
        } else if (e.key === 'Escape') {
          e.preventDefault();
          cancelledRef.current = true;
          onCancel();
        }
        // Stop the row-level shortcuts from firing while editing.
        e.stopPropagation();
      }}
      onBlur={() => {
        if (cancelledRef.current) {
          cancelledRef.current = false;
          return;
        }
        onCommit(value);
      }}
    />
  );
}

/**
 * The children of a folder — fetched on first expand. Self-contained
 * loading/empty states keep the parent row simple.
 */
export function ChildrenList({
  spaceId,
  parentId,
  depth,
}: {
  spaceId: string;
  parentId: string;
  depth: number;
}) {
  const { data, isPending, isError } = useObjectChildren(spaceId, parentId);

  if (isPending) {
    return (
      <ul style={{ paddingLeft: `${0.375 + depth * 1}rem` }} className="py-1">
        <li className="text-xs text-foreground/40">Loading…</li>
      </ul>
    );
  }
  if (isError) {
    return (
      <ul style={{ paddingLeft: `${0.375 + depth * 1}rem` }} className="py-1">
        <li className="text-xs text-destructive">Failed to load</li>
      </ul>
    );
  }
  if (!data || data.length === 0) {
    return (
      <ul style={{ paddingLeft: `${0.375 + depth * 1}rem` }} className="py-1">
        <li className="text-xs text-foreground/40">Empty</li>
      </ul>
    );
  }
  return (
    <ul role="group">
      {data.map((child) => (
        <ObjectRow key={child.id} spaceId={spaceId} obj={child} depth={depth} />
      ))}
    </ul>
  );
}
