import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { NAV_ITEM, type ObjectRecord } from './model';
import { findObjectNameInCache, objectKeys } from './cache';
import { queryObjects } from './transport';

export interface ObjectsByTypeOptions {
  /** Sort key, e.g. 'any.name' or '<typeId>.<propId>'. */
  sortKey?: string;
  /** Sort direction. 'asc' is the SDK default; 'desc' uses the leading '-'. */
  sortDir?: 'asc' | 'desc' | null;
  /** Page size for virtualized/infinite consumers. */
  pageSize?: number;
}

function sortClauseFor(opts: ObjectsByTypeOptions) {
  const sortKey = opts.sortKey ?? 'nav.pos';
  const sortDir = opts.sortDir ?? 'asc';
  return sortDir === 'desc' ? `-${sortKey}` : sortKey;
}

/**
 * Objects stamped with a given type. Cache key includes the sort so
 * cycling sort doesn't share a cache slot with the previous order.
 */
export function useObjectsByType(
  spaceId: string | null,
  typeId: string | null,
  opts: ObjectsByTypeOptions = {},
) {
  const sortClause = sortClauseFor(opts);
  return useQuery({
    queryKey:
      spaceId && typeId
        ? objectKeys.byType(spaceId, typeId, sortClause)
        : objectKeys.byTypeNone,
    queryFn: ({ signal }) =>
      queryObjects(
        spaceId!,
        { filter: { 'any.types': typeId }, sort: [sortClause] },
        signal,
      ),
    enabled: spaceId != null && typeId != null,
  });
}

export interface ObjectsPage {
  records: ObjectRecord[];
  nextOffset?: number;
}

/**
 * Paged variant for dense table surfaces. Requests one extra row as a
 * cheap "has more" probe because the current API returns `{records}`
 * without a total count.
 */
export function useObjectsByTypeInfinite(
  spaceId: string | null,
  typeId: string | null,
  opts: ObjectsByTypeOptions = {},
) {
  const sortClause = sortClauseFor(opts);
  const pageSize = opts.pageSize ?? 100;
  return useInfiniteQuery({
    queryKey:
      spaceId && typeId
        ? objectKeys.byTypePages(spaceId, typeId, sortClause, pageSize)
        : objectKeys.byTypePagesNone(pageSize),
    queryFn: async ({ signal, pageParam }): Promise<ObjectsPage> => {
      const offset = typeof pageParam === 'number' ? pageParam : 0;
      const rows = await queryObjects(
        spaceId!,
        {
          filter: { 'any.types': typeId },
          sort: [sortClause],
          limit: pageSize + 1,
          offset,
        },
        signal,
      );
      const hasMore = rows.length > pageSize;
      const page: ObjectsPage = {
        records: hasMore ? rows.slice(0, pageSize) : rows,
      };
      if (hasMore) page.nextOffset = offset + pageSize;
      return page;
    },
    initialPageParam: 0,
    getNextPageParam: (lastPage) => lastPage.nextOffset,
    enabled: spaceId != null && typeId != null,
  });
}

/**
 * Batched sidebar counts for user types. One cross-object query is
 * enough because every object record carries `any.types`; this avoids
 * the old one-query-per-type pattern.
 */
export function useTypeObjectCounts(spaceId: string | null, typeIds: string[]) {
  const normalizedTypeIds = [...new Set(typeIds)].sort();
  return useQuery({
    queryKey:
      spaceId && normalizedTypeIds.length > 0
        ? objectKeys.countsByType(spaceId, normalizedTypeIds)
        : objectKeys.countsByTypeNone,
    queryFn: async ({ signal }) => {
      const counts = Object.fromEntries(normalizedTypeIds.map((id) => [id, 0])) as Record<
        string,
        number
      >;

      const pageSize = 1000;
      for (let offset = 0; ; offset += pageSize) {
        const records = await queryObjects(
          spaceId!,
          { filter: { 'nav.type': NAV_ITEM }, limit: pageSize, offset },
          signal,
        );
        for (const record of records) {
          for (const id of record.any?.types ?? []) {
            if (id in counts) counts[id] = (counts[id] ?? 0) + 1;
          }
        }
        if (records.length < pageSize) break;
      }
      return counts;
    },
    enabled: spaceId != null && normalizedTypeIds.length > 0,
  });
}

/**
 * Fetch the full record for one object via the /objects/query
 * endpoint with an id filter. Used by surfaces (editor type bar)
 * that need both `any.types` and the per-type property values in
 * one place.
 */
export function useObject(spaceId: string | null, objectId: string | null) {
  return useQuery({
    queryKey:
      spaceId && objectId ? objectKeys.one(spaceId, objectId) : objectKeys.oneNone,
    queryFn: async ({ signal }) => {
      const records = await queryObjects(
        spaceId!,
        { filter: { id: objectId! }, limit: 1 },
        signal,
      );
      return records[0] ?? null;
    },
    enabled: spaceId != null && objectId != null,
  });
}

/**
 * Read just the name for one object. Implemented via the same
 * /objects/query endpoint with an id filter — no per-object GET
 * exists yet. Cache key is per (space, object) so the title in the
 * editor and the row in the sidebar can share a cell when they
 * happen to converge.
 */
export function useObjectName(spaceId: string | null, objectId: string | null) {
  const qc = useQueryClient();
  const cachedName =
    spaceId && objectId ? findObjectNameInCache(qc, spaceId, objectId) : undefined;

  return useQuery({
    queryKey:
      spaceId && objectId ? objectKeys.name(spaceId, objectId) : objectKeys.nameNone,
    queryFn: async ({ signal }) => {
      const records = await queryObjects(
        spaceId!,
        { filter: { id: objectId! }, limit: 1 },
        signal,
      );
      const row = records[0];
      return row?.any?.name ?? cachedName ?? '';
    },
    enabled: spaceId != null && objectId != null,
    ...(cachedName !== undefined ? { initialData: cachedName } : {}),
  });
}

/**
 * All "real" (item) objects in the space — used by the Relation
 * picker. Filtered to nav.type=1 so folders don't appear in the
 * picker. Sorted by most-recently-touched (-nav.pos as a stand-in
 * until we have updatedAt) so what you just made is at the top.
 */
export function useObjectsBySpace(spaceId: string | null) {
  return useQuery({
    queryKey: spaceId ? objectKeys.allItems(spaceId) : objectKeys.allItemsNone,
    queryFn: ({ signal }) =>
      queryObjects(
        spaceId!,
        { filter: { 'nav.type': NAV_ITEM }, sort: ['-nav.pos'], limit: 500 },
        signal,
      ),
    enabled: spaceId != null,
  });
}

/**
 * Children of a folder (or the root). One query per (space, parent),
 * so collapsed folders don't fetch and re-expanding is instant from
 * cache.
 */
export function useObjectChildren(spaceId: string | null, parentId: string) {
  return useQuery({
    queryKey: spaceId
      ? objectKeys.childrenOf(spaceId, parentId)
      : objectKeys.childrenOfNone(parentId),
    queryFn: ({ signal }) =>
      queryObjects(
        spaceId!,
        { filter: { 'nav.parentId': parentId }, sort: ['nav.pos'] },
        signal,
      ),
    enabled: spaceId != null,
  });
}
