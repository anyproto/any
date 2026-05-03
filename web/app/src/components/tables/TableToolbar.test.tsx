import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TableToolbar } from './TableToolbar';

const props = [
  { id: 'p_rating', name: 'Rating', kind: 'number' as const },
  { id: 'p_status', name: 'Status', kind: 'string' as const },
];

function renderToolbar(overrides: Partial<Parameters<typeof TableToolbar>[0]> = {}) {
  const handlers = {
    onLayoutChange: vi.fn(),
    onFilterChange: vi.fn(),
    onFilterOpenChange: vi.fn(),
    onSort: vi.fn(),
    onSettingsOpenChange: vi.fn(),
    onCreate: vi.fn(),
  };
  render(
    <TableToolbar
      typeId="t_movie"
      props={props}
      layout="table"
      filter=""
      filterOpen={false}
      hiddenPropCount={0}
      sortKey="nav.pos"
      sortDir="asc"
      settingsOpen={false}
      createPending={false}
      {...handlers}
      {...overrides}
    />,
  );
  return handlers;
}

describe('<TableToolbar>', () => {
  it('routes layout, filter, settings, create, and sort actions to callbacks', async () => {
    const handlers = renderToolbar();

    await userEvent.click(screen.getByRole('button', { name: /list layout/i }));
    expect(handlers.onLayoutChange).toHaveBeenCalledWith('list');

    await userEvent.click(screen.getByRole('button', { name: /gallery layout/i }));
    expect(handlers.onLayoutChange).toHaveBeenCalledWith('gallery');

    await userEvent.click(screen.getByRole('button', { name: /filter/i }));
    expect(handlers.onFilterOpenChange).toHaveBeenCalledWith(true);

    await userEvent.click(screen.getByRole('button', { name: /view settings/i }));
    expect(handlers.onSettingsOpenChange).toHaveBeenCalledWith(true);

    await userEvent.click(screen.getByRole('button', { name: /new row/i }));
    expect(handlers.onCreate).toHaveBeenCalledTimes(1);

    await userEvent.click(screen.getByRole('button', { name: /^sort/i }));
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Rating' }));
    expect(handlers.onSort).toHaveBeenCalledWith('t_movie.p_rating', 'asc');
  });

  it('shows and clears the filter input when filter state is active', async () => {
    const handlers = renderToolbar({ filter: 'ali', filterOpen: true, hiddenPropCount: 2 });

    expect(screen.getByRole('textbox', { name: /filter rows/i })).toHaveValue('ali');
    expect(screen.getByText(/2 hidden properties/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /clear filter/i }));
    expect(handlers.onFilterChange).toHaveBeenCalledWith('');
  });
});
