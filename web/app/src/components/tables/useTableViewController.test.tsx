import { describe, it, expect, beforeEach, vi } from 'vitest';
import { act, renderHook, waitFor, type RenderHookOptions } from '@testing-library/react';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { activeObjectIdAtom, activeSpaceIdAtom, activeViewAtom } from '@/atoms';
import { useTableViewController } from './useTableViewController';

interface FetchCall {
  url: string;
  method: string;
  body?: string;
}

function setupFetch(): FetchCall[] {
  const calls: FetchCall[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    calls.push({
      url,
      method,
      body: typeof init?.body === 'string' ? init.body : undefined,
    });

    if (method === 'GET' && url.endsWith('/types/t_movie')) {
      return json({ id: 't_movie', name: 'Movies' });
    }
    if (method === 'POST' && url.endsWith('/objects/query')) {
      return json({
        records: [
          {
            id: 'obj-alien',
            any: { name: 'Alien', types: ['t_movie'] },
            nav: { type: 1, parentId: '', pos: 'A' },
          },
          {
            id: 'obj-primer',
            any: { name: 'Primer', types: ['t_movie'] },
            nav: { type: 1, parentId: '', pos: 'B' },
          },
        ],
      });
    }
    if (method === 'POST' && url.endsWith('/objects')) {
      return json({ objectId: 'obj-new' }, 201);
    }
    return json({});
  });
  return calls;
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function renderController() {
  const store = createStore();
  store.set(activeSpaceIdAtom, 'spc-test');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper: RenderHookOptions<unknown>['wrapper'] = ({ children }) => (
    <QueryClientProvider client={qc}>
      <Provider store={store}>{children}</Provider>
    </QueryClientProvider>
  );
  const result = renderHook(() => useTableViewController('t_movie'), {
    wrapper,
  });
  return { ...result, store };
}

describe('useTableViewController', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('loads type rows and debounces the client-side name filter', async () => {
    setupFetch();
    const { result } = renderController();

    await waitFor(() => {
      expect(result.current.rows.map((row) => row.id)).toEqual([
        'obj-alien',
        'obj-primer',
      ]);
    });

    act(() => result.current.setFilter('pri'));
    expect(result.current.visibleRows).toHaveLength(2);

    await waitFor(
      () => {
        expect(result.current.debouncedFilter).toBe('pri');
        expect(result.current.visibleRows.map((row) => row.id)).toEqual(['obj-primer']);
      },
      { timeout: 1000 },
    );
  });

  it('updates the query sort and creates a typed row through the shared creator', async () => {
    const calls = setupFetch();
    const { result, store } = renderController();

    await waitFor(() => expect(result.current.typeName).toBe('Movies'));
    act(() => result.current.setSort('any.name', 'desc'));
    expect(result.current.sortKey).toBe('any.name');
    expect(result.current.sortDir).toBe('desc');

    await act(async () => {
      await result.current.createRow();
    });

    const create = calls.find((call) => call.method === 'POST' && call.url.endsWith('/objects'));
    expect(create?.body).toBe(
      JSON.stringify({ nav: { type: 1, parentId: '' }, types: ['t_movie'] }),
    );
    expect(store.get(activeObjectIdAtom)).toBe('obj-new');
    expect(store.get(activeViewAtom)).toEqual({
      kind: 'object',
      objectId: 'obj-new',
      typeId: 't_movie',
    });
  });
});
