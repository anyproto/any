import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { axe } from 'vitest-axe';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectTree } from './ObjectTree';
import { activeObjectIdAtom } from '@/atoms/selection';
import { NAV_FOLDER, NAV_ITEM, type ObjectRecord } from '@/lib/api/objects';

const ROOT_ITEM_1: ObjectRecord = {
  id: 'obj-item-1',
  any: { name: 'Hello' },
  nav: { type: NAV_ITEM, parentId: '', pos: 'A' },
};
const ROOT_FOLDER: ObjectRecord = {
  id: 'obj-folder',
  any: { name: 'My Folder' },
  nav: { type: NAV_FOLDER, parentId: '', pos: 'B' },
};
const CHILD_ITEM: ObjectRecord = {
  id: 'obj-child',
  any: { name: 'Inside' },
  nav: { type: NAV_ITEM, parentId: 'obj-folder', pos: 'A' },
};

function mockResponses(map: Record<string, ObjectRecord[]>) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    if (init?.method === 'POST' && url.includes('/objects/query')) {
      const body = JSON.parse(init.body as string) as { filter?: { 'nav.parentId'?: string } };
      const parentId = body.filter?.['nav.parentId'] ?? '';
      const records = map[parentId] ?? [];
      return new Response(JSON.stringify({ records }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response('{}', { status: 200 });
  });
}

function renderTree({ map = { '': [] } as Record<string, ObjectRecord[]> } = {}) {
  mockResponses(map);
  const store = createStore();
  store.set(activeObjectIdAtom, null);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <ObjectTree spaceId="spc-test" />
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, store };
}

describe('<ObjectTree>', () => {
  it('renders the empty state when /objects/query returns []', async () => {
    renderTree({ map: { '': [] } });
    await screen.findByText(/no objects yet/i);
  });

  it('renders one row per root object', async () => {
    renderTree({ map: { '': [ROOT_ITEM_1, ROOT_FOLDER] } });
    await screen.findByText('Hello');
    await screen.findByText('My Folder');
  });

  it('clicking a row sets activeObjectIdAtom', async () => {
    const { store } = renderTree({ map: { '': [ROOT_ITEM_1] } });
    const row = await screen.findByRole('button', { name: 'Hello' });
    await userEvent.click(row);
    expect(store.get(activeObjectIdAtom)).toBe('obj-item-1');
  });

  it('expanding a folder fetches and renders its children', async () => {
    renderTree({
      map: {
        '': [ROOT_FOLDER],
        'obj-folder': [CHILD_ITEM],
      },
    });
    const expand = await screen.findByRole('button', { name: /expand/i });
    await userEvent.click(expand);
    await waitFor(() => {
      expect(screen.getByText('Inside')).toBeInTheDocument();
    });
  });

  it('shows the error state when query fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: { code: 'server.boom', message: 'oops' } }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const store = createStore();
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <Provider store={store}>
          <ObjectTree spaceId="spc-err" />
        </Provider>
      </QueryClientProvider>,
    );
    await screen.findByRole('alert');
    expect(screen.getByText(/Couldn/i)).toBeInTheDocument();
    expect(screen.getByText('server.boom')).toBeInTheDocument();
  });

  it('has no axe violations (populated)', async () => {
    const { container } = renderTree({ map: { '': [ROOT_ITEM_1, ROOT_FOLDER] } });
    await screen.findByText('Hello');
    expect(await axe(container)).toHaveNoViolations();
  });
});
