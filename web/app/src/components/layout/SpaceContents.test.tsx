import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { axe } from 'vitest-axe';
import { Provider, createStore } from 'jotai';
import { SpaceContents } from './SpaceContents';
import { activeSpaceIdAtom, activeObjectIdAtom } from '@/atoms/selection';

function renderWith({ activeSpaceId }: { activeSpaceId: string | null }) {
  const store = createStore();
  store.set(activeSpaceIdAtom, activeSpaceId);
  const utils = render(
    <Provider store={store}>
      <SpaceContents />
    </Provider>,
  );
  return { ...utils, store };
}

describe('<SpaceContents>', () => {
  it('renders the empty state when no space is active', () => {
    renderWith({ activeSpaceId: null });
    expect(screen.getByText(/pick a space/i)).toBeInTheDocument();
  });

  it('renders sectioned items for the active space', () => {
    renderWith({ activeSpaceId: 'spc-anytype-team' });
    expect(screen.getByLabelText(/contents of/i)).toBeInTheDocument();
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
