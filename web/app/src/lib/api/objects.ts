import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './client';

/**
 * Cross-object record shape returned by /v1/spaces/:s/objects/query.
 * Mirrors what `internal/nav/nav.go` documents:
 *
 *   { id, any: { name?, types? }, nav: { type, parentId, pos } }
 *
 * `nav.type === 1` is item, `2` is folder.
 */
export interface ObjectRecord {
  id: string;
  any?: {
    name?: string;
    types?: string[];
  };
  nav?: {
    type?: number;
    parentId?: string;
    pos?: string;
  };
  // Other type/property data is present but not relied on yet.
  [key: string]: unknown;
}

export const NAV_ITEM = 1;
export const NAV_FOLDER = 2;
export const NAV_ROOT_PARENT_ID = '';
/** typeId for the built-in `any` namespace (name, description, …). */
export const ANY_TYPE_ID = 'any';

interface QueryBody {
  filter?: Record<string, unknown>;
  sort?: string[];
  limit?: number;
  offset?: number;
}

interface QueryResponse {
  records: ObjectRecord[];
}

const KEYS = {
  childrenOf: (spaceId: string, parentId: string) =>
    ['objects', spaceId, 'children', parentId] as const,
};

// ---------------- Plain functions -----------------------------------

export async function queryObjects(
  spaceId: string,
  body: QueryBody,
  signal?: AbortSignal,
): Promise<ObjectRecord[]> {
  const init: { method: 'POST'; json: QueryBody; signal?: AbortSignal } = {
    method: 'POST',
    json: body,
  };
  if (signal) init.signal = signal;
  const res = await apiFetch<QueryResponse>(
    `/spaces/${encodeURIComponent(spaceId)}/objects/query`,
    init,
  );
  return res.records ?? [];
}

export async function createObject(
  spaceId: string,
  body: Record<string, unknown> = {},
): Promise<{ objectId: string }> {
  return apiFetch<{ objectId: string }>(
    `/spaces/${encodeURIComponent(spaceId)}/objects`,
    { method: 'POST', json: body },
  );
}

/**
 * Set base properties on an object — used for renames (typeId = 'any',
 * patch = {name: '...'}) and for tree moves (typeId = 'nav',
 * patch = {parentId, pos}).
 */
export async function setObjectProperty(
  spaceId: string,
  objectId: string,
  typeId: string,
  patch: Record<string, unknown>,
): Promise<unknown> {
  return apiFetch<unknown>(
    `/spaces/${encodeURIComponent(spaceId)}/properties/${encodeURIComponent(objectId)}/base/${encodeURIComponent(typeId)}`,
    { method: 'POST', json: { patch } },
  );
}

export async function deleteObject(spaceId: string, objectId: string): Promise<void> {
  await apiFetch<void>(
    `/spaces/${encodeURIComponent(spaceId)}/objects/${encodeURIComponent(objectId)}`,
    { method: 'DELETE' },
  );
}

// ---------------- React hooks ---------------------------------------

export interface ObjectsByTypeOptions {
  /** Sort key, e.g. 'any.name' or '<typeId>.<propId>'. */
  sortKey?: string;
  /** Sort direction. 'asc' is the SDK default; 'desc' uses the leading '-'. */
  sortDir?: 'asc' | 'desc' | null;
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
  const sortKey = opts.sortKey ?? 'nav.pos';
  const sortDir = opts.sortDir ?? 'asc';
  const sortClause = sortDir === 'desc' ? `-${sortKey}` : sortKey;
  return useQuery({
    queryKey: spaceId && typeId
      ? (['objects', spaceId, 'by-type', typeId, sortClause] as const)
      : (['objects', '__none__', 'by-type', '__none__', 'asc'] as const),
    queryFn: ({ signal }) =>
      queryObjects(
        spaceId!,
        { filter: { 'any.types': typeId }, sort: [sortClause] },
        signal,
      ),
    enabled: spaceId != null && typeId != null,
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
      ? KEYS.childrenOf(spaceId, parentId)
      : (['objects', '__none__', 'children', parentId] as const),
    queryFn: ({ signal }) =>
      queryObjects(
        spaceId!,
        { filter: { 'nav.parentId': parentId }, sort: ['nav.pos'] },
        signal,
      ),
    enabled: spaceId != null,
  });
}

export interface CreateObjectArgs {
  /** Where in the tree the new object lands. Defaults to root. */
  parentId?: string;
  /** True for folders (nav.type=2), false/undefined for items (nav.type=1). */
  folder?: boolean;
  /**
   * Custom user types to stamp on the new object. Server auto-appends
   * `nav`; we leave it off the wire. Empty / undefined → just nav.
   */
  typeIds?: string[];
}

/**
 * Create a new object inside a space. Defaults to a root-level item.
 * Pass `{folder: true}` for a folder; pass `{parentId}` to put it
 * inside a folder; pass `{typeIds: [t]}` for a custom user type.
 */
export function useCreateObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      parentId = NAV_ROOT_PARENT_ID,
      folder,
      typeIds,
    }: CreateObjectArgs = {}) => {
      if (!spaceId) throw new Error('useCreateObject: no active space');
      const nav: Record<string, unknown> = {};
      if (parentId) nav['parentId'] = parentId;
      if (folder) nav['type'] = NAV_FOLDER;
      const body: Record<string, unknown> = {};
      if (Object.keys(nav).length > 0) body['nav'] = nav;
      if (typeIds && typeIds.length > 0) body['types'] = typeIds;
      return createObject(spaceId, body);
    },
    onSuccess: (_res, args) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: KEYS.childrenOf(spaceId, args?.parentId ?? NAV_ROOT_PARENT_ID),
      });
    },
  });
}

