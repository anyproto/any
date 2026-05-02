import { useState } from 'react';
import { useAtom } from 'jotai';
import { ChevronRight, ChevronDown, FileText, Folder } from 'lucide-react';
import {
  useObjectChildren,
  type ObjectRecord,
  NAV_FOLDER,
} from '@/lib/api/objects';
import { activeObjectIdAtom } from '@/atoms/selection';
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
 */
export function ObjectRow({ spaceId, obj, depth }: ObjectRowProps) {
  const [activeObjectId, setActiveObjectId] = useAtom(activeObjectIdAtom);
  const [expanded, setExpanded] = useState(false);

  const isFolder = obj.nav?.type === NAV_FOLDER;
  const active = activeObjectId === obj.id;
  const title =
    obj.any?.name?.trim() || `Untitled (${obj.id.slice(0, 6)}…)`;

  return (
    <li>
      <div
        className={cn(
          'group flex items-center gap-1 rounded-md px-1.5 py-1 text-sm text-foreground',
          'hover:bg-foreground/5',
          active && 'bg-foreground/8 font-medium',
        )}
        // Indent by depth via inline style (Tailwind would need an
        // explicit class per level).
        style={{ paddingLeft: `${0.375 + depth * 1}rem` }}
      >
        {/* Chevron — folders only */}
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

        {/* Type icon */}
        {isFolder ? (
          <Folder className="h-3.5 w-3.5 shrink-0 text-foreground/60" aria-hidden />
        ) : (
          <FileText className="h-3.5 w-3.5 shrink-0 text-foreground/60" aria-hidden />
        )}

        {/* Title — clicking selects. */}
        <button
          type="button"
          onClick={() => setActiveObjectId(obj.id)}
          aria-current={active ? 'page' : undefined}
          className={cn(
            'flex-1 truncate text-left',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent rounded',
          )}
        >
          {title}
        </button>
      </div>

      {/* Lazy-loaded children */}
      {isFolder && expanded && (
        <ChildrenList spaceId={spaceId} parentId={obj.id} depth={depth + 1} />
      )}
    </li>
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
    <ul>
      {data.map((child) => (
        <ObjectRow key={child.id} spaceId={spaceId} obj={child} depth={depth} />
      ))}
    </ul>
  );
}
