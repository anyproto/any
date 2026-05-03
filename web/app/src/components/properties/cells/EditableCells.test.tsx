import { fireEvent, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { activeSpaceIdAtom, activeViewAtom } from '@/atoms';
import { objectKeys, type ObjectRecord } from '@/lib/api/objects';
import { commitTextCell, renderCell } from '@/test/cellHarness';
import { renderWithProviders } from '@/test/renderWithProviders';
import { BoolCell } from './BoolCell';
import { DateCell } from './DateCell';
import { EmailCell } from './EmailCell';
import { LongTextCell } from './LongTextCell';
import { NumberCell } from './NumberCell';
import { RelationCell } from './RelationCell';
import { TextCell } from './TextCell';
import { UrlCell } from './UrlCell';

describe('editable property cells', () => {
  it('commits text edits with Enter', async () => {
    const onCommit = vi.fn();
    const { user } = renderCell(<TextCell value="Alien" onCommit={onCommit} />);

    await commitTextCell({ user, button: 'Alien', next: 'Blade Runner' });

    expect(onCommit).toHaveBeenCalledWith('Blade Runner');
  });

  it('commits valid numbers and ignores invalid ones', async () => {
    const onCommit = vi.fn();
    const { user, rerender } = renderCell(
      <NumberCell value={12} onCommit={onCommit} />,
    );

    await commitTextCell({ user, button: '12', next: '42' });
    expect(onCommit).toHaveBeenCalledWith(42);

    onCommit.mockClear();
    rerender(<NumberCell value={12} onCommit={onCommit} />);
    await commitTextCell({ user, button: '12', next: 'nope' });
    expect(onCommit).not.toHaveBeenCalled();
  });

  it('commits empty numeric input as null', async () => {
    const onCommit = vi.fn();
    const { user } = renderCell(<NumberCell value={12} onCommit={onCommit} />);

    await user.click(screen.getByRole('button', { name: '12' }));
    const input = screen.getByRole('textbox');
    await user.clear(input);
    await user.keyboard('{Enter}');

    expect(onCommit).toHaveBeenCalledWith(null);
  });

  it('commits long text edits from the textarea', async () => {
    const onCommit = vi.fn();
    const { user } = renderCell(
      <LongTextCell value={'First line\nSecond line'} onCommit={onCommit} />,
    );

    await commitTextCell({ user, button: 'First line', next: 'Updated note' });

    expect(onCommit).toHaveBeenCalledWith('Updated note');
  });

  it('commits date edits as ISO day strings', async () => {
    const onCommit = vi.fn();
    const { container, user } = renderCell(
      <DateCell value={null} onCommit={onCommit} />,
    );

    await user.click(screen.getByRole('button'));
    const input = container.querySelector('input[type="date"]');
    expect(input).toBeInstanceOf(HTMLInputElement);
    fireEvent.change(input!, { target: { value: '2026-05-03' } });
    fireEvent.keyDown(input!, { key: 'Enter' });

    expect(onCommit).toHaveBeenCalledWith('2026-05-03');
  });

  it('renders mailto links and commits email edits', async () => {
    const onCommit = vi.fn();
    const { user } = renderCell(
      <EmailCell value="ada@example.com" onCommit={onCommit} />,
    );

    expect(screen.getByRole('link', { name: 'Send email' })).toHaveAttribute(
      'href',
      'mailto:ada@example.com',
    );

    await commitTextCell({
      user,
      button: 'ada@example.com',
      next: 'grace@example.com',
    });
    expect(onCommit).toHaveBeenCalledWith('grace@example.com');
  });

  it('normalizes display links and commits URL edits', async () => {
    const onCommit = vi.fn();
    const { user } = renderCell(<UrlCell value="example.com" onCommit={onCommit} />);

    expect(screen.getByRole('link', { name: 'Open link' })).toHaveAttribute(
      'href',
      'https://example.com',
    );

    await commitTextCell({
      user,
      button: 'example.com',
      next: 'https://anytype.io',
    });
    expect(onCommit).toHaveBeenCalledWith('https://anytype.io');
  });

  it('toggles null booleans to true', async () => {
    const onCommit = vi.fn();
    const { user } = renderCell(<BoolCell value={null} onCommit={onCommit} />);

    await user.click(screen.getByRole('checkbox'));

    expect(onCommit).toHaveBeenCalledWith(true);
  });
});

describe('<RelationCell>', () => {
  const records: ObjectRecord[] = [
    { id: 'obj-self', any: { name: 'Current row' } },
    { id: 'obj-target', any: { name: 'Target page' } },
  ];

  it('filters out the current row and commits the picked target', async () => {
    const onCommit = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <RelationCell value="" rowId="obj-self" onCommit={onCommit} />,
      {
        initializeStore: (store) => {
          store.set(activeSpaceIdAtom, 'spc-test');
        },
        initializeQueryClient: (queryClient) => {
          queryClient.setQueryData(objectKeys.allItems('spc-test'), records);
        },
      },
    );

    await user.click(screen.getByRole('button'));

    expect(screen.queryByText('Current row')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Target page' }));

    expect(onCommit).toHaveBeenCalledWith('obj-target');
  });

  it('opens the linked object from the chip action', async () => {
    const onCommit = vi.fn();
    const user = userEvent.setup();
    const { store } = renderWithProviders(
      <RelationCell value="obj-target" rowId="obj-self" onCommit={onCommit} />,
      {
        initializeStore: (s) => {
          s.set(activeSpaceIdAtom, 'spc-test');
        },
        initializeQueryClient: (queryClient) => {
          queryClient.setQueryData(objectKeys.allItems('spc-test'), records);
        },
      },
    );

    await user.click(screen.getByRole('button', { name: 'Open Target page' }));

    expect(store.get(activeViewAtom)).toEqual({
      kind: 'object',
      objectId: 'obj-target',
    });
    expect(onCommit).not.toHaveBeenCalled();
  });
});
