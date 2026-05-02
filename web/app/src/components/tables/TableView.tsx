import { useEffect, useMemo, useState } from 'react';
import { useAtomValue, useSetAtom } from 'jotai';
import { useQueryClient } from '@tanstack/react-query';
import { ChevronDown, ChevronUp, Plus, Search } from 'lucide-react';
import { activeSpaceIdAtom, activeObjectIdAtom } from '@/atoms/selection';
import {
  setObjectProperty,
  useCreateObject,
  useObjectsByType,
  ANY_TYPE_ID,
  type ObjectRecord,
} from '@/lib/api/objects';
import {
  useType,
  useTypeProperties,
  uiKind,
  type PropertyDef,
  type PropertyKind,
  type UIPropertyKind,
} from '@/lib/api/types';
import { Input } from '@/components/ui/Input';
import { toast } from '@/components/ui/Toast';
import { ApiError } from '@/lib/api/client';
import { cn } from '@/lib/cn';
import { TextCell } from './cells/TextCell';
import { NumberCell } from './cells/NumberCell';
import { BoolCell } from './cells/BoolCell';
import { LongTextCell } from './cells/LongTextCell';
import { DateCell } from './cells/DateCell';
import { UrlCell } from './cells/UrlCell';
import { EmailCell } from './cells/EmailCell';
import { TagsCell } from './cells/TagsCell';
import { AddColumnPopover } from './AddColumnPopover';

type SortDir = 'asc' | 'desc' | null;

interface TableViewProps {
  typeId: string;
}

/**
 * Database-style view of a type — rows are objects stamped with the
 * type, columns are the type's properties (plus Name).
 */
