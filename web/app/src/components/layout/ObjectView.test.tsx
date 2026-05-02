import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectView } from './ObjectView';
import { activeObjectIdAtom, activeSpaceIdAtom } from '@/atoms/selection';

// BlockNote depends on browser APIs jsdom doesn't fully implement.
// Stub MarkdownEditor so this test only covers the header / pane shell.
vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: ({ objectId }: { objectId: string }) => (
    <div data-testid="editor-stub">editor for {objectId}</div>
  ),
}));

function renderWith({
  activeObjectId,
  activeSpaceId,
}: {
  activeObjectId: string | null;
  activeSpaceId: string | null;
}) {
  const store = createStore();
  store.set(activeObjectIdAtom, activeObjectId);
  store.set(activeSpaceIdAtom, activeSpaceId);
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
    renderWith({ activeObjectId: null, activeSpaceId: 'spc-test' });
    expect(screen.getByText(/Pick an item/i)).toBeInTheDocument();
  });

  it('renders the editor when an object is selected', () => {
    renderWith({ activeObjectId: 'obj-abc-12345678', activeSpaceId: 'spc-test' });
    expect(screen.getByTestId('editor-stub')).toBeInTheDocument();
    // Breadcrumb shows the truncated id.
    expect(screen.getByLabelText('Breadcrumb')).toHaveTextContent(/obj-abc/);
  });

  it('does not render the editor without an active space', () => {
    renderWith({ activeObjectId: 'obj-abc', activeSpaceId: null });
    expect(screen.queryByTestId('editor-stub')).not.toBeInTheDocument();
  });
});
