import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useSetAtom } from 'jotai';
import { ArrowUpRight, FileText, Image, Plus } from 'lucide-react';
import { openObjectFromTypeAtom } from '@/atoms';
import { preloadViewModule } from '@/app/viewModules';
import { ApiError } from '@/lib/api/client';
import type { ObjectRecord } from '@/lib/api/objects';
import type { PropertyDef } from '@/lib/api/types';
import { cn } from '@/lib/cn';
import {
  formatListPropertyValue,
  readTablePropValue,
} from './tableObjectValues';

export interface GalleryViewConfig {
  minCardWidth: number;
  cardHeight: number;
  gap: number;
  previewPropertyLimit: number;
  showPropertyLabels: boolean;
}

export const DEFAULT_GALLERY_VIEW_CONFIG: GalleryViewConfig = {
  minCardWidth: 220,
  cardHeight: 178,
  gap: 12,
  previewPropertyLimit: 4,
  showPropertyLabels: true,
};

export function GalleryRowsView({
  typeId,
  rows,
  props,
  isSuccess,
  error,
  filter,
  typeName,
  creating,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  onCreate,
  config = DEFAULT_GALLERY_VIEW_CONFIG,
}: {
  typeId: string;
  rows: ObjectRecord[];
  props: PropertyDef[];
  isSuccess: boolean;
  error: unknown;
  filter: string;
  typeName: string;
  creating: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadMore: () => void;
  onCreate: () => void;
  config?: GalleryViewConfig;
}) {
  const virtual = useGalleryVirtualGrid({
    count: rows.length,
    minCardWidth: config.minCardWidth,
    cardHeight: config.cardHeight,
    gap: config.gap,
  });
  const visibleRows = rows.slice(virtual.startIndex, virtual.endIndex);

  useEffect(() => {
    if (filter.trim() !== '') return;
    if (!hasNextPage || isFetchingNextPage) return;
    const preloadThreshold = Math.max(0, rows.length - virtual.columns * 3);
    if (virtual.endIndex >= preloadThreshold) onLoadMore();
  }, [
    filter,
    hasNextPage,
    isFetchingNextPage,
    onLoadMore,
    rows.length,
    virtual.columns,
    virtual.endIndex,
  ]);

  return (
    <div
      ref={virtual.scrollRef}
      onScroll={virtual.onScroll}
      className="min-w-0 flex-1 overflow-auto px-8 pb-10"
    >
      <div className="mx-auto w-full max-w-[1180px]">
        {error != null && (
          <div className="border-y border-foreground/[0.075] px-4 py-4 text-sm text-destructive">
            {(error instanceof ApiError ? error.code : 'unknown') +
              ' - ' +
              (error instanceof Error ? error.message : 'Failed to load')}
          </div>
        )}

        {isSuccess && rows.length === 0 && (
          <div className="border-y border-foreground/[0.075] px-4 py-10 text-left">
            <div className="max-w-sm rounded-lg bg-foreground/[0.025] px-4 py-3">
              <p className="text-sm font-medium text-foreground/70">
                {filter ? 'No matches' : `No ${typeName} yet`}
              </p>
              <p className="mt-1 text-xs text-foreground/45">
                {filter
                  ? 'Try a different filter or clear it from the toolbar.'
                  : 'Create the first item and it will appear in this gallery.'}
              </p>
            </div>
          </div>
        )}

        {isSuccess && rows.length > 0 && (
          <>
            {virtual.paddingTop > 0 && (
              <div aria-hidden style={{ height: virtual.paddingTop }} />
            )}
            <div
              role="list"
              aria-label={`${typeName} gallery`}
              className="grid"
              style={{
                gap: config.gap,
                gridTemplateColumns: `repeat(${virtual.columns}, minmax(0, 1fr))`,
                gridAutoRows: `${config.cardHeight}px`,
              }}
            >
              {visibleRows.map((row) => (
                <GalleryCard
                  key={row.id}
                  typeId={typeId}
                  row={row}
                  props={props}
                  config={config}
                />
              ))}
            </div>
            {virtual.paddingBottom > 0 && (
              <div aria-hidden style={{ height: virtual.paddingBottom }} />
            )}
          </>
        )}

        {hasNextPage && (
          <button
            type="button"
            onClick={onLoadMore}
            disabled={isFetchingNextPage}
            className={cn(
              'mt-4 flex h-10 w-full items-center justify-center rounded-lg text-[12px] text-foreground/45',
              'bg-foreground/[0.025] hover:bg-foreground/[0.04] hover:text-foreground/75',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
              'disabled:cursor-wait disabled:opacity-70',
            )}
          >
            {isFetchingNextPage ? 'Loading...' : 'Load more'}
          </button>
        )}

        <button
          type="button"
          onClick={onCreate}
          disabled={creating}
          className={cn(
            'mt-4 flex h-11 w-full items-center gap-2 rounded-lg px-3 text-left text-[14px] text-foreground/45',
            'hover:bg-foreground/[0.035] hover:text-foreground/75',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            'disabled:cursor-wait disabled:opacity-70',
          )}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
          {creating ? 'Adding...' : `New ${typeName}`}
        </button>
      </div>
    </div>
  );
}

