import { useSetAtom } from 'jotai';
import { ArrowUpRight, FileText, Plus } from 'lucide-react';
import { activeObjectIdAtom } from '@/atoms';
import { ApiError } from '@/lib/api/client';
import type { ObjectRecord } from '@/lib/api/objects';
import type { PropertyDef } from '@/lib/api/types';
import { cn } from '@/lib/cn';
import { preloadViewModule } from '@/app/viewModules';
import {
  formatListPropertyValue,
  readTablePropValue,
} from './tableObjectValues';

export const LIST_ROW_HEIGHT = 74;

export function ListRowsView({
  typeId,
  rows,
  props,
  visibleCount,
  paddingTop,
  paddingBottom,
  isSuccess,
  error,
  filter,
  typeName,
  creating,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  onCreate,
}: {
  typeId: string;
  rows: ObjectRecord[];
  props: PropertyDef[];
  visibleCount: number;
  paddingTop: number;
  paddingBottom: number;
  isSuccess: boolean;
  error: unknown;
  filter: string;
  typeName: string;
  creating: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadMore: () => void;
  onCreate: () => void;
}) {
  return (
    <div className="mx-auto w-full max-w-[1180px]">
      <div className="overflow-hidden border-y border-foreground/[0.075]">
        {error != null && (
          <div className="px-4 py-4 text-sm text-destructive">
            {(error instanceof ApiError ? error.code : 'unknown') +
              ' — ' +
              (error instanceof Error ? error.message : 'Failed to load')}
          </div>
        )}
        {isSuccess && visibleCount === 0 && (
          <div className="border-b border-foreground/[0.045] px-4 py-10 text-left">
            <div className="max-w-sm rounded-lg bg-foreground/[0.025] px-4 py-3">
              <p className="text-sm font-medium text-foreground/70">
                {filter ? 'No matches' : `No ${typeName} yet`}
              </p>
              <p className="mt-1 text-xs text-foreground/45">
                {filter
                  ? 'Try a different filter or clear it from the toolbar.'
                  : 'Create the first item and it will appear in this list.'}
              </p>
            </div>
          </div>
        )}
        {isSuccess && paddingTop > 0 && <div aria-hidden style={{ height: paddingTop }} />}
        {isSuccess &&
          rows.map((row) => (
            <ListDataRow
              key={row.id}
              typeId={typeId}
              row={row}
              props={props}
            />
          ))}
        {isSuccess && paddingBottom > 0 && (
          <div aria-hidden style={{ height: paddingBottom }} />
        )}
        {hasNextPage && (
          <button
            type="button"
            onClick={onLoadMore}
            disabled={isFetchingNextPage}
            className={cn(
              'flex h-10 w-full items-center justify-center border-b border-foreground/[0.045] text-[12px] text-foreground/45',
              'hover:bg-foreground/[0.025] hover:text-foreground/75',
              'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
              'disabled:cursor-wait disabled:opacity-70',
            )}
          >
            {isFetchingNextPage ? 'Loading…' : 'Load more'}
          </button>
        )}
        <button
          type="button"
          onClick={onCreate}
          disabled={creating}
          className={cn(
            'flex h-[54px] w-full items-center gap-2 px-4 text-left text-[15px] text-foreground/45',
            'hover:bg-foreground/[0.025] hover:text-foreground/75',
            'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
            'disabled:cursor-wait disabled:opacity-70',
          )}
        >
          <Plus className="h-4 w-4" aria-hidden />
          {creating ? 'Adding...' : `New ${typeName}`}
        </button>
      </div>
    </div>
  );
}

function ListDataRow({
  typeId,
  row,
  props,
}: {
  typeId: string;
  row: ObjectRecord;
  props: PropertyDef[];
}) {
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const name = row.any?.name?.trim() || 'Untitled';
  const previews = props
    .map((prop) => ({
      prop,
      value: formatListPropertyValue(
        prop,
        readTablePropValue(row, typeId, prop.id),
      ),
    }))
    .filter((item): item is { prop: PropertyDef; value: string } => item.value != null)
    .slice(0, 4);

  return (
    <button
      type="button"
      aria-label={`Open ${name}`}
      onFocus={() => preloadViewModule('object')}
      onPointerEnter={() => preloadViewModule('object')}
      onClick={() => setActiveObjectId(row.id)}
      className={cn(
        'group/list-row flex h-[74px] w-full items-center gap-3 border-b border-foreground/[0.045] px-4 text-left',
        'bg-background transition-colors hover:bg-foreground/[0.025]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset',
      )}
    >
      <span className="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px] bg-foreground/[0.055] text-foreground/40">
        <FileText className="h-4 w-4" aria-hidden />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block max-w-full truncate px-1 text-[15px] font-medium text-foreground">
          {name}
        </span>
        <span className="mt-1 flex min-w-0 items-center gap-1.5 overflow-hidden">
          {previews.length > 0 ? (
            previews.map(({ prop, value }) => (
              <ListPropertyPill
                key={prop.id}
                label={prop.name ?? `Untitled (${prop.id.slice(0, 6)}...)`}
                value={value}
              />
            ))
          ) : (
            <span className="truncate text-xs text-foreground/35">
              {props.length === 0 ? 'No properties' : 'No filled properties'}
            </span>
          )}
        </span>
      </span>
      <span
        aria-hidden
        className={cn(
          'inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-[9px] text-foreground/35 opacity-0 transition-opacity',
          'group-hover/list-row:opacity-100 group-focus-visible/list-row:opacity-100',
        )}
      >
        <ArrowUpRight className="h-4 w-4" aria-hidden />
      </span>
    </button>
  );
}

function ListPropertyPill({ label, value }: { label: string; value: string }) {
  return (
    <span className="inline-flex max-w-[13rem] shrink items-center gap-1 rounded-full bg-foreground/[0.055] px-2 py-0.5 text-[12px] text-foreground/55">
      <span className="shrink-0 font-medium text-foreground/35">{label}</span>
      <span className="min-w-0 truncate text-foreground/70">{value}</span>
    </span>
  );
}
