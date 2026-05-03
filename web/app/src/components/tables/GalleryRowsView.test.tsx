import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { activeObjectIdAtom, activeViewAtom } from '@/atoms';
import { GalleryRowsView } from './GalleryRowsView';

function renderGallery() {
  const store = createStore();
  const onLoadMore = vi.fn();
  const onCreate = vi.fn();
  render(
    <Provider store={store}>
      <div style={{ height: 600 }}>
        <GalleryRowsView
          typeId="t_movie"
          rows={[
            {
              id: 'obj-1',
              any: { name: 'Alien', types: ['t_movie'] },
              nav: { type: 1, parentId: '', pos: 'A' },
              t_movie: { p_rating: 5, p_status: 'Watched' },
            },
            {
              id: 'obj-2',
              any: { name: 'Primer', types: ['t_movie'] },
              nav: { type: 1, parentId: '', pos: 'B' },
              t_movie: { p_rating: 4, p_status: 'Queued' },
            },
          ]}
          props={[
            { id: 'p_rating', name: 'Rating', kind: 'number' },
            { id: 'p_status', name: 'Status', kind: 'string' },
          ]}
          isSuccess
          error={null}
          filter=""
          typeName="Movies"
          creating={false}
          hasNextPage={false}
          isFetchingNextPage={false}
          onLoadMore={onLoadMore}
          onCreate={onCreate}
        />
      </div>
    </Provider>,
  );
  return { store, onCreate };
}

describe('<GalleryRowsView>', () => {
  it('renders cards with property previews and opens objects from the full card', async () => {
    const { store } = renderGallery();

    expect(screen.getByRole('list', { name: /movies gallery/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /open alien/i })).toBeInTheDocument();
    expect(screen.getByText('Watched')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /open primer/i }));
    expect(store.get(activeObjectIdAtom)).toBe('obj-2');
    expect(store.get(activeViewAtom)).toEqual({
      kind: 'object',
      objectId: 'obj-2',
      typeId: 't_movie',
    });
  });

  it('keeps add-row behavior available in gallery layout', async () => {
    const { onCreate } = renderGallery();

    await userEvent.click(screen.getByRole('button', { name: /new movies/i }));
    expect(onCreate).toHaveBeenCalledTimes(1);
  });
});
