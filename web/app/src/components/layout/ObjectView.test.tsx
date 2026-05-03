import { beforeEach, describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectView } from './ObjectView';
import {
  activeObjectIdAtom,
  activeSpaceIdAtom,
  objectTitleDraftKey,
  objectTitleDraftsAtom,
} from '@/atoms';

// BlockNote depends on browser APIs jsdom doesn't fully implement.
// Stub MarkdownEditor so this test only covers the header / pane shell.
vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: ({ objectId }: { objectId: string }) => (
    <div data-testid="editor-stub">editor for {objectId}</div>
  ),
}));

function renderWith({
  activeObjectId,
  activeSpaceId,
  spacesRailClosed = false,
  spaceContentsClosed = false,
  onToggleSpacesRail,
  onToggleSpaceContents,
  configureStore,
  objectName = 'Loaded title',
}: {
  activeObjectId: string | null;
  activeSpaceId: string | null;
  spacesRailClosed?: boolean;
  spaceContentsClosed?: boolean;
  onToggleSpacesRail?: () => void;
  onToggleSpaceContents?: () => void;
  configureStore?: (store: ReturnType<typeof createStore>) => void;
  objectName?: string;
}) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    if (method === 'POST' && url.endsWith('/objects/query')) {
      return json({
        records: activeObjectId
          ? [{ id: activeObjectId, any: { name: objectName } }]
          : [],
      });
    }
    return json({ ok: true });
  });

  const store = createStore();
  store.set(activeSpaceIdAtom, activeSpaceId);
  store.set(activeObjectIdAtom, activeObjectId);
  configureStore?.(store);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rendered = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <ObjectView
          spacesRailClosed={spacesRailClosed}
          spaceContentsClosed={spaceContentsClosed}
          onToggleSpacesRail={onToggleSpacesRail}
          onToggleSpaceContents={onToggleSpaceContents}
        />
      </Provider>
    </QueryClientProvider>,
  );
  return { ...rendered, store };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('<ObjectView>', () => {
  it('shows the empty state when no object is selected', () => {
    renderWith({ activeObjectId: null, activeSpaceId: 'spc-test' });
    expect(screen.getByText(/Pick an item/i)).toBeInTheDocument();
  });

  it('renders the editor when an object is selected', async () => {
    renderWith({
      activeObjectId: 'obj-abc-12345678',
      activeSpaceId: 'spc-test',
      objectName: 'Book 2014',
    });
    expect(await screen.findByTestId('editor-stub')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByLabelText('Breadcrumb')).toHaveTextContent('Book 2014');
    });
  });

  it('shows a live editor title draft in the header breadcrumb', async () => {
    renderWith({
      activeObjectId: 'obj-abc-12345678',
      activeSpaceId: 'spc-test',
      objectName: 'Old title',
      configureStore: (store) => {
        store.set(objectTitleDraftsAtom, {
          [objectTitleDraftKey('spc-test', 'obj-abc-12345678')]: 'Book 2014',
        });
      },
    });

    expect(await screen.findByTestId('editor-stub')).toBeInTheDocument();
    expect(screen.getByLabelText('Breadcrumb')).toHaveTextContent('Book 2014');
  });

  it('does not render the editor without an active space', () => {
    renderWith({ activeObjectId: 'obj-abc', activeSpaceId: null });
    expect(screen.queryByTestId('editor-stub')).not.toBeInTheDocument();
  });

  it('renders two independent sidebar toggle buttons in the header', async () => {
    const onToggleSpacesRail = vi.fn();
    const onToggleSpaceContents = vi.fn();
    renderWith({
      activeObjectId: null,
      activeSpaceId: 'spc-test',
      onToggleSpacesRail,
      onToggleSpaceContents,
    });

    await userEvent.click(screen.getByRole('button', { name: /hide spaces panel/i }));
    await userEvent.click(screen.getByRole('button', { name: /hide object sidebar/i }));
    expect(onToggleSpacesRail).toHaveBeenCalledTimes(1);
    expect(onToggleSpaceContents).toHaveBeenCalledTimes(1);
  });

  it('labels sidebar buttons as show when panels are closed', () => {
    renderWith({
      activeObjectId: null,
      activeSpaceId: 'spc-test',
      spacesRailClosed: true,
      spaceContentsClosed: true,
    });

    expect(screen.getByRole('button', { name: /show spaces panel/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /show object sidebar/i })).toBeInTheDocument();
  });

  it('uses the header arrows for pane navigation history', async () => {
    const { store } = renderWith({
      activeObjectId: null,
      activeSpaceId: 'spc-nav',
      configureStore: (s) => {
        s.set(activeObjectIdAtom, 'obj-nav-1');
        s.set(activeObjectIdAtom, 'obj-nav-2');
      },
    });

    const back = screen.getByRole('button', { name: 'Back' });
    const forward = screen.getByRole('button', { name: 'Forward' });
    expect(back).not.toBeDisabled();
    expect(forward).toBeDisabled();

    await userEvent.click(back);
    expect(store.get(activeObjectIdAtom)).toBe('obj-nav-1');
    expect(forward).not.toBeDisabled();

    await userEvent.click(forward);
    expect(store.get(activeObjectIdAtom)).toBe('obj-nav-2');
  });
});
