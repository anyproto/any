import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MinWidthGuard } from './MinWidthGuard';

function setMatchMedia(matches: boolean) {
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    matches,
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }));
}

describe('<MinWidthGuard>', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders children when window is wide enough', () => {
    setMatchMedia(false);
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1200 });
    render(
      <MinWidthGuard>
        <div data-testid="child">x</div>
      </MinWidthGuard>,
    );
    expect(screen.getByTestId('child')).toBeInTheDocument();
  });

  it('renders the desktop-only notice below 800 px', () => {
    setMatchMedia(true);
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 600 });
    render(
      <MinWidthGuard>
        <div data-testid="child">x</div>
      </MinWidthGuard>,
    );
    expect(screen.queryByTestId('child')).not.toBeInTheDocument();
    expect(screen.getByText(/desktop-only/i)).toBeInTheDocument();
  });
});
