import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { activeObjectIdAtom, activeViewAtom } from '@/atoms';
import { AddRow, DataRow, LoadMoreRow, SpacerRow } from './TableRows';

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
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  return calls;
}

function renderRows() {
  const store = createStore();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <table>
          <tbody>
            <DataRow
              spaceId="spc-test"
              typeId="t_movie"
              row={{
                id: 'obj-1',
                any: { name: 'Alien', types: ['t_movie'] },
                nav: { type: 1, parentId: '', pos: 'A' },
                t_movie: { p_rating: 5 },
              }}
              props={[{ id: 'p_rating', name: 'Rating', kind: 'number' }]}
            />
          </tbody>
        </table>
      </Provider>
    </QueryClientProvider>,
  );
  return { store };
}

describe('TableRows', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('opens a data row and commits edited cells through the property API', async () => {
    const calls = setupFetch();
    const { store } = renderRows();

    await userEvent.click(screen.getByRole('button', { name: /open alien/i }));
    expect(store.get(activeObjectIdAtom)).toBe('obj-1');
    expect(store.get(activeViewAtom)).toEqual({
      kind: 'object',
      objectId: 'obj-1',
      typeId: 't_movie',
    });

    await userEvent.click(screen.getByRole('button', { name: 'Alien' }));
    const nameInput = screen.getByDisplayValue('Alien');
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, 'Aliens{Enter}');

    await waitFor(() => {
      const patch = calls.find(
        (call) =>
          call.method === 'POST' && call.url.includes('/properties/obj-1/base/any'),
      );
      expect(patch?.body).toBe(JSON.stringify({ patch: { name: 'Aliens' } }));
    });
  });

  it('renders utility rows with stable table semantics', async () => {
    const onLoad = vi.fn();
    const onCreate = vi.fn();
    render(
      <table>
        <tbody>
          <SpacerRow height={24} colSpan={3} />
          <LoadMoreRow colSpan={3} loading={false} onLoad={onLoad} />
          <AddRow creating={false} typeName="Movie" propCount={1} onCreate={onCreate} />
        </tbody>
      </table>,
    );

    await userEvent.click(screen.getByRole('button', { name: /load more/i }));
    await userEvent.click(screen.getByRole('button', { name: /new movie/i }));

    expect(onLoad).toHaveBeenCalledTimes(1);
    expect(onCreate).toHaveBeenCalledTimes(1);
  });
});
