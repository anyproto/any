import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useMemo } from 'react';
import { apiFetch } from './client';

/**
 * Wire shapes mirror internal/api/types.go.
 */

export type PropertyKind = 'string' | 'number' | 'boolean' | 'null' | 'array' | 'object';

/**
 * UI sub-kind layered on top of the SDK kind via property.xKey.
 * Distinct from `PropertyKind` (the storage kind the SDK validates):
 *   string + xKey="longtext" → 'longtext'
 *   string + xKey="date"     → 'date'
 *   string + xKey="url"      → 'url'
 *   string + xKey="email"    → 'email'
 *   string + xKey="relation" → 'relation'  (single-target object link)
 *   array  + xKey="tags"     → 'tags'
 * Anything else falls back to the SDK kind.
 */
export type UIPropertyKind =
  | 'string'
  | 'number'
  | 'boolean'
  | 'longtext'
  | 'date'
  | 'url'
  | 'email'
  | 'tags'
  | 'relation'
  | 'array'
  | 'object'
  | 'null';

export function uiKind(p: Pick<PropertyDef, 'kind' | 'xKey'>): UIPropertyKind {
  if (p.kind === 'string') {
    if (p.xKey === 'longtext') return 'longtext';
    if (p.xKey === 'date') return 'date';
    if (p.xKey === 'url') return 'url';
    if (p.xKey === 'email') return 'email';
    if (p.xKey === 'relation') return 'relation';
    return 'string';
  }
  if (p.kind === 'array' && p.xKey === 'tags') return 'tags';
  return p.kind;
}

/**
 * Inverse — translate a UI kind picked in the popover into the
 * (kind, xKey) tuple sent on AddProperty.
 */
export function toAddPropertyParts(
  ui: UIPropertyKind,
): { kind: PropertyKind; xKey?: string } {
  switch (ui) {
    case 'longtext':
      return { kind: 'string', xKey: 'longtext' };
    case 'date':
      return { kind: 'string', xKey: 'date' };
    case 'url':
      return { kind: 'string', xKey: 'url' };
    case 'email':
      return { kind: 'string', xKey: 'email' };
    case 'tags':
      return { kind: 'array', xKey: 'tags' };
    case 'relation':
      return { kind: 'string', xKey: 'relation' };
    case 'string':
    case 'number':
    case 'boolean':
    case 'null':
    case 'array':
    case 'object':
      return { kind: ui as PropertyKind };
  }
}

export interface TypeInfo {
  id: string;
  name?: string;
  description?: string;
  iconCid?: string;
  builtIn?: boolean;
}

export interface PropertyDef {
  id: string;
  name?: string;
  description?: string;
  xKey?: string;
  xKind?: string;
  kind: PropertyKind;
  items?: PropertyDef;
  properties?: PropertyDef[];
  required?: string[];
}

export interface TypesListResponse {
  types: TypeInfo[];
}

export interface PropertiesListResponse {
  properties: PropertyDef[];
}

export interface CreateTypeRequest {
  name?: string;
  description?: string;
  iconCid?: string;
}

export interface AddPropertyRequest {
  name?: string;
  description?: string;
  xKey?: string;
  kind: PropertyKind;
}

const KEYS = {
  all: (spaceId: string) => ['types', spaceId] as const,
  one: (spaceId: string, typeId: string) => ['types', spaceId, typeId] as const,
  properties: (spaceId: string, typeId: string) =>
    ['types', spaceId, typeId, 'properties'] as const,
};

// ---------------- Plain functions -----------------------------------

export async function listTypes(spaceId: string, signal?: AbortSignal): Promise<TypeInfo[]> {
  const opts: { signal?: AbortSignal } = {};
  if (signal) opts.signal = signal;
  const res = await apiFetch<TypesListResponse>(
    `/spaces/${encodeURIComponent(spaceId)}/types`,
    opts,
  );
  return res.types ?? [];
}

export async function getType(
  spaceId: string,
  typeId: string,
  signal?: AbortSignal,
): Promise<TypeInfo> {
  const opts: { signal?: AbortSignal } = {};
  if (signal) opts.signal = signal;
  return apiFetch<TypeInfo>(
    `/spaces/${encodeURIComponent(spaceId)}/types/${encodeURIComponent(typeId)}`,
    opts,
  );
}

export async function createType(
  spaceId: string,
  req: CreateTypeRequest,
): Promise<{ typeId: string }> {
  return apiFetch<{ typeId: string }>(
    `/spaces/${encodeURIComponent(spaceId)}/types`,
    { method: 'POST', json: req },
  );
}

