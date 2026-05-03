import { useCallback, useEffect, useMemo, useState } from 'react';
import { useAtomValue, useSetAtom } from 'jotai';
import { activeObjectIdAtom, activeSpaceIdAtom, useTypeMeta } from '@/atoms';
import {
  useCreateObject,
  useObjectsByTypeInfinite,
} from '@/lib/api/objects';
import { useType } from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';
import { toast } from '@/components/ui';
import { useVirtualRows } from '@/shared';
import { TABLE_PAGE_SIZE, type SortDir } from './tableSorting';

export function useTableViewController(typeId: string, rowHeight: number) {
  const spaceId = useAtomValue(activeSpaceIdAtom);
  const typeQuery = useType(spaceId, typeId);
  const [filter, setFilter] = useState('');
  const [filterOpen, setFilterOpen] = useState(false);
  const [debouncedFilter, setDebouncedFilter] = useState('');
  const [sortKey, setSortKey] = useState<string>('nav.pos');
  const [sortDir, setSortDir] = useState<SortDir>('asc');

  useEffect(() => {
    const t = window.setTimeout(() => setDebouncedFilter(filter), 200);
    return () => window.clearTimeout(t);
  }, [filter]);

  const objectsQuery = useObjectsByTypeInfinite(spaceId, typeId, {
    sortKey,
    sortDir,
    pageSize: TABLE_PAGE_SIZE,
  });

  const rows = useMemo(
    () => objectsQuery.data?.pages.flatMap((page) => page.records) ?? [],
    [objectsQuery.data],
  );

  const visibleRows = useMemo(() => {
    const q = debouncedFilter.trim().toLowerCase();
    if (q === '') return rows;
    return rows.filter((r) => (r.any?.name ?? '').toLowerCase().includes(q));
  }, [debouncedFilter, rows]);

  const virtual = useVirtualRows({
    count: visibleRows.length,
    rowHeight,
    overscan: 10,
  });
  const virtualRows = visibleRows.slice(virtual.startIndex, virtual.endIndex);
  const { fetchNextPage, hasNextPage, isFetchingNextPage } = objectsQuery;

  useEffect(() => {
    if (debouncedFilter !== '') return;
    if (!hasNextPage || isFetchingNextPage) return;
    if (virtual.endIndex >= Math.max(0, visibleRows.length - 20)) {
      void fetchNextPage();
    }
  }, [
    debouncedFilter,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    virtual.endIndex,
    visibleRows.length,
  ]);

  const rawTypeName =
    typeQuery.data?.name?.trim() || `Untitled (${typeId.slice(0, 6)}...)`;
  const {
    overrideName: overrideTypeName,
    overrideIcon: overrideTypeIcon,
    set: setTypeMeta,
  } = useTypeMeta(spaceId, typeId);
  const typeName = overrideTypeName?.trim() || rawTypeName;
  const create = useCreateObject(spaceId);
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);

  const setSort = useCallback((key: string, dir: SortDir) => {
    setSortKey(key);
    setSortDir(dir);
  }, []);

  const createRow = useCallback(async () => {
    if (!spaceId) return;
    try {
      const { objectId } = await create.mutateAsync({ typeIds: [typeId] });
      setActiveObjectId(objectId);
      toast.success(`Created ${typeName}`);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add row';
      toast.error(`${code}: ${msg}`);
    }
  }, [create, setActiveObjectId, spaceId, typeId, typeName]);

  return {
    spaceId,
    typeQuery,
    filter,
    setFilter,
    filterOpen,
    setFilterOpen,
    debouncedFilter,
    sortKey,
    sortDir,
    setSort,
    objectsQuery,
    rows,
    visibleRows,
    virtual,
    virtualRows,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    typeName,
    overrideTypeIcon,
    setTypeMeta,
    create,
    createRow,
  };
}
