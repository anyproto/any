import { useEffect, useMemo, useRef, useState } from 'react';
import { useSetAtom } from 'jotai';
import { ArrowRight, Plus, Search, Sparkles } from 'lucide-react';
import { activeTypeIdAtom } from '@/atoms/selection';
import {
  useObject,
  useSetObjectProperty,
  type ObjectRecord,
} from '@/lib/api/objects';
import {
  useType,
  useTypeProperties,
  useTypes,
  uiKind,
  type PropertyDef,
  type TypeInfo,
  type UIPropertyKind,
} from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';
import { toast } from '@/components/ui/Toast';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/Popover';
import { Input } from '@/components/ui/Input';
import { CreateTypeDialog } from '@/components/types/CreateTypeDialog';
import { AddColumnPopover } from '@/components/tables/AddColumnPopover';
import { TextCell } from '@/components/tables/cells/TextCell';
import { NumberCell } from '@/components/tables/cells/NumberCell';
import { BoolCell } from '@/components/tables/cells/BoolCell';
import { LongTextCell } from '@/components/tables/cells/LongTextCell';
import { DateCell } from '@/components/tables/cells/DateCell';
import { UrlCell } from '@/components/tables/cells/UrlCell';
import { EmailCell } from '@/components/tables/cells/EmailCell';
import { TagsCell } from '@/components/tables/cells/TagsCell';
import { RelationCell } from '@/components/tables/cells/RelationCell';
import { cn } from '@/lib/cn';

interface Props {
  spaceId: string;
  objectId: string;
}

/**
 * Below the page title — one chip per type the object is stamped
 * with. Click a chip to expand its property values inline. See
 * docs/specs/PR-020-object-type-bar.md for v1 scope.
 */
export function ObjectTypeBar({ spaceId, objectId }: Props) {
  const objQuery = useObject(spaceId, objectId);
  const [expanded, setExpanded] = useState<string | null>(null);

  const types = useMemo(() => {
    const arr = objQuery.data?.any?.types ?? [];
    // Drop the implicit `nav` namespace if it ever leaks into
    // any.types — it's not a user-facing list.
    return arr.filter((t) => t && t !== 'nav');
  }, [objQuery.data]);

  return (
    <div className="mb-4 mt-1">
      <div className="flex flex-wrap items-center gap-1.5">
        {types.map((typeId) => (
          <TypeChip
            key={typeId}
            spaceId={spaceId}
            typeId={typeId}
            active={expanded === typeId}
            onToggle={() =>
              setExpanded((cur) => (cur === typeId ? null : typeId))
            }
          />
        ))}
        <AddTypeChip
          spaceId={spaceId}
          objectId={objectId}
          existingTypes={types}
        />
      </div>

      {expanded && objQuery.data && (
        <TypePropertiesPanel
          spaceId={spaceId}
          typeId={expanded}
          row={objQuery.data}
        />
      )}
    </div>
  );
}

