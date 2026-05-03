import { describe, it, expect, vi, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import { listSpaces, useCreateSpace, useSpace, type SpaceInfo } from './spaces';

describe('listSpaces', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('drops soft-deleted entries even if the server returns them', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          spaces: [
            { id: 'a', status: 'active', ownRole: 'owner', createdAt: '' },
            { id: 'b', status: 'deleted', ownRole: 'owner', createdAt: '' },
            { id: 'c', status: 'joining', ownRole: 'reader', createdAt: '' },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const out = await listSpaces();
    expect(out.map((s) => s.id)).toEqual(['a', 'c']);
  });

  it('returns [] when the body has no spaces', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    expect(await listSpaces()).toEqual([]);
  });
});

describe('useCreateSpace', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('adds the created space to the list cache immediately', async () => {
    const existing: SpaceInfo = {
      id: 'spc-existing',
      name: 'Existing',
      status: 'active',
      ownRole: 'owner',
      createdAt: '2026-05-02T00:00:00Z',
    };
    const created: SpaceInfo = {
      id: 'spc-created',
      name: 'Created',
      status: 'active',
      ownRole: 'owner',
      createdAt: '2026-05-02T00:01:00Z',
    };
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(created), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    qc.setQueryData(['spaces'], [existing]);

    const { result } = renderHook(() => useCreateSpace(), {
      wrapper: ({ children }: { children: ReactNode }) =>
        createElement(QueryClientProvider, { client: qc }, children),
    });

    await result.current.mutateAsync({ name: 'Created' });

    await waitFor(() => {
      expect(qc.getQueryData<SpaceInfo[]>(['spaces'])?.map((s) => s.id)).toEqual([
        'spc-existing',
        'spc-created',
      ]);
    });
    expect(qc.getQueryData<SpaceInfo>(['spaces', 'spc-created'])).toEqual(created);
  });
});

describe('useSpace', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('seeds space detail from the cached spaces list', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const cached: SpaceInfo = {
      id: 'spc-existing',
      name: 'Existing',
      status: 'active',
      ownRole: 'owner',
      createdAt: '2026-05-02T00:00:00Z',
    };
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    qc.setQueryData(['spaces'], [cached]);

    const { result } = renderHook(() => useSpace('spc-existing'), {
      wrapper: ({ children }: { children: ReactNode }) =>
        createElement(QueryClientProvider, { client: qc }, children),
    });

    expect(result.current.data).toEqual(cached);
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
