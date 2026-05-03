import type { QueryClient } from '@tanstack/react-query';
import type { ObjectRecord } from './model';

export const objectKeys = {
  one: (spaceId: string, objectId: string) =>
    ['objects', spaceId, 'one', objectId] as const,
  oneNone: ['objects', '__none__', 'one', '__none__'] as const,
  name: (spaceId: string, objectId: string) =>
    ['objects', spaceId, 'name', objectId] as const,
  nameNone: ['objects', '__none__', 'name', '__none__'] as const,
  allItems: (spaceId: string) => ['objects', spaceId, 'all-items'] as const,
  allItemsNone: ['objects', '__none__', 'all-items'] as const,
  childrenOf: (spaceId: string, parentId: string) =>
    ['objects', spaceId, 'children', parentId] as const,
  childrenRoot: (spaceId: string) => ['objects', spaceId, 'children'] as const,
  childrenOfNone: (parentId: string) =>
    ['objects', '__none__', 'children', parentId] as const,
  byType: (spaceId: string, typeId: string, sortClause: string) =>
    ['objects', spaceId, 'by-type', typeId, sortClause] as const,
  byTypeRoot: (spaceId: string, typeId: string) =>
    ['objects', spaceId, 'by-type', typeId] as const,
  byTypeNone: ['objects', '__none__', 'by-type', '__none__', 'asc'] as const,
  byTypePages: (
    spaceId: string,
    typeId: string,
    sortClause: string,
    pageSize: number,
  ) => ['objects', spaceId, 'by-type-pages', typeId, sortClause, pageSize] as const,
  byTypePagesRoot: (spaceId: string, typeId: string) =>
    ['objects', spaceId, 'by-type-pages', typeId] as const,
  byTypePagesNone: (pageSize: number) =>
    ['objects', '__none__', 'by-type-pages', '__none__', 'asc', pageSize] as const,
  countsByType: (spaceId: string, typeIds: readonly string[]) =>
    ['objects', spaceId, 'counts-by-type', typeIds] as const,
  countsByTypeRoot: (spaceId: string) => ['objects', spaceId, 'counts-by-type'] as const,
  countsByTypeNone: ['objects', '__none__', 'counts-by-type'] as const,
} as const;

export function isObjectsQueryForSpace(queryKey: readonly unknown[], spaceId: string) {
  return queryKey[0] === 'objects' && queryKey[1] === spaceId;
}

export function isObjectsByTypeQueryForSpace(queryKey: readonly unknown[], spaceId: string) {
  return (
    isObjectsQueryForSpace(queryKey, spaceId) &&
    (queryKey[2] === 'by-type' || queryKey[2] === 'by-type-pages')
  );
}

export function isObjectsByTypeQuery(
  queryKey: readonly unknown[],
  spaceId: string,
  typeId: string,
) {
  return isObjectsByTypeQueryForSpace(queryKey, spaceId) && queryKey[3] === typeId;
}

function isObjectNamePatchableQuery(queryKey: readonly unknown[], spaceId: string) {
  if (!isObjectsQueryForSpace(queryKey, spaceId)) return false;
  return (
    queryKey[2] === 'one' ||
    queryKey[2] === 'all-items' ||
    queryKey[2] === 'children' ||
    queryKey[2] === 'by-type' ||
    queryKey[2] === 'by-type-pages'
  );
}

export function patchObjectRecordName(
  record: ObjectRecord,
  objectId: string,
  name: string,
): ObjectRecord {
  if (record.id !== objectId) return record;
  return { ...record, any: { ...(record.any ?? {}), name } };
}

export function findObjectNameInPayload(payload: unknown, objectId: string): string | undefined {
  if (Array.isArray(payload)) {
    for (const item of payload) {
      const name = findObjectNameInPayload(item, objectId);
      if (name !== undefined) return name;
    }
    return undefined;
  }

  if (payload == null || typeof payload !== 'object') return undefined;
  const maybe = payload as {
    id?: unknown;
    any?: { name?: unknown };
    records?: unknown;
    pages?: unknown;
  };

  if (maybe.id === objectId) {
    return typeof maybe.any?.name === 'string' ? maybe.any.name : undefined;
  }

  const recordsName = findObjectNameInPayload(maybe.records, objectId);
  if (recordsName !== undefined) return recordsName;

  return findObjectNameInPayload(maybe.pages, objectId);
}

export function findObjectNameInCache(
  qc: QueryClient,
  spaceId: string,
  objectId: string,
): string | undefined {
  const direct = qc.getQueryData<string>(objectKeys.name(spaceId, objectId));
  if (typeof direct === 'string') return direct;

  const matches = qc.getQueriesData<unknown>({
    predicate: (q) => {
      const k = q.queryKey;
      return Array.isArray(k) && isObjectNamePatchableQuery(k, spaceId);
    },
  });
  for (const [, payload] of matches) {
    const name = findObjectNameInPayload(payload, objectId);
    if (name !== undefined) return name;
  }
  return undefined;
}

export function patchObjectNamePayload<T>(payload: T, objectId: string, name: string): T {
  if (Array.isArray(payload)) {
    return payload.map((record) =>
      typeof record === 'object' && record != null && 'id' in record
        ? patchObjectRecordName(record as ObjectRecord, objectId, name)
        : record,
    ) as T;
  }

  if (payload == null || typeof payload !== 'object') return payload;

  const maybe = payload as { id?: unknown; records?: unknown; pages?: unknown };

  if (typeof maybe.id === 'string') {
    return patchObjectRecordName(maybe as ObjectRecord, objectId, name) as T;
  }

  if (Array.isArray(maybe.records)) {
    return {
      ...payload,
      records: patchObjectNamePayload(maybe.records, objectId, name),
    } as T;
  }

  if (Array.isArray(maybe.pages)) {
    return {
      ...payload,
      pages: maybe.pages.map((page) => patchObjectNamePayload(page, objectId, name)),
    } as T;
  }

  warnUnhandledObjectNamePayload(payload);
  return payload;
}

function warnUnhandledObjectNamePayload(payload: object) {
  if ((import.meta as { env?: { DEV?: boolean } }).env?.DEV) {
    console.warn('patchObjectNamePayload: unrecognized cached object payload shape', {
      keys: Object.keys(payload),
    });
  }
}

export function patchObjectNameEverywhere(
  qc: QueryClient,
  spaceId: string,
  objectId: string,
  name: string,
) {
  qc.setQueryData(objectKeys.name(spaceId, objectId), name);
  qc.setQueriesData(
    {
      predicate: (q) => {
        const k = q.queryKey;
        return Array.isArray(k) && isObjectNamePatchableQuery(k, spaceId);
      },
    },
    (old) => patchObjectNamePayload(old, objectId, name),
  );
}
