import { useSetAtom } from 'jotai';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { ArrowUpRight, Plus } from 'lucide-react';
import { activeObjectIdAtom } from '@/atoms';
import { ApiError } from '@/lib/api/client';
import {
  ANY_TYPE_ID,
  objectKeys,
  setObjectProperty,
  type ObjectRecord,
} from '@/lib/api/objects';
import type { PropertyDef } from '@/lib/api/types';
import { toast } from '@/components/ui';
import { PropertyCell, TextCell } from '@/components/properties';
import { cn } from '@/lib/cn';
import { preloadViewModule } from '@/app/viewModules';
import {
  patchTableRow,
  readTablePropValue,
} from './tableObjectValues';

export const TABLE_ROW_HEIGHT = 42;

export function DataRow({
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
    <tr
      className="group bg-background transition-colors hover:bg-foreground/[0.022]"
      style={{ height: TABLE_ROW_HEIGHT }}
    >
      <td className="border-b border-foreground/[0.045] p-0 align-middle">
        <div className="flex items-center">
          <TextCell
            value={row.any?.name ?? ''}
            placeholder="Untitled"
            onCommit={onCommitName}
          />
          <button
            type="button"
            aria-label={`Open ${row.any?.name?.trim() || 'row'}`}
            onFocus={() => preloadViewModule('object')}
            onPointerEnter={() => preloadViewModule('object')}
            onClick={() => setActiveObjectId(row.id)}
            className={cn(
              'mx-2 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-foreground/35 opacity-0 transition-opacity',
              'hover:bg-foreground/[0.055] hover:text-foreground/80 group-hover:opacity-100 focus-visible:opacity-100',
              'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent',
            )}
            title="Open in editor"
          >
            <ArrowUpRight className="h-3.5 w-3.5" aria-hidden />
          </button>
        </div>
      </td>
      {props.map((p) => {
        const val = readTablePropValue(row, typeId, p.id);
        return (
          <td key={p.id} className="border-b border-foreground/[0.045] p-0 align-middle">
            <PropertyCell
              prop={p}
              value={val}
              rowId={row.id}
              onCommit={(v) => onCommitProp(p.id, v)}
            />
          </td>
        );
      })}
      <td className="border-b border-foreground/[0.045]" />
    </tr>
  );
}

export function SpacerRow({ height, colSpan }: { height: number; colSpan: number }) {
  return (
    <tr aria-hidden>
      <td colSpan={colSpan} style={{ height, padding: 0, border: 0 }} />
    </tr>
  );
}

export function LoadMoreRow({
  colSpan,
  loading,
  onLoad,
}: {
  colSpan: number;
  loading: boolean;
  onLoad: () => void;
}) {
  return (
    <tr>
      <td colSpan={colSpan} className="border-b border-foreground/[0.045] p-0">
        <button
          type="button"
          onClick={onLoad}
          disabled={loading}
          className={cn(
            'flex w-full items-center justify-center px-3 py-3 text-[12px] text-foreground/45',
            'hover:bg-foreground/[0.025] hover:text-foreground/75',
            'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
            'disabled:cursor-wait disabled:opacity-70',
          )}
        >
          {loading ? 'Loading…' : 'Load more'}
        </button>
      </td>
    </tr>
  );
}

export function AddRow({
  creating,
  typeName,
  propCount,
  onCreate,
}: {
  creating: boolean;
  typeName: string;
  propCount: number;
  onCreate: () => void;
}) {
  return (
    <tr>
      <td colSpan={propCount + 2} className="p-0">
        <button
          type="button"
          onClick={onCreate}
          disabled={creating}
          className={cn(
            'flex h-11 w-full items-center gap-2 border-b border-foreground/[0.035] px-3 text-left text-[14px] text-foreground/45',
            'hover:bg-foreground/[0.022] hover:text-foreground/75',
            'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
          )}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
          {creating ? 'Adding...' : `New ${typeName}`}
        </button>
      </td>
    </tr>
  );
}

/**
 * Commit a cell edit: optimistic patch to the by-type cache, then
 * setObjectProperty. Rollback on error.
 *
 * `path` is the namespaced key — `name` for the any namespace,
 * `<propId>` for the type's own namespace.
 */
async function commitCell(
  qc: QueryClient,
  spaceId: string,
  typeId: string,
  objectId: string,
  patchTypeId: string,
  path: string,
  value: unknown,
): Promise<void> {
  const matches = qc.getQueriesData<ObjectRecord[]>({
    queryKey: objectKeys.byTypeRoot(spaceId, typeId),
  });
  const pageMatches = qc.getQueriesData<{ pages: { records: ObjectRecord[] }[] }>({
    queryKey: objectKeys.byTypePagesRoot(spaceId, typeId),
  });
  const snapshot: { key: readonly unknown[]; rows: ObjectRecord[] }[] = [];
  for (const [key, rows] of matches) {
    if (!rows) continue;
    snapshot.push({ key, rows });
    const next = rows.map((r) =>
      r.id === objectId ? patchTableRow(r, patchTypeId, path, value) : r,
    );
    qc.setQueryData<ObjectRecord[]>(key, next);
  }
  const pageSnapshot: {
    key: readonly unknown[];
    pages: { pages: { records: ObjectRecord[] }[] };
  }[] = [];
  for (const [key, data] of pageMatches) {
    if (!data) continue;
    pageSnapshot.push({ key, pages: data });
    qc.setQueryData(key, {
      ...data,
      pages: data.pages.map((page) => ({
        ...page,
        records: page.records.map((r) =>
          r.id === objectId ? patchTableRow(r, patchTypeId, path, value) : r,
        ),
      })),
    });
  }

  try {
    await setObjectProperty(spaceId, objectId, patchTypeId, { [path]: value });
  } catch (err) {
    for (const s of snapshot) qc.setQueryData(s.key, s.rows);
    for (const s of pageSnapshot) qc.setQueryData(s.key, s.pages);
    const code = err instanceof ApiError ? err.code : 'unknown';
    const msg = err instanceof Error ? err.message : 'Failed to save';
    toast.error(`${code}: ${msg}`);
  }
}
