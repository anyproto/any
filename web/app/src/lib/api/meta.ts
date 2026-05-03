import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './client';

export interface HealthResponse {
  status: 'ok' | string;
  version: string;
  startedAt: string;
  account: string;
}

/** GET /v1/health */
export function getHealth(signal?: AbortSignal): Promise<HealthResponse> {
  const opts: { signal?: AbortSignal } = {};
  if (signal) opts.signal = signal;
  return apiFetch<HealthResponse>('/health', opts);
}

export function useHealth() {
  return useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => getHealth(signal),
  });
}
