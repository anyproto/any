import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './client';

/**
 * Wire shapes mirror internal/api/types.go.
 */

export type PropertyKind = 'string' | 'number' | 'boolean' | 'null' | 'array' | 'object';

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
