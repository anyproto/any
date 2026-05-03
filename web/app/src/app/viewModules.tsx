import { Component, lazy, Suspense, type ReactNode } from 'react';
import type { ActiveView } from '@/atoms';
import type { SaveState } from '@/components/editor/saveMachine';
import { keyOf } from '@/shared';

const loadMarkdownEditor = () =>
  import('@/components/editor/MarkdownEditor').then((module) => ({
    default: module.MarkdownEditor,
  }));

const loadTableView = () =>
  import('@/components/tables').then((module) => ({
    default: module.TableView,
  }));

const MarkdownEditor = lazy(loadMarkdownEditor);
const TableView = lazy(loadTableView);

interface ViewContext {
  spaceId: string;
  onEditorStateChange: (state: SaveState) => void;
}

interface ViewModule<K extends ActiveView['kind']> {
  kind: K;
  render: (view: Extract<ActiveView, { kind: K }>, ctx: ViewContext) => ReactNode;
}

const objectViewModule: ViewModule<'object'> = {
  kind: 'object',
  render: (view, ctx) => (
    <MarkdownEditor
      key={keyOf('object', ctx.spaceId, view.objectId)}
      spaceId={ctx.spaceId}
      objectId={view.objectId}
      onStateChange={ctx.onEditorStateChange}
    />
  ),
};

const typeTableViewModule: ViewModule<'type-table'> = {
  kind: 'type-table',
  render: (view) => (
    <TableView
      key={keyOf('type-table', view.typeId)}
      typeId={view.typeId}
    />
  ),
};

export function renderRegisteredView(view: ActiveView, ctx: ViewContext): ReactNode {
  const rendered =
    view.kind === 'object'
      ? objectViewModule.render(view, ctx)
      : view.kind === 'type-table'
        ? typeTableViewModule.render(view, ctx)
        : null;
  if (!rendered) return null;
  return (
    <ViewErrorBoundary resetKey={viewResetKey(view, ctx)}>
      <Suspense fallback={<ViewLoading view={view} />}>
        {rendered}
      </Suspense>
    </ViewErrorBoundary>
  );
}

export function preloadViewModule(kind: ActiveView['kind']) {
  if (kind === 'object') {
    void loadMarkdownEditor();
  } else if (kind === 'type-table') {
    void loadTableView();
  }
}

function ViewLoading({ view }: { view: ActiveView }) {
  if (view.kind === 'type-table' || view.kind === 'object') {
    return <div aria-busy="true" className="h-full bg-background" />;
  }
  return <div aria-busy="true" className="min-h-full bg-background" />;
}

function viewResetKey(view: ActiveView, ctx: ViewContext) {
  return view.kind === 'object'
    ? keyOf('object', ctx.spaceId, view.objectId)
    : view.kind === 'type-table'
      ? keyOf('type-table', view.typeId)
      : keyOf('empty');
}

class ViewErrorBoundary extends Component<
  { resetKey: string; children: ReactNode },
  { error: Error | null }
> {
  override state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: unknown) {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  override componentDidUpdate(prevProps: { resetKey: string }) {
    if (prevProps.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  override render() {
    if (this.state.error) {
      return (
        <div className="mx-auto max-w-xl p-8 text-sm text-destructive">
          Failed to load this view.
        </div>
      );
    }
    return this.props.children;
  }
}
