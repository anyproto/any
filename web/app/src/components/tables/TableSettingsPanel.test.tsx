import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TableSettingsPanel } from './TableSettingsPanel';

const props = [
  { id: 'p_rating', name: 'Rating', kind: 'number' as const },
  { id: 'p_status', name: 'Status', kind: 'string' as const },
];

function renderPanel(
  overrides: Partial<Parameters<typeof TableSettingsPanel>[0]> = {},
) {
  const handlers = {
    onClose: vi.fn(),
    onViewLayoutChange: vi.fn(),
    onFilterChange: vi.fn(),
    onToggleProperty: vi.fn(),
    onMoveProperty: vi.fn(),
    onSort: vi.fn(),
  };
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TableSettingsPanel
        spaceId="spc-test"
        typeId="t_movie"
        props={props}
        hiddenPropIds={new Set()}
        viewLayout="table"
        filter=""
        sortKey="nav.pos"
        sortDir="asc"
        {...handlers}
        {...overrides}
      />
    </QueryClientProvider>,
  );
  return handlers;
}

describe('<TableSettingsPanel>', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('navigates settings screens and wires layout, filter, visibility, and sort controls', async () => {
    const handlers = renderPanel();

    await userEvent.click(screen.getByRole('button', { name: /layout/i }));
    await userEvent.click(await screen.findByRole('button', { name: /^list/i }));
    expect(handlers.onViewLayoutChange).toHaveBeenCalledWith('list');
    await userEvent.click(await screen.findByRole('button', { name: /^gallery/i }));
    expect(handlers.onViewLayoutChange).toHaveBeenCalledWith('gallery');

    await userEvent.click(screen.getByRole('button', { name: /back to view settings/i }));
    await userEvent.click(screen.getByRole('button', { name: /^filter/i }));
    fireEvent.change(await screen.findByRole('textbox', { name: /settings filter rows/i }), {
      target: { value: 'ali' },
    });
    expect(handlers.onFilterChange).toHaveBeenCalledWith('ali');

    await userEvent.click(screen.getByRole('button', { name: /back to view settings/i }));
    await userEvent.click(screen.getByRole('button', { name: /property visibility/i }));
    await userEvent.click(await screen.findByRole('button', { name: /hide rating/i }));
    expect(handlers.onToggleProperty).toHaveBeenCalledWith('p_rating', false);

    await userEvent.click(screen.getByRole('button', { name: /back to view settings/i }));
    await userEvent.click(screen.getByRole('button', { name: /^sort/i }));
    await userEvent.click(await screen.findByRole('button', { name: 'Rating' }));
    expect(handlers.onSort).toHaveBeenCalledWith('t_movie.p_rating', 'asc');
  });

  it('creates a property from the add-property screen and returns to properties', async () => {
    const calls: string[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
      if ((init?.method ?? 'GET').toUpperCase() === 'POST') {
        calls.push(`${url} ${String(init?.body)}`);
      }
      return new Response(JSON.stringify({ propId: 'p_new' }), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      });
    });

    renderPanel();
    await userEvent.click(screen.getByRole('button', { name: /^properties/i }));
    await userEvent.click(await screen.findByRole('button', { name: 'Add' }));
    await userEvent.type(await screen.findByRole('textbox', { name: /property name/i }), 'Deadline');
    await userEvent.click(await screen.findByRole('button', { name: 'Date' }));

    await waitFor(() => {
      expect(calls[0]).toContain('/spaces/spc-test/types/t_movie/properties');
      expect(calls[0]).toContain(
        JSON.stringify({ name: 'Deadline', kind: 'string', xKey: 'date' }),
      );
    });
    expect(await screen.findByText('Rating')).toBeInTheDocument();
  });
});
