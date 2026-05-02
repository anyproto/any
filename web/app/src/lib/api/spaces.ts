import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './client';

/**
 * Wire shape mirrors internal/api/spaces.go (SpaceInfo). Keep these
 * fields aligned with the server type.
 */
export interface SpaceInfo {
  id: string;
  type?: string;
  name?: string;
  description?: string;
  iconCid?: string;
  status: SpaceStatus;
  ownRole: SpacePermission;
  createdAt: string;
}

export type SpaceStatus =
  | 'unknown'
  | 'active'
  | 'joining'
  | 'leaving'
  | 'deleted'
  | 'remote_dead';

export type SpacePermission =
  | 'none'
  | 'reader'
  | 'guest'
  | 'writer'
  | 'admin'
  | 'owner';

export interface SpaceListResponse {
  spaces: SpaceInfo[];
}

export interface SpaceCreateRequest {
  name?: string;
  description?: string;
  iconCid?: string;
  spaceType?: string;
}

const KEYS = {
  all: ['spaces'] as const,
  one: (id: string) => ['spaces', id] as const,
};

// ---------------- Plain functions (testable without React) ----------

export async function listSpaces(signal?: AbortSignal): Promise<SpaceInfo[]> {
  const opts: { signal?: AbortSignal } = {};
  if (signal) opts.signal = signal;
  const body = await apiFetch<SpaceListResponse>('/spaces', opts);
  // Defense in depth: the server already filters soft-deleted entries
  // (PR-016) but we filter again here so older / stale servers don't
  // surface zombies in the rail.
  return (body.spaces ?? []).filter((s) => s.status !== 'deleted');
}

export async function getSpace(id: string, signal?: AbortSignal): Promise<SpaceInfo> {
  const opts: { signal?: AbortSignal } = {};
  if (signal) opts.signal = signal;
  return apiFetch<SpaceInfo>(`/spaces/${encodeURIComponent(id)}`, opts);
}

export async function createSpace(req: SpaceCreateRequest): Promise<SpaceInfo> {
  return apiFetch<SpaceInfo>('/spaces', { method: 'POST', json: req });
}

export async function deleteSpace(id: string): Promise<void> {
  await apiFetch<void>(`/spaces/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

// ---------------- React hooks ---------------------------------------

export function useSpaces() {
  return useQuery({
    queryKey: KEYS.all,
    queryFn: ({ signal }) => listSpaces(signal),
  });
}

export function useSpace(id: string | null) {
  return useQuery({
    queryKey: id ? KEYS.one(id) : (['spaces', '__none__'] as const),
    queryFn: ({ signal }) => getSpace(id!, signal),
    enabled: id != null,
  });
}

export function useCreateSpace() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: createSpace,
    onSuccess: (created) => {
      // Invalidate list so the new space appears in the rail.
      void qc.invalidateQueries({ queryKey: KEYS.all });
      // Seed the per-id cache so navigating to it doesn't refetch.
      qc.setQueryData(KEYS.one(created.id), created);
    },
  });
}

export function useDeleteSpace() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: deleteSpace,
    onSuccess: (_void, id) => {
      void qc.invalidateQueries({ queryKey: KEYS.all });
      qc.removeQueries({ queryKey: KEYS.one(id) });
    },
  });
}
