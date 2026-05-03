import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  activeObjectIdAtom,
  objectTitleDraftsAtom,
  selectedTreeIdsAtom,
  setObjectTitleDraftAtom,
} from '@/atoms';
import type { ObjectRecord } from '@/lib/api/objects';
import { renderWithProviders } from '@/test/renderWithProviders';
import { BulkDeleteObjectsDialog } from './BulkDeleteObjectsDialog';
import { CreateFolderDialog } from './CreateFolderDialog';
import { DeleteObjectDialog } from './DeleteObjectDialog';

function mockDeleteFetch() {
  return vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 204 }));
}

describe('<CreateFolderDialog>', () => {
  it('submits the trimmed folder name and blocks empty names', async () => {
    const onCreate = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <CreateFolderDialog open onOpenChange={vi.fn()} onCreate={onCreate} />,
    );

    const submit = screen.getByRole('button', { name: 'Create' });
    expect(submit).toBeDisabled();

    await user.type(screen.getByLabelText('Name'), '   ');
    expect(submit).toBeDisabled();

    await user.clear(screen.getByLabelText('Name'));
    await user.type(screen.getByLabelText('Name'), '  Roadmap  ');
    await user.click(submit);

    expect(onCreate).toHaveBeenCalledWith('Roadmap');
  });
});

describe('<DeleteObjectDialog>', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('deletes the object, clears active selection, and clears title drafts', async () => {
    const fetchSpy = mockDeleteFetch();
    const onClose = vi.fn();
    const obj: ObjectRecord = { id: 'obj-delete', any: { name: 'Delete me' } };
    const { store } = renderWithProviders(
      <DeleteObjectDialog
        spaceId="spc-test"
        obj={obj}
        parentId=""
        onClose={onClose}
      />,
      {
        initializeStore: (s) => {
          s.set(activeObjectIdAtom, 'obj-delete');
          s.set(setObjectTitleDraftAtom, {
            spaceId: 'spc-test',
            objectId: 'obj-delete',
            title: 'Draft title',
          });
        },
      },
    );

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchSpy).toHaveBeenCalledWith(
      '/v1/spaces/spc-test/objects/obj-delete',
      expect.objectContaining({ method: 'DELETE' }),
    );
    expect(store.get(activeObjectIdAtom)).toBeNull();
    expect(store.get(objectTitleDraftsAtom)).toEqual({});
  });
});

describe('<BulkDeleteObjectsDialog>', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('deletes every id, clears selection, clears active object, and removes drafts', async () => {
    const fetchSpy = mockDeleteFetch();
    const onClose = vi.fn();
    const { store } = renderWithProviders(
      <BulkDeleteObjectsDialog
        spaceId="spc-test"
        ids={['obj-a', 'obj-b']}
        onClose={onClose}
      />,
      {
        initializeStore: (s) => {
          s.set(activeObjectIdAtom, 'obj-b');
          s.set(selectedTreeIdsAtom, new Set(['obj-a', 'obj-b']));
          s.set(setObjectTitleDraftAtom, {
            spaceId: 'spc-test',
            objectId: 'obj-a',
            title: 'A draft',
          });
          s.set(setObjectTitleDraftAtom, {
            spaceId: 'spc-test',
            objectId: 'obj-b',
            title: 'B draft',
          });
        },
      },
    );

    await userEvent.click(screen.getByRole('button', { name: 'Delete 2' }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(fetchSpy).toHaveBeenNthCalledWith(
      1,
      '/v1/spaces/spc-test/objects/obj-a',
      expect.objectContaining({ method: 'DELETE' }),
    );
    expect(fetchSpy).toHaveBeenNthCalledWith(
      2,
      '/v1/spaces/spc-test/objects/obj-b',
      expect.objectContaining({ method: 'DELETE' }),
    );
    expect(store.get(activeObjectIdAtom)).toBeNull();
    expect([...store.get(selectedTreeIdsAtom)]).toEqual([]);
    expect(store.get(objectTitleDraftsAtom)).toEqual({});
  });
});