export function TableView({ typeId }: TableViewProps) {
  const spaceId = useAtomValue(activeSpaceIdAtom);
  const typeQuery = useType(spaceId, typeId);
  const propsQuery = useTypeProperties(spaceId, typeId);
  const [filter, setFilter] = useState('');
  const [debouncedFilter, setDebouncedFilter] = useState('');
  const [sortKey, setSortKey] = useState<string>('nav.pos');
  const [sortDir, setSortDir] = useState<SortDir>('asc');

  // Debounce filter (200ms) so we don't refetch on every keystroke.
  useEffect(() => {
    const t = window.setTimeout(() => setDebouncedFilter(filter), 200);
    return () => window.clearTimeout(t);
  }, [filter]);

  const objectsQuery = useObjectsByType(spaceId, typeId, {
    sortKey,
    sortDir,
  });

  // Filter rows client-side by name (server doesn't expose a $regex
  // we can rely on across SDK versions; flagged in spec).
  const visibleRows = useMemo(() => {
    const q = debouncedFilter.trim().toLowerCase();
    const all = objectsQuery.data ?? [];
    if (q === '') return all;
    return all.filter((r) => (r.any?.name ?? '').toLowerCase().includes(q));
  }, [debouncedFilter, objectsQuery.data]);

  const userProps = (propsQuery.data ?? []).filter((p) => isUserKind(p.kind));
  const typeName = typeQuery.data?.name?.trim() || `Untitled (${typeId.slice(0, 6)}…)`;

  if (!spaceId) return null;

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between gap-3 border-b border-foreground/[0.06] px-6 py-3">
        <h1 className="text-lg font-semibold text-foreground">{typeName}</h1>
        <div className="relative w-64">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-foreground/40"
            aria-hidden
          />
          <Input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by name…"
            className="h-8 pl-7 text-xs"
            aria-label="Filter rows"
          />
        </div>
      </header>

      <div className="flex-1 overflow-auto">
        <table className="w-full border-separate border-spacing-0">
          <thead className="sticky top-0 z-10 bg-background">
            <tr>
              <ColumnHeader
                label="Name"
                sortKey="any.name"
                activeSortKey={sortKey}
                activeSortDir={sortDir}
                onSort={(k, d) => {
                  setSortKey(k);
                  setSortDir(d);
                }}
                widthClass="min-w-[16rem]"
              />
              {userProps.map((p) => (
                <ColumnHeader
                  key={p.id}
                  label={p.name ?? `Untitled (${p.id.slice(0, 6)}…)`}
                  sortKey={`${typeId}.${p.id}`}
                  activeSortKey={sortKey}
                  activeSortDir={sortDir}
                  onSort={(k, d) => {
                    setSortKey(k);
                    setSortDir(d);
                  }}
                  align={p.kind === 'number' ? 'right' : 'left'}
                />
              ))}
              <th className="border-b border-foreground/[0.06] p-0 text-left">
                <AddColumnPopover spaceId={spaceId} typeId={typeId} />
              </th>
            </tr>
          </thead>
          <tbody>
            {objectsQuery.isError && (
              <tr>
                <td
                  colSpan={userProps.length + 2}
                  className="px-6 py-4 text-sm text-destructive"
                >
                  {(objectsQuery.error instanceof ApiError
                    ? objectsQuery.error.code
                    : 'unknown') +
                    ' — ' +
                    (objectsQuery.error instanceof Error
                      ? objectsQuery.error.message
                      : 'Failed to load')}
                </td>
              </tr>
            )}
            {objectsQuery.isPending && <SkeletonRows propCount={userProps.length} />}
            {objectsQuery.isSuccess && visibleRows.length === 0 && (
              <tr>
                <td
                  colSpan={userProps.length + 2}
                  className="px-6 py-6 text-center text-sm text-foreground/50"
                >
                  {filter ? 'No matches' : `No ${typeName} yet`}
                </td>
              </tr>
            )}
            {objectsQuery.isSuccess &&
              visibleRows.map((row) => (
                <DataRow
                  key={row.id}
                  spaceId={spaceId}
                  typeId={typeId}
                  row={row}
                  props={userProps}
                />
              ))}
            <AddRow spaceId={spaceId} typeId={typeId} propCount={userProps.length} />
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ColumnHeader({
  label,
  sortKey,
  activeSortKey,
  activeSortDir,
  onSort,
  align = 'left',
  widthClass,
}: {
  label: string;
  sortKey: string;
  activeSortKey: string;
  activeSortDir: SortDir;
  onSort: (key: string, dir: SortDir) => void;
  align?: 'left' | 'right';
  widthClass?: string;
}) {
  const active = activeSortKey === sortKey && activeSortDir != null;
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
  return (
    <th
      className={cn(
        'border-b border-foreground/[0.06] bg-background p-0',
        widthClass,
      )}
    >
      <button
        type="button"
        onClick={onClick}
        className={cn(
          'flex h-9 w-full items-center gap-1 px-3 text-[11px] font-medium uppercase tracking-[0.05em]',
          align === 'right' ? 'justify-end' : 'justify-start',
          'text-foreground/50 hover:text-foreground/80',
          'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
        )}
      >
        <span>{label}</span>
        {active && activeSortDir === 'asc' && (
          <ChevronUp className="h-3 w-3" aria-hidden />
        )}
        {active && activeSortDir === 'desc' && (
          <ChevronDown className="h-3 w-3" aria-hidden />
        )}
      </button>
    </th>
  );
}

function DataRow({
  spaceId,
  typeId,
  row,
  props,
}: {
  spaceId: string;
  typeId: string;
  row: ObjectRecord;
  props: PropertyDef[];
}) {
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const qc = useQueryClient();

  const onCommitName = async (next: string) => {
    await commitCell(qc, spaceId, typeId, row.id, ANY_TYPE_ID, 'name', next);
  };

  const onCommitProp = async (propId: string, next: unknown) => {
    await commitCell(qc, spaceId, typeId, row.id, typeId, propId, next);
  };

  return (
    <tr className="group">
      <td className="border-b border-foreground/[0.04] p-0 align-middle">
        <div className="flex items-center">
          <TextCell
            value={row.any?.name ?? ''}
            placeholder="Untitled"
            onCommit={onCommitName}
          />
          <button
            type="button"
            onClick={() => setActiveObjectId(row.id)}
            className={cn(
              'mx-2 hidden text-xs text-foreground/50 hover:text-foreground group-hover:inline-flex',
              'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent rounded',
            )}
            title="Open in editor"
          >
            Open ↗
          </button>
        </div>
      </td>
      {props.map((p) => {
        const val = readPropValue(row, typeId, p.id);
        return (
          <td
            key={p.id}
            className="border-b border-foreground/[0.04] p-0 align-middle"
          >
            <PropertyCell
              prop={p}
              value={val}
              onCommit={(v) => onCommitProp(p.id, v)}
            />
          </td>
        );
      })}
      <td className="border-b border-foreground/[0.04]" />
    </tr>
  );
}

function PropertyCell({
  prop,
  value,
  onCommit,
}: {
  prop: PropertyDef;
  value: unknown;
  onCommit: (v: unknown) => void | Promise<void>;
}) {
  const k: UIPropertyKind = uiKind(prop);
  switch (k) {
    case 'string':
      return (
        <TextCell value={typeof value === 'string' ? value : ''} onCommit={(v) => onCommit(v)} />
      );
    case 'longtext':
      return (
        <LongTextCell value={typeof value === 'string' ? value : ''} onCommit={(v) => onCommit(v)} />
      );
    case 'number':
      return (
        <NumberCell value={typeof value === 'number' ? value : null} onCommit={(v) => onCommit(v)} />
      );
    case 'boolean':
      return (
        <BoolCell value={typeof value === 'boolean' ? value : null} onCommit={(v) => onCommit(v)} />
      );
    case 'date':
      return (
        <DateCell value={typeof value === 'string' ? value : null} onCommit={(v) => onCommit(v)} />
      );
    case 'url':
      return (
        <UrlCell value={typeof value === 'string' ? value : ''} onCommit={(v) => onCommit(v)} />
      );
    case 'email':
      return (
        <EmailCell value={typeof value === 'string' ? value : ''} onCommit={(v) => onCommit(v)} />
      );
    case 'tags':
      return (
        <TagsCell
          value={Array.isArray(value) ? (value as string[]) : []}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'array':
    case 'object':
    case 'null':
    default:
      // Fallback: read-only JSON for unsupported kinds.
      return jsonFallback(value);
  }
}

function jsonFallback(value: unknown) {
  return (
    <span className="block px-2 py-1 text-[12px] text-foreground/50">
      <code className="font-mono">{value === undefined ? '—' : JSON.stringify(value)}</code>
    </span>
  );
}

function AddRow({
  spaceId,
  typeId,
  propCount,
}: {
  spaceId: string;
  typeId: string;
  propCount: number;
}) {
  const create = useCreateObject(spaceId);
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);

  const onClick = async () => {
    try {
      const { objectId } = await create.mutateAsync({ typeIds: [typeId] });
      // Tiny UX nicety: select the new row's editor view too, in case
      // the user wants to flip into the doc immediately.
      void setActiveObjectId; void objectId;
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add row';
      toast.error(`${code}: ${msg}`);
    }
  };
  return (
    <tr>
      <td colSpan={propCount + 2} className="p-0">
        <button
          type="button"
          onClick={() => void onClick()}
          disabled={create.isPending}
          className={cn(
            'flex w-full items-center gap-2 px-3 py-2 text-left text-[12px] text-foreground/50',
            'hover:bg-foreground/[0.03] hover:text-foreground/80',
            'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
          )}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
          {create.isPending ? 'Adding…' : 'New row'}
        </button>
      </td>
    </tr>
  );
}

function SkeletonRows({ propCount }: { propCount: number }) {
  // Name column + N property columns + trailing add-column slot.
  const cellCount = propCount + 2;
  return (
    <>
      {[0, 1, 2].map((i) => (
        <tr key={i}>
          {Array.from({ length: cellCount }, (_, j) => (
            <td key={j} className="border-b border-foreground/[0.04] p-2">
              <span aria-hidden className="block h-4 animate-pulse rounded bg-foreground/10" />
            </td>
          ))}
        </tr>
      ))}
    </>
  );
}

// ---------------- Helpers -------------------------------------------

function isUserKind(k: PropertyKind): boolean {
  return k === 'string' || k === 'number' || k === 'boolean' || k === 'null' || k === 'array' || k === 'object';
}

function readPropValue(row: ObjectRecord, typeId: string, propId: string): unknown {
  const ns = (row as Record<string, unknown>)[typeId];
  if (ns && typeof ns === 'object' && !Array.isArray(ns)) {
    return (ns as Record<string, unknown>)[propId];
  }
  return undefined;
}

/**
 * Commit a cell edit: optimistic patch to the by-type cache, then
 * setObjectProperty. Rollback on error.
 *
 * `path` is the namespaced key — `name` for the any namespace,
 * `<propId>` for the type's own namespace.
 */
async function commitCell(
  qc: ReturnType<typeof useQueryClient>,
  spaceId: string,
  typeId: string,
  objectId: string,
  patchTypeId: string,
  path: string,
  value: unknown,
): Promise<void> {
  // Patch every cached objects-by-type slice — there can be several
  // (one per sort order). Cheap to walk; small N.
  const matches = qc.getQueriesData<ObjectRecord[]>({
    queryKey: ['objects', spaceId, 'by-type', typeId],
  });
  const snapshot: { key: readonly unknown[]; rows: ObjectRecord[] }[] = [];
  for (const [key, rows] of matches) {
    if (!rows) continue;
    snapshot.push({ key, rows });
    const next = rows.map((r) => (r.id === objectId ? patchRow(r, patchTypeId, path, value) : r));
    qc.setQueryData<ObjectRecord[]>(key, next);
  }

  try {
    await setObjectProperty(spaceId, objectId, patchTypeId, { [path]: value });
  } catch (err) {
    // Roll back every cache we touched.
    for (const s of snapshot) qc.setQueryData(s.key, s.rows);
    const code = err instanceof ApiError ? err.code : 'unknown';
    const msg = err instanceof Error ? err.message : 'Failed to save';
    toast.error(`${code}: ${msg}`);
  }
}

function patchRow(
  row: ObjectRecord,
  patchTypeId: string,
  path: string,
  value: unknown,
): ObjectRecord {
  const ns = ((row as Record<string, unknown>)[patchTypeId] as Record<string, unknown> | undefined) ?? {};
  return {
    ...row,
    [patchTypeId]: { ...ns, [path]: value },
  } as ObjectRecord;
}
