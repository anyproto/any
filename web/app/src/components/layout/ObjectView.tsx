import { useCallback, useMemo, useState } from 'react';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { selectAtom } from 'jotai/utils';
import {
  ArrowLeft,
  ArrowRight,
  ChevronRight,
  PanelLeft,
  PanelLeftClose,
  Sidebar,
  X,
} from 'lucide-react';
import {
  activeSpaceIdAtom,
  activeViewAtom,
  canGoBackAtom,
  canGoForwardAtom,
  goBackAtom,
  goForwardAtom,
  focusedPaneAtom,
  objectTitleDraftKey,
  objectTitleDraftsAtom,
} from '@/atoms';
import { HealthCard } from '@/components/health';
import { useHealth } from '@/lib/api/meta';
import { useObjectName } from '@/lib/api/objects';
import { type SaveState, statusLabel, type StatusLabel } from '@/components/editor/saveMachine';
import { Button } from '@/components/ui';
import { cn } from '@/lib/cn';
import { renderRegisteredView } from '@/app/viewModules';

/**
 * Pane 3 — open object.
 *
 * Empty state: hint + the existing /v1/health card (kept here until
 * a real Settings page lands).
 *
 * Selected state: BlockNote editor wired to /markdown via MarkdownEditor.
 * Header gains a status indicator driven by the editor's save machine.
 */
export function ObjectView({
  spacesRailClosed = false,
  spaceContentsClosed = false,
  onToggleSpacesRail,
  onToggleSpaceContents,
}: {
  spacesRailClosed?: boolean;
  spaceContentsClosed?: boolean;
  onToggleSpacesRail?: () => void;
  onToggleSpaceContents?: () => void;
}) {
  const [activeView, setActiveView] = useAtom(activeViewAtom);
  const activeSpaceId = useAtomValue(activeSpaceIdAtom);
  const canGoBack = useAtomValue(canGoBackAtom);
  const canGoForward = useAtomValue(canGoForwardAtom);
  const goBack = useSetAtom(goBackAtom);
  const goForward = useSetAtom(goForwardAtom);
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
        spaceId={activeSpaceId}
        objectId={objectId}
        status={showStatus && editorState ? statusLabel(editorState) : null}
        spacesRailClosed={spacesRailClosed}
        spaceContentsClosed={spaceContentsClosed}
        canGoBack={canGoBack}
        canGoForward={canGoForward}
        onBack={goBack}
        onForward={goForward}
        onToggleSpacesRail={onToggleSpacesRail}
        onToggleSpaceContents={onToggleSpaceContents}
        onClose={
          activeView.kind !== 'empty'
            ? () => setActiveView({ kind: 'empty' })
            : undefined
        }
      />
      <div className="flex-1 overflow-y-auto">
        {activeSpaceId && activeView.kind !== 'empty' ? (
          renderRegisteredView(activeView, {
            spaceId: activeSpaceId,
            onEditorStateChange: onStateChange,
          })
        ) : (
          <EmptyState />
        )}
      </div>
    </section>
  );
}

function Header({
  spaceId,
  objectId,
  status,
  spacesRailClosed,
  spaceContentsClosed,
  canGoBack,
  canGoForward,
  onBack,
  onForward,
  onToggleSpacesRail,
  onToggleSpaceContents,
  onClose,
}: {
  spaceId: string | null;
  objectId: string | null;
  status: StatusLabel | null;
  spacesRailClosed: boolean;
  spaceContentsClosed: boolean;
  canGoBack: boolean;
  canGoForward: boolean;
  onBack: () => void;
  onForward: () => void;
  onToggleSpacesRail?: (() => void) | undefined;
  onToggleSpaceContents?: (() => void) | undefined;
  onClose?: (() => void) | undefined;
}) {
  const liveTitleDraftAtom = useMemo(
    () =>
      selectAtom(objectTitleDraftsAtom, (drafts) =>
        spaceId && objectId ? drafts[objectTitleDraftKey(spaceId, objectId)] : undefined,
      ),
    [objectId, spaceId],
  );
  const liveTitleDraft = useAtomValue(liveTitleDraftAtom);
  const nameQuery = useObjectName(spaceId, objectId);
  const objectLabel = objectId
    ? (liveTitleDraft ?? nameQuery.data ?? '').trim() || `Untitled (${objectId.slice(0, 6)}…)`
    : null;

  return (
    <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-2">
      <div className="flex items-center gap-1">
        <NavButton
          aria-label="Back"
          title="Back"
          disabled={!canGoBack}
          onClick={onBack}
          icon={<ArrowLeft className="h-4 w-4" />}
        />
        <NavButton
          aria-label="Forward"
          title="Forward"
          disabled={!canGoForward}
          onClick={onForward}
          icon={<ArrowRight className="h-4 w-4" />}
        />
        <span aria-hidden className="mx-1 h-5 w-px bg-foreground/10" />
        <NavButton
          aria-label={spacesRailClosed ? 'Show spaces panel' : 'Hide spaces panel'}
          title={spacesRailClosed ? 'Show spaces panel' : 'Hide spaces panel'}
          active={!spacesRailClosed}
          {...(onToggleSpacesRail ? { onClick: onToggleSpacesRail } : {})}
          icon={
            spacesRailClosed ? (
              <PanelLeft className="h-4 w-4" />
            ) : (
              <PanelLeftClose className="h-4 w-4" />
            )
          }
        />
        <NavButton
          aria-label={spaceContentsClosed ? 'Show object sidebar' : 'Hide object sidebar'}
          title={spaceContentsClosed ? 'Show object sidebar' : 'Hide object sidebar'}
          active={!spaceContentsClosed}
          {...(onToggleSpaceContents ? { onClick: onToggleSpaceContents } : {})}
          icon={<Sidebar className="h-4 w-4" />}
        />
      </div>
      {objectLabel ? (
        <Breadcrumb segments={[objectLabel]} />
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
  active,
  ...rest
}: {
  icon: React.ReactNode;
  'aria-label': string;
  title?: string;
  active?: boolean;
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
        active && 'bg-foreground/8 text-foreground',
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
