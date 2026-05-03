import { describe, it, expect, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectRow } from './ObjectRow';
import {
  activeObjectIdAtom,
  objectTitleDraftKey,
  objectTitleDraftsAtom,
  renamingObjectIdAtom,
} from '@/atoms';
import {
  selectedTreeIdsAtom,
  sendTreeExpansionSignalAtom,
  treeSelectionAnchorAtom,
  treeSelectionKeyboardEdgeAtom,
} from '@/atoms';
import { NAV_FOLDER, NAV_ITEM, type ObjectRecord } from '@/lib/api/objects';

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

function setup(obj: ObjectRecord = ITEM) {
  const calls: FetchCall[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    calls.push({
      url,
      method,
      body: typeof init?.body === 'string' ? init.body : undefined,
    });
    if (method === 'POST' && url.endsWith('/objects')) {
      return Promise.resolve(
        new Response(JSON.stringify({ objectId: 'obj-created-child' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }
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
          <ObjectRow spaceId="spc-test" obj={obj} depth={0} />
        </ul>
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, store, qc, calls };
}

function setupRows(objects: ObjectRecord[], parentId = '') {
  const calls: FetchCall[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    calls.push({
      url,
      method,
      body: typeof init?.body === 'string' ? init.body : undefined,
    });
    if (method === 'POST' && url.endsWith('/objects')) {
      return Promise.resolve(
        new Response(JSON.stringify({ objectId: 'obj-created-child' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }
    return Promise.resolve(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
  });

  const store = createStore();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(['objects', 'spc-test', 'children', parentId], objects);
  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <ul>
          {objects.map((obj) => (
            <ObjectRow
              key={obj.id}
              spaceId="spc-test"
              obj={obj}
              parentId={parentId}
              depth={0}
            />
          ))}
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

  it('prefers a live editor title draft over the cached title', async () => {
    const { store } = setup();
    act(() => {
      store.set(objectTitleDraftsAtom, {
        [objectTitleDraftKey('spc-test', 'obj-1')]: 'Book 2014',
      });
    });

    expect(await screen.findByText('Book 2014')).toBeInTheDocument();
    expect(screen.queryByText('Hello')).not.toBeInTheDocument();
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

  it('reacts to expand-all and collapse-all signals for folders', async () => {
    const folder: ObjectRecord = {
      id: 'folder-1',
      any: { name: 'Folder' },
      nav: { type: NAV_FOLDER, parentId: '', pos: 'A' },
    };
    const { store } = setup(folder);
    const row = screen.getByRole('treeitem');
    expect(row).toHaveAttribute('aria-expanded', 'false');

    act(() => {
      store.set(sendTreeExpansionSignalAtom, { spaceId: 'spc-test', action: 'expand' });
    });
    await waitFor(() => {
      expect(row).toHaveAttribute('aria-expanded', 'true');
    });

    act(() => {
      store.set(sendTreeExpansionSignalAtom, { spaceId: 'spc-test', action: 'collapse' });
    });
    await waitFor(() => {
      expect(row).toHaveAttribute('aria-expanded', 'false');
    });
  });

  it('folder hover action creates and opens a child object', async () => {
    const folder: ObjectRecord = {
      id: 'folder-1',
      any: { name: 'Folder' },
      nav: { type: NAV_FOLDER, parentId: '', pos: 'A' },
    };
    const { store, calls } = setup(folder);
    const row = screen.getByRole('treeitem');

    await userEvent.click(screen.getByRole('button', { name: 'Create object inside Folder' }));

    await waitFor(() => {
      const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/objects'));
      expect(create).toBeDefined();
      expect(create?.body).toBe(
        JSON.stringify({ nav: { type: 1, parentId: 'folder-1' } }),
      );
    });
    expect(row).toHaveAttribute('aria-expanded', 'true');
    expect(store.get(activeObjectIdAtom)).toBe('obj-created-child');
    expect(Array.from(store.get(selectedTreeIdsAtom))).toEqual(['obj-created-child']);
    expect(store.get(treeSelectionAnchorAtom)).toEqual({
      id: 'obj-created-child',
      parentId: 'folder-1',
    });
  });

  it('extends sibling selection with Shift+ArrowUp and Shift+ArrowDown', async () => {
    const objects: ObjectRecord[] = [
      {
        id: 'obj-a',
        any: { name: 'Alpha' },
        nav: { type: NAV_ITEM, parentId: '', pos: 'A' },
      },
      {
        id: 'obj-b',
        any: { name: 'Beta' },
        nav: { type: NAV_ITEM, parentId: '', pos: 'B' },
      },
      {
        id: 'obj-c',
        any: { name: 'Gamma' },
        nav: { type: NAV_ITEM, parentId: '', pos: 'C' },
      },
    ];
    const { store } = setupRows(objects);
    const rows = screen.getAllByRole('treeitem');

    rows[0].focus();
    await userEvent.keyboard('{Shift>}{ArrowDown}{/Shift}');

    expect(Array.from(store.get(selectedTreeIdsAtom))).toEqual(['obj-a', 'obj-b']);
    expect(store.get(treeSelectionAnchorAtom)?.id).toBe('obj-a');
    await waitFor(() => {
      expect(document.activeElement).toBe(rows[1]);
    });

    await userEvent.keyboard('{Shift>}{ArrowDown}{/Shift}');
    expect(Array.from(store.get(selectedTreeIdsAtom))).toEqual(['obj-a', 'obj-b', 'obj-c']);
    await waitFor(() => {
      expect(document.activeElement).toBe(rows[2]);
    });

    await userEvent.keyboard('{Shift>}{ArrowUp}{/Shift}');
    expect(Array.from(store.get(selectedTreeIdsAtom))).toEqual(['obj-a', 'obj-b']);
    await waitFor(() => {
      expect(document.activeElement).toBe(rows[1]);
    });
  });

  it('extends keyboard selection inside a folder using the rendered parent', async () => {
    const objects: ObjectRecord[] = [
      {
        id: 'obj-child-a',
        any: { name: 'Nested Alpha' },
        nav: { type: NAV_ITEM, pos: 'A' },
      },
      {
        id: 'obj-child-b',
        any: { name: 'Nested Beta' },
        nav: { type: NAV_ITEM, pos: 'B' },
      },
      {
        id: 'obj-child-c',
        any: { name: 'Nested Gamma' },
        nav: { type: NAV_ITEM, pos: 'C' },
      },
    ];
    const { store } = setupRows(objects, 'folder-1');
    const rows = screen.getAllByRole('treeitem');

    rows[0].focus();
    await userEvent.keyboard('{Shift>}{ArrowDown}{ArrowDown}{/Shift}');

    expect(Array.from(store.get(selectedTreeIdsAtom))).toEqual([
      'obj-child-a',
      'obj-child-b',
      'obj-child-c',
    ]);
    expect(store.get(treeSelectionAnchorAtom)?.id).toBe('obj-child-a');
    await waitFor(() => {
      expect(document.activeElement).toBe(rows[2]);
    });
  });

  it('shrinks an upward keyboard range when ArrowDown follows', async () => {
    const objects: ObjectRecord[] = [
      {
        id: 'obj-mokka',
        any: { name: 'mokka' },
        nav: { type: NAV_ITEM, pos: 'A' },
      },
      {
        id: 'obj-bokka',
        any: { name: 'Bokka' },
        nav: { type: NAV_ITEM, pos: 'B' },
      },
      {
        id: 'obj-yukka',
        any: { name: 'Yukka' },
        nav: { type: NAV_ITEM, pos: 'C' },
      },
      {
        id: 'obj-hello',
        any: { name: 'Hello' },
        nav: { type: NAV_ITEM, pos: 'D' },
      },
      {
        id: 'obj-revolut',
        any: { name: 'Revolut' },
        nav: { type: NAV_ITEM, pos: 'E' },
      },
    ];
    const { store } = setupRows(objects, 'folder-1');
    const rows = screen.getAllByRole('treeitem');

    rows[3].focus();
    await userEvent.keyboard('{Shift>}{ArrowUp}{ArrowUp}{ArrowUp}{/Shift}');
    expect(Array.from(store.get(selectedTreeIdsAtom))).toEqual([
      'obj-mokka',
      'obj-bokka',
      'obj-yukka',
      'obj-hello',
    ]);
    expect(store.get(treeSelectionKeyboardEdgeAtom)).toBe('obj-mokka');

    // Chromium can keep dispatching repeat key events to the row that
    // started the gesture; the stored edge should still drive shrink.
    rows[3].focus();
    await userEvent.keyboard('{Shift>}{ArrowDown}{/Shift}');

    expect(Array.from(store.get(selectedTreeIdsAtom))).toEqual([
      'obj-bokka',
      'obj-yukka',
      'obj-hello',
    ]);
    expect(store.get(treeSelectionKeyboardEdgeAtom)).toBe('obj-bokka');
  });
});
