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
  // Other type/property data is present but not relied on in PR #4.
  [key: string]: unknown;
}

export const NAV_ITEM = 1;
export const NAV_FOLDER = 2;
export const NAV_ROOT_PARENT_ID = '';

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

/**
 * Create a new object inside a space. v1 takes a single `parentId`
 * arg (defaults to root). Server stamps the rest of nav.
 */
export function useCreateObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (parentId: string = NAV_ROOT_PARENT_ID) => {
      if (!spaceId) throw new Error('useCreateObject: no active space');
      const body: Record<string, unknown> = parentId
        ? { nav: { parentId } }
        : {};
      return createObject(spaceId, body);
    },
    onSuccess: (_res, parentId) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: KEYS.childrenOf(spaceId, parentId),
      });
    },
  });
}