export async function getTypeProperties(
  spaceId: string,
  typeId: string,
  signal?: AbortSignal,
): Promise<PropertyDef[]> {
  const opts: { signal?: AbortSignal } = {};
  if (signal) opts.signal = signal;
  const res = await apiFetch<PropertiesListResponse>(
    `/spaces/${encodeURIComponent(spaceId)}/types/${encodeURIComponent(typeId)}/properties`,
    opts,
  );
  return res.properties ?? [];
}

export async function addPropertyToType(
  spaceId: string,
  typeId: string,
  req: AddPropertyRequest,
): Promise<{ propId: string }> {
  return apiFetch<{ propId: string }>(
    `/spaces/${encodeURIComponent(spaceId)}/types/${encodeURIComponent(typeId)}/properties`,
    { method: 'POST', json: req },
  );
}

// ---------------- React hooks ---------------------------------------

export function useTypes(spaceId: string | null) {
  return useQuery({
    queryKey: spaceId ? KEYS.all(spaceId) : (['types', '__none__'] as const),
    queryFn: ({ signal }) => listTypes(spaceId!, signal),
    enabled: spaceId != null,
  });
}

export function useType(spaceId: string | null, typeId: string | null) {
  return useQuery({
    queryKey:
      spaceId && typeId ? KEYS.one(spaceId, typeId) : (['types', '__none__', '__none__'] as const),
    queryFn: ({ signal }) => getType(spaceId!, typeId!, signal),
    enabled: spaceId != null && typeId != null,
  });
}

export function useTypeProperties(spaceId: string | null, typeId: string | null) {
  return useQuery({
    queryKey:
      spaceId && typeId
        ? KEYS.properties(spaceId, typeId)
        : (['types', '__none__', '__none__', 'properties'] as const),
    queryFn: ({ signal }) => getTypeProperties(spaceId!, typeId!, signal),
    enabled: spaceId != null && typeId != null,
  });
}

export function useCreateType(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateTypeRequest) => {
      if (!spaceId) throw new Error('useCreateType: no active space');
      return createType(spaceId, req);
    },
    onSuccess: () => {
      if (!spaceId) return;
      void qc.invalidateQueries({ queryKey: KEYS.all(spaceId) });
    },
  });
}

export interface UseAddPropertyArgs {
  typeId: string;
  req: AddPropertyRequest;
}

/**
 * One row per (type, property) for every user type in the space.
 * Used by the "From another list" panel in AddColumnPopover (PR-022)
 * to let the user reuse a property's shape rather than retype it.
 *
 * Implementation: we read the type list (cached) then call N
 * getTypeProperties queries via useQueries — each result is cached
 * under the same key the table view uses, so flipping between this
 * popover and a table doesn't double-fetch.
 */
export interface PropertyDefWithOwner extends PropertyDef {
  ownerTypeId: string;
  ownerTypeName: string;
}

export function useAllPropertiesInSpace(spaceId: string | null) {
  const typesQuery = useTypes(spaceId);
  const userTypes = useMemo(
    () => (typesQuery.data ?? []).filter((t) => !t.builtIn),
    [typesQuery.data],
  );

  const propsResults = useQueries({
    queries: userTypes.map((t) => ({
      queryKey:
        spaceId
          ? KEYS.properties(spaceId, t.id)
          : (['types', '__none__', '__none__', 'properties'] as const),
      queryFn: ({ signal }: { signal?: AbortSignal }) =>
        getTypeProperties(spaceId!, t.id, signal),
      enabled: spaceId != null,
    })),
  });

  const flat = useMemo<PropertyDefWithOwner[]>(() => {
    const out: PropertyDefWithOwner[] = [];
    userTypes.forEach((t, i) => {
      const props = propsResults[i]?.data ?? [];
      for (const p of props) {
        out.push({
          ...p,
          ownerTypeId: t.id,
          ownerTypeName: t.name?.trim() || `List ${t.id.slice(0, 6)}…`,
        });
      }
    });
    return out;
  }, [userTypes, propsResults]);

  const isLoading =
    typesQuery.isPending ||
    propsResults.some((r) => r.isPending && (r.fetchStatus ?? '') !== 'idle');

  return { properties: flat, isLoading };
}

export function useAddPropertyToType(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ typeId, req }: UseAddPropertyArgs) => {
      if (!spaceId) throw new Error('useAddPropertyToType: no active space');
      return addPropertyToType(spaceId, typeId, req);
    },
    onSuccess: (_res, { typeId }) => {
      if (!spaceId) return;
      void qc.invalidateQueries({ queryKey: KEYS.properties(spaceId, typeId) });
    },
  });
}
