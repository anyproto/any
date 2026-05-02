import { memo, useEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react';
import { useAtomValue, useSetAtom, useStore } from 'jotai';
import { selectAtom } from 'jotai/utils';
import { useQueryClient } from '@tanstack/react-query';
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
  pendingBulkDeleteAtom,
  rangeIds,
  selectedTreeIdsAtom,
  treeSelectionAnchorAtom,
} from '@/atoms/tree-selection';
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
/**
 * Per-row derived atoms — `selectAtom` ensures a row only re-renders
 * when *its own* slice of the global state changes, not on every
 * change to the underlying selection set / active id / renaming id.
 *
 * Without these the multi-select Set update triggers all N rows to
 * re-render on every plain click. With 200 objects that's the source
 * of the perceived lag.
 */
function ObjectRowImpl({ spaceId, obj, depth }: ObjectRowProps) {
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const setRenamingId = useSetAtom(renamingObjectIdAtom);
  const setSelectedIds = useSetAtom(selectedTreeIdsAtom);
  const setAnchor = useSetAtom(treeSelectionAnchorAtom);
  const setPendingBulkDelete = useSetAtom(pendingBulkDeleteAtom);

  // Per-row derived booleans. Memo keyed on obj.id so the derived
  // atom is stable across renders.
  const activeAtom = useMemo(
    () => selectAtom(activeObjectIdAtom, (id) => id === obj.id),
    [obj.id],
  );
  const selectedAtom = useMemo(
    () => selectAtom(selectedTreeIdsAtom, (set) => set.has(obj.id)),
    [obj.id],
  );
  const renamingAtom = useMemo(
    () => selectAtom(renamingObjectIdAtom, (id) => id === obj.id),
    [obj.id],
  );
  const active = useAtomValue(activeAtom);
  const selected = useAtomValue(selectedAtom);
  const renaming = useAtomValue(renamingAtom);

  // Read the anchor + selection lazily inside event handlers so
  // changes don't subscribe the row to re-renders.
  const store = useStore();

  const qc = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [pendingDelete, setPendingDelete] = useState(false);
  const renameMutation = useRenameObject(spaceId);

  const isFolder = obj.nav?.type === NAV_FOLDER;
  const parentId = obj.nav?.parentId ?? NAV_ROOT_PARENT_ID;
  const title = obj.any?.name?.trim() || `Untitled (${obj.id.slice(0, 6)}…)`;

  /**
   * Click handler with modifier-aware multi-select. See
   * docs/specs/PR-024-tree-multi-select.md. Reads anchor + selection
   * from the store rather than subscribing — the row doesn't need
   * to re-render when those change.
   */
  const onTitleClick = (e: MouseEvent<HTMLButtonElement>) => {
    if (e.shiftKey) {
      const anchor = store.get(treeSelectionAnchorAtom);
      if (anchor && anchor.parentId === parentId) {
        const siblings = qc.getQueryData<ObjectRecord[]>([
          'objects',
          spaceId,
          'children',
          parentId,
        ]) ?? [];
        const ids = rangeIds(siblings, anchor.id, obj.id);
        setSelectedIds(new Set(ids));
        return;
      }
      // No anchor or cross-parent: fall through to single-select.
    }
    if (e.metaKey || e.ctrlKey) {
      const cur = store.get(selectedTreeIdsAtom);
      const next = new Set(cur);
      if (next.has(obj.id)) next.delete(obj.id);
      else next.add(obj.id);
      setSelectedIds(next);
      setAnchor({ id: obj.id, parentId });
      return;
    }
    // Plain click: clear multi-selection, single-select, open.
    setSelectedIds(new Set([obj.id]));
    setAnchor({ id: obj.id, parentId });
    setActiveObjectId(obj.id);
  };

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
      // Read selection lazily from store — no subscription needed
      // for a one-shot keypress check.
      const cur = store.get(selectedTreeIdsAtom);
      if (cur.size > 1 && cur.has(obj.id)) {
        setPendingBulkDelete(Array.from(cur));
      } else {
        setPendingDelete(true);
      }
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
          ? {
              transform: `translate3d(${drag.transform.x}px, ${drag.transform.y}px, 0)`,
              opacity: drag.isDragging ? 0.4 : 1,
              // Off-screen rows: tell the browser to skip layout + paint
              // until they scroll into view. Big paint win in long lists.
              contentVisibility: 'auto',
              containIntrinsicSize: 'auto 28px',
            }
          : {
              contentVisibility: 'auto',
              containIntrinsicSize: 'auto 28px',
            }
      }
    >
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            ref={drag.setNodeRef}
            {...drag.listeners}
            className={cn(
              'group flex h-7 items-center gap-1.5 rounded-md px-2 text-[13px] text-foreground/85',
              'hover:bg-foreground/5',
              // Selected (in multi-selection) but not the open one — softer tint.
              selected && !active && 'bg-foreground/[0.06] text-foreground',
              active && 'bg-foreground/8 text-foreground font-medium',
              isDropTarget && (isFolder ? 'bg-accent/15 ring-1 ring-accent/40' : 'ring-1 ring-accent/30'),
              'group-focus-visible:ring-2 group-focus-visible:ring-accent',
            )}
            style={{ paddingLeft: `${0.5 + depth * 1}rem` }}
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
              <Folder className="h-4 w-4 shrink-0 text-foreground/60" aria-hidden />
            ) : (
              <FileText className="h-4 w-4 shrink-0 text-foreground/60" aria-hidden />
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
                onClick={onTitleClick}
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

      {/* Lazy-mount: even when closed, mounting Radix Dialog per row
          across N rows compounds. Only mount when the user actually
          opened the confirm. */}
      {pendingDelete && (
        <DeleteObjectDialog
          spaceId={spaceId}
          obj={obj}
          parentId={parentId}
          onClose={() => setPendingDelete(false)}
        />
      )}
    </li>
  );
}

/**
 * Memoised export — re-renders only when its props change. Combined
 * with the per-row selectAtom subscriptions above, this keeps a
 * 200-row tree responsive: a click only re-renders the rows whose
 * derived state actually flipped.
 *
 * `obj` is referentially stable from TanStack Query's cache until
 * a refetch lands, so this guard is meaningful in practice.
 */
export const ObjectRow = memo(ObjectRowImpl);

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
