import { useState, type MouseEvent as ReactMouseEvent } from 'react';
import { ChevronDown, ChevronUp, Columns3, GripVertical } from 'lucide-react';
import { cn } from '@/lib/cn';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui';
import type { SortDir } from './tableSorting';

export function TableColumnHeader({
  propId,
  label,
  sortKey,
  activeSortKey,
  activeSortDir,
  onSort,
  dragging,
  dropTarget,
  onColumnDragStart,
  onColumnDragEnd,
  onColumnDragEnter,
  onColumnDrop,
  align = 'left',
  width,
  onResize,
  onAutoResize,
  onAutoResizeAll,
}: {
  propId?: string;
  label: string;
  sortKey: string;
  activeSortKey: string;
  activeSortDir: SortDir;
  onSort: (key: string, dir: SortDir) => void;
  dragging?: boolean;
  dropTarget?: boolean;
  onColumnDragStart?: (propId: string) => void;
  onColumnDragEnd?: () => void;
  onColumnDragEnter?: (propId: string) => void;
  onColumnDrop?: (sourceId: string, targetId: string) => void;
  align?: 'left' | 'right';
  width?: number;
  onResize?: (width: number) => void;
  onAutoResize?: () => void;
  onAutoResizeAll?: () => void;
}) {
  const active = activeSortKey === sortKey && activeSortDir != null;
  const draggable = propId != null;
  const resizable = width != null && onResize != null;
  const [resizing, setResizing] = useState(false);

  const onClick = () => {
    if (activeSortKey !== sortKey) {
      onSort(sortKey, 'asc');
    } else if (activeSortDir === 'asc') {
      onSort(sortKey, 'desc');
    } else if (activeSortDir === 'desc') {
      onSort('nav.pos', 'asc');
    } else {
      onSort(sortKey, 'asc');
    }
  };

  const startResize = (e: ReactMouseEvent<HTMLButtonElement>) => {
    if (width == null || onResize == null || e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();

    const startX = e.clientX;
    const startWidth = width;
    const originalCursor = document.body.style.cursor;
    const originalUserSelect = document.body.style.userSelect;
    setResizing(true);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    const onMove = (event: MouseEvent) => {
      onResize(startWidth + event.clientX - startX);
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      document.body.style.cursor = originalCursor;
      document.body.style.userSelect = originalUserSelect;
      setResizing(false);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp, { once: true });
  };

  const header = (
    <th
      onDragEnter={(e) => {
        if (!draggable || !propId) return;
        e.preventDefault();
        onColumnDragEnter?.(propId);
      }}
      onDragOver={(e) => {
        if (!draggable) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
      }}
      onDrop={(e) => {
        if (!draggable || !propId) return;
        e.preventDefault();
        const sourceId =
          e.dataTransfer.getData('application/x-any-property-id') ||
          e.dataTransfer.getData('text/plain');
        if (sourceId) onColumnDrop?.(sourceId, propId);
      }}
      style={width == null ? undefined : { width, minWidth: width, maxWidth: width }}
      className={cn(
        'group relative border-y border-foreground/[0.075] bg-background p-0 transition-colors',
        dropTarget && 'bg-accent/[0.06] ring-2 ring-inset ring-accent',
        dragging && 'opacity-45',
        resizing && 'z-20',
      )}
    >
      <div className="flex h-10 items-center">
        {draggable && (
          <button
            type="button"
            draggable
            aria-label={`Drag ${label} column`}
            onClick={(e) => e.stopPropagation()}
            onDragStart={(e) => {
              if (!propId) return;
              e.dataTransfer.effectAllowed = 'move';
              e.dataTransfer.setData('application/x-any-property-id', propId);
              e.dataTransfer.setData('text/plain', propId);
              onColumnDragStart?.(propId);
            }}
            onDragEnd={onColumnDragEnd}
            className={cn(
              'ml-1 inline-flex h-7 w-6 shrink-0 cursor-grab items-center justify-center rounded text-foreground/25',
              'opacity-0 transition-opacity hover:bg-foreground/5 hover:text-foreground/55 active:cursor-grabbing',
              'group-hover:opacity-100 group-focus-within:opacity-100',
              'focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <GripVertical className="h-3.5 w-3.5" aria-hidden />
          </button>
        )}
        <button
          type="button"
          onClick={onClick}
          className={cn(
            'flex h-full min-w-0 flex-1 items-center gap-1 px-3 text-[12px] font-semibold uppercase tracking-[0.06em]',
            draggable && 'pl-1',
            align === 'right' ? 'justify-end' : 'justify-start',
            'text-foreground/48 hover:text-foreground/78',
            'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
          )}
        >
          <span className="truncate">{label}</span>
          {active && activeSortDir === 'asc' && (
            <ChevronUp className="h-3 w-3 shrink-0" aria-hidden />
          )}
          {active && activeSortDir === 'desc' && (
            <ChevronDown className="h-3 w-3 shrink-0" aria-hidden />
          )}
        </button>
      </div>
      {resizable && (
        <button
          type="button"
          aria-label={`Resize ${label} column`}
          title={`Resize ${label} column`}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
          }}
          onMouseDown={startResize}
          onDoubleClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            onAutoResize?.();
          }}
          className={cn(
            'absolute right-0 top-0 z-10 h-full w-2 translate-x-1/2 cursor-col-resize',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            'after:absolute after:right-[3px] after:top-1 after:h-[calc(100%-0.5rem)] after:w-px after:rounded-full',
            'after:bg-foreground/0 after:transition-colors hover:after:bg-accent/80 focus-visible:after:bg-accent/80',
            'group-hover:after:bg-foreground/20',
            resizing && 'after:bg-accent',
          )}
        />
      )}
    </th>
  );

  if (!onAutoResize && !onAutoResizeAll) return header;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>{header}</ContextMenuTrigger>
      <ContextMenuContent alignOffset={-4} className="min-w-[13rem]">
        {onAutoResize && (
          <ContextMenuItem onSelect={onAutoResize}>
            <Columns3 className="h-3.5 w-3.5" aria-hidden />
            Auto-fit column width
          </ContextMenuItem>
        )}
        {onAutoResize && onAutoResizeAll && <ContextMenuSeparator />}
        {onAutoResizeAll && (
          <ContextMenuItem onSelect={onAutoResizeAll}>
            <Columns3 className="h-3.5 w-3.5" aria-hidden />
            Auto-fit all columns
          </ContextMenuItem>
        )}
      </ContextMenuContent>
    </ContextMenu>
  );
}
