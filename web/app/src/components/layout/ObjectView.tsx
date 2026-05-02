import { useCallback, useState } from 'react';
import { useAtom, useAtomValue } from 'jotai';
import { ArrowLeft, ArrowRight, ChevronRight, X } from 'lucide-react';
import { activeSpaceIdAtom, activeViewAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { HealthCard } from '@/components/health/HealthCard';
import { useHealth } from '@/lib/api/meta';
import { MarkdownEditor } from '@/components/editor/MarkdownEditor';
import { type SaveState, statusLabel, type StatusLabel } from '@/components/editor/saveMachine';
import { TableView } from '@/components/tables/TableView';
import { Button } from '@/components/ui/Button';
import { cn } from '@/lib/cn';

/**
 * Pane 3 — open object.
 *
 * Empty state: hint + the existing /v1/health card (kept here until
 * a real Settings page lands).
 *
 * Selected state: BlockNote editor wired to /markdown via MarkdownEditor.
 * Header gains a status indicator driven by the editor's save machine.
 */
export function ObjectView() {
  const [activeView, setActiveView] = useAtom(activeViewAtom);
  const activeSpaceId = useAtomValue(activeSpaceIdAtom);
  const setFocused = useAtom(focusedPaneAtom)[1];

  const [editorState, setEditorState] = useState<SaveState | null>(null);
  const onStateChange = useCallback((s: SaveState) => setEditorState(s), []);

  const showStatus = activeView.kind === 'object' && editorState != null;
  const objectId = activeView.kind === 'object' ? activeView.objectId : null;

  return (
    <section
      aria-label="Object view"
      data-pane="3"
      onFocus={() => setFocused(3)}
      className="flex h-full flex-col bg-background"
    >
      <Header
        objectId={objectId}
        status={showStatus && editorState ? statusLabel(editorState) : null}
        onClose={
          activeView.kind !== 'empty'
            ? () => setActiveView({ kind: 'empty' })
            : undefined
        }
      />
      <div className="flex-1 overflow-y-auto">
        {activeSpaceId && activeView.kind === 'object' ? (
          <MarkdownEditor
            // key forces remount on object switch so initial fetch + state
            // machine reset happen cleanly.
            key={`${activeSpaceId}:${activeView.objectId}`}
            spaceId={activeSpaceId}
            objectId={activeView.objectId}
            onStateChange={onStateChange}
          />
        ) : activeSpaceId && activeView.kind === 'type-table' ? (
          <TableView
            key={`${activeSpaceId}:type:${activeView.typeId}`}
            typeId={activeView.typeId}
          />
        ) : (
          <EmptyState />
        )}
      </div>
    </section>
  );
}

function Header({
  objectId,
  status,
  onClose,
}: {
  objectId: string | null;
  status: StatusLabel | null;
  onClose?: (() => void) | undefined;
}) {
  return (
    <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-2">
      <div className="flex items-center gap-1">
        <NavButton aria-label="Back" disabled icon={<ArrowLeft className="h-4 w-4" />} />
        <NavButton aria-label="Forward" disabled icon={<ArrowRight className="h-4 w-4" />} />
      </div>
      {objectId ? (
        <Breadcrumb segments={[`Object ${objectId.slice(0, 8)}…`]} />
      ) : (
        <div aria-hidden className="flex-1" />
      )}
      <div className="flex items-center gap-2">
        {status && <SaveIndicator status={status} />}
        {onClose && (
          <NavButton
            aria-label="Close"
            onClick={onClose}
            icon={<X className="h-4 w-4" />}
          />
        )}
      </div>
    </header>
  );
}

function SaveIndicator({ status }: { status: StatusLabel }) {
  switch (status) {
    case 'loading':
      return <span className="text-xs text-foreground/40">Loading…</span>;
    case 'idle':
      return <span className="text-xs text-foreground/40">Saved</span>;
    case 'dirty':
      return <span className="text-xs text-foreground/60">Editing…</span>;
    case 'saving':
      return <span className="text-xs text-foreground/60">Saving…</span>;
    case 'save_error':
      return (
        <Button size="sm" variant="ghost" className="h-6 px-2 text-xs text-destructive">
          Save failed
        </Button>
      );
  }
}

function NavButton({
  icon,
  ...rest
}: {
  icon: React.ReactNode;
  'aria-label': string;
  disabled?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      {...rest}
      className={cn(
        'inline-flex h-7 w-7 items-center justify-center rounded-md text-foreground/60',
        'hover:bg-foreground/5 hover:text-foreground',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        'disabled:cursor-not-allowed disabled:opacity-40',
      )}
    >
      {icon}
    </button>
  );
}

function Breadcrumb({ segments }: { segments: string[] }) {
  return (
    <nav
      aria-label="Breadcrumb"
      className="flex flex-1 items-center gap-1 overflow-hidden text-xs text-foreground/60"
    >
      {segments.map((seg, i) => {
        const last = i === segments.length - 1;
        return (
          <span key={`${seg}-${i}`} className="flex items-center gap-1">
            {i > 0 && <ChevronRight className="h-3 w-3 text-foreground/30" aria-hidden />}
            <span className={cn('truncate', last && 'font-medium text-foreground')}>
              {seg}
            </span>
          </span>
        );
      })}
    </nav>
  );
}

function EmptyState() {
  const health = useHealth();
  return (
    <div className="mx-auto flex max-w-xl flex-col gap-6 p-8">
      <p className="text-sm text-foreground/60">
        Pick an item from the sidebar to view it. Until a real Settings page
        lands, the server health card is shown here for convenience.
      </p>
      <HealthCard query={health} />
    </div>
  );
}
