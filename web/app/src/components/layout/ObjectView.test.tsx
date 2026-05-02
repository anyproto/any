import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { axe } from 'vitest-axe';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectView } from './ObjectView';
import { activeObjectIdAtom } from '@/atoms/selection';

function renderWith({ activeObjectId }: { activeObjectId: string | null }) {
  const store = createStore();
  store.set(activeObjectIdAtom, activeObjectId);
  // Standalone QueryClient so the empty-state HealthCard doesn't barf
  // on a missing provider.
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <ObjectView />
      </Provider>
    </QueryClientProvider>,
  );
}

describe('<ObjectView>', () => {
  it('shows the empty state when no object is selected', () => {
    renderWith({ activeObjectId: null });
    expect(screen.getByText(/Pick an item/i)).toBeInTheDocument();
  });

  it('shows the selected object id and a placeholder body', () => {
    renderWith({ activeObjectId: 'obj-random-12345678' });
    // Title is "Object obj-rand…" (first 8 chars + ellipsis).
    expect(
      screen.getByRole('heading', { level: 1, name: /object obj-rand/i }),
    ).toBeInTheDocument();
    // Full id rendered in the body.
    expect(screen.getByText('obj-random-12345678')).toBeInTheDocument();
  });

  it('breadcrumb is rendered when selected', () => {
    renderWith({ activeObjectId: 'obj-random-12345678' });
    expect(screen.getByLabelText('Breadcrumb')).toBeInTheDocument();
  });

  it('has no axe violations (selected)', async () => {
    const { container } = renderWith({ activeObjectId: 'obj-random-12345678' });
    expect(await axe(container)).toHaveNoViolations();
  });
});
