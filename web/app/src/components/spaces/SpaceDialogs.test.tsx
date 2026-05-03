import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  activeSpaceIdAtom,
  spaceMetaOverridesAtom,
} from '@/atoms';
import type { SpaceInfo } from '@/lib/api/spaces';
import { renderWithProviders } from '@/test/renderWithProviders';
import { CreateSpaceDialog } from './CreateSpaceDialog';
import { DeleteSpaceDialog } from './DeleteSpaceDialog';
import { EditSpaceDialog } from './EditSpaceDialog';

function activeSpace(): SpaceInfo {
  return {
    id: 'spc-delete',
    name: 'Delete Space',
    status: 'active',
    ownRole: 'owner',
    createdAt: '2026-05-03T00:00:00Z',
  };
}

describe('<CreateSpaceDialog>', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('posts name and description, then selects the created space', async () => {
    const created: SpaceInfo = {
      id: 'spc-created',
      name: 'Created Space',
      description: 'Research notes',
      status: 'active',
      ownRole: 'owner',
      createdAt: '2026-05-03T00:00:00Z',
    };
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(created), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const onOpenChange = vi.fn();
    const { store } = renderWithProviders(
      <CreateSpaceDialog open onOpenChange={onOpenChange} />,
    );

    await userEvent.type(screen.getByLabelText('Name'), '  Created Space  ');
    await userEvent.type(
      screen.getByLabelText('Description (optional)'),
      '  Research notes  ',
    );
    await userEvent.click(screen.getByRole('button', { name: 'Create space' }));

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(fetchSpy).toHaveBeenCalledWith(
      '/v1/spaces',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          name: 'Created Space',
          description: 'Research notes',
        }),
      }),
    );
    expect(store.get(activeSpaceIdAtom)).toBe('spc-created');
  });
});

describe('<EditSpaceDialog>', () => {
  it('saves local name and icon overrides', async () => {
    const onClose = vi.fn();
    const { store } = renderWithProviders(
      <EditSpaceDialog
        spaceId="spc-edit"
        serverName="Server Space"
        onClose={onClose}
      />,
      {
        initializeStore: (s) => {
          s.set(spaceMetaOverridesAtom, {
            'spc-edit': { name: 'Old name', icon: 'O' },
          });
        },
      },
    );

    await userEvent.clear(screen.getByLabelText('Name'));
    await userEvent.type(screen.getByLabelText('Name'), '  Local Name  ');
    await userEvent.clear(screen.getByLabelText('Icon'));
    await userEvent.type(screen.getByLabelText('Icon'), 'L');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onClose).toHaveBeenCalled();
    expect(store.get(spaceMetaOverridesAtom)['spc-edit']).toEqual({
      name: 'Local Name',
      icon: 'L',
    });
  });

  it('clears local overrides on reset', async () => {
    const onClose = vi.fn();
    const { store } = renderWithProviders(
      <EditSpaceDialog spaceId="spc-edit" serverName="Server Space" onClose={onClose} />,
      {
        initializeStore: (s) => {
          s.set(spaceMetaOverridesAtom, {
            'spc-edit': { name: 'Old name', icon: 'O' },
          });
        },
      },
    );

    await userEvent.click(screen.getByRole('button', { name: 'Reset to default' }));

    expect(onClose).toHaveBeenCalled();
    expect(store.get(spaceMetaOverridesAtom)).toEqual({});
  });
});

describe('<DeleteSpaceDialog>', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('deletes the space and clears active space when it was selected', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(new Response(null, { status: 204 }));
    const onClose = vi.fn();
    const { store } = renderWithProviders(
      <DeleteSpaceDialog space={activeSpace()} onClose={onClose} />,
      {
        initializeStore: (s) => {
          s.set(activeSpaceIdAtom, 'spc-delete');
        },
      },
    );

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchSpy).toHaveBeenCalledWith(
      '/v1/spaces/spc-delete',
      expect.objectContaining({ method: 'DELETE' }),
    );
    expect(store.get(activeSpaceIdAtom)).toBeNull();
  });
});
