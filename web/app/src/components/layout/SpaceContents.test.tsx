import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SpaceContents } from './SpaceContents';
import { activeSpaceIdAtom, activeObjectIdAtom } from '@/atoms/selection';

interface FetchCall {
  url: string;
  method: string;
  body?: string;
}

function setupFetchMocks(opts: {
  spaceName?: string;
  rootObjects?: { id: string; name: string }[];
  createReturnsId?: string;
  types?: { id: string; name: string; builtIn?: boolean }[];
}): FetchCall[] {
  const calls: FetchCall[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    calls.push({
      url,
      method,
      body: typeof init?.body === 'string' ? init.body : undefined,
    });

    // GET /v1/spaces/:id/types → list types
    if (method === 'GET' && /\/v1\/spaces\/[^/]+\/types$/.test(url)) {
      return new Response(JSON.stringify({ types: opts.types ?? [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }

    // GET /v1/spaces/:id  → space metadata
    if (method === 'GET' && /\/v1\/spaces\/[^/]+$/.test(url)) {
      return new Response(
        JSON.stringify({
          id: 'spc-test',
          name: opts.spaceName ?? 'Test Space',
          status: 'active',
          ownRole: 'owner',
          createdAt: '2026-04-01T00:00:00Z',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    }

    // POST /v1/spaces/:id/objects/query → root objects (also used for type counts)
    if (method === 'POST' && url.endsWith('/objects/query')) {
      return new Response(
        JSON.stringify({
          records: (opts.rootObjects ?? []).map((o) => ({
            id: o.id,
            any: { name: o.name },
            nav: { type: 1, parentId: '', pos: 'A' },
          })),
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    }

    // POST /v1/spaces/:id/objects → create
    if (method === 'POST' && /\/objects$/.test(url)) {
      return new Response(JSON.stringify({ objectId: opts.createReturnsId ?? 'obj-new' }), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      });
    }

    return new Response('{}', { status: 200 });
  });
  return calls;
}

function renderWith(activeSpaceId: string | null) {
  const store = createStore();
  store.set(activeSpaceIdAtom, activeSpaceId);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <SpaceContents />
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, store, qc };
}

describe('<SpaceContents>', () => {
  it('renders the empty state when no space is active', () => {
    setupFetchMocks({});
    renderWith(null);
    expect(screen.getByText(/pick a space/i)).toBeInTheDocument();
  });

  it('renders the space name in the header', async () => {
    setupFetchMocks({ spaceName: 'My Personal' });
    renderWith('spc-test');
    // The space name now appears in both the header (chevron-menu trigger)
    // and the Pages section label, so we expect at least two matches.
    const matches = await screen.findAllByText('My Personal');
    expect(matches.length).toBeGreaterThanOrEqual(2);
  });

  it('renders the object tree empty state for an empty space', async () => {
    setupFetchMocks({ rootObjects: [] });
    renderWith('spc-test');
    await screen.findByText(/no objects yet/i);
  });

  it('renders rows for root objects', async () => {
    setupFetchMocks({ rootObjects: [{ id: 'obj-1', name: 'Hello' }] });
    renderWith('spc-test');
    await screen.findByText('Hello');
  });

  it('+ New menu → New page POSTs /objects and selects the new id', async () => {
    const calls = setupFetchMocks({ rootObjects: [], createReturnsId: 'obj-fresh' });
    const { store } = renderWith('spc-test');
    const newBtn = await screen.findByRole('button', { name: /new object/i });
    await userEvent.click(newBtn);
    const newPage = await screen.findByRole('menuitem', { name: /new page/i });
    await userEvent.click(newPage);
    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      // Page → no nav body
      expect(create?.body).toBe('{}');
    });
    await waitFor(() => {
      expect(store.get(activeObjectIdAtom)).toBe('obj-fresh');
    });
  });

  it('+ New menu → New folder POSTs {nav:{type:2}}', async () => {
    const calls = setupFetchMocks({ rootObjects: [], createReturnsId: 'obj-folder' });
    renderWith('spc-test');
    const newBtn = await screen.findByRole('button', { name: /new object/i });
    await userEvent.click(newBtn);
    const newFolder = await screen.findByRole('menuitem', { name: /new folder/i });
    await userEvent.click(newFolder);
    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(JSON.stringify({ nav: { type: 2 } }));
    });
  });

  it('Pages section uses the space name as its label', async () => {
    setupFetchMocks({ spaceName: 'My Personal', rootObjects: [] });
    renderWith('spc-test');
    // The header button is labelled "Edit space" (the click handler opens
    // the rename/icon dialog — PR-018) but its visible text is the
    // space name; the Pages section header is its own button labelled
    // by the same name.
    expect(
      await screen.findByRole('button', { name: /^my personal$/i }),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole('button', { name: /edit space/i }),
    ).toBeInTheDocument();
    // Types section header still present.
    expect(await screen.findByRole('button', { name: /^types/i })).toBeInTheDocument();
  });

  it('Types section is collapsed by default; expanding shows user types only', async () => {
    setupFetchMocks({
      rootObjects: [],
      types: [
        { id: 't-recipe', name: 'Recipe' },
        { id: 't-builtin', name: 'Built-in', builtIn: true },
      ],
    });
    renderWith('spc-test');
    // Collapsed → user type not visible yet.
    const typesHeader = await screen.findByRole('button', { name: /^types/i });
    expect(typesHeader).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('Recipe')).not.toBeInTheDocument();

    await userEvent.click(typesHeader);
    expect(typesHeader).toHaveAttribute('aria-expanded', 'true');
    await screen.findByText('Recipe');
    // Built-in stays hidden.
    expect(screen.queryByText('Built-in')).not.toBeInTheDocument();
  });
});
