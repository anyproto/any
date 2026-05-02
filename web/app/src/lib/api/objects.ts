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
}

/**
 * Create a new object inside a space. Defaults to a root-level item.
 * Pass `{folder: true}` for a folder; pass `{parentId}` to put it
 * inside a folder.
 */
export function useCreateObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ parentId = NAV_ROOT_PARENT_ID, folder }: CreateObjectArgs = {}) => {
      if (!spaceId) throw new Error('useCreateObject: no active space');
      const nav: Record<string, unknown> = {};
      if (parentId) nav['parentId'] = parentId;
      if (folder) nav['type'] = NAV_FOLDER;
      const body: Record<string, unknown> = Object.keys(nav).length > 0 ? { nav } : {};
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
