import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { axe } from 'vitest-axe';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SpaceContents } from './SpaceContents';
import { activeSpaceIdAtom, activeObjectIdAtom } from '@/atoms/selection';

function renderWith({ activeSpaceId }: { activeSpaceId: string | null }) {
  // useSpace() inside the header issues GET /v1/spaces/:id; stub it.
  vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    if (url.includes('/v1/spaces/')) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            id: activeSpaceId ?? '',
            name: 'Anytype Team',
            status: 'active',
            ownRole: 'owner',
            createdAt: '2026-04-01T00:00:00Z',
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      );
    }
    return Promise.resolve(new Response('{}', { status: 200 }));
  });

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
    renderWith({ activeSpaceId: null });
    expect(screen.getByText(/pick a space/i)).toBeInTheDocument();
  });

  it('renders sectioned items for the active space', () => {
    renderWith({ activeSpaceId: 'spc-anytype-team' });
    expect(screen.getByLabelText(/space contents/i)).toBeInTheDocument();
    expect(screen.getByText('Chats')).toBeInTheDocument();
    expect(screen.getByText('My Favorites')).toBeInTheDocument();
  });

  it('selecting an item updates active object', async () => {
    const { store } = renderWith({ activeSpaceId: 'spc-anytype-team' });
    await userEvent.click(screen.getByRole('button', { name: /random/i }));
    expect(store.get(activeObjectIdAtom)).toBe('obj-random');
  });

  it('collapsible section toggles', async () => {
    renderWith({ activeSpaceId: 'spc-anytype-team' });
    const favoritesHeader = screen.getByRole('button', { name: /My Favorites/i });
    expect(favoritesHeader).toHaveAttribute('aria-expanded', 'true');
    await userEvent.click(favoritesHeader);
    expect(favoritesHeader).toHaveAttribute('aria-expanded', 'false');
  });

  it('has no axe violations (populated)', async () => {
    const { container } = renderWith({ activeSpaceId: 'spc-anytype-team' });
    expect(await axe(container)).toHaveNoViolations();
  });
});
