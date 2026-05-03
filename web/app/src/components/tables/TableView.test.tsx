import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TableView } from './TableView';
import { activeObjectIdAtom, activeSpaceIdAtom } from '@/atoms';
import { keyOf } from '@/shared';

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

    if (method === 'GET' && url.endsWith('/types/t_movie/properties')) {
      return json({
        properties: [
          { id: 'p_rating', name: 'Rating', kind: 'number' },
          { id: 'p_status', name: 'Status', kind: 'string' },
        ],
      });
    }

    if (method === 'POST' && url.endsWith('/types/t_movie/properties')) {
      return json({ propId: 'p_new' }, 201);
    }

    if (method === 'POST' && url.endsWith('/objects/query')) {
      return json({
        records: [
          {
            id: 'obj-1',
            any: { name: 'Alien', types: ['t_movie'] },
            nav: { type: 1, parentId: '', pos: 'A' },
            t_movie: { p_rating: 5, p_status: 'Watched' },
          },
          {
            id: 'obj-2',
            any: { name: 'Primer', types: ['t_movie'] },
            nav: { type: 1, parentId: '', pos: 'B' },
            t_movie: { p_rating: 4, p_status: 'Queued' },
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

function renderTable() {
  const store = createStore();
  store.set(activeSpaceIdAtom, 'spc-test');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <TableView typeId="t_movie" />
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, store };
}

beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('<TableView>', () => {
  it('renders database toolbar controls', async () => {
    setupFetch();
    renderTable();

    expect(await screen.findByRole('heading', { name: 'Movies' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'New row' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /table layout/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /list layout/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /filter/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /sort/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /view settings/i })).toBeInTheDocument();
  });

  it('switches to a polished list layout and opens rows from it', async () => {
    setupFetch();
    const { store } = renderTable();

    expect(await screen.findByRole('button', { name: 'Rating' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /list layout/i }));

    expect(screen.queryByRole('button', { name: 'Rating' })).not.toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Open Alien' })).toBeInTheDocument();
    expect(screen.getByText('Watched')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();

    expect(screen.getByRole('button', { name: 'Open Primer' })).toBeInTheDocument();
    await userEvent.click(screen.getByText('Queued'));
    expect(store.get(activeObjectIdAtom)).toBe('obj-2');
    expect(JSON.parse(localStorage.getItem('any.tables.viewLayouts.v1') ?? '{}')).toMatchObject({
      t_movie: 'list',
    });
  });

  it('edits the list title and icon locally from the table header', async () => {
    setupFetch();
    renderTable();

    await userEvent.click(await screen.findByRole('button', { name: 'Movies' }));
    const nameInput = await screen.findByRole('textbox', { name: /list name/i });
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, 'Films{Enter}');

    expect(await screen.findByRole('heading', { name: 'Films' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /add icon to films list/i }));
    await userEvent.click(await screen.findByRole('button', { name: /use film icon/i }));

    expect(JSON.parse(localStorage.getItem('any.type.meta.v1') ?? '{}')).toMatchObject({
      [keyOf('spc-test', 't_movie')]: { name: 'Films', icon: 'lucide:film' },
    });
  });

  it('searches the full Lucide catalog when choosing a list icon', async () => {
    setupFetch();
    renderTable();

    await userEvent.click(await screen.findByRole('button', { name: 'Movies' }));
    await userEvent.click(screen.getByRole('button', { name: /add icon to movies list/i }));
    await userEvent.type(await screen.findByRole('textbox', { name: /search icons/i }), 'zoom out');
    await userEvent.click(await screen.findByRole('button', { name: /use zoom out icon/i }));

    expect(JSON.parse(localStorage.getItem('any.type.meta.v1') ?? '{}')).toMatchObject({
      [keyOf('spc-test', 't_movie')]: { icon: 'lucide:zoom-out' },
    });
  });

  it('filters rows by name from the toolbar', async () => {
    setupFetch();
    renderTable();

    await screen.findByText('Alien');
    await userEvent.click(screen.getByRole('button', { name: /filter/i }));
    await userEvent.type(screen.getByRole('textbox', { name: /filter rows/i }), 'pri');

    await waitFor(() => {
      expect(screen.queryByText('Alien')).not.toBeInTheDocument();
    });
    expect(screen.getByText('Primer')).toBeInTheDocument();
  });

  it('hides and shows property columns from the properties menu', async () => {
    setupFetch();
    renderTable();

    expect(await screen.findByText('Rating')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /view settings/i }));
    await userEvent.click(await screen.findByRole('button', { name: /property visibility/i }));
    expect(await screen.findByRole('button', { name: /back to view settings/i })).toBeInTheDocument();
    await userEvent.click(await screen.findByRole('button', { name: 'Hide Rating' }));

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: 'Rating' })).not.toBeInTheDocument();
    });
    expect(screen.getByText(/1 hidden property/i)).toBeInTheDocument();
  });

  it('reorders property columns from the drag handles', async () => {
    setupFetch();
    renderTable();

    expect(await screen.findByRole('button', { name: 'Rating' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /view settings/i }));
    await userEvent.click(await screen.findByRole('button', { name: /property visibility/i }));

    setPropertyVisibilityRects([
      ['p_rating', 0],
      ['p_status', 36],
    ]);
    const statusGrip = await screen.findByRole('button', { name: 'Drag Status' });
    fireEvent.pointerDown(statusGrip, {
      button: 0,
      buttons: 1,
      pointerId: 1,
      clientY: 54,
    });
    fireEvent.pointerMove(statusGrip, {
      buttons: 1,
      pointerId: 1,
      clientY: 8,
    });
    fireEvent.pointerUp(statusGrip, {
      pointerId: 1,
      clientY: 8,
    });

    await waitFor(() => {
      const headerLabels = screen
        .getAllByRole('columnheader')
        .map((header) => headerSortLabel(header));
      expect(headerLabels.slice(0, 3)).toEqual(['Name', 'Status', 'Rating']);
    });
  });

  it('reorders property columns by dragging table headers', async () => {
    setupFetch();
    renderTable();

    expect(await screen.findByRole('button', { name: 'Rating' })).toBeInTheDocument();

    const dataTransfer = makeDataTransfer();
    fireEvent.dragStart(await screen.findByRole('button', { name: 'Drag Status column' }), {
      dataTransfer,
    });
    fireEvent.dragEnter(await screen.findByRole('button', { name: 'Drag Rating column' }), {
      dataTransfer,
    });
    fireEvent.drop(await screen.findByRole('button', { name: 'Drag Rating column' }), {
      dataTransfer,
    });

    const headerLabels = screen
      .getAllByRole('columnheader')
      .map((header) => headerSortLabel(header));
    expect(headerLabels.slice(0, 3)).toEqual(['Name', 'Status', 'Rating']);
  });

  it('resizes property columns from the header edge and persists the width', async () => {
    setupFetch();
    const { container } = renderTable();

    expect(await screen.findByRole('button', { name: 'Rating' })).toBeInTheDocument();
    const ratingCol = () =>
      container.querySelector<HTMLElement>('col[data-column-id="p_rating"]');
    expect(ratingCol).not.toBeNull();
    expect(ratingCol()).toHaveStyle({ width: '220px' });

    fireEvent.mouseDown(screen.getByRole('button', { name: 'Resize Rating column' }), {
      button: 0,
      clientX: 100,
    });
    fireEvent.mouseMove(window, { clientX: 160 });
    fireEvent.mouseUp(window);

    await waitFor(() => {
      expect(ratingCol()).toHaveStyle({ width: '280px' });
    });
    expect(JSON.parse(localStorage.getItem('any.tables.columnWidths.v1') ?? '{}')).toMatchObject({
      [keyOf('t_movie', 'p_rating')]: 280,
    });
  });

  it('creates a property from the nested properties screen', async () => {
    const calls = setupFetch();
    renderTable();

    await screen.findByText('Alien');
    await userEvent.click(screen.getByRole('button', { name: /view settings/i }));
    await userEvent.click(await screen.findByRole('button', { name: /^properties/i }));
    await userEvent.click(await screen.findByRole('button', { name: 'Add' }));
    await userEvent.type(await screen.findByRole('textbox', { name: /property name/i }), 'Deadline');
    await userEvent.click(await screen.findByRole('button', { name: 'Text' }));

    await waitFor(() => {
      const create = calls.find(
        (c) => c.method === 'POST' && c.url.endsWith('/types/t_movie/properties'),
      );
      expect(create).toBeDefined();
      expect(create?.body).toBe(JSON.stringify({ name: 'Deadline', kind: 'string' }));
    });
  });

  it('creates a new row from the header button and selects it', async () => {
    const calls = setupFetch();
    const { store } = renderTable();

    await userEvent.click(await screen.findByRole('button', { name: 'New row' }));

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(
        JSON.stringify({ nav: { type: 1, parentId: '' }, types: ['t_movie'] }),
      );
    });
    expect(store.get(activeObjectIdAtom)).toBe('obj-new');
  });
});

function makeDataTransfer() {
  const data = new Map<string, string>();
  return {
    effectAllowed: 'move',
    setData: vi.fn((type: string, value: string) => {
      data.set(type, value);
    }),
    getData: vi.fn((type: string) => data.get(type) ?? ''),
  };
}

function setPropertyVisibilityRects(entries: [string, number][]) {
  for (const [propId, top] of entries) {
    const row = document.querySelector<HTMLElement>(
      `[data-property-visibility-row="${propId}"]`,
    );
    if (!row) throw new Error(`Missing property visibility row ${propId}`);
    vi.spyOn(row, 'getBoundingClientRect').mockReturnValue({
      x: 0,
      y: top,
      top,
      left: 0,
      right: 280,
      bottom: top + 32,
      width: 280,
      height: 32,
      toJSON: () => ({}),
    });
  }
}

function headerSortLabel(header: HTMLElement) {
  return (
    within(header)
      .queryByRole('button', { name: /^(Name|Rating|Status)$/ })
      ?.textContent?.trim() ?? ''
  );
}
