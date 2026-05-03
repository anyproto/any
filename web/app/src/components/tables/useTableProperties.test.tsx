import { describe, it, expect, beforeEach, vi } from 'vitest';
import { act, renderHook, waitFor, type RenderHookOptions } from '@testing-library/react';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { keyOf } from '@/shared';
import { useTableProperties } from './useTableProperties';

function setupFetch() {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    if (method === 'GET' && url.endsWith('/types/t_movie/properties')) {
      return new Response(
        JSON.stringify({
          properties: [
            { id: 'p_rating', name: 'Rating', kind: 'number' },
            { id: 'p_status', name: 'Status', kind: 'string' },
            { id: 'p_relation', name: 'Relation', kind: 'string', xKey: 'relation' },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
}

function renderProperties() {
  const store = createStore();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper: RenderHookOptions<unknown>['wrapper'] = ({ children }) => (
    <QueryClientProvider client={qc}>
      <Provider store={store}>{children}</Provider>
    </QueryClientProvider>
  );
  return renderHook(() => useTableProperties('spc-test', 't_movie'), { wrapper });
}

describe('useTableProperties', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('owns property visibility, order, and local width state', async () => {
    setupFetch();
    const { result } = renderProperties();

    await waitFor(() => {
      expect(result.current.userProps.map((prop) => prop.id)).toEqual([
        'p_rating',
        'p_status',
        'p_relation',
      ]);
    });

    act(() => result.current.toggleProperty('p_status', false));
    expect(result.current.visibleProps.map((prop) => prop.id)).toEqual([
      'p_rating',
      'p_relation',
    ]);
    expect(result.current.hiddenPropIds.has('p_status')).toBe(true);

    act(() => result.current.toggleProperty('p_status', true));
    act(() => result.current.moveProperty('p_status', 'p_rating'));
    expect(result.current.userProps.map((prop) => prop.id)).toEqual([
      'p_status',
      'p_rating',
      'p_relation',
    ]);

    act(() => result.current.resizeColumn('p_rating', 275, 220));
    expect(result.current.propertyColumnWidths.p_rating).toBe(275);
    expect(JSON.parse(localStorage.getItem('any.tables.columnWidths.v1') ?? '{}')).toMatchObject({
      [keyOf('t_movie', 'p_rating')]: 275,
    });
    expect(result.current.tablePixelWidth).toBeGreaterThan(0);
  });
});