export interface RenameArgs {
  objectId: string;
  parentId: string;
  name: string;
}

/**
 * Rename an object — POST .../properties/:o/base/any with
 * `{patch: {name}}`. Optimistic: patches the row's `any.name` in the
 * children-of-parent cache before the network round-trip; rolls back
 * on error.
 */
export function useRenameObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ objectId, name }: RenameArgs) => {
      if (!spaceId) throw new Error('useRenameObject: no active space');
      return setObjectProperty(spaceId, objectId, ANY_TYPE_ID, { name });
    },
    onMutate: async ({ objectId, parentId, name }) => {
      if (!spaceId) return;
      const key = KEYS.childrenOf(spaceId, parentId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<ObjectRecord[]>(key);
      if (previous) {
        qc.setQueryData<ObjectRecord[]>(
          key,
          previous.map((r) =>
            r.id === objectId ? { ...r, any: { ...(r.any ?? {}), name } } : r,
          ),
        );
      }
      return { previous, key };
    },
    onError: (_err, _args, ctx) => {
      if (ctx?.previous && ctx.key) {
        qc.setQueryData(ctx.key, ctx.previous);
      }
    },
    onSettled: (_data, _err, args) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: KEYS.childrenOf(spaceId, args.parentId),
      });
    },
  });
}

export interface MoveArgs {
  objectId: string;
  fromParentId: string;
  toParentId: string;
  /** New nav.pos string (computed via lexid client-side). */
  pos: string;
}

/**
 * Move an object to a new parent + position.
 *
 * Sends `POST .../properties/:o/base/nav` with `{patch: {parentId, pos}}`
 * — the server commits both fields in one DAG change.
 *
 * Optimistic: removes the row from the source parent's cached
 * children and inserts it into the target's, sorted by nav.pos.
 * Rolls back on error.
 */
export function useMoveObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ objectId, toParentId, pos }: MoveArgs) => {
      if (!spaceId) throw new Error('useMoveObject: no active space');
      return setObjectProperty(spaceId, objectId, 'nav', { parentId: toParentId, pos });
    },
    onMutate: async ({ objectId, fromParentId, toParentId, pos }) => {
      if (!spaceId) return;
      const fromKey = KEYS.childrenOf(spaceId, fromParentId);
      const toKey = KEYS.childrenOf(spaceId, toParentId);
      await Promise.all([
        qc.cancelQueries({ queryKey: fromKey }),
        qc.cancelQueries({ queryKey: toKey }),
      ]);
      const fromPrev = qc.getQueryData<ObjectRecord[]>(fromKey);
      const toPrev = qc.getQueryData<ObjectRecord[]>(toKey);

      // Find the row in source parent's cached list.
      const movedRow =
        fromPrev?.find((r) => r.id === objectId) ??
        toPrev?.find((r) => r.id === objectId); // same-parent moves
      if (!movedRow) return { fromPrev, toPrev, fromKey, toKey };

      // Patch the moved row's nav.parentId + nav.pos optimistically.
      const updatedRow: ObjectRecord = {
        ...movedRow,
        nav: { ...(movedRow.nav ?? {}), parentId: toParentId, pos },
      };

      if (fromParentId !== toParentId) {
        if (fromPrev) {
          qc.setQueryData<ObjectRecord[]>(
            fromKey,
            fromPrev.filter((r) => r.id !== objectId),
          );
        }
        if (toPrev !== undefined) {
          const next = [...toPrev.filter((r) => r.id !== objectId), updatedRow];
          next.sort(byNavPos);
          qc.setQueryData<ObjectRecord[]>(toKey, next);
        }
      } else {
        // Same parent — re-sort the existing list with the new pos.
        if (toPrev) {
          const next = toPrev.map((r) => (r.id === objectId ? updatedRow : r));
          next.sort(byNavPos);
          qc.setQueryData<ObjectRecord[]>(toKey, next);
        }
      }

      return { fromPrev, toPrev, fromKey, toKey };
    },
    onError: (_err, _args, ctx) => {
      if (!ctx) return;
      if (ctx.fromPrev !== undefined) qc.setQueryData(ctx.fromKey, ctx.fromPrev);
      if (ctx.toPrev !== undefined) qc.setQueryData(ctx.toKey, ctx.toPrev);
    },
    onSettled: (_data, _err, args) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: KEYS.childrenOf(spaceId, args.fromParentId),
      });
      if (args.toParentId !== args.fromParentId) {
        void qc.invalidateQueries({
          queryKey: KEYS.childrenOf(spaceId, args.toParentId),
        });
      }
    },
  });
}

function byNavPos(a: ObjectRecord, b: ObjectRecord): number {
  const ap = a.nav?.pos ?? '';
  const bp = b.nav?.pos ?? '';
  return ap < bp ? -1 : ap > bp ? 1 : 0;
}

export interface DeleteArgs {
  objectId: string;
  parentId: string;
}

export function useDeleteObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ objectId }: DeleteArgs) => {
      if (!spaceId) throw new Error('useDeleteObject: no active space');
      await deleteObject(spaceId, objectId);
    },
    onSuccess: (_res, { parentId }) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: KEYS.childrenOf(spaceId, parentId),
      });
    },
  });
}
