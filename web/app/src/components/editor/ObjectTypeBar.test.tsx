import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { activeViewAtom, type ActiveView } from '@/atoms';
import { ObjectTypeBar } from './ObjectTypeBar';

function setupFetch() {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();

    if (method === 'GET' && url.endsWith('/types')) {
      return json({ types: [{ id: 't_movie', name: 'Movies' }] });
    }
    if (method === 'GET' && url.endsWith('/types/t_movie')) {
      return json({ id: 't_movie', name: 'Movies' });
    }
    if (method === 'GET' && url.endsWith('/types/t_movie/properties')) {
      return json({
        properties: [
          { id: 'p_rating', name: 'Rating', kind: 'number' },
          { id: 'p_director', name: 'Director', kind: 'string' },
        ],
      });
    }
    if (method === 'POST' && url.endsWith('/objects/query')) {
      return json({
        records: [
          {
            id: 'obj-stalker',
            any: { name: 'Stalker', types: ['t_movie'] },
            nav: { type: 1, parentId: '', pos: 'A' },
            t_movie: {
              p_rating: 10,
              p_director: 'Andrei Tarkovsky',
            },
          },
        ],
      });
    }
    return json({});
  });
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function renderBar(activeView: ActiveView) {
  const store = createStore();
  store.set(activeViewAtom, activeView);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <ObjectTypeBar spaceId="spc-test" objectId="obj-stalker" />
      </Provider>
    </QueryClientProvider>,
  );
  return { store };
}

describe('<ObjectTypeBar>', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
    setupFetch();
  });

  it('auto-expands the source list chip when an object is opened from a list', async () => {
    renderBar({ kind: 'object', objectId: 'obj-stalker', typeId: 't_movie' });

    expect(await screen.findByRole('button', { name: 'Movies' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(await screen.findByText('Rating')).toBeInTheDocument();
    expect(screen.getByText('Director')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '10' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Andrei Tarkovsky' })).toBeInTheDocument();
  });

  it('keeps plain object navigation collapsed until the user opens a chip', async () => {
    renderBar({ kind: 'object', objectId: 'obj-stalker' });

    const chip = await screen.findByRole('button', { name: 'Movies' });
    expect(chip).toHaveAttribute('aria-pressed', 'false');
    expect(screen.queryByText('Rating')).not.toBeInTheDocument();

    await userEvent.click(chip);
    expect(await screen.findByText('Rating')).toBeInTheDocument();
  });
});
