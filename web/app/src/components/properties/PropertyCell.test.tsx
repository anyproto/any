import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { PropertyDef } from '@/lib/api/types';
import { PropertyCell } from './PropertyCell';

function prop(kind: PropertyDef['kind'], xKey?: string): PropertyDef {
  return { id: 'p_value', name: 'Value', kind, xKey };
}

describe('<PropertyCell>', () => {
  it('renders and commits text values', async () => {
    const onCommit = vi.fn();
    render(
      <PropertyCell
        prop={prop('string')}
        value="Alien"
        rowId="obj-1"
        onCommit={onCommit}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Alien' }));
    const input = screen.getByRole('textbox');
    await userEvent.clear(input);
    await userEvent.type(input, 'Blade Runner{Enter}');

    expect(onCommit).toHaveBeenCalledWith('Blade Runner');
  });

  it('toggles boolean values', async () => {
    const onCommit = vi.fn();
    render(
      <PropertyCell
        prop={prop('boolean')}
        value={false}
        rowId="obj-1"
        onCommit={onCommit}
      />,
    );

    await userEvent.click(screen.getByRole('checkbox'));

    expect(onCommit).toHaveBeenCalledWith(true);
  });

  it('filters tags to strings and commits normalized edits', async () => {
    const onCommit = vi.fn();
    render(
      <PropertyCell
        prop={prop('array', 'tags')}
        value={['sci-fi', 7, 'classic']}
        rowId="obj-1"
        onCommit={onCommit}
      />,
    );

    expect(screen.getByText('sci-fi')).toBeInTheDocument();
    expect(screen.getByText('classic')).toBeInTheDocument();
    expect(screen.queryByText('7')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button'));
    const input = screen.getByRole('textbox');
    await userEvent.clear(input);
    await userEvent.type(input, 'watched, sci-fi, watched{Enter}');

    expect(onCommit).toHaveBeenCalledWith(['watched', 'sci-fi']);
  });
});