function TypeChip({
  spaceId,
  typeId,
  active,
  onToggle,
}: {
  spaceId: string;
  typeId: string;
  active: boolean;
  onToggle: () => void;
}) {
  const typeQuery = useType(spaceId, typeId);
  const setActiveTypeId = useSetAtom(activeTypeIdAtom);
  const label = typeQuery.data?.name?.trim() || `List ${typeId.slice(0, 6)}…`;

  // Two buttons share the rounded chip frame: the main toggle (most
  // of the surface) and a hover-revealed arrow that navigates to
  // the list's table view in pane 3.
  return (
    <div
      className={cn(
        'group inline-flex items-center rounded-full border text-[13px]',
        'transition-colors',
        active
          ? 'border-foreground/15 bg-foreground/[0.06] text-foreground'
          : 'border-foreground/10 bg-transparent text-foreground/70 hover:bg-foreground/[0.04]',
      )}
    >
      <button
        type="button"
        onClick={onToggle}
        aria-pressed={active}
        className={cn(
          'inline-flex items-center gap-1.5 rounded-l-full px-2.5 py-1',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          // Round the right side too when the arrow is hidden so the
          // focus ring doesn't sit on a square corner.
          'group-hover:rounded-r-none focus-visible:rounded-r-none',
        )}
      >
        <Sparkles className="h-3.5 w-3.5 text-foreground/50" aria-hidden />
        <span className="truncate">{label}</span>
      </button>
      <button
        type="button"
        aria-label={`Open ${label}`}
        title={`Open ${label}`}
        onClick={(e) => {
          e.stopPropagation();
          setActiveTypeId(typeId);
        }}
        className={cn(
          // Animate width + opacity so the arrow slides in smoothly
          // on hover/focus rather than just appearing.
          'flex h-7 items-center overflow-hidden rounded-r-full',
          'pr-1.5 text-foreground/45 hover:text-foreground/80',
          'opacity-0 [width:0]',
          'transition-[width,opacity,padding] duration-150 ease-out',
          'group-hover:opacity-100 group-hover:[width:1.5rem]',
          'focus-visible:opacity-100 focus-visible:[width:1.5rem]',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <ArrowRight className="h-3.5 w-3.5" aria-hidden />
      </button>
    </div>
  );
}

function TypePropertiesPanel({
  spaceId,
  typeId,
  row,
}: {
  spaceId: string;
  typeId: string;
  row: ObjectRecord;
}) {
  const propsQuery = useTypeProperties(spaceId, typeId);
  const setProp = useSetObjectProperty(spaceId);

  // Skip `name` (it's not a type prop anyway since it lives in
  // `any`, but be defensive in case a type ever defines one).
  const visibleProps = (propsQuery.data ?? []).filter(
    (p) => p.id !== 'name' && (p.name ?? '').trim() !== '',
  );

  const onCommit = async (propId: string, value: unknown) => {
    try {
      await setProp.mutateAsync({
        objectId: row.id,
        typeId,
        patch: { [propId]: value },
      });
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to save';
      toast.error(`${code}: ${msg}`);
    }
  };

  if (propsQuery.isPending) {
    return (
      <div className="mt-3 px-1 text-[13px] text-foreground/40">Loading…</div>
    );
  }

  return (
    // Group container so the "+ Add Property" row only appears on
    // hover of this zone, then a thin divider below to set it apart
    // from the BlockNote body.
    <div className="group/typepanel mt-3">
      {visibleProps.length === 0 ? (
        <div className="px-1 text-[13px] text-foreground/40">
          This list has no properties yet.
        </div>
      ) : (
        <dl className="grid grid-cols-[140px_1fr] items-center gap-x-3 gap-y-0.5">
          {visibleProps.map((p) => {
            const propLabel = p.name?.trim() || `Property ${p.id.slice(0, 6)}…`;
            const value = readPropValue(row, typeId, p.id);
            return (
              <div className="contents" key={p.id}>
                <dt className="truncate text-[13px] text-foreground/55">
                  {propLabel}
                </dt>
                <dd className="min-w-0">
                  <PropertyValue
                    prop={p}
                    rowId={row.id}
                    value={value}
                    onCommit={(v) => onCommit(p.id, v)}
                  />
                </dd>
              </div>
            );
          })}
        </dl>
      )}

      {/* Hover-revealed "+ Add Property" row. Reuses AddColumnPopover
          (the same surface used by the table view's column +). */}
      <div
        className={cn(
          'mt-1 transition-opacity duration-150',
          'opacity-0 group-hover/typepanel:opacity-100 focus-within:opacity-100',
        )}
      >
        <AddColumnPopover spaceId={spaceId} typeId={typeId}>
          <button
            type="button"
            className={cn(
              'inline-flex items-center gap-1.5 rounded px-1.5 py-1 text-[13px] text-foreground/55',
              'hover:bg-foreground/[0.04] hover:text-foreground/80',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
            Add Property
          </button>
        </AddColumnPopover>
      </div>

      {/* Thin divider between the property zone and the editor blocks
          below — matches the editor's hr token. */}
      <hr className="mt-3 border-0 border-t border-foreground/[0.08]" />
    </div>
  );
}

function PropertyValue({
  prop,
  rowId,
  value,
  onCommit,
}: {
  prop: PropertyDef;
  rowId: string;
  value: unknown;
  onCommit: (v: unknown) => void | Promise<void>;
}) {
  const k: UIPropertyKind = uiKind(prop);
  switch (k) {
    case 'string':
      return (
        <TextCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'longtext':
      return (
        <LongTextCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'number':
      return (
        <NumberCell
          value={typeof value === 'number' ? value : null}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'boolean':
      return (
        <BoolCell
          value={typeof value === 'boolean' ? value : null}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'date':
      return (
        <DateCell
          value={typeof value === 'string' ? value : null}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'url':
      return (
        <UrlCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'email':
      return (
        <EmailCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'tags':
      return (
        <TagsCell
          value={Array.isArray(value) ? (value as string[]) : []}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'relation':
      return (
        <RelationCell
          value={typeof value === 'string' ? value : ''}
          rowId={rowId}
          onCommit={(v) => onCommit(v)}
        />
      );
    default:
      return (
        <span className="text-[12px] text-foreground/40">
          <code className="font-mono">
            {value === undefined ? '—' : JSON.stringify(value)}
          </code>
        </span>
      );
  }
}

function readPropValue(row: ObjectRecord, typeId: string, propId: string): unknown {
  const ns = (row as Record<string, unknown>)[typeId];
  if (ns && typeof ns === 'object' && !Array.isArray(ns)) {
    return (ns as Record<string, unknown>)[propId];
  }
  return undefined;
}

/**
 * '+' chip at the end of the bar. Opens a popover with a search +
 * the list of types in the space that aren't already attached to
 * this object, plus a "Create new list…" entry that opens the
 * existing CreateTypeDialog. After the dialog closes successfully
 * the type's id won't be attached automatically — the user picks it
 * from the now-grown list. Keeps the dialog single-purpose.
 */
function AddTypeChip({
  spaceId,
  objectId,
  existingTypes,
}: {
  spaceId: string;
  objectId: string;
  existingTypes: string[];
}) {
  const [open, setOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [query, setQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const typesQuery = useTypes(spaceId);
  const setProp = useSetObjectProperty(spaceId);

  // Reset filter every time the popover opens.
  useEffect(() => {
    if (open) {
      setQuery('');
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  }, [open]);

  const candidates = useMemo(() => {
    const all = (typesQuery.data ?? []).filter((t) => !t.builtIn);
    const taken = new Set(existingTypes);
    const q = query.trim().toLowerCase();
    return all
      .filter((t) => !taken.has(t.id))
      .filter((t) => {
        if (!q) return true;
        return (t.name ?? '').toLowerCase().includes(q);
      });
  }, [typesQuery.data, existingTypes, query]);

  const attach = async (typeId: string) => {
    const merged = Array.from(new Set([...existingTypes, typeId]));
    if (merged.length === existingTypes.length) {
      setOpen(false);
      return;
    }
    try {
      await setProp.mutateAsync({
        objectId,
        typeId: 'any',
        patch: { types: merged },
      });
      setOpen(false);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to attach list';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label="Add list"
            title="Add list"
            className={cn(
              'inline-flex h-7 w-7 items-center justify-center rounded-full border',
              'border-foreground/10 bg-transparent text-foreground/55',
              'hover:bg-foreground/[0.04] hover:text-foreground/80',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" sideOffset={4} className="w-72 p-0">
          <div className="flex items-center gap-2 border-b border-foreground/10 px-2 py-1.5">
            <Search className="h-3.5 w-3.5 text-foreground/40" aria-hidden />
            <Input
              ref={inputRef}
              type="text"
              autoComplete="off"
              placeholder="Search lists…"
              className="h-7 border-0 bg-transparent px-0 text-[13px] focus-visible:ring-0 focus-visible:ring-offset-0"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <ul className="max-h-64 overflow-y-auto py-1" role="listbox">
            {candidates.length === 0 ? (
              <li className="px-3 py-2 text-[13px] text-foreground/40">
                {query ? 'No matching lists.' : 'All lists are already attached.'}
              </li>
            ) : (
              candidates.map((t) => <CandidateRow key={t.id} type={t} onPick={attach} />)
            )}
          </ul>
          <div className="border-t border-foreground/10 py-1">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                setCreateOpen(true);
              }}
              className={cn(
                'flex w-full items-center gap-2 px-3 py-1.5 text-left text-[13px]',
                'text-foreground/80 hover:bg-foreground/[0.04]',
                'focus-visible:outline-none focus-visible:bg-foreground/[0.04]',
              )}
            >
              <Plus className="h-3.5 w-3.5 text-foreground/50" aria-hidden />
              Create new list…
            </button>
          </div>
        </PopoverContent>
      </Popover>
      <CreateTypeDialog open={createOpen} onOpenChange={setCreateOpen} />
    </>
  );
}

function CandidateRow({
  type,
  onPick,
}: {
  type: TypeInfo;
  onPick: (id: string) => void | Promise<void>;
}) {
  const label = type.name?.trim() || `List ${type.id.slice(0, 6)}…`;
  return (
    <li role="option" aria-selected={false}>
      <button
        type="button"
        onClick={() => void onPick(type.id)}
        className={cn(
          'flex w-full items-center gap-2 truncate px-3 py-1.5 text-left text-[13px]',
          'text-foreground/85 hover:bg-foreground/[0.04]',
          'focus-visible:outline-none focus-visible:bg-foreground/[0.04]',
        )}
      >
        <Sparkles className="h-3.5 w-3.5 shrink-0 text-foreground/50" aria-hidden />
        <span className="truncate">{label}</span>
      </button>
    </li>
  );
}
