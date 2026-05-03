import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '@/test/renderWithProviders';
import { DeleteListDialog, RenameListDialog } from './ListMetaControls';

describe('<RenameListDialog>', () => {
  it('trims the submitted name and closes after rename', async () => {
    const onRename = vi.fn();
    const onOpenChange = vi.fn();
    renderWithProviders(
      <RenameListDialog
        open
        initialName="Recipes"
        onOpenChange={onOpenChange}
        onRename={onRename}
      />,
    );

    await userEvent.clear(screen.getByLabelText('Name'));
    await userEvent.type(screen.getByLabelText('Name'), '  Movies  ');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onRename).toHaveBeenCalledWith('Movies');
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe('<DeleteListDialog>', () => {
  it('calls delete and closes the dialog', async () => {
    const onDelete = vi.fn();
    const onOpenChange = vi.fn();
    renderWithProviders(
      <DeleteListDialog
        open
        label="Recipes"
        onOpenChange={onOpenChange}
        onDelete={onDelete}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    expect(onDelete).toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
