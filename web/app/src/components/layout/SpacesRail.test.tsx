import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { axe } from 'vitest-axe';
import { Provider, createStore } from 'jotai';
import { SpacesRail } from './SpacesRail';
import { activeSpaceIdAtom } from '@/atoms/selection';
import { MOCK_SPACES } from '@/lib/mock-data';

function renderWith(initialActive: string | null = null) {
  const store = createStore();
  store.set(activeSpaceIdAtom, initialActive);
  const utils = render(
    <Provider store={store}>
      <SpacesRail />
    </Provider>,
  );
  return { ...utils, store };
}

describe('<SpacesRail>', () => {
  it('renders one button per mock space + new + settings', () => {
    renderWith();
    for (const space of MOCK_SPACES) {
      expect(screen.getByRole('button', { name: space.name })).toBeInTheDocument();
    }
    expect(screen.getByRole('button', { name: 'New space' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Settings' })).toBeDisabled();
  });

  it('clicking a space updates the active-space atom', async () => {
    const { store } = renderWith();
    const target = MOCK_SPACES[1]!;
    await userEvent.click(screen.getByRole('button', { name: target.name }));
    expect(store.get(activeSpaceIdAtom)).toBe(target.id);
  });

  it('marks the active space with aria-current', () => {
    const target = MOCK_SPACES[0]!;
    renderWith(target.id);
    const btn = screen.getByRole('button', { name: target.name });
    expect(btn).toHaveAttribute('aria-current', 'page');
  });

  it('has no axe violations', async () => {
    const { container } = renderWith();
    expect(await axe(container)).toHaveNoViolations();
  });
});
