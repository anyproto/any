import { memo, useEffect, useState, type CSSProperties, type MouseEvent } from 'react';
import { useAtomValue, useSetAtom } from 'jotai';
import { useDraggable, useDroppable } from '@dnd-kit/core';
import { ChevronDown, ChevronRight, FileText, Folder } from 'lucide-react';
import {
  useCreateObject,
  useObjectChildren,
  useRenameObject,
  type ObjectRecord,
  NAV_FOLDER,
} from '@/lib/api/objects';
import {
  pendingBulkDeleteAtom,
  treeExpansionSignalAtom,
} from '@/atoms';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuTrigger,
  toast,
} from '@/components/ui';
import { ApiError } from '@/lib/api/client';
import { DeleteObjectDialog } from '@/components/objects';
import { cn } from '@/lib/cn';
import { preloadViewModule } from '@/app/viewModules';
import {
  ObjectRowActions,
  ObjectRowContextActions,
} from './ObjectRowActions';
import { ObjectRowRename } from './ObjectRowRename';
import { useObjectRowKeyboard } from './useObjectRowKeyboard';
import { useObjectRowSelection } from './useObjectRowSelection';

interface ObjectRowProps {
  spaceId: string;
  obj: ObjectRecord;
  parentId?: string;
  depth: number;
  pagesListId?: string | null | undefined;
}

function ObjectRowImpl({
  spaceId,
  obj,
  parentId: renderedParentId,
  depth,
  pagesListId,
}: ObjectRowProps) {
  const [expanded, setExpanded] = useState(false);
  const [pendingDelete, setPendingDelete] = useState(false);
  const createMutation = useCreateObject(spaceId);
  const renameMutation = useRenameObject(spaceId);
  const setPendingBulkDelete = useSetAtom(pendingBulkDeleteAtom);
  const expansionSignal = useAtomValue(treeExpansionSignalAtom);
  const isFolder = obj.nav?.type === NAV_FOLDER;

  const {
    active,
    selected,
    renaming,
    liveTitleDraft,
    parentId,
    store,
    setActiveObjectId,
    setSelectedIds,
    setAnchor,
    setKeyboardEdge,
    startRename,
    stopRename,
    onTitleClick,
  } = useObjectRowSelection({
    spaceId,
    obj,
    renderedParentId,
    isFolder,
    onToggleFolder: () => setExpanded((v) => !v),
  });

  const titleSource = liveTitleDraft ?? obj.any?.name ?? '';
  const title = titleSource.trim() || `Untitled (${obj.id.slice(0, 6)}…)`;

  useEffect(() => {
    if (!isFolder || expansionSignal?.spaceId !== spaceId) return;
    setExpanded(expansionSignal.action === 'expand');
  }, [expansionSignal, isFolder, spaceId]);

  const drag = useDraggable({ id: obj.id, disabled: renaming });
  const drop = useDroppable({ id: obj.id });
  const isDropTarget = drop.isOver && drop.active && drop.active.id !== obj.id;
  const rowStyle: CSSProperties | undefined = drag.transform
    ? {
        transform: `translate3d(${drag.transform.x}px, ${drag.transform.y}px, 0)`,
        opacity: drag.isDragging ? 0.4 : 1,
      }
    : isFolder
      ? undefined
      : leafRowStyle;

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

  const createChildObject = async (e: MouseEvent<HTMLButtonElement>) => {
    e.preventDefault();
    e.stopPropagation();
    if (!isFolder) return;
    try {
      const { objectId } = await createMutation.mutateAsync({
        parentId: obj.id,
        ...(pagesListId ? { typeIds: [pagesListId] } : {}),
      });
      setExpanded(true);
      setSelectedIds(new Set([objectId]));
      setAnchor({ id: objectId, parentId: obj.id });
      setKeyboardEdge(objectId);
      setActiveObjectId(objectId);
      toast.success('Created page');
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to create object';
      toast.error(`${code}: ${msg}`);
    }
  };

  const onRowKeyDown = useObjectRowKeyboard({
    objId: obj.id,
    isFolder,
    renaming,
    store,
    setAnchor,
    setSelectedIds,
    setKeyboardEdge,
    setPendingBulkDelete,
    setPendingDelete,
    startRename,
    toggleFolder: () => setExpanded((v) => !v),
  });

  return (
    <li
      ref={drop.setNodeRef}
      role="treeitem"
      aria-selected={active}
      aria-expanded={isFolder ? expanded : undefined}
      data-tree-row-id={obj.id}
      tabIndex={0}
      onKeyDown={onRowKeyDown}
      className="focus-visible:outline-none"
      style={rowStyle}
    >
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            ref={drag.setNodeRef}
            {...drag.listeners}
            className={cn(
              'group flex h-7 items-center gap-1.5 rounded-md px-2 text-[13px] text-foreground/85',
              'hover:bg-foreground/5',
              selected && !active && 'bg-foreground/[0.06] text-foreground',
              active && 'bg-foreground/8 text-foreground font-medium',
              isDropTarget &&
                (isFolder
                  ? 'bg-accent/15 ring-1 ring-accent/40'
                  : 'ring-1 ring-accent/30'),
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
              <ObjectRowRename
                initial={obj.any?.name ?? ''}
                onCommit={(v) => void commitRename(v)}
                onCancel={stopRename}
              />
            ) : (
              <button
                type="button"
                onClick={onTitleClick}
                onDoubleClick={startRename}
                onFocus={() => preloadViewModule('object')}
                aria-current={active ? 'page' : undefined}
                className="flex-1 truncate rounded text-left focus-visible:outline-none"
              >
                {title}
              </button>
            )}
            <ObjectRowActions
              isFolder={isFolder}
              expanded={expanded}
              renaming={renaming}
              title={title}
              creating={createMutation.isPending}
              onCreateChild={(e) => void createChildObject(e)}
            />
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ObjectRowContextActions
            onRename={startRename}
            onDelete={() => setPendingDelete(true)}
          />
        </ContextMenuContent>
      </ContextMenu>

      {isFolder && expanded && (
        <ChildrenList
          spaceId={spaceId}
          parentId={obj.id}
          depth={depth + 1}
          pagesListId={pagesListId}
        />
      )}

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

export const ObjectRow = memo(ObjectRowImpl);

const leafRowStyle: CSSProperties = {
  contentVisibility: 'auto',
  containIntrinsicSize: 'auto 28px',
};

export function ChildrenList({
  spaceId,
  parentId,
  depth,
  pagesListId,
}: {
  spaceId: string;
  parentId: string;
  depth: number;
  pagesListId?: string | null | undefined;
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
        <ObjectRow
          key={child.id}
          spaceId={spaceId}
          obj={child}
          parentId={parentId}
          depth={depth}
          pagesListId={pagesListId}
        />
      ))}
    </ul>
  );
}
