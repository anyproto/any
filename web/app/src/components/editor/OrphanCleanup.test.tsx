import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { OrphanCleanup } from './OrphanCleanup';
import { activeObjectIdAtom } from '@/atoms/selection';

interface FetchCall {
  url: string;
  method: string;
  body?: string;
}

function setup({ activeObjectId = 'obj-orphan' as string | null } = {}) {
  const calls: FetchCall[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    calls.push({
      url,
      method,
      body: typeof init?.body === 'string' ? init.body : undefined,
    });
    return Promise.resolve(new Response(null, { status: 204 }));
  });

  const store = createStore();
  store.set(activeObjectIdAtom, activeObjectId);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // Seed the children-of-root cache with the orphan so parentId lookup hits.
  qc.setQueryData(['objects', 'spc-test', 'children', ''], [
    { id: 'obj-orphan', any: { name: 'ghost' }, nav: { type: 1, parentId: '', pos: 'A' } },
  ]);

  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <OrphanCleanup spaceId="spc-test" objectId="obj-orphan" />
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, store, calls };
}

describe('<OrphanCleanup>', () => {
  it('renders a Remove from list button', () => {
    setup();
    expect(
      screen.getByRole('button', { name: /remove from list/i }),
    ).toBeInTheDocument();
  });

  it('clicking issues DELETE on the object', async () => {
    const { calls } = setup();
    await userEvent.click(screen.getByRole('button', { name: /remove from list/i }));
    await waitFor(() => {
      const del = calls.find(
        (c) => c.method === 'DELETE' && c.url === '/v1/spaces/spc-test/objects/obj-orphan',
      );
      expect(del).toBeDefined();
    });
  });

  it('clears active selection on success', async () => {
    const { store } = setup({ activeObjectId: 'obj-orphan' });
    await userEvent.click(screen.getByRole('button', { name: /remove from list/i }));
    await waitFor(() => {
      expect(store.get(activeObjectIdAtom)).toBeNull();
    });
  });
});
