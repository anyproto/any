import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectRow } from './ObjectRow';
import { renamingObjectIdAtom } from '@/atoms/edit';
import { activeObjectIdAtom } from '@/atoms/selection';
import { NAV_ITEM, type ObjectRecord } from '@/lib/api/objects';

const ITEM: ObjectRecord = {
  id: 'obj-1',
  any: { name: 'Hello' },
  nav: { type: NAV_ITEM, parentId: '', pos: 'A' },
};

interface FetchCall {
  url: string;
  method: string;
  body?: string;
}

function setup() {
  const calls: FetchCall[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    calls.push({
      url,
      method,
      body: typeof init?.body === 'string' ? init.body : undefined,
    });
    return Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
  });

  const store = createStore();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <ul>
          <ObjectRow spaceId="spc-test" obj={ITEM} depth={0} />
        </ul>
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, store, qc, calls };
}

describe('<ObjectRow>', () => {
  it('renders the title', () => {
    setup();
    expect(screen.getByText('Hello')).toBeInTheDocument();
  });

  it('clicking the title selects the row', async () => {
    const { store } = setup();
    await userEvent.click(screen.getByRole('button', { name: 'Hello' }));
    expect(store.get(activeObjectIdAtom)).toBe('obj-1');
  });

  it('F2 on the focused row enters rename mode', async () => {
    const { store } = setup();
    const row = screen.getByRole('treeitem');
    row.focus();
    await userEvent.keyboard('{F2}');
    expect(store.get(renamingObjectIdAtom)).toBe('obj-1');
    // Input is rendered with the original value selected.
    expect(screen.getByDisplayValue('Hello')).toBeInTheDocument();
  });

  it('Enter while renaming POSTs the patch', async () => {
    const { calls } = setup();
    const row = screen.getByRole('treeitem');
    row.focus();
    await userEvent.keyboard('{F2}');
    const input = screen.getByDisplayValue('Hello');
    await userEvent.clear(input);
    await userEvent.type(input, 'World{Enter}');
    await waitFor(() => {
      const patch = calls.find(
        (c) => c.method === 'POST' && c.url.includes('/properties/obj-1/base/any'),
      );
      expect(patch).toBeDefined();
      expect(patch?.body).toBe(JSON.stringify({ patch: { name: 'World' } }));
    });
  });

  it('Esc cancels rename without an API call', async () => {
    const { store, calls } = setup();
    const row = screen.getByRole('treeitem');
    row.focus();
    await userEvent.keyboard('{F2}');
    const input = screen.getByDisplayValue('Hello');
    await userEvent.clear(input);
    await userEvent.type(input, 'Cancelled');
    await userEvent.keyboard('{Escape}');
    expect(store.get(renamingObjectIdAtom)).toBeNull();
    expect(calls.find((c) => c.method === 'POST')).toBeUndefined();
  });

  it('Backspace on the focused row opens the delete dialog', async () => {
    setup();
    const row = screen.getByRole('treeitem');
    row.focus();
    await userEvent.keyboard('{Backspace}');
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText(/cannot be undone/i)).toBeInTheDocument();
  });
});
