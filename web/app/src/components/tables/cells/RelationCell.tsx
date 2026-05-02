import { useEffect, useMemo, useRef, useState } from 'react';
import { useAtomValue, useSetAtom } from 'jotai';
import { ArrowUpRight, Search, X } from 'lucide-react';
import { activeSpaceIdAtom, activeViewAtom } from '@/atoms/selection';
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from '@/components/ui/Popover';
import { Input } from '@/components/ui/Input';
import { useObjectsBySpace, type ObjectRecord } from '@/lib/api/objects';
import { cn } from '@/lib/cn';

interface RelationCellProps {
  /** The current value — an object id, or empty/undefined. */
  value: string;
  /** The id of the row's own object — filtered out of the picker so
   *  you can't link an object to itself. */
  rowId: string;
  onCommit: (next: string) => void | Promise<void>;
}

/**
 * Single-target relation cell.
 *
 * Display: clickable chip with the linked object's name + a tiny
 * arrow icon — clicking the arrow navigates pane 3 to that object.
 * Clicking the cell body opens the picker.
 *
 * Edit:   popover with a search input and a list of candidate
 * objects in the same space. Filter is plain name.includes().
 */
export function RelationCell({ value, rowId, onCommit }: RelationCellProps) {
  const [open, setOpen] = useState(false);
  const spaceId = useAtomValue(activeSpaceIdAtom);
  const setActiveView = useSetAtom(activeViewAtom);
  const objs = useObjectsBySpace(spaceId);

  const linked = useMemo(() => {
    if (!value) return null;
    return objs.data?.find((o) => o.id === value) ?? null;
  }, [objs.data, value]);

  // The cell trigger is the whole button — Popover anchors to it.
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div className="relative h-full w-full">
          <button
            type="button"
            onClick={() => setOpen(true)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === 'F2') {
                e.preventDefault();
                setOpen(true);
              }
            }}
            className={cn(
              'flex h-full w-full items-center gap-1 truncate px-2 py-1 text-left',
              'hover:bg-foreground/[0.03]',
              'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
            )}
          >
            {!value ? (
              <span className="text-[13px] text-foreground/40">—</span>
            ) : objs.isLoading ? (
              <span className="text-[13px] text-foreground/40">Loading…</span>
            ) : linked ? (
              <RelationChip
                name={nameOf(linked) ?? 'Untitled'}
                onOpen={(e) => {
                  e.stopPropagation();
                  setActiveView({ kind: 'object', objectId: linked.id });
                }}
              />
            ) : (
              <span className="inline-flex h-5 items-center rounded-full bg-destructive/10 px-2 text-[11px] font-medium text-destructive/80">
                Missing object
              </span>
            )}
          </button>
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={2}
        className="w-72 p-0"
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <RelationPicker
          objects={objs.data ?? []}
          excludeId={rowId}
          currentValue={value}
          onPick={async (id) => {
            setOpen(false);
            if (id !== value) await onCommit(id);
          }}
          onClear={async () => {
            setOpen(false);
            if (value) await onCommit('');
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

function RelationChip({
  name,
  onOpen,
}: {
  name: string;
  onOpen: (e: React.MouseEvent) => void;
}) {
  return (
    <span className="inline-flex items-center gap-1">
      <span className="inline-flex h-5 items-center rounded-full bg-accent/15 px-2 text-[11px] font-medium text-foreground/80">
        {name}
      </span>
      <button
        type="button"
        aria-label={`Open ${name}`}
        onClick={onOpen}
        className={cn(
          'inline-flex h-5 w-5 items-center justify-center rounded text-foreground/40',
          'hover:bg-foreground/5 hover:text-foreground',
          'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent',
        )}
      >
        <ArrowUpRight className="h-3 w-3" aria-hidden />
      </button>
    </span>
  );
}

function RelationPicker({
  objects,
  excludeId,
  currentValue,
  onPick,
  onClear,
}: {
  objects: readonly ObjectRecord[];
  excludeId: string;
  currentValue: string;
  onPick: (id: string) => void | Promise<void>;
  onClear: () => void | Promise<void>;
}) {
  const [query, setQuery] = useState('');
  const [highlight, setHighlight] = useState(0);
  const ref = useRef<HTMLInputElement>(null);

  useEffect(() => {
    ref.current?.focus();
  }, []);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = objects
      .filter((o) => o.id !== excludeId)
      .filter((o) => {
        const n = nameOf(o);
        if (!q) return true;
        return (n ?? '').toLowerCase().includes(q);
      })
      .slice(0, 50);
    return list;
  }, [objects, excludeId, query]);

  // Keep highlight in range when filtering shrinks the list.
  useEffect(() => {
    if (highlight >= matches.length) setHighlight(0);
  }, [matches.length, highlight]);

  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-2 border-b border-foreground/10 px-2 py-1.5">
        <Search className="h-3.5 w-3.5 text-foreground/40" aria-hidden />
        <Input
          ref={ref}
          type="text"
          autoComplete="off"
          placeholder="Search objects…"
          className="h-7 border-0 bg-transparent px-0 text-[13px] focus-visible:ring-0 focus-visible:ring-offset-0"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') {
              e.preventDefault();
              setHighlight((h) => Math.min(h + 1, Math.max(0, matches.length - 1)));
            } else if (e.key === 'ArrowUp') {
              e.preventDefault();
              setHighlight((h) => Math.max(0, h - 1));
            } else if (e.key === 'Enter') {
              e.preventDefault();
              const pick = matches[highlight];
              if (pick) void onPick(pick.id);
            }
          }}
        />
        {currentValue && (
          <button
            type="button"
            onClick={() => void onClear()}
            aria-label="Clear relation"
            title="Clear relation"
            className={cn(
              'inline-flex h-6 w-6 items-center justify-center rounded text-foreground/40',
              'hover:bg-foreground/5 hover:text-foreground',
              'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent',
            )}
          >
            <X className="h-3.5 w-3.5" aria-hidden />
          </button>
        )}
      </div>
      <ul role="listbox" className="max-h-72 overflow-y-auto py-1">
        {matches.length === 0 && (
          <li className="px-3 py-2 text-[13px] text-foreground/40">
            No matches.
          </li>
        )}
        {matches.map((o, i) => {
          const n = nameOf(o) ?? 'Untitled';
          const selected = i === highlight;
          const current = o.id === currentValue;
          return (
            <li key={o.id} role="option" aria-selected={selected}>
              <button
                type="button"
                onMouseEnter={() => setHighlight(i)}
                onClick={() => void onPick(o.id)}
                className={cn(
                  'flex w-full items-center justify-between gap-2 truncate px-3 py-1.5 text-left text-[13px]',
                  selected && 'bg-foreground/[0.04]',
                )}
              >
                <span className="truncate">{n}</span>
                {current && (
                  <span className="text-[10px] uppercase tracking-wide text-foreground/40">
                    selected
                  </span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function nameOf(o: ObjectRecord): string | undefined {
  const v = o.any?.name;
  return typeof v === 'string' && v.length > 0 ? v : undefined;
}
