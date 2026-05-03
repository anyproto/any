import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SpaceContents } from './SpaceContents';
import { activeObjectIdAtom, activeSpaceIdAtom } from '@/atoms';

interface FetchCall {
  url: string;
  method: string;
  body?: string;
}

function setupFetchMocks(opts: {
  spaceName?: string;
  rootObjects?: { id: string; name: string; navType?: number }[];
  createReturnsId?: string;
  createTypeReturnsId?: string;
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

    // POST /v1/spaces/:id/types → create type/list
    if (method === 'POST' && /\/v1\/spaces\/[^/]+\/types$/.test(url)) {
      return new Response(JSON.stringify({ typeId: opts.createTypeReturnsId ?? 't-pages' }), {
        status: 201,
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
            nav: { type: o.navType ?? 1, parentId: '', pos: 'A' },
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
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

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
    const calls = setupFetchMocks({
      rootObjects: [],
      createReturnsId: 'obj-fresh',
      types: [{ id: 't-pages', name: 'Pages' }],
    });
    const { store } = renderWith('spc-test');
    const newBtn = await screen.findByRole('button', { name: /new object/i });
    await userEvent.click(newBtn);
    const newPage = await screen.findByRole('menuitem', { name: /new page/i });
    await userEvent.click(newPage);
    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(
        JSON.stringify({ nav: { type: 1, parentId: '' }, types: ['t-pages'] }),
      );
    });
    await waitFor(() => {
      expect(store.get(activeObjectIdAtom)).toBe('obj-fresh');
    });
  });

  it('+ New menu → New folder opens a dialog, creates a folder, and does not select it', async () => {
    const calls = setupFetchMocks({ rootObjects: [], createReturnsId: 'obj-folder' });
    const { store } = renderWith('spc-test');
    const newBtn = await screen.findByRole('button', { name: /new object/i });
    await userEvent.click(newBtn);
    const newFolder = await screen.findByRole('menuitem', { name: /new folder/i });
    await userEvent.click(newFolder);
    const dialog = await screen.findByRole('dialog', { name: /new folder/i });
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: 'Projects' } });
    fireEvent.submit(dialog.querySelector('form')!);

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(JSON.stringify({ nav: { type: 2, parentId: '' } }));
    });
    await waitFor(() => {
      const rename = calls.find(
        (c) => c.method === 'POST' && c.url.includes('/properties/obj-folder/base/any'),
      );
      expect(rename).toBeDefined();
      expect(rename?.body).toBe(JSON.stringify({ patch: { name: 'Projects' } }));
    });
    expect(dialog).not.toBeInTheDocument();
    expect(store.get(activeObjectIdAtom)).toBeNull();
  });

  it('space section + creates a root page and selects it', async () => {
    const calls = setupFetchMocks({
      rootObjects: [],
      createReturnsId: 'obj-section-page',
      types: [{ id: 't-pages', name: 'Pages' }],
    });
    const { store } = renderWith('spc-test');
    const sectionNewPage = await screen.findByRole('button', { name: /new page/i });
    await userEvent.click(sectionNewPage);

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(
        JSON.stringify({ nav: { type: 1, parentId: '' }, types: ['t-pages'] }),
      );
    });
    await waitFor(() => {
      expect(store.get(activeObjectIdAtom)).toBe('obj-section-page');
    });
  });

  it('space section folder-plus opens the same folder dialog', async () => {
    const calls = setupFetchMocks({ rootObjects: [], createReturnsId: 'obj-section-folder' });
    renderWith('spc-test');
    const sectionNewFolder = await screen.findByRole('button', { name: /new folder/i });
    await userEvent.click(sectionNewFolder);
    const dialog = await screen.findByRole('dialog', { name: /new folder/i });
    fireEvent.change(screen.getByLabelText(/^name$/i), { target: { value: 'Archive' } });
    fireEvent.submit(dialog.querySelector('form')!);

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(JSON.stringify({ nav: { type: 2, parentId: '' } }));
    });
    await waitFor(() => {
      const rename = calls.find(
        (c) => c.method === 'POST' && c.url.includes('/properties/obj-section-folder/base/any'),
      );
      expect(rename).toBeDefined();
      expect(rename?.body).toBe(JSON.stringify({ patch: { name: 'Archive' } }));
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
    // Lists section header still present (was "Types" — UX renamed
    // to "Lists" while the wire/code term stays "type").
    expect(await screen.findByRole('button', { name: /^lists/i })).toBeInTheDocument();
  });

  it('Lists section is expanded by default and shows user types only', async () => {
    setupFetchMocks({
      rootObjects: [],
      types: [
        { id: 't-recipe', name: 'Recipe' },
        { id: 't-builtin', name: 'Built-in', builtIn: true },
      ],
    });
    renderWith('spc-test');
    const listsHeader = await screen.findByRole('button', { name: /^lists/i });
    expect(listsHeader).toHaveAttribute('aria-expanded', 'true');
    await screen.findByText('Recipe');
    // Built-in stays hidden.
    expect(screen.queryByText('Built-in')).not.toBeInTheDocument();
  });

  it('deduplicates duplicate default Pages lists in the Lists section', async () => {
    setupFetchMocks({
      rootObjects: [],
      types: [
        { id: 't-pages-1', name: 'Pages' },
        { id: 't-pages-2', name: 'Pages' },
        { id: 't-pages-3', name: 'Pages' },
      ],
    });
    renderWith('spc-test');

    expect(await screen.findByText('Pages')).toBeInTheDocument();
    expect(screen.getAllByText('Pages')).toHaveLength(1);
  });

  it('list row hover action creates an object in that list and opens it', async () => {
    const calls = setupFetchMocks({
      rootObjects: [],
      createReturnsId: 'obj-recipe',
      types: [{ id: 't-recipe', name: 'Recipe' }],
    });
    const { store } = renderWith('spc-test');

    await userEvent.click(await screen.findByRole('button', { name: 'New Recipe' }));

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(
        JSON.stringify({ nav: { type: 1, parentId: '' }, types: ['t-recipe'] }),
      );
    });
    expect(store.get(activeObjectIdAtom)).toBe('obj-recipe');
  });

  it('renames, sets an icon for, and removes a list from the sidebar context menu', async () => {
    setupFetchMocks({
      rootObjects: [],
      types: [{ id: 't-recipe', name: 'Recipe' }],
    });
    renderWith('spc-test');

    const recipeRow = await screen.findByRole('button', { name: 'Recipe' });

    fireEvent.contextMenu(recipeRow);
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Rename' }));
    const nameInput = await screen.findByRole('textbox', { name: /^name$/i });
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, 'Films');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('button', { name: 'Films' })).toBeInTheDocument();

    fireEvent.contextMenu(screen.getByRole('button', { name: 'Films' }));
    await userEvent.click(await screen.findByRole('menuitem', { name: /change icon/i }));
    await userEvent.click(await screen.findByRole('tab', { name: /emojis/i }));
    await userEvent.click(await screen.findByRole('button', { name: /use 🎬 icon/i }));
    expect(screen.getByText('🎬')).toBeInTheDocument();

    fireEvent.contextMenu(screen.getByRole('button', { name: 'Films' }));
    await userEvent.click(await screen.findByRole('menuitem', { name: /delete list/i }));
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: 'Films' })).not.toBeInTheDocument();
    });
  });

  it('only creates the default Pages list once for an empty space', async () => {
    const calls = setupFetchMocks({
      rootObjects: [],
      types: [],
      createTypeReturnsId: 't-pages',
    });
    renderWith('spc-empty-pages');

    await waitFor(() => {
      expect(
        calls.filter((c) => c.method === 'POST' && /\/types$/.test(c.url)),
      ).toHaveLength(1);
    });
  });
});