function GalleryCard({
  typeId,
  row,
  props,
  config,
}: {
  typeId: string;
  row: ObjectRecord;
  props: PropertyDef[];
  config: GalleryViewConfig;
}) {
  const openObjectFromType = useSetAtom(openObjectFromTypeAtom);
  const name = row.any?.name?.trim() || 'Untitled';
  const previews = useMemo(
    () =>
      props
        .map((prop) => ({
          prop,
          value: formatListPropertyValue(
            prop,
            readTablePropValue(row, typeId, prop.id),
          ),
        }))
        .filter((item): item is { prop: PropertyDef; value: string } => item.value != null)
        .slice(0, config.previewPropertyLimit),
    [config.previewPropertyLimit, props, row, typeId],
  );

  return (
    <div role="listitem" className="min-w-0">
      <button
        type="button"
        aria-label={`Open ${name}`}
        onFocus={() => preloadViewModule('object')}
        onPointerEnter={() => preloadViewModule('object')}
        onClick={() => openObjectFromType({ objectId: row.id, typeId })}
        className={cn(
          'group/gallery-card flex h-full w-full min-w-0 flex-col overflow-hidden rounded-lg border border-foreground/[0.075]',
          'bg-foreground/[0.018] text-left transition-[background-color,border-color,box-shadow,transform]',
          'hover:-translate-y-0.5 hover:border-foreground/[0.13] hover:bg-foreground/[0.03] hover:shadow-sm hover:shadow-foreground/5',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <div className="flex h-16 shrink-0 items-center justify-between border-b border-foreground/[0.055] bg-foreground/[0.025] px-3">
          <span className="inline-flex h-9 w-9 items-center justify-center rounded-lg bg-background/70 text-foreground/35 shadow-sm shadow-foreground/5">
            <Image className="h-4 w-4" aria-hidden />
          </span>
          <span
            aria-hidden
            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-foreground/30 opacity-0 transition-opacity group-hover/gallery-card:opacity-100 group-focus-visible/gallery-card:opacity-100"
          >
            <ArrowUpRight className="h-3.5 w-3.5" aria-hidden />
          </span>
        </div>
        <div className="flex min-h-0 flex-1 flex-col px-3 py-2.5">
          <div className="flex min-w-0 items-center gap-2">
            <FileText className="h-3.5 w-3.5 shrink-0 text-foreground/35" aria-hidden />
            <span className="min-w-0 truncate text-[15px] font-semibold text-foreground">
              {name}
            </span>
          </div>
          <div className="mt-2 flex min-h-0 flex-1 flex-wrap content-start gap-1.5 overflow-hidden">
            {previews.length > 0 ? (
              previews.map(({ prop, value }) => (
                <GalleryPropertyBadge
                  key={prop.id}
                  label={prop.name ?? `Untitled (${prop.id.slice(0, 6)}...)`}
                  value={value}
                  showLabel={config.showPropertyLabels}
                />
              ))
            ) : (
              <span className="text-xs text-foreground/35">
                {props.length === 0 ? 'No properties' : 'No filled properties'}
              </span>
            )}
          </div>
        </div>
      </button>
    </div>
  );
}

function GalleryPropertyBadge({
  label,
  value,
  showLabel,
}: {
  label: string;
  value: string;
  showLabel: boolean;
}) {
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-full bg-foreground/[0.055] px-2 py-0.5 text-[12px] text-foreground/55">
      {showLabel && <span className="shrink-0 font-medium text-foreground/35">{label}</span>}
      <span className="min-w-0 truncate text-foreground/70">{value}</span>
    </span>
  );
}

function useGalleryVirtualGrid({
  count,
  minCardWidth,
  cardHeight,
  gap,
  overscan = 2,
}: {
  count: number;
  minCardWidth: number;
  cardHeight: number;
  gap: number;
  overscan?: number;
}) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(0);
  const [viewportWidth, setViewportWidth] = useState(0);

  const measure = useCallback(() => {
    const node = scrollRef.current;
    setViewportHeight(node?.clientHeight ?? 0);
    setViewportWidth(node?.clientWidth ?? 0);
  }, []);

  useEffect(() => {
    measure();
    const node = scrollRef.current;
    if (!node || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [measure]);

  const onScroll = useCallback(() => {
    setScrollTop(scrollRef.current?.scrollTop ?? 0);
  }, []);

  return useMemo(() => {
    const columns = Math.max(
      1,
      Math.floor((Math.max(0, viewportWidth) + gap) / (minCardWidth + gap)),
    );
    const rowStep = cardHeight + gap;
    const totalGridRows = Math.ceil(count / columns);
    const visibleGridRows = Math.max(1, Math.ceil(viewportHeight / rowStep));
    const startRow = Math.max(0, Math.floor(scrollTop / rowStep) - overscan);
    const endRow = Math.min(totalGridRows, startRow + visibleGridRows + overscan * 2);

    return {
      scrollRef,
      onScroll,
      columns,
      startIndex: startRow * columns,
      endIndex: Math.min(count, endRow * columns),
      paddingTop: startRow * rowStep,
      paddingBottom: Math.max(0, (totalGridRows - endRow) * rowStep),
    };
  }, [
    cardHeight,
    count,
    gap,
    minCardWidth,
    onScroll,
    overscan,
    scrollTop,
    viewportHeight,
    viewportWidth,
  ]);
}
